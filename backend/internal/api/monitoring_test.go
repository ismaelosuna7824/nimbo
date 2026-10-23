package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/monitoring"
)

// Adding monitoring twice to a docker workflow lists the generated compose
// file once, keeps one Grafana password and one copy of each service.
func TestDockerMonitoringIdempotent(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	var preview model.MonitoringSummary
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/monitoring", nil, &preview)
	if preview.Enabled || len(preview.Components) != 3 || preview.Credentials.Password != "" {
		t.Fatalf("preview %+v", preview)
	}
	var first, second model.MonitoringResult
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, &first)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, &second)
	files := second.Workflow.Compose.Files
	if len(files) != 2 || files[1] != "monitoring/compose.yaml" {
		t.Fatalf("compose files %v", files)
	}
	if first.Summary.Credentials.Password == "" || second.Summary.Credentials.Password != first.Summary.Credentials.Password {
		t.Fatalf("password changed: %q then %q", first.Summary.Credentials.Password, second.Summary.Credentials.Password)
	}
	if n := strings.Count(e.file(wf.ID, ".env"), monitoring.GrafanaPasswordVar+"="); n != 1 {
		t.Fatalf(".env has %d Grafana passwords", n)
	}
	count := map[string]int{}
	for _, n := range second.Workflow.Compose.Graph.Nodes {
		count[n.Name]++
	}
	for _, s := range []string{"prometheus", "grafana", "cadvisor"} {
		if count[s] != 1 {
			t.Errorf("service %s appears %d times", s, count[s])
		}
	}
	for _, d := range second.Workflow.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("%s %s:%d %s", d.Code, d.File, d.Line, d.Message)
		}
	}
	var after model.MonitoringSummary
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/monitoring", nil, &after)
	if !after.Enabled {
		t.Fatal("GET does not report the added monitoring")
	}
}

// With kube-prometheus-stack already a source, adding monitoring twice
// adds the generated source once, right after the chart, and moves the
// chart before a source whose ServiceMonitor needs its CRDs.
func TestKubeMonitoringIdempotentAndOrdered(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "shop"}, &wf)
	files := map[string]string{
		"k8s/app.yaml":       "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: default\nspec:\n  selector:\n    matchLabels: {app: web}\n  template:\n    metadata:\n      labels: {app: web}\n    spec:\n      containers:\n        - name: web\n          image: nginx:1.27\n          resources:\n            requests: {cpu: 10m, memory: 32Mi}\n            limits: {memory: 64Mi}\n",
		"extra/monitor.yaml": "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: web\n  namespace: default\nspec:\n  selector:\n    matchLabels: {app: web}\n  endpoints:\n    - port: metrics\n",
		// A stand-in for the vendored chart (the real one downloads).
		"charts/kps/Chart.yaml":            "apiVersion: v2\nname: kube-prometheus-stack\nversion: 1.0.0\n",
		"charts/kps/templates/marker.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: marker\ndata: {}\n",
	}
	for rel, data := range files {
		p := filepath.Join(e.st.AssetsDir(wf.ID), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wf.Kubernetes.Sources = []model.KubeSource{
		{Kind: model.KubeManifests, Path: "k8s"},
		{Kind: model.KubeManifests, Path: "extra"},
		{Kind: model.KubeHelm, Path: "charts/kps", Release: "kps", Namespace: "monitoring"},
	}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	var res model.MonitoringResult
	for range 2 {
		e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, &res)
	}
	var paths []string
	for _, s := range res.Workflow.Kubernetes.Sources {
		paths = append(paths, s.Path)
	}
	if strings.Join(paths, ",") != "k8s,charts/kps,monitoring,extra" {
		t.Fatalf("sources %v", paths)
	}
	if !strings.Contains(e.file(wf.ID, "monitoring/rules.yaml"), "name: default/deployment/web") || len(res.Summary.Alerts) == 0 {
		t.Fatalf("no alerts for web: %+v", res.Summary.Alerts)
	}
	for _, d := range res.Workflow.Diagnostics {
		if d.Severity != model.SeverityInfo && strings.HasPrefix(d.File, "monitoring/") {
			t.Errorf("%s %s %s:%d %s", d.Severity, d.Code, d.File, d.Line, d.Message)
		}
	}
	// A monitoring/ folder the user wrote is never overwritten.
	if err := os.WriteFile(filepath.Join(e.st.AssetsDir(wf.ID), "monitoring", "rules.yaml"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustCall(http.StatusConflict, "POST", "/workflows/"+wf.ID+"/monitoring", nil, nil)
}

// HTTP thresholds set once are kept by later updates without a body; bad
// ones are refused.
func TestMonitoringThresholdsPersist(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	var res model.MonitoringResult
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", map[string]any{"thresholds": map[string]any{"latencyP95Ms": 800}}, &res)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, &res)
	want := model.MonitoringThresholds{ErrorPercent: 5, LatencyP95Ms: 800, MinRequestsPerSecond: 0.5}
	if res.Summary.Thresholds != want {
		t.Fatalf("thresholds %+v, want %+v", res.Summary.Thresholds, want)
	}
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+wf.ID+"/monitoring", map[string]any{"thresholds": map[string]any{"errorPercent": 150}}, nil)
}

// Services built from the node, python and go starters, and the API of
// the app templates, serve the HTTP histogram Add monitoring alerts on and
// carry the scrape labels: monitoring marks them HTTP-covered (otel).
func TestStartersAndTemplatesAreHTTPCovered(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"kind": "docker", "name": "starters"})
	for _, starter := range []string{"node", "python", "go", "static"} {
		e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/compose/services", map[string]any{"name": starter, "build": map[string]string{"context": starter, "starter": starter}}, nil)
	}
	compose := e.file(wf.ID, "compose.yaml")
	if n := strings.Count(compose, `prometheus.io/scrape: "true"`); n != 3 || !strings.Contains(compose, `prometheus.io/port: "8080"`) {
		t.Fatalf("%d services labelled for scraping:\n%s", n, compose)
	}
	for _, f := range []string{"node/server.js", "python/app.py", "go/main.go"} {
		src := e.file(wf.ID, f)
		for _, s := range []string{"http_server_request_duration_seconds", "http_response_status_code", "http_route", "/metrics"} {
			if !strings.Contains(src, s) {
				t.Errorf("%s lacks %s", f, s)
			}
		}
	}
	covered := func(id string) map[string][]string {
		var sum model.MonitoringSummary
		e.mustCall(http.StatusOK, "GET", "/workflows/"+id+"/monitoring", nil, &sum)
		out := map[string][]string{}
		for _, h := range sum.HTTP {
			out[h.Target] = h.Sources
		}
		return out
	}
	got := covered(wf.ID)
	for _, s := range []string{"node", "python", "go"} {
		if !slices.Equal(got[s], []string{monitoring.SourceOTel}) {
			t.Errorf("%s sources %v", s, got[s])
		}
	}
	if len(got["static"]) != 0 {
		t.Errorf("static (no metrics) sources %v", got["static"])
	}
	for _, tmpl := range []string{"docker-node-postgres-redis", "docker-fastapi-postgres", "docker-go-nginx"} {
		w := e.createDocker(map[string]string{"templateId": tmpl})
		if got := covered(w.ID); !slices.Equal(got["api"], []string{monitoring.SourceOTel}) {
			t.Errorf("%s api sources %v", tmpl, got["api"])
		}
	}
}
