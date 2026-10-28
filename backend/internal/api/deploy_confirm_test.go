package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// fakeTerraformDocker is a docker CLI for Terraform runs of a workflow
// without clouds: sandbox calls succeed with no output; `terraform plan`
// prints dir/summary and saves its -out file, `show -json` prints
// dir/plan.json (dir/check.json for the post-deploy check's plan), the
// check's plan exits 2 (changes) when dir/drift exists, and apply prints a
// summary with changes. Every call is logged to dir/docker.log.
func fakeTerraformDocker(t *testing.T) (docker, dir string) {
	t.Helper()
	dir = t.TempDir()
	docker = filepath.Join(dir, "docker")
	script := `#!/bin/sh
d='` + dir + `'
echo "$*" >> "$d/docker.log"
work=""; prev=""; out=""
for a in "$@"; do
  case "$prev" in -v) case "$a" in *:/work) work="${a%:/work}" ;; esac ;; esac
  case "$a" in -out=*) out="${a#-out=}" ;; esac
  prev="$a"
done
case "$*" in
*"show -json"*check.tfplan*) cat "$d/check.json" ;;
*"show -json"*) cat "$d/plan.json" ;;
*" plan -input=false -no-color -detailed-exitcode"*)
  : > "$work/$out"
  if [ -e "$d/drift" ]; then exit 2; fi ;;
*" plan -input=false"*)
  cat "$d/summary"
  : > "$work/$out" ;;
*" apply -input=false"*) echo "Apply complete! Resources: 1 added, 1 changed, 1 destroyed." ;;
esac
exit 0
`
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return docker, dir
}

func (e *testEnv) waitRun(id string) model.Run {
	e.t.Helper()
	var run model.Run
	deadline := time.Now().Add(30 * time.Second)
	for {
		e.mustCall(http.StatusOK, "GET", "/runs/"+id, nil, &run)
		if run.EndedAt != nil {
			return run
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("run %s still %s", id, run.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runLog(r model.Run) string {
	var b strings.Builder
	for _, l := range r.Log {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

// A deploy whose plan deletes or replaces deployed resources applies
// nothing until confirmed; the confirmation applies exactly that saved
// plan, unless the workflow changed since, which plans and asks again.
func TestDeployNeedsConfirmation(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a shell script as docker")
	}
	docker, dir := fakeTerraformDocker(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyFixture := func(name, fixture string) {
		t.Helper()
		src, err := os.ReadFile(filepath.Join("..", "sandbox", "testdata", "plan", fixture))
		if err != nil {
			t.Fatal(err)
		}
		write(name, string(src))
	}
	dockerCalls := func() string {
		data, _ := os.ReadFile(filepath.Join(dir, "docker.log"))
		_ = os.Remove(filepath.Join(dir, "docker.log"))
		return string(data)
	}
	e := newEnvDocker(t, docker)
	wf := e.createEmpty("guarded")
	base := "/workflows/" + wf.ID
	pending := func(id string) string {
		return filepath.Join(e.home, "workflows", wf.ID, "work", ".nimbo", "pending-"+id+".tfplan")
	}
	start := func(body map[string]any) model.Run {
		t.Helper()
		var run model.Run
		e.mustCall(http.StatusCreated, "POST", base+"/runs", body, &run)
		return e.waitRun(run.ID)
	}

	// 1. The plan replaces and deletes: nothing is applied.
	copyFixture("plan.json", "update.json")
	write("summary", "Plan: 1 to add, 4 to change, 3 to destroy.\n")
	asked := start(map[string]any{"action": "apply"})
	if asked.Status != model.RunNeedsConfirmation || asked.Error != "" {
		t.Fatalf("status %s (%s), want needs_confirmation:\n%s", asked.Status, asked.Error, runLog(asked))
	}
	if len(asked.Destructive) != 3 || asked.Plan == nil || asked.Plan.Destroy != 3 {
		t.Fatalf("destructive %+v, plan %+v", asked.Destructive, asked.Plan)
	}
	if calls := dockerCalls(); strings.Contains(calls, " apply ") {
		t.Fatalf("an unconfirmed destructive deploy ran terraform apply:\n%s", calls)
	}
	if _, err := os.Stat(pending(asked.ID)); err != nil {
		t.Fatalf("the plan to confirm is not kept: %v", err)
	}
	// The list survives in list responses (no detail fetch needed).
	var runs []model.Run
	e.mustCall(http.StatusOK, "GET", base+"/runs", nil, &runs)
	if len(runs) == 0 || runs[0].ID != asked.ID || len(runs[0].Destructive) != 3 {
		t.Fatalf("runs = %+v", runs)
	}

	// 2. Only a run waiting for confirmation can be confirmed.
	e.mustCall(http.StatusBadRequest, "POST", base+"/runs", map[string]any{"action": "apply", "confirmDestroy": true, "confirmRunId": "nope"}, nil)

	// 3. Confirmed: that very plan file is applied, then the post-deploy
	// check finds resources that keep changing.
	copyFixture("check.json", "drift.json")
	write("drift", "")
	done := start(map[string]any{"action": "apply", "confirmDestroy": true, "confirmRunId": asked.ID})
	if done.Status != model.RunSucceeded || done.PlanRunID != asked.ID {
		t.Fatalf("confirmed run %s planRunId %q:\n%s", done.Status, done.PlanRunID, runLog(done))
	}
	calls := dockerCalls()
	if !strings.Contains(calls, " apply -input=false -no-color .nimbo/pending-"+asked.ID+".tfplan") {
		t.Errorf("the confirmed plan was not applied:\n%s", calls)
	}
	if n := strings.Count(calls, " plan -input=false"); n != 1 || !strings.Contains(calls, "-detailed-exitcode") {
		t.Errorf("a confirmed deploy must not plan again (only the post-deploy check): %d plans\n%s", n, calls)
	}
	if _, err := os.Stat(pending(asked.ID)); !os.IsNotExist(err) {
		t.Errorf("applied plan kept: %v", err)
	}
	if len(done.Drift) != 5 || done.Drift[0].Address != "aws_iam_policy.read" || done.Drift[0].Cause != model.DriftJSONNormalization {
		t.Errorf("drift = %+v", done.Drift)
	}
	if log := runLog(done); !strings.Contains(log, "still change after deploy") || !strings.Contains(log, "Run succeeded.") {
		t.Errorf("log:\n%s", log)
	}
	// Confirming again: nothing waits anymore, so it plans and asks.
	again := start(map[string]any{"action": "apply", "confirmDestroy": true})
	if again.Status != model.RunNeedsConfirmation || !strings.Contains(runLog(again), "No plan is waiting for confirmation") {
		t.Errorf("second confirmation: %s\n%s", again.Status, runLog(again))
	}
	dockerCalls()

	// 4. The workflow changed after the list was shown: plan and ask again.
	e.mustCall(http.StatusOK, "PUT", base+"/terraform", map[string]any{"files": map[string]string{"main.tf": "variable \"x\" {\n  default = \"y\"\n}\n"}}, nil)
	stale := start(map[string]any{"action": "apply", "confirmDestroy": true})
	if stale.Status != model.RunNeedsConfirmation || !strings.Contains(runLog(stale), "The workflow changed since the plan you confirmed") {
		t.Fatalf("stale confirmation: %s\n%s", stale.Status, runLog(stale))
	}
	if calls := dockerCalls(); strings.Contains(calls, " apply ") {
		t.Fatalf("a stale confirmation applied:\n%s", calls)
	}
	if _, err := os.Stat(pending(again.ID)); !os.IsNotExist(err) {
		t.Errorf("superseded plan kept: %v", err)
	}

	// 5. Nothing destructive: the plan is applied right away; a clean
	// post-deploy check records no drift.
	copyFixture("plan.json", "create.json")
	write("summary", "Plan: 9 to add, 0 to change, 0 to destroy.\n")
	if err := os.Remove(filepath.Join(dir, "drift")); err != nil {
		t.Fatal(err)
	}
	ok := start(map[string]any{"action": "apply"})
	if ok.Status != model.RunSucceeded || len(ok.Destructive) != 0 || len(ok.Drift) != 0 {
		t.Fatalf("safe deploy %s destructive %+v drift %+v:\n%s", ok.Status, ok.Destructive, ok.Drift, runLog(ok))
	}
	if !strings.Contains(runLog(ok), "Post-deploy check: a new plan has no changes.") {
		t.Errorf("log:\n%s", runLog(ok))
	}
	if _, err := os.Stat(pending(stale.ID)); !os.IsNotExist(err) {
		t.Errorf("pending plan of an older run kept: %v", err)
	}
}
