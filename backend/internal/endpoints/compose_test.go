package endpoints

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

func publishedPort(container, host int) model.ComposePort {
	return model.ComposePort{Container: container, Protocol: "tcp", HostIP: "0.0.0.0", Host: host, URL: fmt.Sprintf("http://127.0.0.1:%d/", host)}
}

func composeRT(webPort int) sandbox.ComposeRuntime {
	svc := func(name, image string, state model.ComposeState, ports ...model.ComposePort) model.ComposeServiceStatus {
		s := model.ComposeServiceStatus{Name: name, NodeID: model.ComposeServiceID(name), State: state, Image: image}
		if state != model.ComposeAbsent {
			s.Containers = []model.ComposeContainer{{Name: "nimbo-dk-wf-" + name + "-1", State: "running", Image: image, Ports: ports}}
		}
		return s
	}
	return sandbox.ComposeRuntime{
		Status: model.ComposeStatus{WorkflowID: "wf", Services: []model.ComposeServiceStatus{
			svc("api", "", model.ComposeRunning, publishedPort(3000, webPort), publishedPort(9229, 59229)),
			svc("cache", "valkey/valkey:8", model.ComposeRunning, publishedPort(6379, 56379)),
			svc("db", "postgres:16-alpine", model.ComposeRunning, publishedPort(5432, 55432)),
			svc("mq", "rabbitmq:4", model.ComposeStarting, publishedPort(5672, 55672)),
			svc("sql", "mysql:8", model.ComposeRunning, publishedPort(3306, 53306)),
			svc("worker", "busybox", model.ComposeRunning),
			svc("off", "nginx", model.ComposeAbsent),
		}},
		Env: map[string]map[string]string{
			"db":  {"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cret pw", "POSTGRES_DB": "shop"},
			"sql": {"MYSQL_ROOT_PASSWORD": "rootpw", "MYSQL_DATABASE": "wp"},
		},
	}
}

func card(t *testing.T, info model.EndpointsInfo, name string) model.ResourceAccess {
	t.Helper()
	for _, r := range info.Resources {
		if r.Label == name {
			return r
		}
	}
	t.Fatalf("no card %q in %+v", name, info.Resources)
	return model.ResourceAccess{}
}

func fieldValue(r model.ResourceAccess, label string) string {
	for _, f := range r.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

func snippetCode(r model.ResourceAccess, label string) string {
	for _, s := range r.Snippets {
		if s.Label == label {
			return s.Code
		}
	}
	return ""
}

func TestBuildCompose(t *testing.T) {
	info := BuildCompose(&model.Workflow{ID: "wf"}, composeRT(53000))
	if info.WorkflowID != "wf" || len(info.Sandbox) != 0 || len(info.Resources) != 5 {
		t.Fatalf("info = %+v", info)
	}

	api := card(t, info, "api")
	if api.Kind != KindWebService || api.NodeID != "service:api" || api.Address != "service:api" || api.Type != "" && api.Type != "nimbo-dk-wf-api" {
		t.Errorf("api = %+v", api)
	}
	if len(api.Endpoints) != 2 || api.Endpoints[0].URL != "http://127.0.0.1:53000/" || len(api.Actions) != 1 || api.Actions[0].ID != ActRequest {
		t.Errorf("api endpoints/actions = %+v %+v", api.Endpoints, api.Actions)
	}
	if !strings.Contains(snippetCode(api, "curl"), "curl -i http://127.0.0.1:53000/") || snippetCode(api, "Open shell") != "docker exec -it nimbo-dk-wf-api-1 sh" {
		t.Errorf("api snippets = %+v", api.Snippets)
	}

	db := card(t, info, "db")
	if db.Kind != KindDatabase || fieldValue(db, "Port") != "55432" || fieldValue(db, "User") != "app" || fieldValue(db, "Database") != "shop" {
		t.Errorf("db = %+v", db)
	}
	if got := fieldValue(db, "Connection URL"); got != "postgres://app:s3cret%20pw@127.0.0.1:55432/shop" {
		t.Errorf("db url = %q", got)
	}
	if got := snippetCode(db, "psql"); got != "PGPASSWORD='s3cret pw' psql -h 127.0.0.1 -p 55432 -U app -d shop" {
		t.Errorf("psql = %q", got)
	}
	for _, f := range db.Fields {
		if (f.Label == "Password" || f.Label == "Connection URL") && !f.Secret {
			t.Errorf("%s not secret", f.Label)
		}
	}

	sql := card(t, info, "sql")
	if got := snippetCode(sql, "mysql"); sql.Kind != KindDatabase || got != "mysql -h 127.0.0.1 -P 53306 -u root -prootpw wp" {
		t.Errorf("mysql = %q (%s)", got, sql.Kind)
	}
	cache := card(t, info, "cache")
	if cache.Kind != KindCache || snippetCode(cache, "redis-cli") != "redis-cli -h 127.0.0.1 -p 56379 ping" {
		t.Errorf("cache = %+v", cache)
	}
	mq := card(t, info, "mq")
	if mq.Kind != KindGeneric || fieldValue(mq, "Host port (5672/tcp)") != "127.0.0.1:55672" || mq.Warning == "" || len(mq.Endpoints) != 0 {
		t.Errorf("mq = %+v", mq)
	}
}

func TestTryCompose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"path":%q,"method":%q}`, r.URL.Path, r.Method)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	rt := composeRT(port)
	ctx := context.Background()

	res, err := TryCompose(ctx, rt, model.TryRequest{NodeID: "service:api", Action: ActRequest, Path: "/health"})
	if err != nil || !res.OK || !strings.Contains(res.Body, `"path": "/health"`) || !strings.HasPrefix(res.Status, "200") {
		t.Fatalf("relative path: %+v, %v", res, err)
	}
	res, err = TryCompose(ctx, rt, model.TryRequest{NodeID: "service:api", Action: ActRequest, Method: "post", Path: srv.URL + "/x", Input: "{}"})
	if err != nil || !strings.Contains(res.Body, `"method": "POST"`) {
		t.Fatalf("full URL: %+v, %v", res, err)
	}

	var re *RequestError
	if _, err := TryCompose(ctx, rt, model.TryRequest{NodeID: "service:api", Action: ActRequest, Path: "http://127.0.0.1:1/"}); !errors.As(err, &re) {
		t.Errorf("unpublished port: %v", err)
	}
	if _, err := TryCompose(ctx, rt, model.TryRequest{NodeID: "service:api", Action: ActRequest, Path: "http://example.com:" + u.Port() + "/"}); !errors.As(err, &re) {
		t.Errorf("remote host: %v", err)
	}
	if _, err := TryCompose(ctx, rt, model.TryRequest{NodeID: "service:db", Action: ActRequest}); !errors.As(err, &re) {
		t.Errorf("database request: %v", err)
	}
	if _, err := TryCompose(ctx, rt, model.TryRequest{NodeID: "service:nope", Action: ActRequest}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown service: %v", err)
	}
}

func TestPostmanCompose(t *testing.T) {
	col := PostmanCompose(&model.Workflow{ID: "wf", Name: "Shop"}, composeRT(53000))
	if col.Info.Name != "Shop (Nimbo compose)" || len(col.Item) != 1 || col.Item[0].Name != "api" || len(col.Item[0].Item) != 2 {
		t.Fatalf("collection = %+v", col)
	}
	if r := col.Item[0].Item[0].Request; r.URL.Raw != "http://127.0.0.1:53000/" || r.URL.Port != "53000" {
		t.Errorf("request = %+v", r)
	}
}
