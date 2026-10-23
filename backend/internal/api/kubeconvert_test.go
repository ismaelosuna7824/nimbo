package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// saveRun stores a finished load test run of wfID where the engine keeps
// them.
func (e *testEnv) saveRun(wfID string, run model.LoadTestRun) {
	e.t.Helper()
	run.WorkflowID = wfID
	dir := filepath.Join(e.home, "workflows", wfID, "loadtests")
	data, _ := json.Marshal(run)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, run.ID+".json"), data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func measuredRun(id, target string, at time.Time) model.LoadTestRun {
	var tl []model.ResourcePoint
	for i := range 30 {
		tl = append(tl, model.ResourcePoint{T: float64(i + 1), CPUPercent: 20, MemBytes: 100 << 20, Containers: 1})
	}
	return model.LoadTestRun{
		ID: id, Status: model.LoadPassed, StartedAt: at, Summary: &model.LoadSummary{RPS: 90, PeakRPS: 120},
		Resources: &model.LoadResources{Service: target, Services: []model.LoadServiceResources{
			{Service: target, CPULimit: 1, CPULimited: true, MemLimit: 256 << 20, MemLimited: true, Timeline: tl},
		}, Verdicts: []model.ResourceVerdict{{Service: target, Kind: "ok"}}},
	}
}

func TestConvertSizesFromLoadTest(t *testing.T) {
	e := newEnv(t)
	docker := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	now := time.Now()
	e.saveRun(docker.ID, measuredRun("older", "api", now.Add(-time.Hour)))
	// The newest run measured a service that no longer exists: skipped.
	e.saveRun(docker.ID, measuredRun("stale", "web", now))
	path := "/workflows/" + docker.ID + "/convert/kubernetes"

	var res convertKubeResponse
	e.mustCall(http.StatusOK, "POST", path, map[string]any{"format": "kustomize", "dryRun": true}, &res)
	if lt := res.Report.LoadTest; lt == nil || lt.RunID != "older" || !lt.Used {
		t.Fatalf("load test %+v", lt)
	}
	var api model.KubeConvertSizing
	for _, s := range res.Report.Sizing {
		if s.Service == "api" {
			api = s
		}
	}
	// The template's explicit limits stay; the requests come from the run
	// (0.2 cores → 230m, 100Mi → 120Mi).
	if api.Source != model.SizingLoadTest || api.Requests != (model.KubeResourceQty{CPU: "230m", Memory: "120Mi"}) || api.Limits != (model.KubeResourceQty{CPU: "1", Memory: "256Mi"}) {
		t.Errorf("api sizing %+v", api)
	}
	if len(res.Report.Autoscaling) != 1 || res.Report.Autoscaling[0].Service != "api" {
		t.Errorf("autoscaling %+v", res.Report.Autoscaling)
	}

	// Turned off, and HPAs tuned or disabled.
	var off convertKubeResponse
	e.mustCall(http.StatusOK, "POST", path, map[string]any{"format": "kustomize", "dryRun": true, "sizeFromLoadTest": false,
		"autoscaling": map[string]any{"enabled": false}}, &off)
	if off.Report.LoadTest == nil || off.Report.LoadTest.Used || len(off.Report.Autoscaling) != 0 || off.Files["k8s/overlays/prod/hpa.yaml"] != "" {
		t.Errorf("off: %+v %+v", off.Report.LoadTest, off.Report.Autoscaling)
	}
	var tuned convertKubeResponse
	e.mustCall(http.StatusOK, "POST", path, map[string]any{"format": "helm", "dryRun": true, "autoscaling": map[string]any{"maxReplicas": 8, "targetCpu": 60}}, &tuned)
	if h := tuned.Report.Autoscaling[0]; h.MaxReplicas != 8 || h.TargetCPU != 60 {
		t.Errorf("tuned %+v", h)
	}

	// A specific run: unknown → 404, stale → 400.
	e.mustCall(http.StatusNotFound, "POST", path, map[string]any{"dryRun": true, "loadTestRunId": "nope"}, nil)
	e.mustCall(http.StatusBadRequest, "POST", path, map[string]any{"dryRun": true, "loadTestRunId": "stale"}, nil)
	e.mustCall(http.StatusBadRequest, "POST", path, map[string]any{"dryRun": true, "autoscaling": map[string]any{"maxReplicas": 1}}, nil)
}
