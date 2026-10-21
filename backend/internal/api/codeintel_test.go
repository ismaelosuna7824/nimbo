package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestCatalogTypesSchemasAndFunctions(t *testing.T) {
	e := newEnv(t)
	var types []model.TypeInfo
	e.mustCall(http.StatusOK, "GET", "/catalog/types", nil, &types)
	byType := map[string]model.TypeInfo{}
	for _, ti := range types {
		byType[string(ti.Kind)+"/"+ti.Type] = ti
	}
	if len(types) < 5000 {
		t.Fatalf("got %d types, want every resource and data type (> 5000)", len(types))
	}
	if q := byType["resource/aws_sqs_queue"]; q.Cloud != model.CloudAWS || !q.Local || q.Label == "" {
		t.Errorf("aws_sqs_queue = %+v", q)
	}
	if r, ok := byType["resource/random_id"]; !ok || r.Cloud != "" || !r.Local {
		t.Errorf("utility type random_id = %+v (found %v)", r, ok)
	}
	if _, ok := byType["data/aws_iam_policy_document"]; !ok {
		t.Error("data sources missing")
	}

	var schemas map[string]model.ResourceSchema
	e.mustCall(http.StatusOK, "GET", "/catalog/schemas?types=resource/aws_sqs_queue,provider/aws,data/aws_nope,provider/google", nil, &schemas)
	if len(schemas) != 3 {
		t.Fatalf("schemas = %v, want sqs, aws and google providers", keys(schemas))
	}
	hasAttr := func(b model.SchemaBlock, name string) bool {
		for _, a := range b.Attributes {
			if a.Name == name {
				return true
			}
		}
		return false
	}
	if s := schemas["resource/aws_sqs_queue"]; !hasAttr(s.Block, "visibility_timeout_seconds") || s.DocsURL == "" {
		t.Errorf("sqs schema = %+v", s.ResourceSummary)
	}
	if p := schemas["provider/aws"]; !hasAttr(p.Block, "region") || p.Kind != "provider" || !strings.Contains(p.DocsURL, "hashicorp/aws") {
		t.Errorf("aws provider schema = %+v", p.ResourceSummary)
	}
	if e.call("GET", "/catalog/schemas?types=module/x", nil, nil) != http.StatusBadRequest {
		t.Error("invalid kind accepted")
	}

	var fns []model.FunctionSignature
	e.mustCall(http.StatusOK, "GET", "/catalog/functions", nil, &fns)
	var join *model.FunctionSignature
	for i := range fns {
		if strings.Contains(fns[i].Name, "::") {
			t.Errorf("namespaced function %s", fns[i].Name)
		}
		if fns[i].Name == "join" {
			join = &fns[i]
		}
	}
	if len(fns) < 100 || join == nil || len(join.Parameters) != 1 || join.VariadicParameter == nil || join.Description == "" {
		t.Fatalf("functions: %d, join = %+v", len(fns), join)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCodeWorkspace(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/dev"}})
	var ws model.CodeWorkspace
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform/workspace", nil, &ws)
	for _, p := range []string{"main.tf", "variables.tf", "modules/network/main.tf", "modules/subnets/main.tf"} {
		if ws.Files[p] == "" {
			t.Errorf("workspace misses %s (has %v)", p, keys(ws.Files))
		}
	}
	for p := range ws.Files {
		if !strings.HasSuffix(p, ".tf") {
			t.Errorf("non-Terraform file %s", p)
		}
	}
	if len(ws.Modules) != 0 {
		t.Errorf("modules before init = %+v", ws.Modules)
	}

	work := filepath.Join(e.home, "workflows", wf.ID, "work")
	writeFile(t, filepath.Join(work, ".terraform", "modules", "modules.json"),
		`{"Modules":[{"Key":"","Source":"","Dir":"."},{"Key":"network","Source":"./modules/network","Dir":"modules/network"},{"Key":"logs","Source":"registry.terraform.io/terraform-aws-modules/s3-bucket/aws","Version":"4.1.2","Dir":".terraform/modules/logs"},{"Key":"evil","Dir":".terraform/modules/../../../x"}]}`)
	writeFile(t, filepath.Join(work, ".terraform", "modules", "logs", "variables.tf"), `variable "bucket" {}`)
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform/workspace", nil, &ws)
	if len(ws.Modules) != 1 || ws.Modules[0].Key != "logs" || ws.Modules[0].Version != "4.1.2" || ws.Modules[0].Files["variables.tf"] == "" {
		t.Fatalf("downloaded modules = %+v", ws.Modules)
	}
}

type checkResult struct {
	Problems []model.CodeProblem `json:"problems"`
}

func (e *testEnv) check(id, path string, files map[string]string) []model.CodeProblem {
	e.t.Helper()
	var res checkResult
	e.mustCall(http.StatusOK, "POST", "/workflows/"+id+"/terraform/check", map[string]any{"path": path, "files": files}, &res)
	return res.Problems
}

func findProblem(ps []model.CodeProblem, code string) *model.CodeProblem {
	for i := range ps {
		if ps[i].Code == code {
			return &ps[i]
		}
	}
	return nil
}

func TestCheckCode(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Check")

	// Syntax errors are errors, located.
	ps := e.check(wf.ID, "main.tf", map[string]string{"main.tf": "resource \"aws_sns_topic\" \"t\" {\n  name = \n}\n"})
	if len(ps) == 0 || ps[0].Code != "syntax" || ps[0].Severity != model.SeverityError || ps[0].Line != 2 {
		t.Fatalf("syntax problems = %+v", ps)
	}

	src := `variable "prefix" {
  type = string
}

resource "aws_sqs_queue" "q" {
  name      = "${var.prefix}-jobs"
  nme       = var.missing
  versioning {
  }
}

resource "aws_sqs_queue_policy" "p" {
  queue_url = aws_sqs_queue.q.url
}

output "arn" {
  value = aws_sqs_queue.q.arnx
}

output "ghost" {
  value = aws_sns_topic.none.arn
}

output "loc" {
  value = local.nope
}

provider "aws" {
  regon = "us-east-1"
}
`
	ps = e.check(wf.ID, "main.tf", map[string]string{"main.tf": src})
	for _, p := range ps {
		if p.Severity != model.SeverityWarning {
			t.Errorf("problem %+v is not a warning", p)
		}
	}
	want := map[string][3]int{ // code -> line, column, end column
		"unknown_attribute":     {7, 3, 6},
		"unknown_block":         {8, 3, 13},
		"unknown_variable":      {7, 15, 26},
		"missing_attribute":     {12, 1, 38},
		"unsupported_attribute": {17, 27, 31},
		"unknown_reference":     {21, 11, 29},
		"unknown_local":         {25, 11, 21},
	}
	for code, w := range want {
		p := findProblem(ps, code)
		if p == nil {
			t.Errorf("no %s problem in %+v", code, ps)
			continue
		}
		if p.Line != w[0] || p.Column != w[1] || p.EndColumn != w[2] {
			t.Errorf("%s at %d:%d-%d, want %d:%d-%d (%s)", code, p.Line, p.Column, p.EndColumn, w[0], w[1], w[2], p.Message)
		}
	}
	// The provider block is checked against the provider's schema.
	found := false
	for _, p := range ps {
		if p.Code == "unknown_attribute" && p.Line == 29 {
			found = true
		}
	}
	if !found {
		t.Errorf("provider argument typo not reported: %+v", ps)
	}

	// Declarations of other files count, unsaved ones included.
	ps = e.check(wf.ID, "outputs.tf", map[string]string{
		"outputs.tf": "output \"p\" {\n  value = var.prefix\n}\n",
		"vars.tf":    "variable \"prefix\" {}\n",
	})
	if len(ps) != 0 {
		t.Errorf("cross-file reference problems = %+v", ps)
	}

	if e.call("POST", "/workflows/"+wf.ID+"/terraform/check", map[string]any{"path": "README.md", "files": map[string]string{}}, nil) != http.StatusBadRequest {
		t.Error("non-Terraform path accepted")
	}
	if e.call("POST", "/workflows/"+wf.ID+"/terraform/check", map[string]any{"path": "../x.tf", "files": map[string]string{}}, nil) != http.StatusBadRequest {
		t.Error("path outside the workflow accepted")
	}
}

func TestCheckCodeInModules(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/dev"}})
	var ws model.CodeWorkspace
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform/workspace", nil, &ws)

	// A module's own variables resolve inside its files; the root's don't.
	mod := ws.Files["modules/network/main.tf"] + "\nlocals {\n  x = var.cidr\n  y = var.region_of_root\n}\n"
	ps := e.check(wf.ID, "modules/network/main.tf", map[string]string{"modules/network/main.tf": mod})
	if len(ps) != 1 || ps[0].Code != "unknown_variable" || !strings.Contains(ps[0].Message, "region_of_root") {
		t.Errorf("module file problems = %+v", ps)
	}

	// Outputs of a local module are checked from the root.
	root := ws.Files["outputs.tf"] + "\noutput \"bad\" {\n  value = module.network.nope\n}\n"
	ps = e.check(wf.ID, "outputs.tf", map[string]string{"outputs.tf": root})
	if p := findProblem(ps, "unknown_output"); p == nil || len(ps) != 1 {
		t.Errorf("root problems = %+v", ps)
	}
}
