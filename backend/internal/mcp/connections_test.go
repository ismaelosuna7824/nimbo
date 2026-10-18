package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestConnectionToolsRedactSecrets(t *testing.T) {
	cs, _ := session(t)
	var created model.Connection
	mustTool(t, cs, "create_connection", map[string]any{
		"name": "Orders DB", "kind": "postgres", "host": "db.internal", "username": "app",
		"password": "pg-secret-pw", "database": "orders",
	}, &created)
	var api model.Connection
	mustTool(t, cs, "create_connection", map[string]any{
		"name": "Partner API", "kind": "http", "base_url": "https://partner.example.com",
		"headers": map[string]string{"X-Api-Token": "hdr-secret", "Accept": "application/json"},
		"auth":    map[string]string{"type": "basic", "username": "u", "password": "basic-secret"},
	}, &api)

	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"list_connections", map[string]any{}},
		{"get_connection", map[string]any{"connection": "orders db"}},
		{"get_connection", map[string]any{"connection": api.ID}},
		{"update_connection", map[string]any{"connection": api.ID, "description": "edited"}},
	} {
		text := mustTool(t, cs, call.name, call.args, nil)
		for _, secret := range []string{"pg-secret-pw", "hdr-secret", "basic-secret"} {
			if strings.Contains(text, secret) {
				t.Fatalf("%s leaked %q: %s", call.name, secret, text)
			}
		}
		if call.name == "list_connections" && (!strings.Contains(text, "application/json") || !strings.Contains(text, `"password"`)) {
			t.Fatalf("list lost non-secret data or secretsSet: %s", text)
		}
	}

	var got model.Connection
	mustTool(t, cs, "get_connection", map[string]any{"connection": api.ID}, &got)
	if got.Description != "edited" || got.Auth == nil || got.Auth.Username != "u" {
		t.Fatalf("partial update lost fields: %+v", got)
	}
	found := false
	for _, s := range got.SecretsSet {
		found = found || s == "auth.password"
	}
	if !found {
		t.Fatalf("update dropped the stored basic password: %+v", got.SecretsSet)
	}
}

func TestRunConnectionLoadTestNeedsConfirmation(t *testing.T) {
	cs, hub := session(t)
	updates, cancel := hub.Subscribe("")
	defer cancel()
	var hits atomic.Int64
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer svc.Close()

	var conn model.Connection
	mustTool(t, cs, "create_connection", map[string]any{"name": "svc", "kind": "http", "base_url": svc.URL}, &conn)
	args := map[string]any{
		"connection": conn.ID,
		"target":     map[string]any{"kind": "http", "label": "root", "http": map[string]any{"method": "GET", "url": "/"}},
		"stages":     []map[string]any{{"durationSec": 1, "target": 5}},
		"mode":       "rps",
	}
	text, isErr := callTool(t, cs, "run_connection_load_test", args)
	if !isErr || !strings.Contains(text, "approval") {
		t.Fatalf("unconfirmed run: isErr=%v %s", isErr, text)
	}

	// run_load_test (workflow) maps the daemon's 428 to the same guidance.
	var wf struct{ ID string }
	mustTool(t, cs, "create_workflow", map[string]any{"name": "ext"}, &wf)
	text, isErr = callTool(t, cs, "run_load_test", map[string]any{
		"workflow": wf.ID,
		"target":   map[string]any{"kind": "http", "connectionId": conn.ID, "http": map[string]any{"method": "GET", "url": "/"}},
		"stages":   []map[string]any{{"durationSec": 1, "target": 5}},
	})
	if !isErr || !strings.Contains(text, "confirm_external=true") {
		t.Fatalf("unconfirmed workflow run: isErr=%v %s", isErr, text)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests sent before confirmation", hits.Load())
	}

	args["confirm_external"] = true
	var run loadRunView
	mustTool(t, cs, "run_connection_load_test", args, &run)
	if run.Status != model.LoadPassed || hits.Load() == 0 {
		t.Fatalf("confirmed run: %+v (hits %d)", run, hits.Load())
	}
	waitRunSaved(t, updates, run.ID)
}

// waitRunSaved blocks until the engine has persisted run: the tool returns
// as soon as the status is final, but the history file is written just
// after, and the temp home must not be removed while it is being written.
// The final status event is published only after the save.
func waitRunSaved(t *testing.T, updates <-chan events.Message, runID string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-updates:
			if !ok {
				t.Fatal("event subscription dropped")
			}
			if m.Type != model.EventLoadTestStatus {
				continue
			}
			var ev struct {
				Data model.LoadTestRun `json:"data"`
			}
			if err := json.Unmarshal(m.Data, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Data.ID == runID && ev.Data.Status != model.LoadRunning {
				return
			}
		case <-timeout:
			t.Fatalf("run %s was never saved", runID)
		}
	}
}
