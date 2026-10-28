//go:build docker

package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The deploy guard with real Terraform (terraform_data, no emulator): a
// replacement waits for confirmation, the confirmation applies the saved
// plan, and a timestamp() input is reported as drift after every deploy.
func TestDeployGuardTerraformData(t *testing.T) {
	m, _ := newTestManager(t)
	wfID := testWorkflowID("guard")
	t.Cleanup(func() { cleanupSandbox(t, m, wfID) })
	wf := &model.Workflow{ID: wfID, Name: "guard", UpdatedAt: time.Now().UTC()}
	main := func(trigger string) map[string][]byte {
		return map[string][]byte{"main.tf": []byte(`
resource "terraform_data" "rep" {
  triggers_replace = "` + trigger + `"
}
resource "terraform_data" "stamp" {
  input = timestamp()
}
`)}
	}

	first := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: main("one")})
	if first.Plan == nil || first.Plan.Add != 2 || len(first.Destructive) != 0 {
		t.Fatalf("first apply: plan %+v destructive %+v", first.Plan, first.Destructive)
	}
	if len(first.Drift) != 1 || first.Drift[0].Address != "terraform_data.stamp" || first.Drift[0].Cause != model.DriftDynamicValue {
		t.Errorf("drift = %+v", first.Drift)
	}

	wf.UpdatedAt = time.Now().UTC()
	run, err := m.Run(context.Background(), RunRequest{Workflow: wf, Action: model.ActionApply, Files: main("two")})
	if err != nil {
		t.Fatal(err)
	}
	asked := waitEnded(t, m, run.ID)
	if asked.Status != model.RunNeedsConfirmation || len(asked.Destructive) != 1 {
		t.Fatalf("replace: %s %+v", asked.Status, asked.Destructive)
	}
	if d := asked.Destructive[0]; d.Address != "terraform_data.rep" || d.Action != model.PlanReplace || len(d.Reasons) != 1 || d.Reasons[0] != "triggers_replace" {
		t.Errorf("destructive = %+v", d)
	}

	done := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: main("two"), ConfirmDestroy: true, ConfirmRun: asked.ID})
	if done.PlanRunID != asked.ID || done.Plan == nil || done.Plan.Add != 1 || done.Plan.Destroy != 1 {
		t.Fatalf("confirmed: planRunId %q plan %+v", done.PlanRunID, done.Plan)
	}
	var log strings.Builder
	for _, l := range done.Log {
		log.WriteString(l.Text + "\n")
	}
	if strings.Count(log.String(), "$ terraform plan") != 0 || !strings.Contains(log.String(), "still change after deploy") {
		t.Errorf("confirmed run log:\n%s", log.String())
	}
}

func waitEnded(t *testing.T, m *Manager, id string) *model.Run {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if r, ok := m.GetRun(id); ok && r.EndedAt != nil {
			return r
		}
	}
	t.Fatalf("run %s did not finish", id)
	return nil
}
