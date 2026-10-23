package monitoring

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// httpApp: web behind the default class (Traefik), api behind class nginx
// (ingress-nginx in production, Traefik on the local cluster), metrics
// exposing a scraped metrics port, quiet with no Ingress and no metrics,
// and admin behind an Ingress class nobody measures here.
const httpApp = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: shop}
spec:
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web, tier: front}}
    spec: {containers: [{name: web, image: "nginx:1.27"}]}
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: shop}
spec:
  selector: {app: web}
  ports: [{name: http, port: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: shop, namespace: shop}
spec:
  rules:
    - http:
        paths:
          - {path: /, pathType: Prefix, backend: {service: {name: web, port: {name: http}}}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec: {containers: [{name: api, image: "api:1"}]}
---
apiVersion: v1
kind: Service
metadata: {name: api, namespace: shop}
spec:
  selector: {app: api}
  ports: [{port: 8080}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: api, namespace: shop}
spec:
  ingressClassName: nginx
  rules:
    - http:
        paths:
          - {path: /api, pathType: Prefix, backend: {service: {name: api, port: {number: 8080}}}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: metrics, namespace: shop}
spec:
  selector: {matchLabels: {app: metrics}}
  template:
    metadata: {labels: {app: metrics}}
    spec: {containers: [{name: metrics, image: "app:1"}]}
---
apiVersion: v1
kind: Service
metadata: {name: metrics, namespace: shop, labels: {app: metrics}}
spec:
  selector: {app: metrics}
  ports: [{name: http, port: 80}, {name: metrics, port: 9464}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: quiet, namespace: shop}
spec:
  selector: {matchLabels: {app: quiet}}
  template:
    metadata: {labels: {app: quiet}}
    spec: {containers: [{name: quiet, image: "quiet:1"}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: admin, namespace: shop}
spec:
  selector: {matchLabels: {app: admin}}
  template:
    metadata: {labels: {app: admin}}
    spec: {containers: [{name: admin, image: "admin:1"}]}
---
apiVersion: v1
kind: Service
metadata: {name: admin, namespace: shop}
spec:
  selector: {app: admin}
  ports: [{name: http, port: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: admin, namespace: shop}
spec:
  ingressClassName: haproxy
  rules:
    - http:
        paths:
          - {path: /admin, pathType: Prefix, backend: {service: {name: admin, port: {name: http}}}}
`

func httpKube(t *testing.T, th HTTPThresholds) KubeOutput {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"k8s/app.yaml": httpApp})
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}}}}
	p := kube.Open(context.Background(), dir, wf, kube.Options{})
	if len(p.Diagnostics) > 0 {
		t.Fatalf("app does not render: %v", p.Diagnostics)
	}
	return Kube(KubeInput{Projects: []*kube.Project{p}, Namespace: KubeNamespace, Thresholds: th})
}

func TestHTTPSourceSelection(t *testing.T) {
	out := httpKube(t, HTTPThresholds{})
	got := map[string][]string{}
	for _, h := range out.HTTP {
		got[h.Target] = h.Sources
	}
	want := map[string][]string{
		"shop/Deployment/web":     {SourceTraefik},
		"shop/Deployment/api":     {SourceTraefik, SourceIngressNginx}, // local cluster: Traefik; production: ingress-nginx
		"shop/Deployment/metrics": {SourceOTel},
		"shop/Deployment/quiet":   {},
		"shop/Deployment/admin":   {}, // class haproxy: no request metrics we can read
	}
	for target, w := range want {
		if !slices.Equal(got[target], w) {
			t.Errorf("%s sources = %v, want %v", target, got[target], w)
		}
	}
	for _, a := range out.Alerts {
		if strings.Contains(a.Name, "HTTP") && (a.Target == "shop/Deployment/quiet" || a.Target == "shop/Deployment/admin") {
			t.Errorf("%s has %s without request metrics", a.Target, a.Name)
		}
	}
	rules := string(out.Files[KubeRulesFile])
	for _, s := range []string{
		`service=~"shop-web-http@kubernetes"`, `service=~"shop-api-8080@kubernetes"`,
		`exported_namespace="shop", ingress=~"api", exported_service=~"api"`,
		`http_server_request_duration_seconds_bucket{namespace="shop"}`,
	} {
		if !strings.Contains(rules, s) {
			t.Errorf("rules miss %s", s)
		}
	}
	// Traefik is scraped because Ingresses route through it.
	if !strings.Contains(string(out.Files[kubeScrapeFile]), "name: traefik") {
		t.Errorf("no Traefik PodMonitor:\n%s", out.Files[kubeScrapeFile])
	}
	if !strings.Contains(string(out.Files[kubeDashboardsFile]), "p95 latency") {
		t.Error("dashboard has no HTTP panels")
	}
}

func TestHTTPThresholdsKeptInRules(t *testing.T) {
	th := HTTPThresholds{ErrorPercent: 2.5, LatencyP95Ms: 300, MinRequestsPerSecond: 1}
	out := httpKube(t, th)
	if got := ParseThresholds(out.Files[KubeRulesFile]); got != th {
		t.Fatalf("thresholds read back %+v, want %+v", got, th)
	}
	if !strings.Contains(string(out.Files[KubeRulesFile]), "> 0.025\n") || !strings.Contains(string(out.Files[KubeRulesFile]), "> 0.3\n") {
		t.Fatal("rules do not use the thresholds")
	}
	if _, err := ValidThresholds(HTTPThresholds{ErrorPercent: 200}); err == nil {
		t.Fatal("200% accepted")
	}
	if d, _ := ValidThresholds(HTTPThresholds{}); d != DefaultThresholds {
		t.Fatalf("defaults %+v", d)
	}
}

// promtool unit tests on synthetic series: each HTTP alert fires above its
// threshold, stays quiet below it, and the minimum-traffic guard holds.
const promtoolTests = `rule_files: [rules.yml]
evaluation_interval: 1m
tests:
  # web (Traefik): 1.8 req/s OK + 0.2 req/s 500 = 10% errors; p95 ≈ 1 s.
  # api (ingress-nginx): 1.67% errors, everything under 100 ms.
  - interval: 1m
    input_series:
      - series: 'traefik_service_requests_total{service="shop-web-http@kubernetes", code="200"}'
        values: '0+108x40'
      - series: 'traefik_service_requests_total{service="shop-web-http@kubernetes", code="500"}'
        values: '0+12x40'
      - series: 'traefik_service_request_duration_seconds_bucket{service="shop-web-http@kubernetes", le="0.1"}'
        values: '0+60x40'
      - series: 'traefik_service_request_duration_seconds_bucket{service="shop-web-http@kubernetes", le="0.3"}'
        values: '0+90x40'
      - series: 'traefik_service_request_duration_seconds_bucket{service="shop-web-http@kubernetes", le="1.2"}'
        values: '0+120x40'
      - series: 'traefik_service_request_duration_seconds_bucket{service="shop-web-http@kubernetes", le="+Inf"}'
        values: '0+120x40'
      - series: 'nginx_ingress_controller_requests{exported_namespace="shop", ingress="api", exported_service="api", status="200"}'
        values: '0+118x40'
      - series: 'nginx_ingress_controller_requests{exported_namespace="shop", ingress="api", exported_service="api", status="500"}'
        values: '0+2x40'
      - series: 'nginx_ingress_controller_request_duration_seconds_bucket{exported_namespace="shop", ingress="api", exported_service="api", le="0.1"}'
        values: '0+120x40'
      - series: 'nginx_ingress_controller_request_duration_seconds_bucket{exported_namespace="shop", ingress="api", exported_service="api", le="+Inf"}'
        values: '0+120x40'
    alert_rule_test:
      - eval_time: 3m
        alertname: WorkloadHTTPErrorRateHigh
        exp_alerts: []
      - eval_time: 12m
        alertname: WorkloadHTTPErrorRateHigh
        exp_alerts:
          - exp_labels: {severity: critical, namespace: shop, workload: web, workload_type: deployment, source: traefik}
      - eval_time: 8m
        alertname: WorkloadHTTPLatencyHigh
        exp_alerts: []
      - eval_time: 20m
        alertname: WorkloadHTTPLatencyHigh
        exp_alerts:
          - exp_labels: {severity: warning, namespace: shop, workload: web, workload_type: deployment, source: traefik}
  # metrics (OpenTelemetry, joined to its pod's owner): 2 req/s with 10%
  # errors fires; the same 50% error share at 0.1 req/s (below the minimum
  # traffic) does not.
  - interval: 1m
    input_series:
      - series: 'namespace_workload_pod:kube_pod_owner:relabel{namespace="shop", pod="metrics-1", workload="metrics", workload_type="deployment"}'
        values: '1x40'
      - series: 'http_server_request_duration_seconds_count{namespace="shop", pod="metrics-1", http_response_status_code="200"}'
        values: '0+108x40'
      - series: 'http_server_request_duration_seconds_count{namespace="shop", pod="metrics-1", http_response_status_code="503"}'
        values: '0+12x40'
      - series: 'http_server_request_duration_seconds_bucket{namespace="shop", pod="metrics-1", le="0.25"}'
        values: '0+120x40'
      - series: 'http_server_request_duration_seconds_bucket{namespace="shop", pod="metrics-1", le="+Inf"}'
        values: '0+120x40'
    alert_rule_test:
      - eval_time: 12m
        alertname: WorkloadHTTPErrorRateHigh
        exp_alerts:
          - exp_labels: {severity: critical, namespace: shop, workload: metrics, workload_type: deployment, source: otel}
      - eval_time: 20m
        alertname: WorkloadHTTPLatencyHigh
        exp_alerts: []
  - interval: 1m
    input_series:
      - series: 'namespace_workload_pod:kube_pod_owner:relabel{namespace="shop", pod="metrics-1", workload="metrics", workload_type="deployment"}'
        values: '1x40'
      - series: 'http_server_request_duration_seconds_count{namespace="shop", pod="metrics-1", http_response_status_code="200"}'
        values: '0+3x40'
      - series: 'http_server_request_duration_seconds_count{namespace="shop", pod="metrics-1", http_response_status_code="500"}'
        values: '0+3x40'
      - series: 'http_server_request_duration_seconds_bucket{namespace="shop", pod="metrics-1", le="0.25"}'
        values: '0+1x40'
      - series: 'http_server_request_duration_seconds_bucket{namespace="shop", pod="metrics-1", le="+Inf"}'
        values: '0+6x40'
    alert_rule_test:
      - eval_time: 20m
        alertname: WorkloadHTTPErrorRateHigh
        exp_alerts: []
      - eval_time: 20m
        alertname: WorkloadHTTPLatencyHigh
        exp_alerts: []
`

// TestHTTPAlertsPromtool runs promtool test rules (the pinned Prometheus
// image) on the generated groups. It needs Docker; it skips without it.
func TestHTTPAlertsPromtool(t *testing.T) {
	if testing.Short() {
		t.Skip("promtool runs in Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not running")
	}
	out := httpKube(t, HTTPThresholds{})
	var doc struct {
		Spec struct {
			Groups []promGroup `yaml:"groups"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(out.Files[KubeRulesFile], &doc); err != nil {
		t.Fatal(err)
	}
	// Annotations hold templates promtool would compare verbatim: the test
	// checks labels.
	for gi := range doc.Spec.Groups {
		for ri := range doc.Spec.Groups[gi].Rules {
			doc.Spec.Groups[gi].Rules[ri].Annotations = nil
		}
	}
	dir := t.TempDir()
	rules, err := yaml.Marshal(map[string]any{"groups": doc.Spec.Groups})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.yml"), rules, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.yml"), []byte(promtoolTests), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "-v", dir+":/w:ro", "-w", "/w", "--entrypoint", "promtool", stackImage("prometheus"), "test", "rules", "test.yml")
	res, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("promtool test rules: %v\n%s", err, res)
	}
	t.Logf("%s", res)
}

func TestComposeHTTPAlerts(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"compose.yaml": shopCompose, ".env": "POSTGRES_PASSWORD=x\n"})
	out, err := Compose(ComposeInput{Project: openCompose(t, dir, "compose.yaml"), ProjectName: "p", Thresholds: HTTPThresholds{LatencyP95Ms: 250}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, h := range out.Summary.HTTP {
		got[h.Target] = h.Sources
	}
	// api is labelled for scraping; db is not (no request metrics).
	if !slices.Equal(got["api"], []string{SourceOTel}) || len(got["db"]) != 0 {
		t.Fatalf("sources %v", got)
	}
	rules := out.Files["monitoring/prometheus/alerts.yml"]
	if !strings.Contains(string(rules), `http_server_request_duration_seconds_count{job="api"}`) || ParseThresholds(rules).LatencyP95Ms != 250 {
		t.Fatalf("alerts.yml:\n%s", rules)
	}
	if _, err := Compose(ComposeInput{Project: openCompose(t, dir, "compose.yaml"), ProjectName: "p", Thresholds: HTTPThresholds{MinRequestsPerSecond: -1}}); err == nil {
		t.Fatal("negative traffic guard accepted")
	}
}
