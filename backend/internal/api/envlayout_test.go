package api

import (
	"bytes"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func readDirTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// envLayoutWorkflow is aws-serverless-api with its table extracted into a
// local module, a dependency lock file and dev/prod environments.
func envLayoutWorkflow(t *testing.T, e *testEnv) model.Workflow {
	t.Helper()
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Items API", "templateId": "aws-serverless-api"}, &wf)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/extract",
		model.ModuleExtractRequest{NodeIDs: []string{"aws_dynamodb_table.items"}, Name: "data"}, &wf)
	writeFile(t, filepath.Join(e.st.AssetsDir(wf.ID), ".terraform.lock.hcl"), "# lock\n")
	if wf.Layout != model.LayoutAuto {
		t.Fatalf("new workflow layout = %q", wf.Layout)
	}
	str := func(s string) model.Value { return model.Value{Expr: `"` + s + `"`} }
	wf.Environments = []model.Environment{
		{Name: "dev", Values: map[string]model.Value{"environment": str("dev")}, EnvVars: []model.EnvVar{}},
		{Name: "prod", Values: map[string]model.Value{"environment": str("prod"), "lambda_memory_mb": {Expr: "1024"}}, EnvVars: []model.EnvVar{}},
	}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	return wf
}

func TestEnvironmentsLayoutExportImportRoundTrip(t *testing.T) {
	e := newEnv(t)
	wf := envLayoutWorkflow(t, e)

	first := filepath.Join(t.TempDir(), "first")
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": first, "environments": true}, nil)
	tree := readDirTree(t, first)
	for _, want := range []string{
		"Makefile", "README.md", ".gitignore",
		"modules/items_api/main.tf", "modules/items_api/versions.tf", "modules/items_api/README.md",
		"modules/data/main.tf", "modules/data/versions.tf",
		"environments/dev/main.tf", "environments/dev/backend.tf", "environments/dev/terraform.tfvars", "environments/dev/.terraform.lock.hcl",
		"environments/prod/main.tf", "environments/prod/.terraform.lock.hcl",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("export lacks %s; has %v", want, slices.Sorted(maps.Keys(tree)))
		}
	}
	for name := range tree {
		if !strings.Contains(name, "/") && strings.HasSuffix(name, ".tf") {
			t.Errorf("root module file %s in the environments layout", name)
		}
	}
	if s := string(tree["modules/items_api/main.tf"]); !strings.Contains(s, `"../data"`) {
		t.Errorf("service module does not call ../data:\n%s", s)
	}

	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]any{"dir": first}, &scan)
	if len(scan.Projects) != 1 || scan.Projects[0].Path != "." || scan.Projects[0].Layout != model.LayoutEnvironments ||
		!slices.Equal(scan.Projects[0].Environments, []string{"dev", "prod"}) {
		t.Fatalf("scan = %+v", scan.Projects)
	}
	back := e.importOne(map[string]any{"dir": first, "name": "Items API"})
	if back.Layout != model.LayoutEnvironments || back.Service != "items_api" || len(back.Environments) != 2 {
		t.Fatalf("imported layout %q service %q environments %+v", back.Layout, back.Service, back.Environments)
	}
	if got := moduleSource(t, &back, "data"); got != "./modules/data" {
		t.Errorf("module source %q", got)
	}
	for _, d := range back.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("error after import: %+v", d)
		}
	}

	second := filepath.Join(t.TempDir(), "second")
	e.mustCall(http.StatusOK, "POST", "/workflows/"+back.ID+"/export", map[string]any{"dir": second, "environments": true}, nil)
	again := readDirTree(t, second)
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		// READMEs show the description, app data the tree does not carry.
		if filepath.Base(name) != hclcodec.ReadmeFile && !bytes.Equal(tree[name], again[name]) {
			t.Errorf("%s differs after import:\n--- first\n%s\n--- second\n%s", name, tree[name], again[name])
		}
	}
	if a, b := slices.Sorted(maps.Keys(tree)), slices.Sorted(maps.Keys(again)); !slices.Equal(a, b) {
		t.Errorf("trees differ:\n%v\n%v", a, b)
	}

	// Renaming keeps the service module (and so every state address).
	back.Name = "Renamed"
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+back.ID, back, &back)
	var code struct{ Files map[string]string }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+back.ID+"/terraform", nil, &code)
	if _, ok := code.Files["modules/items_api/main.tf"]; !ok {
		t.Errorf("rename moved the service module: %v", slices.Sorted(maps.Keys(code.Files)))
	}
}

func TestEnvironmentsLayoutCodeTabAndLayoutField(t *testing.T) {
	e := newEnv(t)
	wf := envLayoutWorkflow(t, e)

	var code struct{ Files map[string]string }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &code)
	for name := range code.Files {
		if !strings.HasSuffix(name, ".tf") || !(strings.HasPrefix(name, "modules/items_api/") || strings.HasPrefix(name, "environments/")) {
			t.Errorf("code file %s", name)
		}
	}
	// The files view shows the layout; its code is editable.
	var listing struct{ Files []model.WorkflowFile }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/files", nil, &listing)
	byPath := map[string]model.WorkflowFile{}
	for _, f := range listing.Files {
		byPath[f.Path] = f
	}
	for _, p := range []string{"modules/items_api/main.tf", "environments/prod/main.tf"} {
		if f := byPath[p]; f.Kind != model.FileTerraform || !f.Editable || !f.Generated {
			t.Errorf("%s = %+v", p, f)
		}
	}
	if f := byPath["Makefile"]; f.Kind != model.FileScaffold {
		t.Errorf("Makefile = %+v", f)
	}
	if f := byPath["environments/dev/.terraform.lock.hcl"]; f.Generated || f.Path == "" {
		t.Errorf("lock file = %+v", f)
	}
	var content model.WorkflowFileContent
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/files/content?path=environments/dev/.terraform.lock.hcl", nil, &content)
	if content.Content != "# lock\n" {
		t.Errorf("lock file content %q", content.Content)
	}

	// Saving the code unchanged changes nothing; an edit lands in the model.
	var same model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": code.Files}, &same)
	var after struct{ Files map[string]string }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &after)
	for name, src := range code.Files {
		if after.Files[name] != src {
			t.Errorf("%s changed by a no-op save:\n%s\nwant\n%s", name, after.Files[name], src)
		}
	}
	if same.Layout != model.LayoutAuto || len(same.Environments) != 2 || same.Environments[1].Values["lambda_memory_mb"].Expr != "1024" {
		t.Errorf("no-op save changed layout %q / environments %+v", same.Layout, same.Environments)
	}
	for _, v := range same.Variables {
		if v.Name == "lambda_memory_mb" && !slices.ContainsFunc(v.Body.Attrs, func(a model.Attr) bool { return a.Name == "default" }) {
			t.Errorf("lambda_memory_mb lost its default: %+v", v.Body.Attrs)
		}
	}
	edited := maps.Clone(code.Files)
	edited["modules/items_api/main.tf"] += "\nresource \"aws_sqs_queue\" \"extra\" {\n  name = \"extra\"\n}\n"
	var next model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": edited}, &next)
	if !slices.ContainsFunc(next.Nodes, func(n model.Node) bool { return hclcodec.Address(&n) == "aws_sqs_queue.extra" }) {
		t.Errorf("edit not imported")
	}

	// Layout: validated, kept when a body omits it.
	var bad struct{ Error string }
	next.Layout = "flat"
	if got := e.call("PUT", "/workflows/"+wf.ID, next, &bad); got != http.StatusBadRequest {
		t.Errorf("invalid layout accepted: %d", got)
	}
	next.Layout = model.LayoutSingle
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, next, &next)
	var raw map[string]any
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &raw)
	delete(raw, "layout")
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, raw, &next)
	if next.Layout != model.LayoutSingle {
		t.Errorf("layout lost by a body without it: %q", next.Layout)
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &code)
	if _, ok := code.Files["main.tf"]; !ok {
		t.Errorf("single layout code: %v", slices.Sorted(maps.Keys(code.Files)))
	}
}

// Imported single roots keep their layout, whatever their variable files.
func TestImportSingleRootKeepsSingleLayout(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/prod"}})
	if wf.Layout != model.LayoutSingle {
		t.Errorf("layout = %q", wf.Layout)
	}
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]any{"dir": fixtureRepo(t)}, &scan)
	for _, p := range scan.Projects {
		if p.Layout != model.LayoutSingle {
			t.Errorf("%s scanned as %q: its roots hold resources", p.Path, p.Layout)
		}
	}
}
