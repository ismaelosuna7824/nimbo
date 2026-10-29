package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// deploy hands a destructive plan back for the user's decision and sends
// confirmDestroy only when the agent passes confirm_destroy.
func TestDeployConfirmDestroy(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	now := time.Now().UTC()
	runs := map[string]model.Run{
		"r1": {ID: "r1", Action: model.ActionApply, Status: model.RunNeedsConfirmation, EndedAt: &now,
			Plan: &model.PlanSummary{Add: 1, Destroy: 2},
			Destructive: []model.DestructiveChange{
				{Address: "aws_s3_bucket.b", Action: model.PlanReplace, Cause: "replace_because_cannot_update", Reasons: []string{"bucket"}},
				{Address: "aws_sqs_queue.q", Action: model.PlanDelete, Cause: "delete_because_no_resource_config"},
			}},
		"r2": {ID: "r2", Action: model.ActionApply, Status: model.RunSucceeded, EndedAt: &now, PlanRunID: "r1",
			Drift: []model.DriftChange{{Address: "aws_iam_policy.p", Action: model.PlanUpdate, Attributes: []string{"policy"}, Cause: model.DriftJSONNormalization, Hint: "use jsonencode()"}}},
	}
	daemon := httptest.NewServer(http.StripPrefix("/api/v1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/workflows/wf1":
			_ = json.NewEncoder(w).Encode(model.Workflow{ID: "wf1", Name: "guarded"})
		case r.Method == http.MethodPost && r.URL.Path == "/workflows/wf1/runs":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			bodies = append(bodies, body)
			id := "r1"
			if body["confirmDestroy"] == true {
				id = "r2"
			}
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(model.Run{ID: id, Status: model.RunQueued})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/runs/"):
			_ = json.NewEncoder(w).Encode(runs[strings.TrimPrefix(r.URL.Path, "/runs/")])
		case r.Method == http.MethodGet && r.URL.Path == "/workflows/wf1/state":
			_ = json.NewEncoder(w).Encode(model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{}})
		default:
			http.NotFound(w, r)
		}
	})))
	defer daemon.Close()

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := NewServer(FixedEndpoint(daemon.URL), "test").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	var asked runView
	mustTool(t, cs, "deploy", map[string]any{"workflow": "wf1"}, &asked)
	if asked.Status != model.RunNeedsConfirmation || len(asked.Destructive) != 2 || asked.Destructive[0].Reasons[0] != "bucket" {
		t.Fatalf("deploy = %+v", asked)
	}
	if !strings.Contains(asked.Hint, "confirm_destroy=true") || !strings.Contains(asked.Hint, "only after they agree") {
		t.Errorf("hint = %q", asked.Hint)
	}

	var done runView
	mustTool(t, cs, "deploy", map[string]any{"workflow": "wf1", "confirm_destroy": true}, &done)
	if done.Status != model.RunSucceeded || len(done.Drift) != 1 || !strings.Contains(done.Hint, "still change") {
		t.Fatalf("confirmed deploy = %+v", done)
	}
	// confirm_destroy means nothing to other run tools.
	mustTool(t, cs, "plan", map[string]any{"workflow": "wf1", "confirm_destroy": true}, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("bodies = %v", bodies)
	}
	if _, ok := bodies[0]["confirmDestroy"]; ok {
		t.Errorf("unconfirmed deploy sent %v", bodies[0])
	}
	if bodies[1]["confirmDestroy"] != true || bodies[1]["action"] != "apply" {
		t.Errorf("confirmed deploy sent %v", bodies[1])
	}
	if _, ok := bodies[2]["confirmDestroy"]; ok || bodies[2]["action"] != "plan" {
		t.Errorf("plan sent %v", bodies[2])
	}
}

// Provider version warnings reach deploy/plan results with a hint to fix
// the constraint; confirmation and drift hints come first.
func TestRunViewWarnings(t *testing.T) {
	w := "hashicorp/google 6.50.0 (selected by the lock file) is known to break google_sql_database.app on the local emulators"
	v := viewRun(&model.Run{Status: model.RunFailed, Warnings: []string{w}})
	if len(v.Warnings) != 1 || v.Warnings[0] != w || !strings.Contains(v.Hint, "version constraint") || !strings.Contains(v.Hint, "structure.providers") {
		t.Errorf("view = %+v", v)
	}
	drift := viewRun(&model.Run{Status: model.RunSucceeded, Warnings: []string{w}, Drift: []model.DriftChange{{Address: "a.b"}}})
	if len(drift.Warnings) != 1 || !strings.Contains(drift.Hint, "still change") {
		t.Errorf("drift view = %+v", drift)
	}
	if v := viewRun(&model.Run{Status: model.RunSucceeded}); v.Warnings != nil || v.Hint != "" {
		t.Errorf("clean run view = %+v", v)
	}
}
