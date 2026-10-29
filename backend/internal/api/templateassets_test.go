package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const workerHandler = "services/worker/src/index.mjs"

// templateHandler is the worker source the aws-queue-worker template ships.
func templateHandler(t *testing.T) []byte {
	t.Helper()
	files, ok := testCatalog.TemplateFiles("aws-queue-worker")
	if !ok || files[workerHandler] == nil {
		t.Fatalf("aws-queue-worker ships no %s", workerHandler)
	}
	return files[workerHandler]
}

// assertAssets fails unless wf's assets hold the template's worker source
// and no Terraform file, and the archive zips it with no inline code.
func assertAssets(t *testing.T, e *testEnv, wf *model.Workflow) {
	t.Helper()
	assets := e.st.AssetsDir(wf.ID)
	if got, err := os.ReadFile(filepath.Join(assets, workerHandler)); err != nil || !bytes.Equal(got, templateHandler(t)) {
		t.Errorf("asset %s: %v\n%s", workerHandler, err, got)
	}
	if _, err := os.Stat(filepath.Join(assets, "main.tf")); err == nil {
		t.Error("template Terraform stored as an asset")
	}
	for _, d := range wf.Diagnostics {
		if d.Code == "bp_inline_function_code" || d.Severity == model.SeverityError {
			t.Errorf("diagnostic %+v", d)
		}
	}
}

// zipsWorker reports whether a .tf file directly in dir ("" = the root) of
// an exported tree zips the worker sources through path.module.
func zipsWorker(tree map[string][]byte, dir string) bool {
	for name, data := range tree {
		rest, ok := strings.CutPrefix(name, dir)
		if ok && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".tf") &&
			strings.Contains(string(data), `source_dir  = "${path.module}/services/worker/src"`) {
			return true
		}
	}
	return false
}

// A template's function sources become workflow assets: stored on create
// and exported next to the Terraform; in the environments layout they move
// into the service module with the archive_file, so path.module still finds
// them. (Runs copy the assets into the work dir: the Floci test in
// templateassets_docker_test.go deploys this workflow.)
func TestTemplateAssetsCreateExport(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Jobs", "templateId": "aws-queue-worker"}, &wf)
	assertAssets(t, e, &wf)
	want := templateHandler(t)

	single := filepath.Join(t.TempDir(), "single")
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": single}, nil)
	tree := readDirTree(t, single)
	if !bytes.Equal(tree[workerHandler], want) {
		t.Errorf("export lacks %s", workerHandler)
	}
	if !zipsWorker(tree, "") {
		t.Error("no root .tf file zips ${path.module}/services/worker/src")
	}

	wf.Environments = []model.Environment{
		{Name: "dev", Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}},
		{Name: "prod", Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}},
	}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	envs := filepath.Join(t.TempDir(), "envs")
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": envs, "environments": true}, nil)
	tree = readDirTree(t, envs)
	var module string
	for name, data := range tree {
		if strings.HasPrefix(name, "modules/") && strings.HasSuffix(name, "/"+workerHandler) && bytes.Equal(data, want) {
			module = strings.TrimSuffix(name, workerHandler)
		}
	}
	if module == "" {
		t.Fatalf("environments export lacks modules/<service>/%s", workerHandler)
	}
	if !zipsWorker(tree, module) {
		t.Errorf("no .tf file of %s zips ${path.module}/services/worker/src", module)
	}
}

// A challenge starting from a Terraform template gets its assets too.
func TestTemplateAssetsChallengeStarter(t *testing.T) {
	fsys := fstest.MapFS{"20260101000000_aws_worker.yaml": {Data: []byte(`description: AWS worker
operations:
  - op: upsert
    kind: challenge
    id: aws-worker
    data:
      provider: aws
      level: beginner
      title: Queue worker
      summary: Practice queue workers
      minutes: 10
      tags: [queues]
      runtime: local
      problem: Make the worker work.
      requirements: [It works]
      hints: [Read the docs]
      solution: Do it.
      starter: { templateId: aws-queue-worker }
      checks:
        - { id: valid, label: The project is valid, type: validate }
      adaptations: {}
      sources:
        - { title: Lambda with SQS, url: "https://docs.aws.amazon.com/lambda/latest/dg/with-sqs.html" }
      solvableWithoutCode: false
`)}}
	e := newEnvWith(t, "", fsys)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/learn/challenges/aws-worker/start", map[string]string{}, &wf)
	assertAssets(t, e, &wf)
}

// Adding a Lambda writes its handler as a workflow file the archive zips;
// a file already there (the user's code) is kept.
func TestAddNodeWritesStarterFiles(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("fn")
	handler := filepath.Join(e.st.AssetsDir(wf.ID), "services", "function", "src", "index.mjs")
	wf, _ = e.addNode(wf.ID, "aws_lambda_function")
	got, err := os.ReadFile(handler)
	if err != nil || !strings.Contains(string(got), "export const handler") {
		t.Fatalf("handler: %v\n%s", err, got)
	}
	for _, d := range wf.Diagnostics {
		if d.Code == "bp_inline_function_code" {
			t.Errorf("diagnostic %+v", d)
		}
	}

	mine := filepath.Join(e.st.AssetsDir(wf.ID), "services", "function2", "src", "index.mjs")
	writeFile(t, mine, "export const handler = async () => 'mine';\n")
	e.addNode(wf.ID, "aws_lambda_function")
	if got, _ := os.ReadFile(mine); string(got) != "export const handler = async () => 'mine';\n" {
		t.Errorf("existing file overwritten:\n%s", got)
	}
}
