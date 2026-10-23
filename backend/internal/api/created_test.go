package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The desktop app offers to open workflows an AI agent created (MCP requests
// carry the client header) and ignores the ones it created itself.
func TestWorkflowCreatedEventOrigin(t *testing.T) {
	e := newEnv(t)
	events, cancel := e.hub.Subscribe("some-other-workflow") // filtered subscribers get it too
	defer cancel()

	create := func(name string, mcp bool) {
		body, _ := json.Marshal(map[string]string{"name": name})
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/workflows", bytes.NewReader(body))
		if mcp {
			req.Header.Set(model.ClientHeader, model.ClientMCP)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d", name, res.StatusCode)
		}
	}
	next := func() model.WorkflowCreated {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case m := <-events:
				if m.Type != model.EventWorkflowCreated {
					continue
				}
				var ev struct{ Data model.WorkflowCreated }
				if err := json.Unmarshal(m.Data, &ev); err != nil {
					t.Fatal(err)
				}
				return ev.Data
			case <-deadline:
				t.Fatal("no workflow.created event")
			}
		}
	}

	create("From the app", false)
	if got := next(); got.Name != "From the app" || got.Origin != "app" || got.ID == "" {
		t.Fatalf("app event %+v", got)
	}
	create("From an agent", true)
	if got := next(); got.Name != "From an agent" || got.Origin != model.ClientMCP {
		t.Fatalf("mcp event %+v", got)
	}
}
