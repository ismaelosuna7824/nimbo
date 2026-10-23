package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// add_monitoring: Prometheus, Grafana and alerting for a docker or
// Kubernetes workflow (POST /workflows/{id}/monitoring; GET previews).

const groupMonitoring = "Monitoring"

func registerMonitoringTools(r *registry, t *tools) {
	r.group = groupMonitoring
	addTool(r, "add_monitoring", "Add monitoring",
		"Add (or, when present, regenerate) monitoring for a docker or Kubernetes workflow so bottlenecks show before production. "+
			"Kubernetes: a kube-prometheus-stack helm source (pinned chart vendored, namespace monitoring: Prometheus Operator, Prometheus with 7d retention on a 5Gi volume, Alertmanager with a null receiver, Grafana with a generated admin password in a Secret, kube-state-metrics, node-exporter, the default Kubernetes alerts and dashboards) placed before any source with monitoring CRs, then a monitoring/ manifests source with a ServiceMonitor/PodMonitor for every Service that exposes metrics (port named metrics or *-metrics, or prometheus.io/scrape annotations; nothing else is scraped), a PrometheusRule with workload-scoped alerts for every Deployment/StatefulSet/DaemonSet of every environment (memory > 90% of limit, CPU throttling, OOM kills, restarts, no/unavailable replicas, HPA at maxReplicas with CPU above target) and a Grafana dashboard (replicas vs HPA, CPU and memory vs requests/limits, restarts, network). "+
			"Docker: monitoring/compose.yaml (listed after the compose files) with Prometheus, Grafana and cAdvisor (pinned images, healthchecks, limits, read-only) plus prometheus.yml (cAdvisor and services labelled prometheus.io/scrape/port/path), container alert rules, and Grafana provisioning with a per-service dashboard; the Grafana password goes to .env (GRAFANA_ADMIN_PASSWORD). "+
			"HTTP alerts (5xx ratio above error_percent for 5m, p95 above latency_p95_ms for 10m, both only above min_requests_per_second; defaults 5%, 500 ms, 0.5 req/s; kept by later runs) and requests/s, 5xx and p95 dashboard panels for every workload whose requests are measured: Kubernetes Ingresses through Traefik (k3s and the local cluster, which also serves class nginx; its metrics entrypoint is scraped by a PodMonitor), Ingresses of class nginx through ingress-nginx (production clusters running it with controller.metrics.enabled and its ServiceMonitor), and apps whose scraped metrics follow the OpenTelemetry HTTP semantic conventions (http_server_request_duration_seconds); docker: labelled services' OpenTelemetry HTTP metrics. summary.http lists each workload's sources (empty = no request metrics, no HTTP alert). "+
			"Running it again regenerates the generated files from the current services/workloads (no duplicates; chart values and passwords kept). preview=true only reports what it would add. Returns what was added, the alerts, scrape targets, Grafana credentials, resource cost and the local URLs (Grafana, Prometheus, Alertmanager) once deployed; then deploy.",
		t.addMonitoring)
}

type addMonitoringIn struct {
	Workflow             string  `json:"workflow,omitempty" jsonschema:"docker or Kubernetes workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Preview              bool    `json:"preview,omitempty" jsonschema:"only report what would be added (and the URLs when it is there), change nothing"`
	ErrorPercent         float64 `json:"error_percent,omitempty" jsonschema:"HTTP 5xx share of requests (0.1-100) that fires the error alert after 5 minutes (default 5, or the value set before)"`
	LatencyP95Ms         int     `json:"latency_p95_ms,omitempty" jsonschema:"p95 request latency in ms (10-600000) that fires the latency alert after 10 minutes (default 500, or the value set before)"`
	MinRequestsPerSecond float64 `json:"min_requests_per_second,omitempty" jsonschema:"traffic below which the HTTP alerts stay quiet (default 0.5, or the value set before)"`
}

func (t *tools) addMonitoring(ctx context.Context, in addMonitoringIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return nil, fmt.Errorf("%s is a %s workflow; add_monitoring is for docker and Kubernetes workflows", wf.Name, model.KindLabel(wf.Kind))
	}
	p := "/workflows/" + pathEscape(wf.ID) + "/monitoring"
	if in.Preview {
		var sum model.MonitoringSummary
		if err := t.c.get(ctx, p, &sum); err != nil {
			return nil, err
		}
		return map[string]any{"summary": sum}, nil
	}
	var res model.MonitoringResult
	var body any
	if in.ErrorPercent != 0 || in.LatencyP95Ms != 0 || in.MinRequestsPerSecond != 0 {
		body = model.MonitoringRequest{Thresholds: &model.MonitoringThresholds{ErrorPercent: in.ErrorPercent, LatencyP95Ms: in.LatencyP95Ms, MinRequestsPerSecond: in.MinRequestsPerSecond}}
	}
	if err := t.c.do(ctx, http.MethodPost, p, body, &res); err != nil {
		return nil, err
	}
	out := map[string]any{"summary": res.Summary}
	if res.Workflow != nil {
		out["workflow"] = map[string]any{"id": res.Workflow.ID, "name": res.Workflow.Name, "kind": res.Workflow.Kind, "diagnostics": convertDiagLines(res.Workflow.Diagnostics)}
		out["next"] = fmt.Sprintf("Deploy with deploy workflow %q; then open the Grafana link (summary.links) and sign in with summary.credentials.", res.Workflow.ID)
	}
	return out, nil
}
