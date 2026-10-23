package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestKubernetesWorkflow creates a blank Kubernetes workflow, adds objects,
// edits a field in place, connects a Service, switches environments
// (Kustomize overlays) and checks the kind gates.
func TestKubernetesWorkflow(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "shop"}, &wf)
	if !wf.IsKubernetes() || len(wf.Kubernetes.Sources) != 1 || wf.Kubernetes.Sources[0].Path != "k8s" {
		t.Fatalf("created %+v", wf.Kubernetes)
	}
	deploy := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web # the shop\nspec:\n  replicas: 1\n  selector:\n    matchLabels: {app: web}\n  template:\n    metadata:\n      labels: {app: web}\n    spec:\n      containers:\n        - name: web\n          image: nginx:1.27\n          ports:\n            - containerPort: 80\n"
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/kubernetes/objects", map[string]any{"yaml": deploy}, &wf)
	if len(wf.Kubernetes.Graph.Nodes) != 1 || wf.Kubernetes.Graph.Nodes[0].ID != "Deployment/default/web" {
		t.Fatalf("graph %+v", wf.Kubernetes.Graph.Nodes)
	}
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+wf.ID+"/kubernetes/objects", map[string]any{
		"node": "Deployment/default/web", "set": map[string]any{"spec.replicas": 3, "spec.template.spec.containers[name=web].image": "nginx:1.28"},
	}, &wf)
	file := e.file(wf.ID, "k8s/deployment-web.yaml")
	if !strings.Contains(file, "replicas: 3\n") || !strings.Contains(file, "image: nginx:1.28\n") || !strings.Contains(file, "name: web # the shop") {
		t.Fatalf("edit not in place:\n%s", file)
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/kubernetes/connect", map[string]any{"from": "Deployment/default/web", "kind": "expose"}, &wf)
	var selects bool
	for _, ed := range wf.Kubernetes.Graph.Edges {
		selects = selects || ed.Kind == model.KubeEdgeSelects && ed.Source == "Service/default/web" && ed.Target == "Deployment/default/web"
	}
	if !selects {
		t.Fatalf("edges %+v", wf.Kubernetes.Graph.Edges)
	}
	var fields model.KubeFields
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/kubernetes/fields?node=Deployment/default/web", nil, &fields)
	if len(fields.Fields) == 0 || !fields.Fields[0].Editable {
		t.Fatalf("fields %+v", fields)
	}
	// Kind gates both ways.
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+wf.ID+"/structure", nil, nil)
	var tf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "infra"}, &tf)
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/kubernetes/render", nil, nil)
}

// TestKubernetesKustomizeEnvironments maps overlays to environments.
func TestKubernetesKustomizeEnvironments(t *testing.T) {
	e := newEnv(t)
	files := map[string][]byte{
		"base/kustomization.yaml":          []byte("resources:\n  - deploy.yaml\n"),
		"base/deploy.yaml":                 []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 1\n  selector:\n    matchLabels: {app: api}\n  template:\n    metadata:\n      labels: {app: api}\n    spec:\n      containers:\n        - name: api\n          image: api:1\n"),
		"overlays/prod/kustomization.yaml": []byte("resources:\n  - ../../base\nreplicas:\n  - name: api\n    count: 4\n"),
	}
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "api"}, &wf)
	for rel, data := range files {
		p := filepath.Join(e.st.AssetsDir(wf.ID), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wf.Kubernetes.Sources = []model.KubeSource{{Kind: model.KubeKustomize, Path: "base"}}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	if len(wf.Environments) != 1 || wf.Environments[0].Name != "prod" {
		t.Fatalf("environments %+v", wf.Environments)
	}
	wf.ActiveEnvironment = "prod"
	var out model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &out)
	if out.Kubernetes.Environment != "prod" || *out.Kubernetes.Graph.Nodes[0].Workload.Replicas != 4 {
		t.Fatalf("prod render %+v", out.Kubernetes.Graph.Nodes)
	}
	// The replicas field is written to the overlay's transformer.
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+wf.ID+"/kubernetes/objects", map[string]any{"node": "Deployment/default/api", "set": map[string]any{"spec.replicas": 5}}, &out)
	if got := e.file(wf.ID, "overlays/prod/kustomization.yaml"); !strings.Contains(got, "count: 5") {
		t.Fatalf("overlay:\n%s", got)
	}
}

// TestKubernetesChartLayout: a chart vendored from a repository is one
// KubeChartGroup whose collapsed position PATCH /kubernetes/layout saves and
// later renders keep.
func TestKubernetesChartLayout(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "obs"}, &wf)
	files := map[string]string{
		"charts/obs/Chart.yaml":                       "apiVersion: v2\nname: obs\nversion: 0.1.0\ndependencies:\n  - name: grafana\n    version: 1.0.0\n    repository: oci://registry.example.com/charts\n",
		"charts/obs/charts/grafana/Chart.yaml":        "apiVersion: v2\nname: grafana\nversion: 1.0.0\n",
		"charts/obs/charts/grafana/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: grafana\ndata: {a: b}\n",
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
	wf.Kubernetes.Sources = []model.KubeSource{{Kind: model.KubeHelm, Path: "charts/obs", Namespace: "obs"}}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	const id = "chart:charts/obs/charts/grafana"
	if g := wf.Kubernetes.Graph; len(g.Charts) != 1 || g.Charts[0].ID != id || g.Charts[0].Objects != 1 || len(g.Nodes) != 1 || g.Nodes[0].Chart != id {
		t.Fatalf("graph %+v", g)
	}
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+wf.ID+"/kubernetes/layout", map[string]any{"positions": map[string]any{id: map[string]int{"x": 10, "y": 20}}}, &wf)
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	if p := wf.Kubernetes.Graph.Charts[0].Position; p == nil || p.X != 10 || p.Y != 20 {
		t.Fatalf("chart position %+v", p)
	}
	e.mustCall(http.StatusBadRequest, "PATCH", "/workflows/"+wf.ID+"/kubernetes/layout", map[string]any{"positions": map[string]any{"chart:nope": map[string]int{"x": 1, "y": 1}}}, nil)
}

// TestKubernetesImportScan detects a chart, a kustomize tree and a manifests
// folder next to a compose project, and imports the kustomize tree.
func TestKubernetesImportScan(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n  a: b\n"
	write("deploy/k8s/base/kustomization.yaml", "resources:\n  - cm.yaml\n")
	write("deploy/k8s/base/cm.yaml", cm)
	write("deploy/k8s/overlays/dev/kustomization.yaml", "resources:\n  - ../../base\nnamePrefix: dev-\n")
	write("raw/cm.yaml", cm)
	write("chart/Chart.yaml", "apiVersion: v2\nname: demo\nversion: 0.1.0\n")
	write("chart/values.yaml", "name: x\n")
	write("chart/values-prod.yaml", "name: y\n")
	write("chart/templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Values.name }}\n")
	write("compose.yaml", "services:\n  web:\n    image: nginx\n")
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]any{"dir": dir}, &scan)
	got := map[string]model.ImportProject{}
	for _, p := range scan.Projects {
		got[p.Path] = p
	}
	kz, chart, raw := got["kubernetes:deploy/k8s"], got["kubernetes:chart"], got["kubernetes:raw"]
	if kz.Kind != model.WorkflowKubernetes || kz.Objects != 1 || len(kz.Environments) != 1 || kz.Environments[0] != "dev" ||
		chart.Objects != 1 || len(chart.Environments) != 1 || raw.Objects != 1 || got["."].Kind != model.WorkflowDocker {
		t.Fatalf("scan %+v", scan.Projects)
	}
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": dir, "projects": []string{"kubernetes:deploy/k8s"}}, &res)
	wf := res.Workflows[0]
	if !wf.IsKubernetes() || wf.Kubernetes.Sources[0].Path != "base" || len(wf.Environments) != 1 || len(wf.Kubernetes.Graph.Nodes) != 1 {
		t.Fatalf("imported %+v", wf.Kubernetes)
	}
}
