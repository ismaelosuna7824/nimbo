package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The update.json fixture replaces terraform_data.rep (triggers_replace
// changed) and deletes terraform_data.b[1] (count shrank) and
// terraform_data.gone[0] (node removed); everything else is a create,
// update or no-op.
func TestDestructiveChangesFromPlan(t *testing.T) {
	data, err := os.ReadFile("testdata/plan/update.json")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := parsePlan(data, planWorkflow())
	if err != nil {
		t.Fatal(err)
	}
	got := destructiveChanges(changes)
	want := []model.DestructiveChange{
		{Address: "terraform_data.b[1]", NodeID: "n-b", Action: model.PlanDelete, Cause: "delete_because_count_index"},
		{Address: "terraform_data.gone[0]", Action: model.PlanDelete, Cause: "delete_because_count_index"},
		{Address: "terraform_data.rep", NodeID: "n-rep", Action: model.PlanReplace, Cause: "replace_because_cannot_update", Reasons: []string{"triggers_replace"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("destructive = %+v\nwant %+v", got, want)
	}

	data, err = os.ReadFile("testdata/plan/create.json")
	if err != nil {
		t.Fatal(err)
	}
	if changes, err = parsePlan(data, planWorkflow()); err != nil {
		t.Fatal(err)
	}
	if got := destructiveChanges(changes); got != nil {
		t.Errorf("a plan from scratch destroys %+v", got)
	}
}

// Reads of data sources, forgets (removed blocks keeping the object) and
// creates never need a confirmation; create_before_destroy replacements
// do.
func TestDestructiveChangesActions(t *testing.T) {
	plan := `{"resource_changes":[
		{"address":"data.aws_region.r","mode":"data","type":"aws_region","name":"r","change":{"actions":["read"]}},
		{"address":"aws_s3_bucket.kept","mode":"managed","type":"aws_s3_bucket","name":"kept","change":{"actions":["forget"],"before":{"bucket":"kept"}}},
		{"address":"aws_s3_bucket.new","mode":"managed","type":"aws_s3_bucket","name":"new","change":{"actions":["create"],"after":{"bucket":"new"}}},
		{"address":"aws_instance.web","mode":"managed","type":"aws_instance","name":"web","action_reason":"replace_because_tainted",
		 "change":{"actions":["create","delete"],"before":{"ami":"a"},"after":{"ami":"a"},"after_unknown":{"id":true}}}
	]}`
	changes, err := parsePlan([]byte(plan), &model.Workflow{ID: "wf"})
	if err != nil {
		t.Fatal(err)
	}
	got := destructiveChanges(changes)
	if len(got) != 1 || got[0].Address != "aws_instance.web" || got[0].Action != model.PlanReplace || got[0].Cause != "replace_because_tainted" || got[0].Reasons != nil {
		t.Fatalf("destructive = %+v", got)
	}
}

func driftWorkflow(defaultTags bool) *model.Workflow {
	wf := &model.Workflow{ID: "wf", Nodes: []model.Node{
		{ID: "n-policy", Kind: model.KindResource, Type: "aws_iam_policy", Name: "read"},
		{ID: "n-param", Kind: model.KindResource, Type: "aws_ssm_parameter", Name: "deployed_at", Body: model.Body{Attrs: []model.Attr{
			{Name: "value", Value: model.Value{Expr: "timestamp()"}},
		}}},
		{ID: "n-assets", Kind: model.KindResource, Type: "aws_s3_bucket", Name: "assets"},
		{ID: "n-jobs", Kind: model.KindResource, Type: "aws_sqs_queue", Name: "jobs"},
	}}
	if defaultTags {
		wf.Providers = []model.NamedBlock{{Name: "aws", Body: model.Body{Blocks: []model.Block{{Type: "default_tags"}}}}}
	}
	return wf
}

func TestDriftChanges(t *testing.T) {
	data, err := os.ReadFile("testdata/plan/drift.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := driftChanges(data, driftWorkflow(true))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		addr, node string
		action     model.PlanAction
		attrs      []string
		cause      string
		hint       string // substring
	}
	want := []row{
		{"aws_iam_policy.read", "n-policy", model.PlanUpdate, []string{"policy"}, model.DriftJSONNormalization, "jsonencode()"},
		{"aws_ssm_parameter.deployed_at", "n-param", model.PlanUpdate, []string{"value"}, model.DriftDynamicValue, "ignore_changes = [value]"},
		{"aws_s3_bucket.assets", "n-assets", model.PlanUpdate, []string{"tags"}, model.DriftDefaultTags, "default_tags"},
		{"aws_sqs_queue.jobs", "n-jobs", model.PlanUpdate, []string{"kms_data_key_reuse_period_seconds"}, model.DriftOther, "ignore_changes = [kms_data_key_reuse_period_seconds]"},
		{"aws_sns_topic.gone", "", model.PlanCreate, []string{}, model.DriftOther, "no longer finds"},
	}
	if len(got) != len(want) {
		t.Fatalf("drift = %+v", got)
	}
	for i, w := range want {
		g := got[i]
		if g.Address != w.addr || g.NodeID != w.node || g.Action != w.action || !reflect.DeepEqual(g.Attributes, w.attrs) || g.Cause != w.cause || !strings.Contains(g.Hint, w.hint) {
			t.Errorf("drift[%d] = %+v\nwant %+v", i, g, w)
		}
	}

	// Without default_tags on the provider a tags diff is a plain one.
	got, err = driftChanges(data, driftWorkflow(false))
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Cause != model.DriftOther {
		t.Errorf("tags without default_tags: %+v", got[2])
	}
}

// A confirmed apply applies the saved plan only while it still describes
// the configuration and environment the user reviewed; a workflow save
// that changes no Terraform (a card move) keeps it valid.
func TestStaleConfirmation(t *testing.T) {
	work := t.TempDir()
	confirmed := &model.Run{ID: "r1", Environment: "dev"}
	req := func(main, env string, updated time.Time) RunRequest {
		return RunRequest{
			Workflow:    &model.Workflow{ID: "wf", UpdatedAt: updated},
			Files:       map[string][]byte{"main.tf": []byte(main)},
			Environment: &model.Environment{Name: env},
		}
	}
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	reviewed := req(`resource "aws_sqs_queue" "q" {}`, "dev", t0)
	if why := staleConfirmation(confirmed, reviewed, work); !strings.Contains(why, "no longer available") {
		t.Errorf("missing plan file: %q", why)
	}
	for name, content := range map[string]string{pendingPlanFile("r1"): "plan", pendingConfigFile("r1"): configDigest(reviewed)} {
		if err := writeFile(filepath.Join(work, filepath.FromSlash(name)), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if why := staleConfirmation(confirmed, reviewed, work); why != "" {
		t.Errorf("valid confirmation refused: %q", why)
	}
	if why := staleConfirmation(confirmed, req(`resource "aws_sqs_queue" "q" {}`, "dev", t0.Add(time.Minute)), work); why != "" {
		t.Errorf("a layout-only save invalidated the plan: %q", why)
	}
	if why := staleConfirmation(confirmed, req(`resource "aws_sqs_queue" "r" {}`, "dev", t0), work); !strings.Contains(why, "workflow changed") {
		t.Errorf("edited workflow: %q", why)
	}
	if why := staleConfirmation(confirmed, req(`resource "aws_sqs_queue" "q" {}`, "prod", t0), work); !strings.Contains(why, "environment") {
		t.Errorf("other environment: %q", why)
	}
	removePendingPlans(work)
	if left, _ := filepath.Glob(filepath.Join(work, ".nimbo", "*")); len(left) != 0 {
		t.Errorf("pending files kept: %v", left)
	}
}

func TestPendingRun(t *testing.T) {
	m, err := New(Config{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	add := func(id, wf string, action model.RunAction, status model.RunStatus, at time.Duration) {
		m.runs[id] = &runState{run: model.Run{ID: id, WorkflowID: wf, Action: action, Status: status, CreatedAt: t0.Add(at)}}
	}
	add("a1", "wf", model.ActionApply, model.RunNeedsConfirmation, 0)
	add("p1", "wf", model.ActionPlan, model.RunSucceeded, time.Minute)
	add("o1", "other", model.ActionApply, model.RunNeedsConfirmation, 2*time.Minute)

	m.mu.Lock()
	defer m.mu.Unlock()
	if r, err := m.pendingRun("wf", "a1"); err != nil || r.ID != "a1" {
		t.Errorf("by id: %v, %v", r, err)
	}
	for _, id := range []string{"p1", "o1", "nope"} {
		if _, err := m.pendingRun("wf", id); !errors.Is(err, ErrNotPending) {
			t.Errorf("%s: err %v, want ErrNotPending", id, err)
		}
	}
	// A later plan changes nothing: the apply still waits.
	if r, err := m.pendingRun("wf", ""); err != nil || r == nil || r.ID != "a1" {
		t.Errorf("newest pending behind a plan: %v, %v", r, err)
	}
	// A later apply or destroy supersedes it.
	add("d1", "wf", model.ActionDestroy, model.RunSucceeded, 3*time.Minute)
	if r, err := m.pendingRun("wf", ""); err != nil || r != nil {
		t.Errorf("superseded by a destroy: %v, %v", r, err)
	}
	add("a2", "wf", model.ActionApply, model.RunNeedsConfirmation, 4*time.Minute)
	if r, err := m.pendingRun("wf", ""); err != nil || r == nil || r.ID != "a2" {
		t.Errorf("newest pending: %v, %v", r, err)
	}
}
