package monitoring

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const shopApp = `apiVersion: v1
kind: Namespace
metadata:
  name: shop
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
spec:
  selector:
    matchLabels: {app: api}
  template:
    metadata:
      labels: {app: api}
    spec:
      containers:
        - name: api
          image: ghcr.io/stefanprodan/podinfo:6.15.0
          ports:
            - {name: http, containerPort: 9898}
          resources:
            requests: {cpu: 100m, memory: 64Mi}
            limits: {cpu: 500m, memory: 128Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: api
  namespace: shop
spec:
  selector: {app: api}
  ports:
    - {name: http, port: 80, targetPort: http}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  namespace: shop
spec:
  selector:
    matchLabels: {app: worker}
  template:
    metadata:
      labels: {app: worker}
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9100"
    spec:
      containers:
        - name: worker
          image: busybox:1.36
          resources:
            requests: {cpu: 10m, memory: 16Mi}
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
  namespace: shop
spec:
  serviceName: db
  selector:
    matchLabels: {app: db}
  template:
    metadata:
      labels: {app: db}
    spec:
      containers:
        - name: db
          image: postgres:16-alpine
          resources:
            limits: {memory: 512Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: db
  namespace: shop
  labels: {app: db}
spec:
  selector: {app: db}
  ports:
    - {name: postgres, port: 5432}
    - {name: metrics, port: 9187}
---
apiVersion: v1
kind: Service
metadata:
  name: cache
  namespace: shop
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9121"
    prometheus.io/path: /stats
spec:
  selector: {app: cache}
  ports:
    - {name: redis, port: 6379}
    - {port: 9121}
`

const prodHPA = `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: api-hpa
  namespace: shop
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: api}
  minReplicas: 2
  maxReplicas: 6
  metrics:
    - type: Resource
      resource:
        name: cpu
        target: {type: Utilization, averageUtilization: 70}
`

// kubeApp renders the shop app for the base and a prod overlay that adds
// an HPA (Kustomize).
func kubeApp(t *testing.T) (string, []*kube.Project, *model.Workflow) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"base/kustomization.yaml":          "resources:\n  - app.yaml\n",
		"base/app.yaml":                    shopApp,
		"overlays/prod/kustomization.yaml": "resources:\n  - ../../base\n  - hpa.yaml\n",
		"overlays/prod/hpa.yaml":           prodHPA,
	})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{
		{Kind: model.KubeKustomize, Path: "base", Environments: map[string]string{"dev": "base", "prod": "overlays/prod"}},
	}}}
	var ps []*kube.Project
	for _, env := range []string{"dev", "prod"} {
		ps = append(ps, kube.Open(context.Background(), dir, wf, kube.Options{Environment: env, NoSourceMap: true}))
	}
	return dir, ps, wf
}

func alertsOf(out KubeOutput, target string) []string {
	var names []string
	for _, a := range out.Alerts {
		if a.Target == target {
			names = append(names, a.Name)
		}
	}
	return names
}

func TestKubeAlertsPerWorkload(t *testing.T) {
	_, ps, _ := kubeApp(t)
	out := Kube(KubeInput{Projects: ps, Namespace: KubeNamespace})
	want := map[string][]string{
		// Limits on both resources and an HPA (prod only) on CPU.
		"shop/Deployment/api": {"WorkloadMemoryNearLimit", "WorkloadCPUThrottled", "WorkloadOOMKilled", "WorkloadRestarting", "WorkloadDown", "WorkloadReplicasUnavailable", "WorkloadHPASaturated"},
		// No limits: no alert that compares usage with one. Scraped (an
		// annotated pod template, a Service metrics port): HTTP alerts on the
		// app's OpenTelemetry metrics.
		"shop/Deployment/worker": {"WorkloadOOMKilled", "WorkloadRestarting", "WorkloadDown", "WorkloadReplicasUnavailable", "WorkloadHTTPErrorRateHigh", "WorkloadHTTPLatencyHigh"},
		"shop/StatefulSet/db":    {"WorkloadMemoryNearLimit", "WorkloadOOMKilled", "WorkloadRestarting", "WorkloadDown", "WorkloadReplicasUnavailable", "WorkloadHTTPErrorRateHigh", "WorkloadHTTPLatencyHigh"},
	}
	for target, names := range want {
		if got := alertsOf(out, target); !slices.Equal(got, names) {
			t.Errorf("%s alerts = %v, want %v", target, got, names)
		}
	}
	var doc struct {
		Metadata struct{ Namespace string }
		Spec     struct {
			Groups []struct {
				Name  string
				Rules []struct {
					Record string
					Alert  string
					Expr   string
					Labels map[string]string
				}
			}
		}
	}
	if err := yaml.Unmarshal(out.Files[KubeRulesFile], &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Namespace != KubeNamespace || len(doc.Spec.Groups) != 3 {
		t.Fatalf("PrometheusRule: namespace %q, %d groups", doc.Metadata.Namespace, len(doc.Spec.Groups))
	}
	for _, g := range doc.Spec.Groups {
		for _, r := range g.Rules {
			// Scoped to the workload, labelled for routing.
			if r.Labels["namespace"] != "shop" || !strings.Contains(r.Expr, `namespace="shop"`) || r.Alert != "" && r.Labels["severity"] == "" {
				t.Errorf("%s %s: labels %v expr %s", g.Name, r.Alert, r.Labels, r.Expr)
			}
			if r.Alert == "WorkloadHPASaturated" && !strings.Contains(r.Expr, `horizontalpodautoscaler="api-hpa"`) {
				t.Errorf("HPA alert does not watch api-hpa: %s", r.Expr)
			}
			if g.Name == "shop/statefulset/db" && r.Alert == "WorkloadDown" && !strings.Contains(r.Expr, `kube_statefulset_status_replicas_ready{job="kube-state-metrics", namespace="shop", statefulset="db"}`) {
				t.Errorf("StatefulSet down alert: %s", r.Expr)
			}
		}
	}
}

func TestKubeScrapesOnlyMetricsEndpoints(t *testing.T) {
	_, ps, _ := kubeApp(t)
	out := Kube(KubeInput{Projects: ps, Namespace: KubeNamespace})
	got := map[string]string{}
	for _, s := range out.Scrapes {
		got[s.Target+":"+s.Port+s.Path] = s.Via
	}
	want := map[string]string{
		"shop/db:metrics/metrics":  "ServiceMonitor", // labelled Service, port named metrics
		"shop/cache:9121/stats":    "PodMonitor",     // annotated Service without labels: its pods
		"shop/worker:9100/metrics": "PodMonitor",     // annotated pod template
	}
	if len(got) != len(want) {
		t.Fatalf("scrapes %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("scrape %s via %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	// The api Service (port http) and the redis port are not scraped.
	if strings.Contains(string(out.Files[kubeScrapeFile]), "name: shop-api") {
		t.Errorf("api scraped:\n%s", out.Files[kubeScrapeFile])
	}
}

func TestKubeNoMetricsNoScrapeFile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"k8s/app.yaml": strings.SplitN(shopApp, "---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: worker", 2)[0]})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	out := Kube(KubeInput{Projects: []*kube.Project{kube.Open(context.Background(), dir, wf, kube.Options{})}})
	if data, set := out.Files[kubeScrapeFile]; !set || data != nil {
		t.Fatalf("scrape file should be removed when nothing exposes metrics: %q", data)
	}
	if len(out.Scrapes) != 0 || len(out.Notes) == 0 {
		t.Fatalf("scrapes %v notes %v", out.Scrapes, out.Notes)
	}
}

// The generated source renders next to the app and passes the k8s_* rules
// without warnings or errors; the dashboard is valid Grafana JSON.
func TestKubeGeneratedSourceIsClean(t *testing.T) {
	dir, ps, wf := kubeApp(t)
	out := Kube(KubeInput{Projects: ps, Namespace: KubeNamespace})
	files := map[string]string{}
	for f, data := range out.Files {
		if data != nil {
			files[f] = string(data)
		}
	}
	writeFiles(t, dir, files)
	wf.Kubernetes.Sources = append(wf.Kubernetes.Sources, model.KubeSource{Kind: model.KubeManifests, Path: KubeDir, Namespace: KubeNamespace})
	p := kube.Open(context.Background(), dir, wf, kube.Options{Environment: "prod"})
	kinds := map[string]int{}
	for _, o := range p.Objects {
		if o.Source == 1 {
			kinds[o.Kind]++
		}
	}
	if kinds["PrometheusRule"] != 1 || kinds["ServiceMonitor"] != 1 || kinds["PodMonitor"] != 2 || kinds["ConfigMap"] != 1 {
		t.Fatalf("rendered %v", kinds)
	}
	for _, d := range append(p.Diagnostics, kuberules.Check(p, wf, model.BestPracticeSettings{})...) {
		if d.Severity != model.SeverityInfo && strings.HasPrefix(d.File, KubeDir+"/") {
			t.Errorf("%s %s %s:%d %s", d.Severity, d.Code, d.File, d.Line, d.Message)
		}
	}
	if !strings.Contains(files[kubeDashboardsFile], `grafana_dashboard: "1"`) {
		t.Fatalf("dashboard ConfigMap not labelled for the Grafana sidecar:\n%s", files[kubeDashboardsFile])
	}
}

func TestKubeAdminSecretRoundTrip(t *testing.T) {
	pw := NewPassword()
	if len(pw) != 24 || pw == NewPassword() {
		t.Fatalf("password %q", pw)
	}
	data := KubeAdminSecretYAML(pw)
	if KubeAdminPassword(data) != pw || !IsGenerated(data) {
		t.Fatalf("password not read back from:\n%s", data)
	}
	if strings.Contains(KubeValues(), "adminPassword") || !strings.Contains(KubeValues(), "existingSecret: "+KubeAdminSecret) {
		t.Fatal("Grafana's password must come from the generated Secret")
	}
}
