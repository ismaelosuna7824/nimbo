package mcp

import (
	"encoding/json"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSetAttrCreatesNestedBlocks(t *testing.T) {
	var b model.Body
	if err := setAttr(&b, "bucket", `"logs"`); err != nil {
		t.Fatal(err)
	}
	if err := setAttr(&b, "versioning.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := setAttr(&b, "rule[0].id", `"expire"`); err != nil {
		t.Fatal(err)
	}
	if err := setAttr(&b, "rule[1].id", `"archive"`); err != nil {
		t.Fatal(err)
	}
	if err := setAttr(&b, "bucket", `"logs-2"`); err != nil {
		t.Fatal(err)
	}
	if len(b.Attrs) != 1 || b.Attrs[0].Value.Expr != `"logs-2"` {
		t.Fatalf("attrs = %+v", b.Attrs)
	}
	if len(b.Blocks) != 3 || b.Blocks[0].Type != "versioning" || b.Blocks[2].Body.Attrs[0].Value.Expr != `"archive"` {
		t.Fatalf("blocks = %+v", b.Blocks)
	}
	if err := setAttr(&b, "rule[5].id", `"x"`); err == nil {
		t.Fatal("expected error for a non-contiguous block index")
	}
	if err := setAttr(&b, "bucket.x", "1"); err == nil {
		t.Fatal("expected error when pathing into an attribute")
	}
	if err := setAttr(&b, "versioning", "true"); err == nil {
		t.Fatal("expected error when assigning to a block")
	}
}

func TestUnsetAttr(t *testing.T) {
	var b model.Body
	setAttr(&b, "bucket", `"logs"`)
	setAttr(&b, "rule[0].id", `"a"`)
	setAttr(&b, "rule[1].id", `"b"`)
	setAttr(&b, "rule[1].prefix", `"p/"`)

	if err := unsetAttr(&b, "rule[1].prefix"); err != nil {
		t.Fatal(err)
	}
	if got := len(b.Blocks[1].Body.Attrs); got != 1 {
		t.Fatalf("rule[1] has %d attrs", got)
	}
	if err := unsetAttr(&b, "rule[0]"); err != nil {
		t.Fatal(err)
	}
	if len(b.Blocks) != 1 || b.Blocks[0].Body.Attrs[0].Value.Expr != `"b"` {
		t.Fatalf("blocks = %+v", b.Blocks)
	}
	if err := unsetAttr(&b, "bucket"); err != nil || len(b.Attrs) != 0 {
		t.Fatalf("unset bucket: %v %+v", err, b.Attrs)
	}
	if err := unsetAttr(&b, "missing"); err == nil {
		t.Fatal("expected error for a missing attribute")
	}
}

func TestCtyType(t *testing.T) {
	cases := map[string]string{
		`"string"`:                             "string",
		`["list","string"]`:                    "list(string)",
		`["map",["set","number"]]`:             "map(set(number))",
		`["object",{"b":"bool","a":"string"}]`: "object({a=string, b=bool})",
		`"dynamic"`:                            "any",
	}
	for in, want := range cases {
		if got := ctyType(json.RawMessage(in)); got != want {
			t.Errorf("ctyType(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestViewEnvironmentsRedactsSecrets(t *testing.T) {
	wf := &model.Workflow{
		Variables: []model.NamedBlock{{Name: "db_password", Body: model.Body{Attrs: []model.Attr{{Name: "sensitive", Value: model.Value{Expr: "true"}}}}}},
		Environments: []model.Environment{{
			Name:    "dev",
			Values:  map[string]model.Value{"db_password": {Expr: `"hunter2"`}, "region": {Expr: `"us-east-1"`}},
			EnvVars: []model.EnvVar{{Key: "TOKEN", Value: "abc", Secret: true}, {Key: "LOG", Value: "debug"}},
		}},
		ActiveEnvironment: "dev",
	}
	v := viewEnvironments(wf)
	if len(v) != 1 || !v[0].Active {
		t.Fatalf("got %+v", v)
	}
	if v[0].Values["db_password"] != redacted || v[0].Values["region"] != `"us-east-1"` {
		t.Fatalf("values = %+v", v[0].Values)
	}
	if v[0].EnvVars["TOKEN"] != redacted || v[0].EnvVars["LOG"] != "debug" {
		t.Fatalf("env vars = %+v", v[0].EnvVars)
	}
}
