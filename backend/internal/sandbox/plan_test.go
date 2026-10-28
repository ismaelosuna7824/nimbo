package sandbox

import (
	"os"
	"reflect"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// planWorkflow declares the fixtures' resources except terraform_data.gone,
// as if its node had been deleted. The fixtures are real
// `terraform show -json <planfile>` output (Terraform 1.14) of a config of
// terraform_data resources: create.json plans it from scratch, update.json
// plans an edit of it after apply.
func planWorkflow() *model.Workflow {
	res := func(id, name string) model.Node {
		return model.Node{ID: id, Kind: model.KindResource, Type: "terraform_data", Name: name}
	}
	return &model.Workflow{ID: "wf", Nodes: []model.Node{
		res("n-a", "a"), res("n-b", "b"), res("n-fe", "fe"), res("n-keep", "keep"), res("n-nested", "nested"),
		res("n-rep", "rep"), res("n-secret", "secret"),
		{ID: "n-m", Kind: model.KindModule, Name: "m"},
	}}
}

func readPlanFixture(t *testing.T, name string) map[string]model.PlannedChange {
	t.Helper()
	data, err := os.ReadFile("testdata/plan/" + name)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := parsePlan(data, planWorkflow())
	if err != nil {
		t.Fatal(err)
	}
	byAddr := map[string]model.PlannedChange{}
	for _, c := range changes {
		byAddr[c.Address] = c
	}
	if len(byAddr) != len(changes) {
		t.Fatalf("duplicate addresses in %v", changes)
	}
	return byAddr
}

func attrsByName(c model.PlannedChange) map[string]model.AttributeChange {
	out := map[string]model.AttributeChange{}
	for _, a := range c.Attributes {
		out[a.Name] = a
	}
	return out
}

func TestParsePlanMapsAddressesToNodes(t *testing.T) {
	got := readPlanFixture(t, "update.json")
	want := map[string]string{
		"terraform_data.a":                        "n-a",
		"terraform_data.b[0]":                     "n-b",
		"terraform_data.b[1]":                     "n-b",
		`terraform_data.fe["x"]`:                  "n-fe",
		`terraform_data.fe["y"]`:                  "n-fe",
		"terraform_data.gone[0]":                  "",
		"terraform_data.keep":                     "n-keep",
		"terraform_data.nested":                   "n-nested",
		"terraform_data.rep":                      "n-rep",
		"terraform_data.secret":                   "n-secret",
		"module.m.terraform_data.inner":           "n-m",
		"module.m.module.sub.terraform_data.deep": "n-m",
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %v", got)
	}
	for addr, id := range want {
		if c, ok := got[addr]; !ok || c.NodeID != id {
			t.Errorf("%s -> %q, want %q", addr, c.NodeID, id)
		}
	}
	if got[`terraform_data.fe["y"]`].Index != "y" || got["terraform_data.b[1]"].Index != float64(1) {
		t.Errorf("indexes = %v, %v", got[`terraform_data.fe["y"]`].Index, got["terraform_data.b[1]"].Index)
	}
}

func TestParsePlanActions(t *testing.T) {
	got := readPlanFixture(t, "update.json")
	want := map[string]model.PlanAction{
		"terraform_data.a":                        model.PlanUpdate,
		"terraform_data.b[0]":                     model.PlanNoOp,
		"terraform_data.b[1]":                     model.PlanDelete,
		`terraform_data.fe["y"]`:                  model.PlanCreate,
		"terraform_data.gone[0]":                  model.PlanDelete,
		"terraform_data.rep":                      model.PlanReplace,
		"module.m.module.sub.terraform_data.deep": model.PlanUpdate,
	}
	for addr, action := range want {
		if got[addr].Action != action {
			t.Errorf("%s: action %q, want %q", addr, got[addr].Action, action)
		}
	}
	if r := got["terraform_data.rep"].Reason; r != "replace_because_cannot_update" {
		t.Errorf("rep reason = %q", r)
	}
	for _, addr := range []string{"terraform_data.b[0]", "terraform_data.b[1]", "terraform_data.gone[0]"} {
		if a := got[addr].Attributes; a != nil {
			t.Errorf("%s: attributes %v, want none", addr, a)
		}
	}
}

func TestParsePlanAttributeChanges(t *testing.T) {
	got := readPlanFixture(t, "update.json")

	a := attrsByName(got["terraform_data.a"])
	if len(a) != 2 {
		t.Fatalf("a attributes = %v (unchanged id and triggers_replace must be left out)", a)
	}
	if in := a["input"]; in.Before != "first" || in.After != "changed" || in.Unknown {
		t.Errorf("a.input = %+v", in)
	}
	if out := a["output"]; out.Before != "first" || out.After != nil || !out.Unknown {
		t.Errorf("a.output = %+v, want first -> known after apply", out)
	}

	rep := attrsByName(got["terraform_data.rep"])
	if tr := rep["triggers_replace"]; tr.Before != "one" || tr.After != "two" || !tr.ForcesReplacement {
		t.Errorf("rep.triggers_replace = %+v", tr)
	}
	if id := rep["id"]; !id.Unknown || id.ForcesReplacement {
		t.Errorf("rep.id = %+v", id)
	}
	if _, ok := rep["input"]; ok {
		t.Error("rep.input did not change")
	}

	in := attrsByName(got["terraform_data.secret"])["input"]
	if !in.Sensitive || in.Before != nil || in.After != nil {
		t.Errorf("secret.input = %+v, want masked", in)
	}
}

func TestParsePlanCreate(t *testing.T) {
	got := readPlanFixture(t, "create.json")
	for addr, c := range got {
		if c.Action != model.PlanCreate {
			t.Errorf("%s: action %q", addr, c.Action)
		}
	}

	a := attrsByName(got["terraform_data.a"])
	if _, ok := a["triggers_replace"]; ok {
		t.Error("a create lists the unset triggers_replace")
	}
	if a["input"].After != "first" || !a["id"].Unknown || !a["output"].Unknown {
		t.Errorf("a attributes = %v", a)
	}

	nested := attrsByName(got["terraform_data.nested"])["input"]
	if want := map[string]any{"known": "x", "later": model.UnknownValue}; nested.Unknown || !reflect.DeepEqual(nested.After, want) {
		t.Errorf("nested.input = %+v, want %v", nested, want)
	}

	in := attrsByName(got["terraform_data.secret"])["input"]
	if !in.Sensitive || in.After != nil {
		t.Errorf("secret.input = %+v, want masked", in)
	}
}
