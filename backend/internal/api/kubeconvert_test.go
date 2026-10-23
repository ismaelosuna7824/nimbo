package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestConvertDockerToKubernetes(t *testing.T) {
	e := newEnv(t)
	docker := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})

	// Dry run: report and generated files, nothing created.
	var preview convertKubeResponse
	e.mustCall(http.StatusOK, "POST", "/workflows/"+docker.ID+"/convert/kubernetes", map[string]any{"format": "kustomize", "dryRun": true}, &preview)
	if preview.Workflow != nil || preview.Report.Name != "node-js-api-postgresql-redis" || preview.Files["k8s/base/kustomization.yaml"] == "" {
		t.Fatalf("preview: %+v", preview.Report)
	}
	if _, copied := preview.Files["api/server.js"]; copied {
		t.Error("dry run lists build context files")
	}
	var list []model.WorkflowSummary
	e.mustCall(http.StatusOK, "GET", "/workflows", nil, &list)
	if len(list) != 1 {
		t.Fatalf("dry run created a workflow: %d", len(list))
	}

	var res convertKubeResponse
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+docker.ID+"/convert/kubernetes", map[string]any{"format": "helm", "name": "notes", "namespace": "notes-dev"}, &res)
	wf := res.Workflow
	if wf == nil || !wf.IsKubernetes() || wf.Kubernetes == nil || !strings.HasSuffix(wf.Name, "(Kubernetes)") {
		t.Fatalf("workflow: %+v", wf)
	}
	if len(wf.Kubernetes.Sources) != 1 || wf.Kubernetes.Sources[0].Kind != model.KubeHelm || wf.Kubernetes.Sources[0].Namespace != "notes-dev" ||
		len(wf.Kubernetes.Builds) != 1 || wf.Kubernetes.Builds[0].Image != "api:dev" {
		t.Errorf("kubernetes: %+v", wf.Kubernetes)
	}
	if len(wf.Kubernetes.Graph.Nodes) == 0 {
		t.Error("graph not derived")
	}
	for _, d := range wf.Diagnostics {
		if d.Severity != model.SeverityInfo {
			t.Errorf("diagnostic %s %s:%d %s", d.Code, d.File, d.Line, d.Message)
		}
	}
	if !strings.Contains(e.file(wf.ID, "chart/values.yaml"), "replicaCount") || !strings.Contains(e.file(wf.ID, "api/server.js"), "express") {
		t.Error("files not written")
	}
	// The docker workflow is untouched.
	var again model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+docker.ID, nil, &again)
	if !again.IsDocker() {
		t.Error("docker workflow changed")
	}

	// Only docker workflows convert; bad options are 400s.
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+wf.ID+"/convert/kubernetes", map[string]any{"format": "manifests"}, nil)
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+docker.ID+"/convert/kubernetes", map[string]any{"format": "ansible"}, nil)
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+docker.ID+"/convert/kubernetes", map[string]any{"name": "Not_Valid"}, nil)
}
