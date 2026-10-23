package monitoring

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// HTTP alerts: request rate, 5xx ratio and p95 latency per workload (or
// compose service), recorded from whatever really measures its requests:
//
//   - traefik: the ingress controller of k3s (and the local cluster, which
//     also serves IngressClass nginx through it) counts every request it
//     routes per backend Service port (traefik_service_requests_total,
//     traefik_service_request_duration_seconds; service label
//     "<namespace>-<service>-<port>@kubernetes").
//   - ingress-nginx: the NGINX Ingress controller, for Ingresses of class
//     nginx on clusters that run it with controller.metrics.enabled and its
//     ServiceMonitor (nginx_ingress_controller_requests and
//     nginx_ingress_controller_request_duration_seconds; with the
//     ServiceMonitor's default honorLabels false the Ingress' namespace and
//     Service are exported_namespace / exported_service).
//   - otel: the OpenTelemetry HTTP semantic conventions
//     (http.server.request.duration, a histogram in seconds with
//     http.response.status_code) of applications whose metrics endpoint is
//     scraped: http_server_request_duration_seconds_* in Prometheus.
//
// Recording rules give every source the same series
// (workload:http_requests:rate5m, workload:http_errors:rate5m,
// workload:http_request_duration_seconds:p95_5m labelled namespace,
// workload, workload_type and source), which the alerts and the dashboard
// read. A workload without any of them gets no HTTP alert.

// Sources of request metrics.
const (
	SourceTraefik      = "traefik"
	SourceIngressNginx = "ingress-nginx"
	SourceOTel         = "otel"
)

// HTTPThresholds tune the HTTP alerts.
type HTTPThresholds = model.MonitoringThresholds

// DefaultThresholds: 5xx above 5% for 5 minutes, p95 above 500 ms for 10
// minutes, both only while the workload serves at least 0.5 requests per
// second (below that a couple of failed requests are not a trend).
var DefaultThresholds = HTTPThresholds{ErrorPercent: 5, LatencyP95Ms: 500, MinRequestsPerSecond: 0.5}

// ValidThresholds fills zero fields with the defaults and checks the ranges.
func ValidThresholds(t HTTPThresholds) (HTTPThresholds, error) {
	if t.ErrorPercent == 0 {
		t.ErrorPercent = DefaultThresholds.ErrorPercent
	}
	if t.LatencyP95Ms == 0 {
		t.LatencyP95Ms = DefaultThresholds.LatencyP95Ms
	}
	if t.MinRequestsPerSecond == 0 {
		t.MinRequestsPerSecond = DefaultThresholds.MinRequestsPerSecond
	}
	switch {
	case t.ErrorPercent < 0.1 || t.ErrorPercent > 100:
		return t, fmt.Errorf("errorPercent must be between 0.1 and 100")
	case t.LatencyP95Ms < 10 || t.LatencyP95Ms > 600000:
		return t, fmt.Errorf("latencyP95Ms must be between 10 and 600000")
	case t.MinRequestsPerSecond < 0.01 || t.MinRequestsPerSecond > 100000:
		return t, fmt.Errorf("minRequestsPerSecond must be between 0.01 and 100000")
	}
	return t, nil
}

// thresholdsLine is written in the generated rules files, so Update
// monitoring keeps thresholds set once.
const thresholdsPrefix = "# http-thresholds:"

func thresholdsLine(t HTTPThresholds) string {
	return fmt.Sprintf("%s errorPercent=%s latencyP95Ms=%d minRequestsPerSecond=%s\n", thresholdsPrefix,
		strconv.FormatFloat(t.ErrorPercent, 'f', -1, 64), t.LatencyP95Ms, strconv.FormatFloat(t.MinRequestsPerSecond, 'f', -1, 64))
}

var thresholdField = regexp.MustCompile(`(errorPercent|latencyP95Ms|minRequestsPerSecond)=([0-9.]+)`)

// ParseThresholds reads the thresholds of a generated rules file (zero
// fields when absent).
func ParseThresholds(data []byte) HTTPThresholds {
	var t HTTPThresholds
	for line := range strings.Lines(string(data)) {
		if !strings.HasPrefix(line, thresholdsPrefix) {
			continue
		}
		for _, m := range thresholdField.FindAllStringSubmatch(line, -1) {
			v, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				continue
			}
			switch m[1] {
			case "errorPercent":
				t.ErrorPercent = v
			case "latencyP95Ms":
				t.LatencyP95Ms = int(v)
			case "minRequestsPerSecond":
				t.MinRequestsPerSecond = v
			}
		}
	}
	return t
}

// httpSource is how one workload's requests are measured.
type httpSource struct {
	kind string // SourceTraefik | SourceIngressNginx | SourceOTel
	// traefik: the Traefik service labels; ingress-nginx: ingress and
	// Service pairs.
	traefik []string
	nginx   [][2]string
}

// kubeService is a Service and its ports.
type kubeService struct {
	namespace, name string
	selector        map[string]string
	labels          map[string]string
}

// kubeIngressBackend is a Service port an Ingress routes to.
type kubeIngressBackend struct {
	ingress, class, service, port string
}

// ingressBackends lists the backends of an Ingress (rules and default).
func ingressBackends(o *kube.Object) []kubeIngressBackend {
	class := digStr(o.Obj, "spec", "ingressClassName")
	if class == "" {
		class = digLabels(o.Obj, "metadata", "annotations")["kubernetes.io/ingress.class"]
	}
	var out []kubeIngressBackend
	add := func(svc any) {
		name := digStr(svc, "name")
		port := digStr(svc, "port", "name")
		if port == "" {
			port = digStr(svc, "port", "number")
		}
		if name != "" && port != "" {
			out = append(out, kubeIngressBackend{ingress: o.Name, class: class, service: name, port: port})
		}
	}
	if db := dig(o.Obj, "spec", "defaultBackend", "service"); db != nil {
		add(db)
	}
	for _, r := range digList(o.Obj, "spec", "rules") {
		for _, p := range digList(r, "http", "paths") {
			add(dig(p, "backend", "service"))
		}
	}
	return out
}

// TraefikService is the service label Traefik's Kubernetes Ingress
// provider gives a backend ("shop-frontend-http@kubernetes": the port as
// the Ingress names it, by name or number).
func TraefikService(namespace, service, port string) string {
	return namespace + "-" + service + "-" + port + "@kubernetes"
}

// servedByTraefik: Traefik serves the Ingress classes "" (k3s' default
// class), traefik and, on the local cluster, nginx (an alias Nimbo creates
// so Ingresses written for ingress-nginx run unchanged).
func servedByTraefik(class string) bool {
	return class == "" || class == "traefik" || class == "nginx"
}

// httpSources picks the request metrics of every workload: the Ingress
// backends routing to its Services and its scraped metrics endpoints.
func httpSources(workloads []*kubeWorkload, services []*kubeService, backends map[string][]kubeIngressBackend, scrapes []*kubeScrape) map[string][]httpSource {
	out := map[string][]httpSource{}
	for _, w := range workloads {
		var traefik []string
		var nginx [][2]string
		otel := false
		for _, s := range services {
			if s.namespace != w.namespace || !subset(s.selector, w.templateLabels) {
				continue
			}
			for _, b := range backends[s.namespace+"/"+s.name] {
				if servedByTraefik(b.class) {
					traefik = append(traefik, TraefikService(s.namespace, s.name, b.port))
				}
				if b.class == "nginx" {
					nginx = append(nginx, [2]string{b.ingress, s.name})
				}
			}
			for _, sc := range scrapes {
				if sc.namespace == s.namespace && sc.monitor == "ServiceMonitor" && subset(sc.selector, s.labels) {
					otel = true
				}
			}
		}
		for _, sc := range scrapes {
			if sc.namespace == w.namespace && sc.monitor == "PodMonitor" && subset(sc.selector, w.templateLabels) {
				otel = true
			}
		}
		var srcs []httpSource
		if len(traefik) > 0 {
			srcs = append(srcs, httpSource{kind: SourceTraefik, traefik: compactSorted(traefik)})
		}
		if len(nginx) > 0 {
			slices.SortFunc(nginx, func(a, b [2]string) int { return strings.Compare(a[0]+"/"+a[1], b[0]+"/"+b[1]) })
			srcs = append(srcs, httpSource{kind: SourceIngressNginx, nginx: slices.Compact(nginx)})
		}
		if otel {
			srcs = append(srcs, httpSource{kind: SourceOTel})
		}
		if len(srcs) > 0 {
			out[w.key()] = srcs
		}
	}
	return out
}

func compactSorted(s []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(s)))
}

func regexAlt(values []string) string {
	q := make([]string, len(values))
	for i, v := range values {
		q[i] = regexp.QuoteMeta(v)
	}
	return strings.Join(q, "|")
}

// httpRecs names the recorded series of one level (Kubernetes workloads or
// compose services) and the labels that identify a series.
type httpRecs struct {
	requests, errors, p95, by string
}

var (
	workloadRecs = httpRecs{"workload:http_requests:rate5m", "workload:http_errors:rate5m", "workload:http_request_duration_seconds:p95_5m", "namespace, workload, source"}
	serviceRecs  = httpRecs{"service:http_requests:rate5m", "service:http_errors:rate5m", "service:http_request_duration_seconds:p95_5m", "service, source"}
)

// recordRules records total, 5xx and p95 from a source's expressions.
func (r httpRecs) recordRules(total, errors, buckets string, labels map[string]string) []promRule {
	return []promRule{
		{Record: r.requests, Expr: total, Labels: labels},
		// 0 (not absent) while requests flow without a 5xx: the ratio and
		// the dashboard read 0%.
		{Record: r.errors, Expr: errors + "\n  or\n0 * " + total, Labels: labels},
		{Record: r.p95, Expr: "histogram_quantile(0.95, " + buckets + ")", Labels: labels},
	}
}

// recordings are the recording rules of one workload's source.
func recordings(w *kubeWorkload, s httpSource) []promRule {
	var total, errors, buckets string
	switch s.kind {
	case SourceTraefik:
		sel := fmt.Sprintf(`service=~%q`, regexAlt(s.traefik))
		total = fmt.Sprintf(`sum(rate(traefik_service_requests_total{%s}[5m]))`, sel)
		errors = fmt.Sprintf(`sum(rate(traefik_service_requests_total{%s, code=~"5.."}[5m]))`, sel)
		buckets = fmt.Sprintf(`sum by (le) (rate(traefik_service_request_duration_seconds_bucket{%s}[5m]))`, sel)
	case SourceIngressNginx:
		var ings, svcs []string
		for _, p := range s.nginx {
			ings, svcs = append(ings, p[0]), append(svcs, p[1])
		}
		sel := fmt.Sprintf(`exported_namespace=%q, ingress=~%q, exported_service=~%q`, w.namespace, regexAlt(compactSorted(ings)), regexAlt(compactSorted(svcs)))
		total = fmt.Sprintf(`sum(rate(nginx_ingress_controller_requests{%s}[5m]))`, sel)
		errors = fmt.Sprintf(`sum(rate(nginx_ingress_controller_requests{%s, status=~"5.."}[5m]))`, sel)
		buckets = fmt.Sprintf(`sum by (le) (rate(nginx_ingress_controller_request_duration_seconds_bucket{%s}[5m]))`, sel)
	case SourceOTel:
		own := " * on (namespace, pod) group_left () " + ownerOf(w.namespace, w.name, w.typ())
		ns := fmt.Sprintf(`namespace=%q`, w.namespace)
		total = fmt.Sprintf(`sum(rate(http_server_request_duration_seconds_count{%s}[5m])%s)`, ns, own)
		errors = fmt.Sprintf(`sum(rate(http_server_request_duration_seconds_count{%s, http_response_status_code=~"5.."}[5m])%s)`, ns, own)
		buckets = fmt.Sprintf(`sum by (le) (rate(http_server_request_duration_seconds_bucket{%s}[5m])%s)`, ns, own)
	}
	labels := map[string]string{"namespace": w.namespace, "workload": w.name, "workload_type": w.typ(), "source": s.kind}
	return workloadRecs.recordRules(total, errors, buckets, labels)
}

// otelRecordings record a compose service's OpenTelemetry HTTP metrics
// (Prometheus job = the service).
func otelServiceRecordings(service string) []promRule {
	job := fmt.Sprintf(`job=%q`, service)
	return serviceRecs.recordRules(
		fmt.Sprintf(`sum(rate(http_server_request_duration_seconds_count{%s}[5m]))`, job),
		fmt.Sprintf(`sum(rate(http_server_request_duration_seconds_count{%s, http_response_status_code=~"5.."}[5m]))`, job),
		fmt.Sprintf(`sum by (le) (rate(http_server_request_duration_seconds_bucket{%s}[5m]))`, job),
		map[string]string{"service": service, "source": SourceOTel})
}

// httpAlerts are the HTTP alerts of one workload or service (sel selects
// its recorded series; every source fires on its own).
func httpAlerts(r httpRecs, prefix, sel, ref string, t HTTPThresholds, sourceNote string) []alert {
	guard := fmt.Sprintf("and on (%s)\n%s{%s} >= %s", r.by, r.requests, sel, strconv.FormatFloat(t.MinRequestsPerSecond, 'f', -1, 64))
	pct := strconv.FormatFloat(t.ErrorPercent, 'f', -1, 64)
	rps := strconv.FormatFloat(t.MinRequestsPerSecond, 'f', -1, 64)
	return []alert{
		{name: prefix + "HTTPErrorRateHigh", severity: "critical", forDur: "5m",
			expr:    fmt.Sprintf("(\n  %s{%s}\n    / %s{%s}\n) > %s\n%s", r.errors, sel, r.requests, sel, strconv.FormatFloat(t.ErrorPercent/100, 'f', -1, 64), guard),
			short:   fmt.Sprintf("5xx above %s%% of requests for 5m (%s)", pct, sourceNote),
			summary: ref + ": {{ $value | humanizePercentage }} of requests fail with 5xx (measured by {{ $labels.source }})",
			description: fmt.Sprintf("More than %s%% of the requests answered 5xx for 5 minutes while serving at least %s requests/s: users see errors. ", pct, rps) +
				"What to do: read the logs around the first errors, check dependencies (database, downstream services) and recent rollouts; roll back if it started with a deploy."},
		{name: prefix + "HTTPLatencyHigh", severity: "warning", forDur: "10m",
			expr:    fmt.Sprintf("%s{%s} > %s\n%s", r.p95, sel, strconv.FormatFloat(float64(t.LatencyP95Ms)/1000, 'f', -1, 64), guard),
			short:   fmt.Sprintf("p95 latency above %d ms for 10m (%s)", t.LatencyP95Ms, sourceNote),
			summary: ref + ": p95 latency is {{ $value | humanizeDuration }} (measured by {{ $labels.source }})",
			description: fmt.Sprintf("The slowest 5%% of requests took longer than %d ms for 10 minutes while serving at least %s requests/s. ", t.LatencyP95Ms, rps) +
				"What to do: compare with CPU throttling and memory on the dashboard (saturated replicas queue requests), check slow dependencies, and scale out or raise the CPU limit."},
	}
}

// traefikPodMonitor scrapes the cluster's Traefik (k3s runs it in
// kube-system with its metrics entrypoint on port 9100, named metrics).
func traefikPodMonitor(ns string) k8sObject {
	return k8sObject{APIVersion: "monitoring.coreos.com/v1", Kind: "PodMonitor", Metadata: meta("traefik", ns, nil),
		Spec: map[string]any{
			"namespaceSelector":   map[string]any{"matchNames": []string{"kube-system"}},
			"selector":            map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": "traefik"}},
			"podMetricsEndpoints": []monitorEndpoint{{Port: "metrics", Path: "/metrics", Interval: "30s"}},
		}}
}

// httpSummary lists, per workload, where its HTTP alerts come from.
func httpSummary(workloads []*kubeWorkload, sources map[string][]httpSource) []model.MonitoringHTTP {
	out := []model.MonitoringHTTP{}
	for _, w := range workloads {
		h := model.MonitoringHTTP{Target: w.label(), Sources: []string{}}
		for _, s := range sources[w.key()] {
			h.Sources = append(h.Sources, s.kind)
		}
		out = append(out, h)
	}
	return out
}
