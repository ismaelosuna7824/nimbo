package kube

import (
	"context"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestInferredEdges finds Service hosts in env values and in container
// command/args ("--cache-server=tcp://redis.shop:6379").
func TestInferredEdges(t *testing.T) {
	manifests := `apiVersion: v1
kind: Service
metadata: {name: redis, namespace: shop}
spec:
  selector: {app: redis}
  ports: [{port: 6379}]
---
apiVersion: v1
kind: Service
metadata: {name: backend, namespace: shop}
spec:
  selector: {app: backend}
  ports: [{port: 80}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: backend, namespace: shop}
spec:
  selector: {matchLabels: {app: backend}}
  template:
    metadata: {labels: {app: backend}}
    spec:
      containers:
        - name: backend
          image: echo:1
          args: ["--cache-server=tcp://redis.shop:6379"]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: frontend, namespace: shop}
spec:
  selector: {matchLabels: {app: frontend}}
  template:
    metadata: {labels: {app: frontend}}
    spec:
      containers:
        - name: web
          image: web:1
          env:
            - {name: BACKEND_URL, value: "http://backend.shop/echo"}
`
	wf := &model.Workflow{Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	p := Open(context.Background(), t.TempDir(), wf, Options{Content: map[string][]byte{"k8s/app.yaml": []byte(manifests)}})
	g := p.Graph(nil)
	want := map[string]string{
		"Deployment/shop/backend->Service/shop/redis":    "args",
		"Deployment/shop/frontend->Service/shop/backend": "BACKEND_URL",
	}
	for _, e := range g.Edges {
		if e.Kind != model.KubeEdgeInferred {
			continue
		}
		label, ok := want[e.Source+"->"+e.Target]
		if !ok || label != e.Label {
			t.Errorf("unexpected inferred edge %+v", e)
		}
		delete(want, e.Source+"->"+e.Target)
	}
	if len(want) > 0 {
		t.Errorf("missing inferred edges %v (edges %+v)", want, g.Edges)
	}
}

// vendoredChartFiles: a wrapper chart vendoring a chart from a repository
// (the palette's "Add chart"), and the user's own chart with a file://
// subchart.
func vendoredChartFiles() map[string][]byte {
	deploy := func(name string) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\nspec:\n  selector:\n    matchLabels: {app: " + name + "}\n  template:\n    metadata:\n      labels: {app: " + name + "}\n    spec:\n      containers:\n        - name: c\n          image: nginx:1.27\n"
	}
	service := "apiVersion: v1\nkind: Service\nmetadata:\n  name: {{ .Release.Name }}-web\nspec:\n  selector: {app: vendored}\n  ports: [{port: 80}]\n"
	return map[string][]byte{
		"k8s/app.yaml": []byte(deploy("shop")),
		"charts/monitoring/Chart.yaml": []byte("apiVersion: v2\nname: monitoring\nversion: 0.1.0\ndependencies:\n" +
			"  - name: web\n    version: 1.2.3\n    repository: https://charts.example.com\n    alias: frontdoor\n"),
		"charts/monitoring/values.yaml":                       []byte("frontdoor: {}\n"),
		"charts/monitoring/charts/web/Chart.yaml":             []byte("apiVersion: v2\nname: web\nversion: 1.2.3\nappVersion: \"9.0\"\n"),
		"charts/monitoring/charts/web/templates/deploy.yaml":  []byte(deploy("vendored")),
		"charts/monitoring/charts/web/templates/service.yaml": []byte(service),
		"chart/Chart.yaml":                                    []byte("apiVersion: v2\nname: app\nversion: 0.1.0\ndependencies:\n  - name: lib\n    version: 0.1.0\n    repository: file://../lib\n"),
		"chart/templates/api.yaml":                            []byte(deploy("api")),
		"chart/charts/lib/Chart.yaml":                         []byte("apiVersion: v2\nname: lib\nversion: 0.1.0\n"),
		"chart/charts/lib/templates/worker.yaml":              []byte(deploy("worker")),
	}
}

// TestVendoredChartGroups groups the objects of charts vendored from a
// repository by chart, never the workflow's own templates or local
// subcharts, and keeps the collapsed chart's position.
func TestVendoredChartGroups(t *testing.T) {
	wf := &model.Workflow{Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{
		{Kind: model.KubeManifests, Path: "k8s"},
		{Kind: model.KubeHelm, Path: "charts/monitoring", Release: "monitoring", Namespace: "monitoring"},
		{Kind: model.KubeHelm, Path: "chart"},
	}}}
	p := Open(context.Background(), t.TempDir(), wf, Options{Content: vendoredChartFiles()})
	const id = "chart:charts/monitoring/charts/frontdoor"
	g := p.Graph(&model.KubeGraph{Charts: []model.KubeChartGroup{{ID: id, Position: &model.Position{X: 40, Y: 80}}}})
	got := map[string]string{}
	for _, n := range g.Nodes {
		got[n.ID] = n.Chart
	}
	want := map[string]string{
		"Deployment/default/shop":           "",
		"Deployment/monitoring/vendored":    id,
		"Service/monitoring/monitoring-web": id,
		"Deployment/default/api":            "",
		"Deployment/default/worker":         "",
	}
	if len(got) != len(want) {
		t.Fatalf("nodes %v", got)
	}
	for nodeID, chart := range want {
		if c, ok := got[nodeID]; !ok || c != chart {
			t.Errorf("%s: chart %q, want %q", nodeID, c, chart)
		}
	}
	if len(g.Charts) != 1 {
		t.Fatalf("charts %+v", g.Charts)
	}
	c := g.Charts[0]
	if c.ID != id || c.Source != 1 || c.SourcePath != "charts/monitoring" || c.Chart != "web" || c.Version != "1.2.3" || c.AppVersion != "9.0" ||
		c.Repository != "https://charts.example.com" || c.Release != "monitoring" || c.Namespace != "monitoring" || c.Objects != 2 ||
		len(c.ValuesFiles) != 1 || c.ValuesFiles[0] != "charts/monitoring/values.yaml" || c.Position == nil || c.Position.X != 40 {
		t.Errorf("chart %+v", c)
	}
	// The own chart alone (a converter chart): nothing grouped.
	wf.Kubernetes.Sources = wf.Kubernetes.Sources[2:]
	if g := Open(context.Background(), t.TempDir(), wf, Options{Content: vendoredChartFiles()}).Graph(nil); len(g.Charts) != 0 || len(g.Nodes) != 2 {
		t.Errorf("own chart grouped: %+v", g.Charts)
	}
}
