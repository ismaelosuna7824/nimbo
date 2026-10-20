//go:build docker

package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type recorder struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *recorder) Publish(e model.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[e.Type]++
}

func (r *recorder) count(typ string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[typ]
}

func newTestManager(t *testing.T) (*Manager, *recorder) {
	t.Helper()
	rec := &recorder{counts: map[string]int{}}
	m, err := New(Config{Home: t.TempDir(), Events: rec})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DockerAvailable(context.Background()); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	return m, rec
}

func testWorkflowID(prefix string) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

// cleanupSandbox removes the sandbox and asserts nothing named nimbo-<wf>
// is left behind.
func cleanupSandbox(t *testing.T, m *Manager, wfID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := m.Remove(ctx, wfID); err != nil {
		t.Errorf("Remove: %v", err)
	}
	prefix := "nimbo-" + wfID
	for _, args := range [][]string{
		{"ps", "-a", "--format", "{{.Names}}", "--filter", "name=" + prefix},
		{"network", "ls", "--format", "{{.Name}}", "--filter", "name=" + prefix},
		{"volume", "ls", "-q", "--filter", "label=floci_namespace=" + prefix},
		{"volume", "ls", "-q", "--filter", "name=" + prefix},
		{"ps", "-a", "--format", "{{.Names}}", "--filter", "label=floci_namespace=" + prefix},
		{"ps", "-a", "--format", "{{.Names}}", "--filter", "label=" + labelWorkflow + "=" + wfID},
	} {
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Errorf("docker %v: %v", args, err)
			continue
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("leftovers after Remove (docker %s): %s", args[0], s)
		}
	}
	if _, err := os.Stat(m.WorkDir(wfID)); !os.IsNotExist(err) {
		t.Errorf("workdir still exists after Remove")
	}
}

func runAndWait(t *testing.T, m *Manager, req RunRequest) *model.Run {
	t.Helper()
	run, err := m.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run(%s): %v", req.Action, err)
	}
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		r, ok := m.GetRun(run.ID)
		if !ok {
			t.Fatalf("run %s disappeared", run.ID)
		}
		if r.EndedAt != nil {
			if r.Status != model.RunSucceeded {
				var b strings.Builder
				for _, l := range r.Log {
					fmt.Fprintf(&b, "[%s] %s\n", l.Stream, l.Text)
				}
				t.Fatalf("%s run %s: %s\n%s", req.Action, r.Status, r.Error, b.String())
			}
			t.Logf("%s run took %s", req.Action, r.EndedAt.Sub(*r.StartedAt).Round(time.Second))
			return r
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("run %s did not finish", run.ID)
	return nil
}

func node(id string, kind model.NodeKind, typ, name string) model.Node {
	return model.Node{ID: id, Kind: kind, Type: typ, Name: name}
}

const awsMain = `
terraform {
  required_providers {
    aws     = { source = "hashicorp/aws" }
    archive = { source = "hashicorp/archive" }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "b" {
  bucket = "nimbo-it-bucket"
}

resource "aws_sqs_queue" "q" {
  name = "nimbo-it-queue"
}

resource "aws_dynamodb_table" "t" {
  name         = "nimbo-it-table"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"
  attribute {
    name = "id"
    type = "S"
  }
}

data "archive_file" "zip" {
  type        = "zip"
  source_dir  = "${path.module}/src"
  output_path = "${path.module}/build/fn.zip"
}

resource "aws_iam_role" "role" {
  name = "nimbo-it-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "lambda.amazonaws.com" } }]
  })
}

resource "aws_lambda_function" "fn" {
  function_name    = "nimbo-it-fn"
  role             = aws_iam_role.role.arn
  handler          = "index.handler"
  runtime          = "nodejs22.x"
  filename         = data.archive_file.zip.output_path
  source_code_hash = data.archive_file.zip.output_base64sha256
}

resource "aws_apigatewayv2_api" "api" {
  name          = "nimbo-it-api"
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "int" {
  api_id                 = aws_apigatewayv2_api.api.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.fn.invoke_arn
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_route" "hello" {
  api_id    = aws_apigatewayv2_api.api.id
  route_key = "GET /hello"
  target    = "integrations/${aws_apigatewayv2_integration.int.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.api.id
  name        = "$default"
  auto_deploy = true
}

resource "aws_lambda_permission" "api" {
  statement_id  = "AllowAPIGateway"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.fn.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.api.execution_arn}/*/*"
}

output "api_id" {
  value = aws_apigatewayv2_api.api.id
}

output "api_endpoint" {
  value = aws_apigatewayv2_api.api.api_endpoint
}
`

func httpGetBody(t *testing.T, url string) (int, string) {
	t.Helper()
	var lastErr error
	for range 30 {
		resp, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return resp.StatusCode, string(body)
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("GET %s: %v", url, lastErr)
	return 0, ""
}

func TestAWSSandboxEndToEnd(t *testing.T) {
	m, rec := newTestManager(t)
	wfID := testWorkflowID("it-aws")
	t.Cleanup(func() { cleanupSandbox(t, m, wfID) })

	assets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assets, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := `export const handler = async (event) => ({ statusCode: 200, headers: { "content-type": "application/json" }, body: JSON.stringify({ ok: true, path: event.rawPath }) });`
	if err := os.WriteFile(filepath.Join(assets, "src", "index.mjs"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}

	wf := &model.Workflow{
		ID:     wfID,
		Clouds: []model.Cloud{model.CloudAWS},
		Providers: []model.NamedBlock{
			{Name: "aws", Body: model.Body{Attrs: []model.Attr{{Name: "region", Value: model.Value{Expr: `"us-east-1"`}}}}},
		},
		Nodes: []model.Node{
			node("n-b", model.KindResource, "aws_s3_bucket", "b"),
			node("n-q", model.KindResource, "aws_sqs_queue", "q"),
			node("n-t", model.KindResource, "aws_dynamodb_table", "t"),
			node("n-zip", model.KindData, "archive_file", "zip"),
			node("n-fn", model.KindResource, "aws_lambda_function", "fn"),
			node("n-api", model.KindResource, "aws_apigatewayv2_api", "api"),
		},
	}
	files := map[string][]byte{"main.tf": []byte(awsMain)}

	apply := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: files, AssetsDir: assets})
	if apply.Plan == nil || apply.Plan.Add < 10 {
		t.Errorf("apply summary = %+v, want >= 10 added", apply.Plan)
	}
	if rec.count(model.EventRunLog) == 0 || rec.count(model.EventSandboxStatus) == 0 || rec.count(model.EventRunStatus) < 3 {
		t.Errorf("events not published: %v", rec.counts)
	}

	ctx := context.Background()
	st, err := m.State(ctx, wf)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	mapped := map[string]bool{}
	for _, r := range st.Resources {
		if r.NodeID != "" {
			mapped[r.NodeID] = true
		}
	}
	for _, n := range wf.Nodes {
		if !mapped[n.ID] {
			t.Errorf("node %s (%s.%s) not mapped in state", n.ID, n.Type, n.Name)
		}
	}
	apiID, _ := st.Outputs["api_id"].(string)
	endpoint, _ := st.Outputs["api_endpoint"].(string)
	if apiID == "" {
		t.Fatalf("api_id output missing: %v", st.Outputs)
	}
	t.Logf("api_endpoint output: %s", endpoint)

	base, ok := m.EmulatorURL(wfID, model.CloudAWS)
	if !ok {
		t.Fatal("EmulatorURL(aws) not available")
	}
	code, body := httpGetBody(t, base+"/execute-api/"+apiID+"/$default/hello")
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("invoke via EmulatorURL: %d %s", code, body)
	}
	hostURL := m.HostURL(wfID, endpoint+"/hello")
	t.Logf("HostURL(%s/hello) = %s", endpoint, hostURL)
	code, body = httpGetBody(t, hostURL)
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("invoke via HostURL: %d %s", code, body)
	}
	code, body = httpGetBody(t, m.HostURL(wfID, "http://"+apiID+".execute-api.localhost.floci.io:4566/hello"))
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("invoke via HostURL of floci host form: %d %s", code, body)
	}

	sp, err := m.spawned(ctx, wfID)
	if err != nil || len(sp) == 0 {
		t.Errorf("expected Floci-spawned Lambda container on the sandbox network: %v %v", sp, err)
	}
	for _, c := range sp {
		t.Logf("spawned container %s (labels %s)", c.Names, c.Labels)
	}

	status, err := m.Status(ctx, wfID)
	if err != nil || status.State != model.SandboxRunning || len(status.Emulators) != 1 || status.Emulators[0].Version == "" {
		t.Errorf("Status = %+v, %v", status, err)
	}

	// Stop keeps state: after a restart the queue still exists and a plan
	// reports no changes.
	if err := m.Stop(ctx, wfID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s, _ := m.Status(ctx, wfID); s.State != model.SandboxStopped {
		t.Errorf("state after Stop = %s", s.State)
	}
	plan := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionPlan, Files: files, AssetsDir: assets})
	if plan.Plan == nil || plan.Plan.Add != 0 || plan.Plan.Destroy != 0 {
		t.Errorf("plan after stop/start = %+v, want no changes", plan.Plan)
	}

	destroy := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionDestroy, Files: files, AssetsDir: assets})
	if destroy.Plan == nil || destroy.Plan.Destroy < 10 {
		t.Errorf("destroy summary = %+v", destroy.Plan)
	}
	st, err = m.State(ctx, wf)
	if err != nil || len(st.Resources) != 0 {
		t.Errorf("state after destroy: %d resources, %v", len(st.Resources), err)
	}

	runs := m.Runs(wfID)
	if len(runs) != 3 || runs[0].ID != destroy.ID || runs[0].Log != nil {
		t.Errorf("Runs = %d entries, newest %v", len(runs), runs[0].ID)
	}
	m2, _ := New(Config{Home: m.cfg.Home})
	if got := m2.Runs(wfID); len(got) != 3 {
		t.Errorf("persisted history: %d runs", len(got))
	}
	if r, ok := m2.GetRun(apply.ID); !ok || len(r.Log) == 0 {
		t.Errorf("GetRun from disk failed")
	}
}

func TestRunCancel(t *testing.T) {
	m, _ := newTestManager(t)
	wfID := testWorkflowID("it-cancel")
	t.Cleanup(func() { cleanupSandbox(t, m, wfID) })
	wf := &model.Workflow{ID: wfID}
	files := map[string][]byte{"main.tf": []byte(`
terraform {
  required_providers {
    time = { source = "hashicorp/time" }
  }
}
resource "time_sleep" "wait" {
  create_duration = "5m"
}
`)}
	run, err := m.Run(context.Background(), RunRequest{Workflow: wf, Action: model.ActionApply, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(context.Background(), RunRequest{Workflow: wf, Action: model.ActionPlan, Files: files}); err == nil {
		t.Errorf("second concurrent run accepted")
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		r, _ := m.GetRun(run.ID)
		if len(r.Log) > 0 && strings.Contains(r.Log[len(r.Log)-1].Text, "Still creating") {
			break
		}
		time.Sleep(time.Second)
	}
	if err := m.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		r, _ := m.GetRun(run.ID)
		if r.EndedAt != nil {
			if r.Status != model.RunCanceled {
				t.Errorf("status after cancel = %s", r.Status)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("canceled run did not end")
}

func TestOtherCloudsMinimalResources(t *testing.T) {
	cases := []struct {
		cloud model.Cloud
		wf    func(id string) *model.Workflow
		main  string
	}{
		{
			cloud: model.CloudAzure,
			wf: func(id string) *model.Workflow {
				return &model.Workflow{ID: id, Clouds: []model.Cloud{model.CloudAzure},
					Providers: []model.NamedBlock{{Name: "azurerm", Body: model.Body{Blocks: []model.Block{{Type: "features"}}}}},
					Nodes: []model.Node{
						node("n-rg", model.KindResource, "azurerm_resource_group", "rg"),
						node("n-sa", model.KindResource, "azurerm_storage_account", "sa"),
					}}
			},
			main: `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}
provider "azurerm" {
  features {}
}
resource "azurerm_resource_group" "rg" {
  name     = "nimbo-it-rg"
  location = "eastus"
}
resource "azurerm_storage_account" "sa" {
  name                     = "nimboitstorage"
  resource_group_name      = azurerm_resource_group.rg.name
  location                 = azurerm_resource_group.rg.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}
`,
		},
		{
			cloud: model.CloudGCP,
			wf: func(id string) *model.Workflow {
				return &model.Workflow{ID: id, Clouds: []model.Cloud{model.CloudGCP},
					Nodes: []model.Node{node("n-b", model.KindResource, "google_storage_bucket", "b")}}
			},
			main: `
terraform {
  required_providers {
    google = { source = "hashicorp/google" }
  }
}
resource "google_storage_bucket" "b" {
  name     = "nimbo-it-bucket"
  location = "US"
}
`,
		},
		{
			cloud: model.CloudOCI,
			wf: func(id string) *model.Workflow {
				return &model.Workflow{ID: id, Clouds: []model.Cloud{model.CloudOCI},
					Nodes: []model.Node{node("n-b", model.KindResource, "oci_objectstorage_bucket", "b")}}
			},
			main: `
terraform {
  required_providers {
    oci = { source = "oracle/oci" }
  }
}
resource "oci_objectstorage_bucket" "b" {
  compartment_id = "` + ociTenancy + `"
  namespace      = "floci-local"
  name           = "nimbo-it-bucket"
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.cloud), func(t *testing.T) {
			m, _ := newTestManager(t)
			wfID := testWorkflowID("it-" + string(tc.cloud))
			t.Cleanup(func() { cleanupSandbox(t, m, wfID) })
			wf := tc.wf(wfID)
			files := map[string][]byte{"main.tf": []byte(tc.main)}
			apply := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: files})
			t.Logf("apply summary: %+v", apply.Plan)
			st, err := m.State(context.Background(), wf)
			if err != nil {
				t.Fatalf("State: %v", err)
			}
			for _, n := range wf.Nodes {
				found := false
				for _, r := range st.Resources {
					found = found || r.NodeID == n.ID
				}
				if !found {
					t.Errorf("node %s not in state", n.ID)
				}
			}
			if tc.cloud != model.CloudGCP {
				runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionDestroy, Files: files})
				return
			}
			// Reset wipes emulator state and tfstate instead of destroying.
			ctx := context.Background()
			base, _ := m.EmulatorURL(wfID, model.CloudGCP)
			if code, body := httpGetBody(t, base+"/storage/v1/b/nimbo-it-bucket"); code != http.StatusOK {
				t.Fatalf("bucket not found in emulator after apply: %d %s", code, body)
			}
			if err := m.Reset(ctx, wfID); err != nil {
				t.Fatalf("Reset: %v", err)
			}
			if st, err := m.State(ctx, wf); err != nil || len(st.Resources) != 0 {
				t.Errorf("state after Reset: %v, %v", st, err)
			}
			if st, err := m.Status(ctx, wfID); err != nil || st.State != model.SandboxRunning {
				t.Errorf("sandbox not running after Reset: %+v %v", st, err)
			}
			if code, body := httpGetBody(t, base+"/storage/v1/b/nimbo-it-bucket"); code != http.StatusNotFound {
				t.Errorf("bucket still present after Reset: %d %s", code, body)
			}
		})
	}
}

// TestOCIComputeTeardown deploys an OCI function and OKE cluster, invokes the
// function (fnserver then starts an unlabeled function container holding its
// iofs volume) and checks Remove leaves no container or volume behind.
func TestOCIComputeTeardown(t *testing.T) {
	m, _ := newTestManager(t)
	wfID := testWorkflowID("it-oci-fn")
	wf := &model.Workflow{ID: wfID, Clouds: []model.Cloud{model.CloudOCI}, Nodes: []model.Node{
		node("n-app", model.KindResource, "oci_functions_application", "app"),
		node("n-fn", model.KindResource, "oci_functions_function", "fn"),
		node("n-k8s", model.KindResource, "oci_containerengine_cluster", "k8s"),
	}}
	// oracle/oci 9.10 calls Functions API /20260325, which floci-oci 0.5.0 lacks.
	main := `
terraform {
  required_providers {
    oci = { source = "oracle/oci", version = "9.0.0" }
  }
}
resource "oci_functions_application" "app" {
  compartment_id = "` + ociTenancy + `"
  display_name   = "app"
  subnet_ids     = ["ocid1.subnet.oc1.iad.it"]
}
resource "oci_functions_function" "fn" {
  application_id = oci_functions_application.app.id
  display_name   = "fn"
  image          = "fnproject/fn-test-utils:latest"
  memory_in_mbs  = 128
}
resource "oci_containerengine_cluster" "k8s" {
  compartment_id     = "` + ociTenancy + `"
  name               = "it"
  vcn_id             = "ocid1.vcn.oc1.iad.it"
  kubernetes_version = "v1.30.1"
}
`
	removed := false
	t.Cleanup(func() {
		if !removed {
			cleanupSandbox(t, m, wfID)
		}
	})
	runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: map[string][]byte{"main.tf": []byte(main)}})
	st, err := m.State(context.Background(), wf)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	var fnID string
	for _, r := range st.Resources {
		if r.Type == "oci_functions_function" {
			fnID, _ = r.Values["id"].(string)
		}
	}
	base, _ := m.EmulatorURL(wfID, model.CloudOCI)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(base+"/20181201/functions/"+fnID+"/actions/invoke", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke: %d %s", resp.StatusCode, body)
	}
	ns := networkName(wfID)
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "volume=floci-oci-"+ns+"-fnserver-iofs").Output()
	holders := strings.Fields(string(out))
	if err != nil || len(holders) < 2 {
		t.Fatalf("expected fnserver and a function container on the iofs volume, got %q (%v)", holders, err)
	}
	out, _ = exec.Command("docker", "volume", "ls", "-q", "--filter", "name="+okeVolumePrefix(ns)).Output()
	if len(strings.Fields(string(out))) != 1 {
		t.Fatalf("expected one OKE volume, got %q", out)
	}
	removed = true
	cleanupSandbox(t, m, wfID)
	for _, id := range holders {
		if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "id="+id).Output(); strings.TrimSpace(string(out)) != "" {
			t.Errorf("container %s survived Remove", id)
		}
	}
}
