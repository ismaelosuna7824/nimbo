package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// applyChanges sends a code request and returns the workflow.
func applyChanges(t *testing.T, e *testEnv, id string, body map[string]any) model.Workflow {
	t.Helper()
	var wf model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+id+"/terraform", body, &wf)
	return wf
}

func nodeByAddress(wf *model.Workflow, addr string) *model.Node {
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	return nil
}

func zipNames(t *testing.T, e *testEnv, id string) map[string]string {
	t.Helper()
	res, err := http.Get(e.srv.URL + "/api/v1/workflows/" + id + "/export.zip?environments=true")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("export.zip: %v (%s)", err, data)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestCodeChangesCreateRenameDeleteRootFiles(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Files")
	wf, bucket := e.addNode(wf.ID, "aws_s3_bucket")

	// A new root file creates its blocks in that file.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"queues.tf": new("resource \"aws_sqs_queue\" \"jobs\" {\n  name = \"jobs\"\n}\n"),
	}})
	q := nodeByAddress(&wf, "aws_sqs_queue.jobs")
	if q == nil {
		t.Fatalf("queue not imported: %v", wf.Nodes)
	}
	if wf.NodeFiles[q.ID] != "queues.tf" {
		t.Errorf("queue file = %q", wf.NodeFiles[q.ID])
	}
	if nodeByID(&wf, bucket) == nil {
		t.Errorf("unedited bucket lost")
	}
	files := listWorkflowFiles(t, e, wf.ID)
	if f := files["queues.tf"]; !f.Generated || !f.Editable || !f.Movable || f.Kind != model.FileTerraform {
		t.Errorf("queues.tf = %+v", f)
	}

	// Renaming moves the blocks: same node, new file.
	wf = applyChanges(t, e, wf.ID, map[string]any{"moves": []fileMove{{From: "queues.tf", To: "messaging.tf"}}})
	moved := nodeByAddress(&wf, "aws_sqs_queue.jobs")
	if moved == nil || moved.ID != q.ID || moved.Position != q.Position {
		t.Fatalf("rename changed the node: %+v, was %+v", moved, q)
	}
	if wf.NodeFiles[q.ID] != "messaging.tf" {
		t.Errorf("renamed file = %q", wf.NodeFiles[q.ID])
	}
	files = listWorkflowFiles(t, e, wf.ID)
	if _, ok := files["queues.tf"]; ok {
		t.Errorf("old name still listed")
	}

	// Deleting a file removes its blocks.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{"messaging.tf": nil}})
	if nodeByAddress(&wf, "aws_sqs_queue.jobs") != nil || nodeByID(&wf, bucket) == nil {
		t.Errorf("delete: nodes = %v", wf.Nodes)
	}

	// Code without blocks is kept as a file, then imported once it has some.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{"notes.tf": new("# Topics go here.\n")}})
	files = listWorkflowFiles(t, e, wf.ID)
	if f, ok := files["notes.tf"]; !ok || f.Generated {
		t.Fatalf("comment-only notes.tf = %+v (listed %v)", f, ok)
	}
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"notes.tf": new("# Topics go here.\nresource \"aws_sns_topic\" \"alerts\" {}\n"),
	}})
	if n := nodeByAddress(&wf, "aws_sns_topic.alerts"); n == nil || wf.NodeFiles[n.ID] != "notes.tf" {
		t.Fatalf("notes.tf not imported: %v", wf.NodeFiles)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "notes.tf")); err == nil {
		t.Errorf("imported notes.tf kept as a stored file")
	}
	// An emptied file goes away.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{"notes.tf": new("")}})
	if _, ok := listWorkflowFiles(t, e, wf.ID)["notes.tf"]; ok || nodeByAddress(&wf, "aws_sns_topic.alerts") != nil {
		t.Errorf("emptied notes.tf kept")
	}
}

func TestCodeChangesModuleFolderAndAssets(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Modules")
	wf, _ = e.addNode(wf.ID, "aws_s3_bucket")

	var sc model.ModuleScaffold
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/scaffold", map[string]string{"name": "queues"}, &sc)
	want := []string{"modules/queues/README.md", "modules/queues/main.tf", "modules/queues/outputs.tf", "modules/queues/variables.tf", "modules/queues/versions.tf"}
	var got []string
	for p := range sc.Files {
		got = append(got, p)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("scaffold files = %v", got)
	}
	if v := sc.Files["modules/queues/versions.tf"]; !strings.Contains(v, `source  = "hashicorp/aws"`) || !strings.Contains(v, `version = ">= `) || strings.Contains(v, "~>") {
		t.Errorf("versions.tf:\n%s", v)
	}
	if sc.Call.Path != "main.tf" || !strings.Contains(sc.Call.Block, `source = "./modules/queues"`) {
		t.Errorf("call = %+v", sc.Call)
	}
	for _, bad := range []string{"Queues!", "../x", ""} {
		if code := e.call("POST", "/workflows/"+wf.ID+"/modules/scaffold", map[string]string{"name": bad}, nil); code != http.StatusBadRequest {
			t.Errorf("module name %q = %d", bad, code)
		}
	}

	var code struct{ Files map[string]string }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &code)
	changes := map[string]*string{
		"main.tf":                   new(code.Files["main.tf"] + "\n" + sc.Call.Block),
		"templates/user_data.tftpl": new("#!/bin/sh\necho ${name}\n"),
		"scripts/setup.sh":          new("#!/bin/sh\nexit 0\n"),
	}
	for p, c := range sc.Files {
		changes[p] = new(c)
	}
	changes["modules/queues/main.tf"] = new("resource \"aws_sqs_queue\" \"this\" {\n  name = \"jobs\"\n}\n")
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": changes})
	mod := nodeByAddress(&wf, "module.queues")
	if mod == nil || mod.Kind != model.KindModule {
		t.Fatalf("module node missing: %v", wf.Nodes)
	}
	var view model.ModuleView
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/"+mod.ID, nil, &view)
	if view.Workflow == nil || nodeByAddress(view.Workflow, "aws_sqs_queue.this") == nil || view.Dir != "modules/queues" {
		t.Fatalf("module view = %+v", view)
	}

	// Edits inside the module update it.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"modules/queues/main.tf": new("resource \"aws_sqs_queue\" \"this\" {\n  name = \"jobs\"\n}\n\nresource \"aws_sqs_queue\" \"dlq\" {}\n"),
	}})
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/"+mod.ID, nil, &view)
	if view.Workflow == nil || nodeByAddress(view.Workflow, "aws_sqs_queue.dlq") == nil {
		t.Fatalf("module edit not seen: %+v", view.Workflow)
	}

	// Assets are stored and exported.
	zipped := zipNames(t, e, wf.ID)
	for _, p := range []string{"templates/user_data.tftpl", "scripts/setup.sh", "modules/queues/versions.tf", "modules/queues/README.md"} {
		if _, ok := zipped[p]; !ok {
			t.Errorf("export lacks %s", p)
		}
	}

	// Folder rename (moves per file) and delete prune the folders.
	wf = applyChanges(t, e, wf.ID, map[string]any{
		"moves":   []fileMove{{From: "templates/user_data.tftpl", To: "tpl/user_data.tftpl"}},
		"changes": map[string]*string{"scripts/setup.sh": nil},
	})
	assets := e.st.AssetsDir(wf.ID)
	for _, gone := range []string{"templates", "scripts"} {
		if _, err := os.Stat(filepath.Join(assets, gone)); err == nil {
			t.Errorf("%s/ left behind", gone)
		}
	}
	if b, err := os.ReadFile(filepath.Join(assets, "tpl", "user_data.tftpl")); err != nil || !strings.Contains(string(b), "echo") {
		t.Errorf("moved template = %q, %v", b, err)
	}
	if nodeByAddress(&wf, "module.queues") == nil {
		t.Errorf("asset change lost the module")
	}

	// Swapping two files in one request works through the staging folder.
	wf = applyChanges(t, e, wf.ID, map[string]any{"moves": []fileMove{
		{From: "modules/queues/variables.tf", To: "modules/queues/tmp.tf"},
		{From: "modules/queues/outputs.tf", To: "modules/queues/variables.tf"},
		{From: "modules/queues/tmp.tf", To: "modules/queues/outputs.tf"},
	}})
	if b, _ := os.ReadFile(filepath.Join(assets, "modules", "queues", "variables.tf")); !strings.Contains(string(b), "Outputs of") {
		t.Errorf("swap: variables.tf = %q", b)
	}

	// Deleting the module folder and its call removes the module.
	var del = map[string]*string{}
	for p := range listWorkflowFiles(t, e, wf.ID) {
		if strings.HasPrefix(p, "modules/queues/") {
			del[p] = nil
		}
	}
	del["main.tf"] = new(code.Files["main.tf"])
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": del})
	if nodeByAddress(&wf, "module.queues") != nil {
		t.Errorf("module kept")
	}
	if _, err := os.Stat(filepath.Join(assets, "modules")); err == nil {
		t.Errorf("modules/ left behind")
	}
}

func TestCodeChangesRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Bad")
	wf, _ = e.addNode(wf.ID, "aws_s3_bucket")
	before := wf.UpdatedAt
	put := func(body map[string]any) (int, apiErrorBody) {
		var res apiErrorBody
		return e.call("PUT", "/workflows/"+wf.ID+"/terraform", body, &res), res
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"traversal", map[string]any{"changes": map[string]*string{"../x.tf": new("")}}, http.StatusBadRequest},
		{"absolute", map[string]any{"changes": map[string]*string{"/etc/x": new("x")}}, http.StatusBadRequest},
		{"hidden", map[string]any{"changes": map[string]*string{".env": new("x")}}, http.StatusBadRequest},
		{"state", map[string]any{"changes": map[string]*string{"terraform.tfstate": new("{}")}}, http.StatusBadRequest},
		{"clash", map[string]any{"moves": []fileMove{{From: "main.tf", To: "versions.tf"}}}, http.StatusConflict},
		{"scaffold", map[string]any{"changes": map[string]*string{"README.md": new("mine")}}, http.StatusConflict},
		{"delete scaffold", map[string]any{"changes": map[string]*string{".gitignore": nil}}, http.StatusConflict},
		{"missing", map[string]any{"changes": map[string]*string{"nope.tf": nil}}, http.StatusNotFound},
		{"file as folder", map[string]any{"changes": map[string]*string{"main.tf/x.tf": new("")}}, http.StatusConflict},
		{"both", map[string]any{"files": map[string]string{}, "changes": map[string]*string{}}, http.StatusBadRequest},
		{"binary", map[string]any{"changes": map[string]*string{"a.txt": new("a\x00b")}}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got, res := put(c.body); got != c.want {
			t.Errorf("%s = %d, want %d: %s", c.name, got, c.want, res.Error)
		}
	}

	// Syntax errors anywhere (module files too) are located; nothing is written.
	got, res := put(map[string]any{"changes": map[string]*string{
		"modules/x/main.tf": new("resource \"aws_sqs_queue\" \"q\" {\n  name = \n}\n"),
		"notes.md":          new("hi"),
	}})
	if got != http.StatusBadRequest || len(res.Diagnostics) == 0 || res.Diagnostics[0].File != "modules/x/main.tf" || res.Diagnostics[0].Line != 2 {
		t.Fatalf("syntax error = %d %+v", got, res)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "notes.md")); err == nil {
		t.Errorf("a rejected request wrote files")
	}
	var now model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &now)
	if now.UpdatedAt != before {
		t.Errorf("a rejected request saved the workflow")
	}
}

type apiErrorBody struct {
	Error       string             `json:"error"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
}

func TestFormatCode(t *testing.T) {
	e := newEnv(t)
	var res struct {
		Content string `json:"content"`
		Changed bool   `json:"changed"`
	}
	e.mustCall(http.StatusOK, "POST", "/terraform/format", map[string]string{"content": "resource \"a\" \"b\" {\nname=\"x\"\n  tags = {a=1}\n}\n"}, &res)
	if res.Content != "resource \"a\" \"b\" {\n  name = \"x\"\n  tags = { a = 1 }\n}\n" || !res.Changed {
		t.Errorf("formatted = %q", res.Content)
	}
	var bad apiErrorBody
	if got := e.call("POST", "/terraform/format", map[string]string{"content": "x = {\n", "path": "a.tf"}, &bad); got != http.StatusBadRequest || len(bad.Diagnostics) == 0 || bad.Diagnostics[0].File != "a.tf" {
		t.Errorf("syntax error = %d %+v", got, bad)
	}

	// Format on apply formats stored HCL.
	wf := e.createEmpty("Fmt")
	e.addNode(wf.ID, "aws_s3_bucket")
	applyChanges(t, e, wf.ID, map[string]any{"format": true, "changes": map[string]*string{"modules/m/main.tf": new("locals {\na=1\n}\n")}})
	if b, _ := os.ReadFile(filepath.Join(e.st.AssetsDir(wf.ID), "modules", "m", "main.tf")); string(b) != "locals {\n  a = 1\n}\n" {
		t.Errorf("format on apply = %q", b)
	}
}

func TestCodeChangesEnvironmentsLayout(t *testing.T) {
	e := newEnv(t)
	wf := envLayoutWorkflow(t, e)
	files := listWorkflowFiles(t, e, wf.ID)
	for p, editable := range map[string]bool{
		"modules/items_api/main.tf":         true,
		"environments/dev/main.tf":          true,
		"environments/prod/main.tf":         true,
		"environments/dev/providers.tf":     true,
		"environments/prod/providers.tf":    false,
		"environments/dev/variables.tf":     false,
		"environments/dev/terraform.tfvars": false,
	} {
		f := files[p]
		if f.Editable != editable || (f.ReadOnlyReason == "") != editable {
			t.Errorf("%s editable = %v (%q), want %v", p, f.Editable, f.ReadOnlyReason, editable)
		}
		if strings.HasPrefix(p, "environments/") && f.Movable {
			t.Errorf("%s movable", p)
		}
	}
	if !files["modules/items_api/main.tf"].Movable || files["environments/dev/.terraform.lock.hcl"].Movable {
		t.Errorf("movable flags: %+v %+v", files["modules/items_api/main.tf"], files["environments/dev/.terraform.lock.hcl"])
	}

	// New service module code becomes workflow blocks in that file; assets
	// in the service module are stored where AssetPlacement exports them from.
	wf = applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"modules/items_api/queues.tf":         new("resource \"aws_sqs_queue\" \"jobs\" {}\n"),
		"modules/items_api/templates/a.tftpl": new("hello ${name}\n"),
	}})
	q := nodeByAddress(&wf, "aws_sqs_queue.jobs")
	if q == nil || wf.NodeFiles[q.ID] != "queues.tf" {
		t.Fatalf("service module file: %v", wf.NodeFiles)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "templates", "a.tftpl")); err != nil {
		t.Errorf("asset stored at: %v", err)
	}
	if _, ok := listWorkflowFiles(t, e, wf.ID)["modules/items_api/templates/a.tftpl"]; !ok {
		t.Errorf("asset not listed at its export path")
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), ".terraform.lock.hcl")); err != nil {
		t.Errorf("the lock file (placed in every root) was removed: %v", err)
	}

	for name, body := range map[string]map[string]any{
		"new env root file":   {"changes": map[string]*string{"environments/dev/extra.tf": new("locals {}\n")}},
		"delete env root":     {"changes": map[string]*string{"environments/dev/main.tf": nil}},
		"edit derived root":   {"changes": map[string]*string{"environments/dev/variables.tf": new("# mine\n")}},
		"edit other provider": {"changes": map[string]*string{"environments/prod/providers.tf": new("# mine\n")}},
		"root asset":          {"changes": map[string]*string{"notes.md": new("x")}},
		"lock copy":           {"changes": map[string]*string{"environments/dev/.terraform.lock.hcl": nil}},
	} {
		if got := e.call("PUT", "/workflows/"+wf.ID+"/terraform", body, nil); got != http.StatusConflict && got != http.StatusBadRequest {
			t.Errorf("%s = %d", name, got)
		}
	}
}
