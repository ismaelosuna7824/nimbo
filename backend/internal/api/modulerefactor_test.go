package api

import (
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

// workerTypes select the Lambda service of the aws-queue-worker template
// (function, archive, role, policies, log group).
var workerTypes = []string{
	"data.archive_file", "aws_iam_role", "aws_iam_role_policy_attachment", "aws_iam_role_policy",
	"aws_lambda_function", "aws_cloudwatch_log_group",
}

func TestExtractAndInlineModule(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Queue", "templateId": "aws-queue-worker"}, &wf)
	before := wf
	var workerGroup []string
	managed := 0 // resources get moved blocks; data sources don't
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		typ := n.Type
		if n.Kind == model.KindData {
			typ = "data." + typ
		}
		if slices.Contains(workerTypes, typ) {
			workerGroup = append(workerGroup, hclcodec.Address(n))
			if n.Kind != model.KindData {
				managed++
			}
		}
	}
	req := model.ModuleExtractRequest{NodeIDs: workerGroup, Name: "worker"}

	var preview model.ModuleExtraction
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/extract/preview", req, &preview)
	inputs := map[string]string{}
	for _, in := range preview.Inputs {
		inputs[in.Name] = in.Type
	}
	// The consume policy reads the jobs queue ARN: typed from the provider schema.
	if inputs["jobs_arn"] != "string" {
		t.Errorf("preview inputs = %+v", preview.Inputs)
	}
	if len(preview.Moved) != managed || !slices.Contains(preview.Files, "modules/worker/versions.tf") {
		t.Errorf("preview = %+v (want %d moved)", preview, managed)
	}
	var unchanged model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &unchanged)
	if len(unchanged.Nodes) != len(before.Nodes) {
		t.Fatalf("preview changed the workflow")
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "modules", "worker")); err == nil {
		t.Fatalf("preview wrote files")
	}

	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/extract", req, &wf)
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("error after extraction: %+v", d)
		}
	}
	var call *model.Node
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == "module.worker" {
			call = &wf.Nodes[i]
		}
	}
	if call == nil || len(wf.Nodes) != len(before.Nodes)-len(workerGroup)+1 {
		t.Fatalf("nodes after extraction: %d", len(wf.Nodes))
	}
	var files struct{ Files []model.WorkflowFile }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/files", nil, &files)
	var modFiles []string
	for _, f := range files.Files {
		if strings.HasPrefix(f.Path, "modules/worker/") {
			modFiles = append(modFiles, f.Path)
			if f.Kind != model.FileModule {
				t.Errorf("%s kind %s", f.Path, f.Kind)
			}
		}
	}
	if !slices.Contains(modFiles, "modules/worker/README.md") || !slices.Contains(modFiles, "modules/worker/variables.tf") {
		t.Errorf("module files = %v", modFiles)
	}
	var view model.ModuleView
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/modules/module.worker", nil, &view)
	if !view.Local || view.Dir != "modules/worker" || view.Workflow == nil || len(view.Workflow.Nodes) != len(workerGroup) {
		t.Fatalf("module view = %+v", view)
	}
	var tf struct{ Files map[string]string }
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &tf)
	all := strings.Join(slices.Collect(maps.Values(tf.Files)), "\n")
	for _, want := range []string{`module "worker"`, "to   = module.worker.aws_lambda_function.worker", "depends_on              = [module.worker]"} {
		if !strings.Contains(all, want) {
			t.Errorf("root terraform lacks %q:\n%s", want, all)
		}
	}

	// The name is taken now.
	if got := e.call("POST", "/workflows/"+wf.ID+"/modules/extract", model.ModuleExtractRequest{NodeIDs: []string{"aws_sqs_queue.dlq"}, Name: "worker"}, nil); got != http.StatusBadRequest && got != http.StatusConflict {
		t.Errorf("taken name status %d", got)
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/modules/extract", model.ModuleExtractRequest{NodeIDs: []string{"aws_sqs_queue.dlq"}, Name: "Bad-Name"}, nil); got != http.StatusBadRequest {
		t.Errorf("bad name status %d", got)
	}

	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/"+call.ID+"/inline", nil, &wf)
	if len(wf.Nodes) != len(before.Nodes) {
		t.Fatalf("nodes after inline: %d, want %d", len(wf.Nodes), len(before.Nodes))
	}
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("error after inline: %+v", d)
		}
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "modules", "worker")); err == nil {
		t.Errorf("module files kept after inline")
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/modules/aws_sqs_queue.dlq/inline", nil, nil); got != http.StatusBadRequest {
		t.Errorf("inline non-module status %d", got)
	}
}
