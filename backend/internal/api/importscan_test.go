package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// importOne imports a single project and returns its workflow.
func (e *testEnv) importOne(body any) model.Workflow {
	e.t.Helper()
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", body, &res)
	if len(res.Workflows) != 1 {
		e.t.Fatalf("import returned %d workflows, want 1", len(res.Workflows))
	}
	return res.Workflows[0]
}

func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixtureFiles reads the fixture as a browser folder upload: every path
// prefixed with the folder name.
func fixtureFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files["repo/"+filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func projectPaths(scan model.ImportScan) []string {
	var out []string
	for _, p := range scan.Projects {
		out = append(out, p.Path)
	}
	return out
}

func TestScanFixtureFindsEnvironmentsAndSkipsModules(t *testing.T) {
	e := newEnv(t)
	root := fixtureRepo(t)
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": root}, &scan)
	if !slices.Equal(projectPaths(scan), []string{"envs/dev", "envs/prod"}) || scan.Root != root {
		t.Fatalf("scan root=%q projects=%v", scan.Root, projectPaths(scan))
	}
	wantSkipped := []string{
		"modules/network: local module used by envs/dev, envs/prod",
		"modules/network/modules/subnets: local module used by modules/network",
	}
	if !slices.Equal(scan.Skipped, wantSkipped) {
		t.Fatalf("skipped = %q", scan.Skipped)
	}
	dev, prod := scan.Projects[0], scan.Projects[1]
	if dev.Name != "dev" || prod.Name != "prod" {
		t.Fatalf("names = %q, %q", dev.Name, prod.Name)
	}
	if dev.Resources != 2 || dev.DataSources != 1 || dev.Modules != 2 || !dev.HasBackend ||
		!slices.Equal(dev.Providers, []string{"archive", "aws"}) || !slices.Equal(dev.Clouds, []model.Cloud{model.CloudAWS}) ||
		!slices.Equal(dev.TfvarsFiles, []string{"terraform.tfvars"}) || len(dev.Diagnostics) != 0 {
		t.Fatalf("dev = %+v", dev)
	}
	if !slices.Equal(prod.TfvarsFiles, []string{"environments/dr.tfvars", "terraform.tfvars"}) {
		t.Fatalf("prod tfvars = %v", prod.TfvarsFiles)
	}

	// A browser folder upload scans the same way; its folder names the root.
	var fromFiles model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]any{"files": fixtureFiles(t, root)}, &fromFiles)
	if fromFiles.Root != "repo" || !slices.Equal(projectPaths(fromFiles), projectPaths(scan)) || !slices.Equal(fromFiles.Skipped, wantSkipped) {
		t.Fatalf("files scan root=%q projects=%v skipped=%v", fromFiles.Root, projectPaths(fromFiles), fromFiles.Skipped)
	}
}

func moduleSource(t *testing.T, wf *model.Workflow, name string) string {
	t.Helper()
	for _, nd := range wf.Nodes {
		if nd.Kind == model.KindModule && nd.Name == name {
			for _, a := range nd.Body.Attrs {
				if a.Name == "source" {
					var s string
					if err := json.Unmarshal(a.Value.Literal, &s); err != nil {
						t.Fatalf("module.%s source %+v", name, a.Value)
					}
					return s
				}
			}
		}
	}
	t.Fatalf("module.%s not found in %s", name, wf.Name)
	return ""
}

func TestImportFixtureProjectsWithLocalModules(t *testing.T) {
	e := newEnv(t)
	root := fixtureRepo(t)
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{
		"dir": root, "projects": []string{"envs/dev", "envs/prod"}, "names": map[string]string{"envs/prod": "Production"},
	}, &res)
	if len(res.Workflows) != 2 || res.Workflows[0].Name != "dev" || res.Workflows[1].Name != "Production" {
		t.Fatalf("workflows = %d", len(res.Workflows))
	}
	origNetwork, _ := os.ReadFile(filepath.Join(root, "modules", "network", "main.tf"))
	origSubnets, _ := os.ReadFile(filepath.Join(root, "modules", "network", "modules", "subnets", "main.tf"))
	for _, wf := range res.Workflows {
		if got := moduleSource(t, &wf, "network"); got != "./modules/network" {
			t.Fatalf("%s: module.network source = %q", wf.Name, got)
		}
		if got := moduleSource(t, &wf, "logs"); got != "terraform-aws-modules/s3-bucket/aws" {
			t.Fatalf("%s: registry source rewritten to %q", wf.Name, got)
		}
		if errs := errorDiags(&wf); len(errs) != 0 {
			t.Fatalf("%s: error diagnostics %+v", wf.Name, errs)
		}
		assets := e.st.AssetsDir(wf.ID)
		var files []string
		filepath.WalkDir(assets, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files = append(files, relSlash(assets, p))
			}
			return nil
		})
		want := []string{"modules/network/README.md", "modules/network/main.tf", "modules/network/versions.tf", "modules/subnets/main.tf", "src/handler.py"}
		if wf.Name == "dev" { // envs/dev has a dependency lock file
			want = append([]string{".terraform.lock.hcl"}, want...)
		}
		if !slices.Equal(files, want) {
			t.Fatalf("%s: assets = %v, want %v", wf.Name, files, want)
		}
		network, _ := os.ReadFile(filepath.Join(assets, "modules", "network", "main.tf"))
		if string(network) != strings.Replace(string(origNetwork), `"./modules/subnets"`, `"../subnets"`, 1) {
			t.Fatalf("%s: rewritten network module:\n%s", wf.Name, network)
		}
		subnets, _ := os.ReadFile(filepath.Join(assets, "modules", "subnets", "main.tf"))
		if string(subnets) != string(origSubnets) {
			t.Fatalf("%s: subnets module changed:\n%s", wf.Name, subnets)
		}
	}
	if envs := res.Workflows[1].Environments; len(envs) != 2 {
		t.Fatalf("prod environments = %+v", envs)
	}

	// Exported Terraform is valid on its own.
	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Log("terraform not installed; skipping fmt/validate of the export")
		return
	}
	for _, wf := range res.Workflows {
		out := filepath.Join(t.TempDir(), "export")
		e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": out, "environments": true}, nil)
		runTerraform(t, tf, out, "fmt", "-check", "-recursive")
		if testing.Short() || !reachable("registry.terraform.io:443") {
			t.Log("short mode or no network; skipping terraform init/validate")
			continue
		}
		runTerraform(t, tf, out, "init", "-backend=false", "-input=false", "-no-color")
		runTerraform(t, tf, out, "validate", "-no-color")
	}
}

func reachable(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func runTerraform(t *testing.T, tf, dir string, args ...string) {
	t.Helper()
	cache := filepath.Join(os.TempDir(), "nimbo-test-tf-plugins")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tf, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TF_PLUGIN_CACHE_DIR="+cache, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func TestImportFilesProjectsAndRootDefault(t *testing.T) {
	e := newEnv(t)
	files := fixtureFiles(t, fixtureRepo(t))
	// Without projects only the root is imported, and the fixture root has no
	// Terraform: the error points at the projects found.
	var apiErr struct {
		Error string `json:"error"`
	}
	if got := e.call("POST", "/import", map[string]any{"files": files}, &apiErr); got != http.StatusBadRequest ||
		!strings.Contains(apiErr.Error, "envs/dev, envs/prod") {
		t.Fatalf("root import status %d: %q", got, apiErr.Error)
	}
	wf := e.importOne(map[string]any{"files": files, "projects": []string{"envs/dev"}, "name": "Dev API"})
	if wf.Name != "Dev API" || moduleSource(t, &wf, "network") != "./modules/network" {
		t.Fatalf("files import = %q", wf.Name)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "modules", "subnets", "main.tf")); err != nil {
		t.Fatal(err)
	}
	if got := e.call("POST", "/import", map[string]any{"files": files, "projects": []string{"modules/nope"}}, nil); got != http.StatusBadRequest {
		t.Fatalf("unknown project status %d", got)
	}
}

func TestModuleViewLocalNestedAndRegistry(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/dev"}})

	var view model.ModuleView
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/module.network", nil, &view)
	if !view.Local || view.Source != "./modules/network" || view.Workflow == nil {
		t.Fatalf("network view = %+v", view)
	}
	var addrs []string
	for _, nd := range view.Workflow.Nodes {
		addrs = append(addrs, nd.ID)
		if nd.Positioned {
			t.Fatalf("module node %s is positioned", nd.ID)
		}
	}
	if !slices.Equal(addrs, []string{"aws_vpc.this", "module.subnets"}) || len(view.Workflow.Edges) == 0 || len(view.Workflow.Variables) != 3 {
		t.Fatalf("network graph nodes=%v edges=%d vars=%d", addrs, len(view.Workflow.Edges), len(view.Workflow.Variables))
	}

	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/module.network?path=module.subnets", nil, &view)
	if !view.Local || view.Source != "../subnets" || view.Workflow == nil || len(view.Workflow.Nodes) != 1 || view.Workflow.Nodes[0].ID != "aws_subnet.this" {
		t.Fatalf("nested view = %+v", view)
	}

	var reg model.ModuleView
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/module.logs", nil, &reg)
	if reg.Local || reg.Workflow != nil || reg.Message != "Registry module terraform-aws-modules/s3-bucket/aws 4.1.2 — contents are downloaded at deploy time" {
		t.Fatalf("registry view = %+v", reg)
	}
	if got := e.call("GET", "/workflows/"+wf.ID+"/modules/aws_lambda_function.api", nil, nil); got != http.StatusBadRequest {
		t.Fatalf("non-module status %d", got)
	}
}

func TestModuleTreeLocalNestedAndDownloaded(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/dev"}})

	var tree model.ModuleTree
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules", nil, &tree)
	network, subnets, logs := tree.Calls["module.network"], tree.Calls["module.network.module.subnets"], tree.Calls["module.logs"]
	if !network.Local || network.Dir != "modules/network" || !subnets.Local || subnets.Source != "../subnets" || subnets.Dir != "modules/subnets" {
		t.Fatalf("local calls = %+v / %+v", network, subnets)
	}
	if logs.Local || logs.Dir != "" || logs.Source != "terraform-aws-modules/s3-bucket/aws" || logs.Version != "4.1.2" {
		t.Fatalf("registry call before init = %+v", logs)
	}
	if m := tree.Modules["modules/subnets"]; m == nil || len(m.Nodes) != 1 || m.Nodes[0].ID != "aws_subnet.this" || len(tree.Modules) != 2 {
		t.Fatalf("modules = %v", tree.Modules)
	}

	// After `terraform init` the registry module is read from the module cache.
	work := filepath.Join(e.home, "workflows", wf.ID, "work")
	writeFile(t, filepath.Join(work, ".terraform", "modules", "modules.json"),
		`{"Modules":[{"Key":"","Source":"","Dir":"."},{"Key":"logs","Source":"registry.terraform.io/terraform-aws-modules/s3-bucket/aws","Version":"4.1.2","Dir":".terraform/modules/logs"},{"Key":"evil","Dir":"../../x"}]}`)
	writeFile(t, filepath.Join(work, ".terraform", "modules", "logs", "main.tf"), `resource "aws_s3_bucket" "this" {}`)
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules", nil, &tree)
	logs = tree.Calls["module.logs"]
	if m := tree.Modules[logs.Dir]; logs.Dir != ".terraform/modules/logs" || m == nil || len(m.Nodes) != 1 || m.Nodes[0].ID != "aws_s3_bucket.this" {
		t.Fatalf("downloaded call = %+v", logs)
	}
}

func TestDescribeRemoteModuleSources(t *testing.T) {
	for source, want := range map[string]string{
		"git::https://example.com/vpc.git?ref=v1":       "Remote module git::https://example.com/vpc.git?ref=v1 — contents are downloaded at deploy time",
		"github.com/acme/terraform-modules//vpc":        "Remote module github.com/acme/terraform-modules//vpc — contents are downloaded at deploy time",
		"app.terraform.io/acme/vpc/aws":                 "Registry module app.terraform.io/acme/vpc/aws — contents are downloaded at deploy time",
		"terraform-aws-modules/vpc/aws//modules/vpc-ep": "Registry module terraform-aws-modules/vpc/aws//modules/vpc-ep — contents are downloaded at deploy time",
	} {
		if view, dir := describeModule(source, "", "/a", "/a"); dir != "" || view.Message != want {
			t.Errorf("%s: %q", source, view.Message)
		}
	}
	if _, dir := describeModule("../../etc", "", "/a/b", "/a/b"); dir != "" {
		t.Error("local source escaping the assets dir was resolved")
	}
}

func TestScanNamesClashingProjectsAndFlagsUnusedModules(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	for _, dir := range []string{"stacks/api/dev", "stacks/web/dev", "stacks/web/prod", "modules/unused"} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(dir), "main.tf"), `resource "aws_sqs_queue" "q" {}`)
	}
	writeFile(t, filepath.Join(root, "node_modules", "x", "main.tf"), `resource "aws_sqs_queue" "q" {}`)
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": root}, &scan)
	names := map[string]string{}
	unused := map[string]bool{}
	for _, p := range scan.Projects {
		names[p.Path] = p.Name
		for _, d := range p.Diagnostics {
			unused[p.Path] = unused[p.Path] || d.Code == "unused_module"
		}
	}
	want := map[string]string{"modules/unused": "unused", "stacks/api/dev": "api-dev", "stacks/web/dev": "web-dev", "stacks/web/prod": "prod"}
	if !maps.Equal(names, want) || !maps.Equal(unused, map[string]bool{"modules/unused": true}) {
		t.Fatalf("names = %v, unused = %v", names, unused)
	}
}

func TestScanAndImportReportFilesNotImported(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app", "main.tf"), `module "lib" {
  source = "../lib"
}

resource "aws_lambda_function" "f" {
  filename = "${path.module}/../build/f.zip"
}
`)
	writeFile(t, filepath.Join(root, "app", "override.tf"), `resource "aws_lambda_function" "f" {
  timeout = 30
}
`)
	writeFile(t, filepath.Join(root, "app", "extra.tf.json"), `{"variable": {"x": {}}}`)
	writeFile(t, filepath.Join(root, "lib", "main.tf"), `output "policy" {
  value = file("../shared/policy.json")
}
`)
	writeFile(t, filepath.Join(root, "json-only", "main.tf.json"), `{"resource": {}}`)

	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": root}, &scan)
	if !slices.ContainsFunc(scan.Skipped, func(s string) bool { return strings.HasPrefix(s, "json-only: only JSON configuration (main.tf.json)") }) {
		t.Errorf("skipped = %v", scan.Skipped)
	}
	if len(scan.Projects) != 1 || scan.Projects[0].Path != "app" || scan.Projects[0].Resources != 1 {
		t.Fatalf("projects = %+v", scan.Projects)
	}
	codes := func(diags []model.Diagnostic) []string {
		var out []string
		for _, d := range diags {
			out = append(out, d.Code+": "+d.Message)
		}
		slices.Sort(out)
		return out
	}
	want := []string{
		"path_outside_folder: main.tf refers to files outside its folder (${path.module}/../build/f.zip)",
		"path_outside_folder: main.tf refers to files outside its folder (../shared/policy.json)",
		"unsupported_file: extra.tf.json is not shown on the canvas: JSON configuration is not supported",
		"unsupported_file: override.tf is not shown on the canvas: override files are merged by Terraform into other blocks and are not imported",
	}
	if got := codes(scan.Projects[0].Diagnostics); !slices.Equal(got, want) {
		t.Errorf("scan diagnostics =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The import reports the same problems and keeps the files as assets.
	wf := e.importOne(map[string]any{"dir": root, "projects": []string{"app"}})
	var got []string
	for _, c := range codes(wf.Diagnostics) {
		if strings.HasPrefix(c, "path_outside_folder") || strings.HasPrefix(c, "unsupported_file") {
			got = append(got, c)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("workflow diagnostics =\n%s", strings.Join(got, "\n"))
	}
	for _, name := range []string{"override.tf", "extra.tf.json"} {
		if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), name)); err != nil {
			t.Errorf("%s not kept: %v", name, err)
		}
	}
	if len(wf.Nodes) != 2 { // module.lib and the function, not the override
		t.Errorf("nodes = %d", len(wf.Nodes))
	}
}
