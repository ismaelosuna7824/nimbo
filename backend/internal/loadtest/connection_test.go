package loadtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// connections is an in-memory ConnectionResolver.
type connections map[string]model.Connection

func (c connections) Connection(id string) (model.Connection, bool) {
	conn, ok := c[id]
	return conn, ok
}

func TestExternalDetection(t *testing.T) {
	e := NewEngine(EngineConfig{
		Home:        t.TempDir(),
		Resolver:    fixedResolver{model.CloudAWS: "http://emulator.internal:4566"},
		Connections: connections{"api": {ID: "api", Kind: model.ConnHTTP, BaseURL: "http://127.0.0.1:9"}, "acct": {ID: "acct", Kind: model.ConnAWS}},
	})
	httpTarget := func(u string) model.LoadTarget {
		return model.LoadTarget{Kind: model.TargetHTTP, HTTP: &model.HTTPTarget{URL: u}}
	}
	cases := []struct {
		name     string
		target   model.LoadTarget
		external bool
	}{
		{"loopback v4", httpTarget("http://127.0.0.1:8080/x"), false},
		{"loopback range", httpTarget("http://127.1.2.3/"), false},
		{"loopback v6", httpTarget("http://[::1]:80/"), false},
		{"unspecified", httpTarget("http://0.0.0.0:3000/"), false},
		{"localhost", httpTarget("http://localhost:4566/"), false},
		{"localhost uppercase", httpTarget("http://LOCALHOST/"), false},
		{"subdomain of localhost", httpTarget("http://api.localhost:8080/"), false},
		{"floci host", httpTarget("http://abc.execute-api.localhost.floci.io:4566/hello"), false},
		{"sandbox emulator host", httpTarget("http://emulator.internal:4566/path"), false},
		{"public host", httpTarget("https://api.example.com/v1"), true},
		{"private network", httpTarget("http://10.0.0.5/"), true},
		{"localhost lookalike", httpTarget("http://localhost.example.com/"), true},
		{"floci lookalike", httpTarget("http://localhost.floci.io.evil.com/"), true},
		{"real api gateway", httpTarget("https://abc123.execute-api.us-east-1.amazonaws.com/prod"), true},
		{"http connection with local base", model.LoadTarget{Kind: model.TargetHTTP, ConnectionID: "api", HTTP: &model.HTTPTarget{URL: "/health"}}, true},
		{"aws sandbox", model.LoadTarget{Kind: model.TargetAWS, AWS: &model.AWSTarget{Operation: OpSQSSend, Resource: "q"}}, false},
		{"aws connection", model.LoadTarget{Kind: model.TargetAWS, ConnectionID: "acct", AWS: &model.AWSTarget{Operation: OpSQSSend, Resource: "q"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := model.LoadTestSpec{Target: c.target}
			p, err := e.plan(testWF, &spec)
			if err != nil {
				t.Fatal(err)
			}
			if p.external != c.external {
				t.Errorf("external = %v, want %v", p.external, c.external)
			}
		})
	}
}

func TestPlanRejectsUnusableTargets(t *testing.T) {
	e := NewEngine(EngineConfig{Home: t.TempDir(), Connections: connections{"pg": {ID: "pg", Kind: model.ConnPostgres, Host: "db"}}})
	for name, target := range map[string]model.LoadTarget{
		"sql without connection": {Kind: model.TargetSQL, SQL: &model.SQLTarget{Query: "SELECT 1"}},
		"unknown connection":     {Kind: model.TargetSQL, ConnectionID: "nope", SQL: &model.SQLTarget{Query: "SELECT 1"}},
		"kind mismatch":          {Kind: model.TargetRedis, ConnectionID: "pg", Redis: &model.RedisTarget{Command: "PING"}},
	} {
		spec := model.LoadTestSpec{Target: target}
		if _, err := e.plan(testWF, &spec); err == nil {
			t.Errorf("%s: plan succeeded", name)
		}
	}
	spec := model.LoadTestSpec{Target: model.LoadTarget{Kind: model.TargetSQL, ConnectionID: "nope", SQL: &model.SQLTarget{Query: "SELECT 1"}}}
	if _, err := e.plan(testWF, &spec); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("unknown connection: %v, want ErrConnectionNotFound", err)
	}
}

func TestExternalTargetNeedsConfirmationBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Connections: connections{"api": {ID: "api", Name: "API", Kind: model.ConnHTTP, BaseURL: srv.URL}}})

	spec := httpSpec("/", hold(1, 5))
	spec.Target.ConnectionID = "api"
	if _, err := e.Start(context.Background(), testWF, spec); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("Start: %v, want ErrConfirmationRequired", err)
	}
	if _, err := e.StartConnection(context.Background(), "api", httpSpec("/", hold(1, 5))); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("StartConnection: %v, want ErrConfirmationRequired", err)
	}
	if _, err := e.Start(context.Background(), testWF, httpSpec("https://api.example.invalid/", hold(1, 5))); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("public URL: %v, want ErrConfirmationRequired", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server without confirmation", n)
	}
	if runs := e.List(testWF); len(runs) != 0 {
		t.Fatalf("refused starts left %d runs", len(runs))
	}

	spec.ConfirmExternal = true
	run, err := e.Start(context.Background(), testWF, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !run.External || run.WorkflowID != testWF || run.ConnectionID != "" {
		t.Errorf("run external=%v workflow=%q connection=%q", run.External, run.WorkflowID, run.ConnectionID)
	}
	if done := w.wait(t, run.ID, 15*time.Second); done.Status != model.LoadPassed || hits.Load() == 0 {
		t.Fatalf("confirmed run: status %s, %d hits", done.Status, hits.Load())
	}
}

func TestHTTPConnectionHeadersAndBaseURL(t *testing.T) {
	type seen struct {
		path, query string
		header      http.Header
	}
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqs = append(reqs, seen{r.URL.Path, r.URL.RawQuery, r.Header.Clone()})
		mu.Unlock()
	}))
	defer srv.Close()
	conn := model.Connection{
		ID: "api", Kind: model.ConnHTTP, BaseURL: srv.URL + "/v1/",
		Headers: map[string]string{"X-Env": "test", "X-Override": "connection"},
		Auth:    &model.HTTPAuth{Type: "bearer", Token: "s3cret-token"},
	}
	home := t.TempDir()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: home, Events: w, Connections: connections{"api": conn}})
	spec := httpSpec("/users?q={{seq}}", hold(1, 5))
	spec.Target.HTTP.Headers = map[string]string{"x-override": "target"}
	spec.ConfirmExternal = true

	run, err := e.StartConnection(context.Background(), "api", spec)
	if err != nil {
		t.Fatal(err)
	}
	if run.ConnectionID != "api" || run.WorkflowID != "" || !run.External || run.Spec.Target.ConnectionID != "api" {
		t.Fatalf("run owner workflow=%q connection=%q external=%v", run.WorkflowID, run.ConnectionID, run.External)
	}
	if done := w.wait(t, run.ID, 15*time.Second); done.Status != model.LoadPassed {
		t.Fatalf("status %s: %s", done.Status, done.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reqs) < 5 {
		t.Fatalf("%d requests", len(reqs))
	}
	for _, r := range reqs {
		if r.path != "/v1/users" || !strings.HasPrefix(r.query, "q=") {
			t.Fatalf("request %s?%s, want /v1/users?q=<seq>", r.path, r.query)
		}
		if got := r.header.Get("Authorization"); got != "Bearer s3cret-token" {
			t.Fatalf("Authorization %q", got)
		}
		if r.header.Get("X-Env") != "test" || r.header.Get("X-Override") != "target" {
			t.Fatalf("headers %v", r.header)
		}
	}

	// The run belongs to the connection, not to any workflow.
	if _, err := os.Stat(filepath.Join(home, "connections", "api", "loadtests", run.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if got := e.ListConnection("api"); len(got) != 1 || got[0].ID != run.ID {
		t.Fatalf("ListConnection = %v", got)
	}
	if got := e.List(testWF); len(got) != 0 {
		t.Fatalf("workflow lists the connection's run: %v", got)
	}
	reopened := NewEngine(EngineConfig{Home: home})
	if got, ok := reopened.Get(run.ID); !ok || got.ConnectionID != "api" || len(got.Timeline) == 0 {
		t.Fatalf("Get after restart: %v %v", ok, got)
	}
	if err := e.DeleteConnectionRuns("api"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Get(run.ID); ok || len(e.ListConnection("api")) != 0 {
		t.Fatal("runs survived DeleteConnectionRuns")
	}
}

func TestDeleteConnectionRunsRefusesWhileRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Connections: connections{"api": {ID: "api", Kind: model.ConnHTTP, BaseURL: srv.URL}}})
	spec := httpSpec("", hold(30, 5))
	spec.ConfirmExternal = true
	run, err := e.StartConnection(context.Background(), "api", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteConnectionRuns("api"); !errors.Is(err, ErrRunActive) {
		t.Fatalf("DeleteConnectionRuns while running: %v", err)
	}
	if err := e.Stop(run.ID); err != nil {
		t.Fatal(err)
	}
	w.wait(t, run.ID, 10*time.Second)
	if err := e.DeleteConnectionRuns("api"); err != nil {
		t.Fatal(err)
	}
	other := spec
	other.Target.ConnectionID = "elsewhere"
	if _, err := e.StartConnection(context.Background(), "api", other); err == nil {
		t.Fatal("StartConnection accepted a target of another connection")
	}
}

func TestJoinURL(t *testing.T) {
	for _, c := range []struct{ base, path, want string }{
		{"https://api.example.com/v1", "/users", "https://api.example.com/v1/users"},
		{"https://api.example.com/v1/", "users?x=1", "https://api.example.com/v1/users?x=1"},
		{"https://api.example.com", "", "https://api.example.com"},
		{"https://api.example.com/v1", "?page=2", "https://api.example.com/v1?page=2"},
		{"https://api.example.com/v1", "https://other.example.com/x", "https://other.example.com/x"},
	} {
		if got, err := joinURL(c.base, c.path); err != nil || got != c.want {
			t.Errorf("joinURL(%q, %q) = %q, %v; want %q", c.base, c.path, got, err, c.want)
		}
	}
	if _, err := joinURL("", "/x"); err == nil {
		t.Error("relative URL without base accepted")
	}
}

func TestConnHeadersAuth(t *testing.T) {
	cases := []struct {
		auth         model.HTTPAuth
		header, want string
	}{
		{model.HTTPAuth{Type: "bearer", Token: "tok"}, "Authorization", "Bearer tok"},
		{model.HTTPAuth{Type: "basic", Username: "ann", Password: "pw"}, "Authorization", "Basic YW5uOnB3"},
		{model.HTTPAuth{Type: "api_key", Token: "k1"}, "X-API-Key", "k1"},
		{model.HTTPAuth{Type: "api_key", Token: "k2", Header: "X-Custom-Key"}, "X-Custom-Key", "k2"},
	}
	for _, c := range cases {
		h := connHeaders(&model.Connection{Auth: &c.auth, Headers: map[string]string{"Accept": "application/json"}})
		if got := h.Get(c.header); got != c.want || h.Get("Accept") != "application/json" {
			t.Errorf("%s: %s = %q, want %q (%v)", c.auth.Type, c.header, got, c.want, h)
		}
	}
}

func TestSplitRedisCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"PING", []string{"PING"}},
		{"  SET bench:{{seq}}   {{uuid}} ", []string{"SET", "bench:{{seq}}", "{{uuid}}"}},
		{`SET greeting "hello world"`, []string{"SET", "greeting", "hello world"}},
		{`SET k ""`, []string{"SET", "k", ""}},
		{`SET k "say \"hi\" \\ bye"`, []string{"SET", "k", `say "hi" \ bye`}},
		{"HSET\th\tf\tv", []string{"HSET", "h", "f", "v"}},
		{`SET key"with"quote v`, []string{"SET", "keywithquote", "v"}},
	}
	for _, c := range cases {
		got, err := splitCommand(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("splitCommand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "   ", `SET k "open`} {
		if _, err := splitCommand(bad); err == nil {
			t.Errorf("splitCommand(%q) accepted", bad)
		}
	}
}

func TestSQLArgsStayParameters(t *testing.T) {
	const evil = "x'); DROP TABLE users; --"
	for _, kind := range []model.ConnectionKind{model.ConnPostgres, model.ConnMySQL} {
		s, err := newSQLExec(&model.SQLTarget{Query: "SELECT * FROM users WHERE name = $1 AND n = $2", Args: []string{evil, "{{seq}}"}}, model.Connection{Kind: kind, Host: "127.0.0.1", Port: 9, Username: "u", Password: "p"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		args := s.bind(7)
		if s.query != "SELECT * FROM users WHERE name = $1 AND n = $2" || len(args) != 2 || args[0] != evil || args[1] != "7" {
			t.Errorf("%s: query %q args %q", kind, s.query, args)
		}
		s.close()
	}
}

func TestRedactSecrets(t *testing.T) {
	c := &model.Connection{
		URI:      "postgres://admin:Sup3r%2Fsecret@db.example.com:5432/app",
		Password: "fieldpass",
		Auth:     &model.HTTPAuth{Type: "bearer", Token: "tok-123456"},
	}
	in := strings.Join([]string{
		`failed to connect to postgres://admin:Sup3r%2Fsecret@db.example.com:5432/app`,
		`password authentication failed: Sup3r/secret`,
		`dsn root:hunter22@tcp(db:3306)/app`,
		`host=db password=fieldpass user=x`,
		`Authorization: Bearer tok-123456`,
		`mongodb://u:p%40ss@mongo:27017/?authSource=admin`,
	}, "\n")
	out := redactSecrets(in, c)
	for _, leak := range []string{"Sup3r", "secret", "hunter22", "fieldpass", "tok-123456", "p%40ss"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "db.example.com:5432/app") || !strings.Contains(out, "tcp(db:3306)") {
		t.Errorf("redaction removed non-secret context:\n%s", out)
	}
	if got := redactSecrets("GET http://host:8080/path@v2 failed", nil); got != "GET http://host:8080/path@v2 failed" {
		t.Errorf("URL without userinfo altered: %s", got)
	}
}
