package api

import (
	"encoding/json"
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

// recorder is a local HTTP service standing in for an already-deployed API.
type recorder struct {
	srv   *httptest.Server
	hits  atomic.Int64
	mu    sync.Mutex
	auths []string
}

func newRecorder(t *testing.T) *recorder {
	rec := &recorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.hits.Add(1)
		rec.mu.Lock()
		rec.auths = append(rec.auths, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (e *testEnv) createConnection(c model.Connection) model.Connection {
	e.t.Helper()
	var out model.Connection
	if code := e.call("POST", "/connections", c, &out); code != http.StatusCreated {
		e.t.Fatalf("create connection: status %d", code)
	}
	return out
}

func (e *testEnv) rawBody(path string) string {
	e.t.Helper()
	var raw json.RawMessage
	if code := e.call("GET", path, nil, &raw); code != http.StatusOK {
		e.t.Fatalf("GET %s: %d", path, code)
	}
	return string(raw)
}

func shortSpec(target model.LoadTarget) model.LoadTestSpec {
	return model.LoadTestSpec{
		Target:  target,
		Profile: model.LoadProfile{Mode: model.ModeRPS, Stages: []model.LoadStage{{DurationSec: 1, Target: 5}}},
	}
}

func (e *testEnv) waitLoadTest(id string) model.LoadTestRun {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var run model.LoadTestRun
		if code := e.call("GET", "/loadtests/"+id, nil, &run); code != http.StatusOK {
			e.t.Fatalf("get load test: %d", code)
		}
		if run.Status != model.LoadRunning {
			return run
		}
		if time.Now().After(deadline) {
			e.t.Fatal("load test did not finish")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestConnectionCRUDNeverReturnsSecrets(t *testing.T) {
	e := newEnv(t)
	events, cancel := e.hub.Subscribe("some-workflow")
	defer cancel()

	created := e.createConnection(model.Connection{
		Name: "Billing API", Kind: model.ConnHTTP, BaseURL: "https://billing.example.com",
		Auth: &model.HTTPAuth{Type: "bearer", Token: "sk-live-123"},
	})
	if created.Auth == nil || created.Auth.Token != "" || !slices.Contains(created.SecretsSet, "auth.token") {
		t.Fatalf("create response not masked: %+v", created)
	}
	select {
	case m := <-events:
		if m.Type != model.EventConnectionsUpdated || !strings.Contains(string(m.Data), created.ID) {
			t.Fatalf("unexpected event %s %s", m.Type, m.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no connections.updated event reached a workflow-filtered subscriber")
	}

	for _, path := range []string{"/connections", "/connections/" + created.ID} {
		if body := e.rawBody(path); strings.Contains(body, "sk-live-123") || !strings.Contains(body, "Billing API") {
			t.Fatalf("GET %s leaked or lost data: %s", path, body)
		}
	}

	// PUT with the masked object keeps the token; clearSecrets + auth none drops it.
	created.Name = "Billing"
	var upd model.Connection
	if code := e.call("PUT", "/connections/"+created.ID, created, &upd); code != http.StatusOK || upd.Name != "Billing" {
		t.Fatalf("update: %d %+v", code, upd)
	}
	if clear, _ := e.conns.Connection(created.ID); clear.Auth.Token != "sk-live-123" {
		t.Fatal("blank token on update did not keep the stored one")
	}
	if data, err := os.ReadFile(filepath.Join(e.home, "connections.json")); err != nil || !strings.Contains(string(data), "sk-live-123") {
		t.Fatalf("secret not persisted locally: %v", err)
	}

	var apiErr struct{ Error string }
	if code := e.call("POST", "/connections", model.Connection{Name: "bad", Kind: model.ConnHTTP, BaseURL: "not a url"}, &apiErr); code != http.StatusBadRequest || apiErr.Error == "" {
		t.Fatalf("invalid create: %d %+v", code, apiErr)
	}
	if code := e.call("PUT", "/connections/"+created.ID, model.Connection{ID: "other", Name: "x", Kind: model.ConnAWS}, nil); code != http.StatusBadRequest {
		t.Fatalf("id mismatch: %d", code)
	}
	if code := e.call("GET", "/connections/nope-aaaaaa", nil, nil); code != http.StatusNotFound {
		t.Fatalf("missing: %d", code)
	}
}

func TestConnectionDraftTestUsesStoredSecrets(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t)
	created := e.createConnection(model.Connection{
		Name: "svc", Kind: model.ConnHTTP, BaseURL: rec.srv.URL,
		Auth: &model.HTTPAuth{Type: "bearer", Token: "stored-token"},
	})

	// The UI sends the masked connection back as a draft: blank token.
	var check model.ConnectionCheck
	if code := e.call("POST", "/connections/test", created, &check); code != http.StatusOK {
		t.Fatalf("draft test: %d", code)
	}
	if !check.OK {
		t.Fatalf("draft check failed: %+v", check)
	}
	rec.mu.Lock()
	auths := slices.Clone(rec.auths)
	rec.mu.Unlock()
	if !slices.Contains(auths, "Bearer stored-token") {
		t.Fatalf("draft did not use the stored token; saw %q", auths)
	}
	if strings.Contains(check.Message+check.Detail, "stored-token") {
		t.Fatal("check echoes the secret")
	}

	if code := e.call("POST", "/connections/"+created.ID+"/test", nil, &check); code != http.StatusOK || !check.OK {
		t.Fatalf("saved test: %d %+v", code, check)
	}
	if code := e.call("POST", "/connections/test", model.Connection{ID: "missing-aaaaaa", Name: "x", Kind: model.ConnAWS}, nil); code != http.StatusNotFound {
		t.Fatalf("draft with unknown id: %d", code)
	}
}

func TestConnectionLoadTestRequiresConfirmation(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t)
	conn := e.createConnection(model.Connection{Name: "svc", Kind: model.ConnHTTP, BaseURL: rec.srv.URL})
	target := model.LoadTarget{Kind: model.TargetHTTP, Label: "root", HTTP: &model.HTTPTarget{Method: "GET", URL: "/"}}

	var body struct{ Error, Code string }
	if code := e.call("POST", "/connections/"+conn.ID+"/loadtests", shortSpec(target), &body); code != http.StatusPreconditionRequired || body.Code != "confirmation_required" {
		t.Fatalf("unconfirmed connection run: %d %+v", code, body)
	}

	wf := e.createEmpty("Uses external")
	wfTarget := target
	wfTarget.ConnectionID = conn.ID
	body.Code = ""
	if code := e.call("POST", "/workflows/"+wf.ID+"/loadtests", shortSpec(wfTarget), &body); code != http.StatusPreconditionRequired || body.Code != "confirmation_required" {
		t.Fatalf("unconfirmed workflow run: %d %+v", code, body)
	}
	if n := rec.hits.Load(); n != 0 {
		t.Fatalf("engine sent %d requests before confirmation", n)
	}

	spec := shortSpec(target)
	spec.ConfirmExternal = true
	var run model.LoadTestRun
	if code := e.call("POST", "/connections/"+conn.ID+"/loadtests", spec, &run); code != http.StatusCreated {
		t.Fatalf("confirmed run: %d", code)
	}
	if run.ConnectionID != conn.ID || run.WorkflowID != "" || !run.External {
		t.Fatalf("run ownership: %+v", run)
	}
	done := e.waitLoadTest(run.ID)
	if done.Status != model.LoadPassed || rec.hits.Load() == 0 {
		t.Fatalf("run status %s error %q hits %d", done.Status, done.Error, rec.hits.Load())
	}
	var history []model.LoadTestRun
	if code := e.call("GET", "/connections/"+conn.ID+"/loadtests", nil, &history); code != http.StatusOK || len(history) != 1 {
		t.Fatalf("history: %d %d", code, len(history))
	}

	// DELETE removes the connection and its run history.
	dir := filepath.Join(e.home, "connections", conn.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("history dir missing before delete: %v", err)
	}
	if code := e.call("DELETE", "/connections/"+conn.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("history dir survived delete: %v", err)
	}
	if code := e.call("GET", "/connections/"+conn.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("deleted connection still served: %d", code)
	}
}
