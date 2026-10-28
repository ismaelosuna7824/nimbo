package monitoring

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Kubernetes monitoring is two sources: the kube-prometheus-stack chart
// (Prometheus Operator, Prometheus, Alertmanager, Grafana,
// kube-state-metrics, node-exporter and the kubernetes-mixin rules and
// dashboards) and, applied after it so its CRDs exist, a manifests source
// with what is specific to the workflow: ServiceMonitors/PodMonitors for the
// Services that expose metrics, a PrometheusRule per workload and a Grafana
// dashboard ConfigMap.
const (
	// KubeChartID is the palette entry of the chart.
	KubeChartID = "chart:kube-prometheus-stack"
	// KubeChartName is the chart (and wrapper chart / release) name.
	KubeChartName = "kube-prometheus-stack"
	// KubeNamespace is where the stack and the generated objects go.
	KubeNamespace = "monitoring"
	// KubeDir is the generated manifests source.
	KubeDir = "monitoring"
	// KubeAdminSecret is the Secret holding the Grafana admin credentials
	// (a template of the wrapper chart, so it exists before Grafana starts).
	KubeAdminSecret = "grafana-admin"
)

// Generated files of the manifests source.
const (
	KubeRulesFile      = KubeDir + "/rules.yaml"
	kubeScrapeFile     = KubeDir + "/scrape.yaml"
	kubeDashboardsFile = KubeDir + "/dashboards.yaml"
)

// KubeAdminSecretFile is the Secret template in the wrapper chart dir.
func KubeAdminSecretFile(chartDir string) string { return chartDir + "/templates/grafana-admin.yaml" }

// KubeChart is the pinned chart (the palette's kube-prometheus-stack entry).
func KubeChart() kubecatalog.Chart {
	it, ok := kubecatalog.Find(KubeChartID)
	if !ok || it.Chart == nil {
		panic("monitoring: the chart palette has no " + KubeChartID) // the palette is static
	}
	return *it.Chart
}

// KubeValues are the wrapper chart's values: sized for one small node
// (the local k3s cluster) and valid for production clusters, where
// retention, storage and resources grow with the number of series.
func KubeValues() string {
	c := KubeChart()
	return fmt.Sprintf("# %s %s, added by Add monitoring (the dependency's values go under %q).\n# Every option: helm show values %s --repo %s --version %s\n",
		c.Name, c.Version, c.Name, c.Name, c.Repository, c.Version) + kubeValues
}

const kubeValues = `# Sized for a small cluster: raise retention, storage and resources with the
# number of series in production.
kube-prometheus-stack:
  prometheus:
    prometheusSpec:
      # Select every ServiceMonitor, PodMonitor, Probe and PrometheusRule of
      # the cluster, not only the ones labelled release: kube-prometheus-stack.
      serviceMonitorSelectorNilUsesHelmValues: false
      podMonitorSelectorNilUsesHelmValues: false
      probeSelectorNilUsesHelmValues: false
      ruleSelectorNilUsesHelmValues: false
      scrapeConfigSelectorNilUsesHelmValues: false
      retention: 7d
      retentionSize: 4GB
      resources:
        requests:
          cpu: 200m
          memory: 512Mi
        limits:
          memory: 1536Mi
      # Metrics survive pod restarts (the cluster's default StorageClass:
      # local-path on the local k3s cluster).
      storageSpec:
        volumeClaimTemplate:
          spec:
            accessModes: ["ReadWriteOnce"]
            resources:
              requests:
                storage: 5Gi
  # Alertmanager runs with the chart's default route: every alert goes to
  # the "null" receiver and shows in the Alertmanager UI. Add a receiver
  # (Slack, PagerDuty, email) under alertmanager.config to get notified.
  alertmanager:
    alertmanagerSpec:
      resources:
        requests:
          cpu: 10m
          memory: 32Mi
        limits:
          memory: 128Mi
  grafana:
    # Admin credentials from the Secret in templates/grafana-admin.yaml
    # (generated password; no default password).
    admin:
      existingSecret: grafana-admin
      userKey: admin-user
      passwordKey: admin-password
    # Dashboards come from ConfigMaps (label grafana_dashboard: "1").
    persistence:
      enabled: false
    resources:
      requests:
        cpu: 50m
        memory: 160Mi
      limits:
        memory: 512Mi
    sidecar:
      resources:
        requests:
          cpu: 10m
          memory: 64Mi
        limits:
          memory: 128Mi
  prometheusOperator:
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
      limits:
        memory: 256Mi
    prometheusConfigReloader:
      resources:
        requests:
          cpu: 10m
          memory: 32Mi
        limits:
          memory: 64Mi
    admissionWebhooks:
      patch:
        resources:
          requests:
            cpu: 10m
            memory: 32Mi
          limits:
            memory: 64Mi
  kube-state-metrics:
    resources:
      requests:
        cpu: 10m
        memory: 64Mi
      limits:
        memory: 192Mi
  prometheus-node-exporter:
    # The root filesystem mount needs "/" to be a shared mount, which k3s in
    # Docker lacks; enable it on real nodes for host filesystem metrics.
    hostRootFsMount:
      enabled: false
    resources:
      requests:
        cpu: 10m
        memory: 32Mi
      limits:
        memory: 64Mi
  # k3s runs these inside the server process: nothing to scrape separately
  # (enable them on clusters that run them as pods).
  kubeControllerManager:
    enabled: false
  kubeScheduler:
    enabled: false
  kubeProxy:
    enabled: false
  kubeEtcd:
    enabled: false
`

// kubeCost is what KubeValues requests (Prometheus, Alertmanager,
// Grafana and its sidecars, the operator and its config reloaders,
// kube-state-metrics, node-exporter) and the Prometheus volume.
var kubeCost = model.MonitoringCost{
	CPUMillis:   200 + 10 + 10 + 50 + 3*10 + 50 + 2*10 + 10 + 10,
	MemoryBytes: (512 + 32 + 32 + 160 + 3*64 + 64 + 2*32 + 64 + 32) << 20,
	DiskBytes:   5 << 30,
}

// KubeAdminSecretYAML is the Secret template with the Grafana admin
// credentials (password: alphanumeric, so Helm templating leaves it alone).
func KubeAdminSecretYAML(password string) []byte {
	return []byte(`# ` + generatedMarker + `: the Grafana admin credentials
# (grafana.admin.existingSecret in values.yaml). Change the password and
# deploy again to rotate it; keep real credentials out of git.
apiVersion: v1
kind: Secret
metadata:
  name: ` + KubeAdminSecret + `
  namespace: {{ .Release.Namespace }}
  labels:
    app.kubernetes.io/part-of: monitoring
    app.kubernetes.io/managed-by: {{ .Release.Service }}
type: Opaque
stringData:
  admin-user: ` + grafanaUser + `
  admin-password: ` + password + `
`)
}

var adminPasswordRe = regexp.MustCompile(`(?m)^\s*admin-password:\s*"?([A-Za-z0-9]+)"?\s*$`)

// KubeAdminPassword reads the password of a KubeAdminSecretYAML file ("" when none).
func KubeAdminPassword(data []byte) string {
	if m := adminPasswordRe.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// IsGenerated reports whether a file was written by Add monitoring.
func IsGenerated(data []byte) bool { return bytes.Contains(data, []byte(generatedMarker)) }

// ---------------------------------------------------------------------------
// Inventory: what the workflow deploys.

type kubeWorkload struct {
	kind, namespace, name string
	// memoryLimit / cpuLimit: a container sets the limit (in some environment).
	memoryLimit, cpuLimit bool
	// hpa scales the workload on CPU utilization ("" = none).
	hpa       string
	podLabels map[string]string
	// templateLabels are the pod template's labels (what Services select).
	templateLabels map[string]string
	// annotated: the pod template asks to be scraped (prometheus.io/scrape).
	annotated               bool
	annPort, annPath, annSc string
}

func (w *kubeWorkload) key() string   { return w.kind + "/" + w.namespace + "/" + w.name }
func (w *kubeWorkload) typ() string   { return strings.ToLower(w.kind) }
func (w *kubeWorkload) label() string { return w.namespace + "/" + w.kind + "/" + w.name }

// kubeScrape is one metrics endpoint: a ServiceMonitor (the Service has
// labels to select it by and a named port) or a PodMonitor (selecting the
// pods behind the Service, or an annotated workload's pods).
type kubeScrape struct {
	monitor    string // ServiceMonitor | PodMonitor
	namespace  string
	name       string // the Service or workload
	selector   map[string]string
	port       string
	portNumber int
	path       string
	scheme     string
}

// KubeInput is what the Kubernetes generator reads.
type KubeInput struct {
	// Projects are the workflow rendered for each environment (only the
	// base when it has none): alerts cover the union of their workloads.
	Projects []*kube.Project
	// Own reports the sources that are the monitoring itself (skipped).
	Own func(source int) bool
	// Namespace is where the generated objects go (the chart's namespace).
	Namespace string
	// Thresholds tune the HTTP alerts (zero fields: defaults).
	Thresholds HTTPThresholds
}

// KubeOutput is the generated manifests source and what it holds.
type KubeOutput struct {
	// Files are the generated files (a nil value removes a file generated
	// before that is no longer needed).
	Files      map[string][]byte
	Alerts     []model.MonitoringAlert
	Scrapes    []model.MonitoringScrape
	Dashboards []string
	Notes      []string
	// HTTP lists every workload's request-metrics sources.
	HTTP       []model.MonitoringHTTP
	Thresholds HTTPThresholds
	// Live is how each workload's requests are read live.
	Live []LiveSeries
}

func dig(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

func digStr(m any, path ...string) string {
	switch v := dig(m, path...).(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func digList(m any, path ...string) []any {
	l, _ := dig(m, path...).([]any)
	return l
}

func digLabels(m any, path ...string) map[string]string {
	mm, ok := dig(m, path...).(map[string]any)
	if !ok || len(mm) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range mm {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// metricsPortName recognizes the conventional names of a metrics port.
func metricsPortName(name string) bool {
	return name == "metrics" || name == "prometheus" || strings.HasSuffix(name, "-metrics")
}

// inventory collects the workloads and metrics endpoints of every
// environment's rendered objects (not the monitoring's own sources).
func (in KubeInput) inventory() ([]*kubeWorkload, []*kubeScrape, []*kubeService, map[string][]kubeIngressBackend) {
	workloads := map[string]*kubeWorkload{}
	scrapes := map[string]*kubeScrape{}
	services := map[string]*kubeService{}
	backends := map[string][]kubeIngressBackend{} // "<ns>/<service>" -> routes to it
	for _, p := range in.Projects {
		for _, o := range p.Objects {
			if in.Own != nil && in.Own(o.Source) {
				continue
			}
			switch o.Kind {
			case "Deployment", "StatefulSet", "DaemonSet":
				w := workloads[o.Kind+"/"+o.Namespace+"/"+o.Name]
				if w == nil {
					w = &kubeWorkload{kind: o.Kind, namespace: o.Namespace, name: o.Name}
					workloads[w.key()] = w
				}
				w.podLabels = digLabels(o.Obj, "spec", "selector", "matchLabels")
				w.templateLabels = digLabels(o.Obj, "spec", "template", "metadata", "labels")
				for _, c := range digList(o.Obj, "spec", "template", "spec", "containers") {
					w.memoryLimit = w.memoryLimit || digStr(c, "resources", "limits", "memory") != ""
					w.cpuLimit = w.cpuLimit || digStr(c, "resources", "limits", "cpu") != ""
				}
				ann := digLabels(o.Obj, "spec", "template", "metadata", "annotations")
				if ann["prometheus.io/scrape"] == "true" {
					w.annotated = true
					w.annPort, w.annPath, w.annSc = ann["prometheus.io/port"], ann["prometheus.io/path"], ann["prometheus.io/scheme"]
				}
			case "Service":
				for _, s := range serviceScrapes(o) {
					scrapes[s.monitor+"/"+s.namespace+"/"+s.name+"/"+s.port+strconv.Itoa(s.portNumber)] = s
				}
				if sel := digLabels(o.Obj, "spec", "selector"); len(sel) > 0 {
					services[o.Namespace+"/"+o.Name] = &kubeService{namespace: o.Namespace, name: o.Name, selector: sel, labels: digLabels(o.Obj, "metadata", "labels")}
				}
			case "Ingress":
				for _, b := range ingressBackends(o) {
					k := o.Namespace + "/" + b.service
					if !slices.Contains(backends[k], b) {
						backends[k] = append(backends[k], b)
					}
				}
			}
		}
		// HPAs scaling on CPU utilization (after the workloads they target).
		for _, o := range p.Objects {
			if o.Kind != "HorizontalPodAutoscaler" || in.Own != nil && in.Own(o.Source) {
				continue
			}
			ref := dig(o.Obj, "spec", "scaleTargetRef")
			w := workloads[digStr(ref, "kind")+"/"+o.Namespace+"/"+digStr(ref, "name")]
			if w != nil && hpaOnCPU(o.Obj) {
				w.hpa = o.Name
			}
		}
	}
	ws := slices.SortedFunc(maps.Values(workloads), func(a, b *kubeWorkload) int { return strings.Compare(a.label(), b.label()) })
	// Annotated workloads whose pods a Service scrape already covers are not scraped twice.
	for _, w := range ws {
		if !w.annotated || len(w.podLabels) == 0 {
			continue
		}
		covered := false
		for _, s := range scrapes {
			if s.namespace == w.namespace && s.monitor == "PodMonitor" && subset(s.selector, w.podLabels) {
				covered = true
			}
		}
		if covered {
			continue
		}
		s := &kubeScrape{monitor: "PodMonitor", namespace: w.namespace, name: w.name, selector: w.podLabels,
			path: orDefault(w.annPath, "/metrics"), scheme: w.annSc}
		if n, err := strconv.Atoi(w.annPort); err == nil {
			s.portNumber = n
		} else if w.annPort != "" {
			s.port = w.annPort
		} else {
			continue // no port to scrape: Prometheus would try every container port
		}
		scrapes["PodMonitor/"+w.namespace+"/"+w.name] = s
	}
	ss := slices.SortedFunc(maps.Values(scrapes), func(a, b *kubeScrape) int {
		return strings.Compare(a.namespace+"/"+a.name+"/"+a.port, b.namespace+"/"+b.name+"/"+b.port)
	})
	svcs := slices.SortedFunc(maps.Values(services), func(a, b *kubeService) int { return strings.Compare(a.namespace+"/"+a.name, b.namespace+"/"+b.name) })
	return ws, ss, svcs, backends
}

func subset(sel, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return len(sel) > 0
}

func hpaOnCPU(obj map[string]any) bool {
	if digStr(obj, "spec", "targetCPUUtilizationPercentage") != "" { // autoscaling/v1
		return true
	}
	for _, m := range digList(obj, "spec", "metrics") {
		if digStr(m, "type") == "Resource" && digStr(m, "resource", "name") == "cpu" && digStr(m, "resource", "target", "type") == "Utilization" {
			return true
		}
	}
	return false
}

// serviceScrapes are the metrics ports of a Service: ports named like a
// metrics port, or the prometheus.io/port of a Service annotated
// prometheus.io/scrape: "true". Nothing else is scraped: an application
// that does not say it exposes metrics gets no scrape target.
func serviceScrapes(o *kube.Object) []*kubeScrape {
	ann := digLabels(o.Obj, "metadata", "annotations")
	labels := digLabels(o.Obj, "metadata", "labels")
	selector := digLabels(o.Obj, "spec", "selector")
	path := orDefault(ann["prometheus.io/path"], "/metrics")
	var out []*kubeScrape
	for _, sp := range digList(o.Obj, "spec", "ports") {
		name, port, target := digStr(sp, "name"), digStr(sp, "port"), digStr(sp, "targetPort")
		annotated := ann["prometheus.io/scrape"] == "true" && (ann["prometheus.io/port"] == "" && len(digList(o.Obj, "spec", "ports")) == 1 ||
			ann["prometheus.io/port"] == port || ann["prometheus.io/port"] == target)
		if !metricsPortName(name) && !annotated {
			continue
		}
		s := &kubeScrape{namespace: o.Namespace, name: o.Name, path: path, scheme: ann["prometheus.io/scheme"]}
		switch {
		case len(labels) > 0 && name != "":
			s.monitor, s.selector, s.port = "ServiceMonitor", labels, name
		case len(selector) > 0:
			// No labels to select the Service by: scrape its pods directly.
			s.monitor, s.selector = "PodMonitor", selector
			if target == "" {
				target = port
			}
			if n, err := strconv.Atoi(target); err == nil {
				s.portNumber = n
			} else {
				s.port = target
			}
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---------------------------------------------------------------------------
// Generation.

// Kube generates the manifests source of a Kubernetes workflow.
func Kube(in KubeInput) KubeOutput {
	ns := orDefault(in.Namespace, KubeNamespace)
	workloads, scrapes, services, backends := in.inventory()
	t, err := ValidThresholds(in.Thresholds)
	if err != nil {
		t = DefaultThresholds
	}
	sources := httpSources(workloads, services, backends, scrapes)
	out := KubeOutput{Files: map[string][]byte{}, Alerts: []model.MonitoringAlert{}, Scrapes: []model.MonitoringScrape{},
		HTTP: httpSummary(workloads, sources), Thresholds: t, Live: kubeLive(workloads, sources)}

	// Alerts: one rule group per workload (its HTTP recording rules first).
	var groups []promGroup
	traefik := false
	for _, w := range workloads {
		labels := map[string]string{"namespace": w.namespace, "workload": w.name, "workload_type": w.typ()}
		g := promGroup{Name: w.namespace + "/" + w.typ() + "/" + w.name}
		alerts := workloadAlerts(w)
		if srcs := sources[w.key()]; len(srcs) > 0 {
			names := make([]string, len(srcs))
			for i, s := range srcs {
				g.Rules = append(g.Rules, recordings(w, s)...)
				traefik = traefik || s.kind == SourceTraefik
				names[i] = s.kind
			}
			sel := fmt.Sprintf("namespace=%q, workload=%q", w.namespace, w.name)
			alerts = append(alerts, httpAlerts(workloadRecs, "Workload", sel, w.namespace+"/"+w.name, t, strings.Join(names, ", "))...)
		}
		for _, a := range alerts {
			g.Rules = append(g.Rules, a.rule(labels))
			out.Alerts = append(out.Alerts, model.MonitoringAlert{Name: a.name, Severity: a.severity, Target: w.label(), Summary: a.short})
		}
		groups = append(groups, g)
	}
	header := "# " + generatedMarker + ". Update monitoring regenerates this file from\n# the workflow's workloads: edit the copy in another file to customize.\n"
	if len(groups) > 0 {
		rule := k8sObject{APIVersion: "monitoring.coreos.com/v1", Kind: "PrometheusRule",
			Metadata: meta("workload-alerts", ns, nil), Spec: map[string]any{"groups": groups}}
		out.Files[KubeRulesFile] = encodeDocs(header+thresholdsLine(t)+"# Workload alerts that complement kube-prometheus-stack's default rules\n# (kubernetes-mixin): memory near the limit, CPU throttling, OOM kills,\n# restarts, unavailable replicas, HPAs saturated at maxReplicas, and HTTP\n# 5xx ratio / p95 latency from the ingress controller or the app's\n# OpenTelemetry HTTP metrics (workload:http_* recording rules).\n", rule)
	} else {
		out.Files[KubeRulesFile] = nil
		out.Notes = append(out.Notes, "No Deployment, StatefulSet or DaemonSet yet: add workloads and run Update monitoring to get their alerts.")
	}

	// Scrape targets.
	var monitors []k8sObject
	for _, s := range scrapes {
		ep := monitorEndpoint{Port: s.port, PortNumber: s.portNumber, Path: s.path, Scheme: s.scheme, Interval: "30s"}
		spec := map[string]any{
			"namespaceSelector": map[string]any{"matchNames": []string{s.namespace}},
			"selector":          map[string]any{"matchLabels": s.selector},
		}
		if s.monitor == "ServiceMonitor" {
			spec["endpoints"] = []monitorEndpoint{ep}
		} else {
			spec["podMetricsEndpoints"] = []monitorEndpoint{ep}
		}
		monitors = append(monitors, k8sObject{APIVersion: "monitoring.coreos.com/v1", Kind: s.monitor,
			Metadata: meta(objectName(s.namespace+"-"+s.name), ns, nil), Spec: spec})
		port := s.port
		if port == "" {
			port = strconv.Itoa(s.portNumber)
		}
		out.Scrapes = append(out.Scrapes, model.MonitoringScrape{Target: s.namespace + "/" + s.name, Port: port, Path: s.path, Via: s.monitor})
	}
	appMonitors := len(monitors)
	if traefik {
		monitors = append(monitors, traefikPodMonitor(ns))
		out.Scrapes = append(out.Scrapes, model.MonitoringScrape{Target: "kube-system/traefik", Port: "metrics", Path: "/metrics", Via: "PodMonitor (ingress request metrics)"})
	}
	if len(monitors) > 0 {
		out.Files[kubeScrapeFile] = encodeDocs(header+"# Application metrics: Services with a port named metrics (or *-metrics)\n# or annotated prometheus.io/scrape: \"true\" (prometheus.io/port, /path);\n# the cluster's Traefik (kube-system, metrics entrypoint) when Ingresses\n# route to the workloads through it.\n", monitors...)
	} else {
		out.Files[kubeScrapeFile] = nil
	}
	if appMonitors == 0 {
		out.Notes = append(out.Notes, "No Service exposes a metrics port (named metrics or annotated prometheus.io/scrape): only container, Kubernetes and ingress metrics are collected.")
	}

	// Dashboard.
	board := kubeDashboard(workloads, len(sources) > 0)
	cm := k8sObject{APIVersion: "v1", Kind: "ConfigMap",
		Metadata: meta("workload-dashboards", ns, map[string]string{"grafana_dashboard": "1"}),
		Data:     map[string]string{"workloads.json": string(board)}}
	out.Files[kubeDashboardsFile] = encodeDocs(header+"# Grafana loads ConfigMaps labelled grafana_dashboard: \"1\" (its sidecar).\n", cm)
	out.Dashboards = []string{"Workloads overview"}
	return out
}

var nameClean = regexp.MustCompile(`[^a-z0-9-]+`)

func objectName(s string) string {
	s = strings.Trim(nameClean.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

type k8sMeta struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

type k8sObject struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   k8sMeta           `yaml:"metadata"`
	Spec       any               `yaml:"spec,omitempty"`
	Data       map[string]string `yaml:"data,omitempty"`
}

func meta(name, ns string, extra map[string]string) k8sMeta {
	labels := map[string]string{"app.kubernetes.io/part-of": "monitoring", "app.kubernetes.io/managed-by": "nimbo"}
	maps.Copy(labels, extra)
	return k8sMeta{Name: name, Namespace: ns, Labels: labels}
}

type monitorEndpoint struct {
	Port       string `yaml:"port,omitempty"`
	PortNumber int    `yaml:"portNumber,omitempty"`
	Path       string `yaml:"path"`
	Scheme     string `yaml:"scheme,omitempty"`
	Interval   string `yaml:"interval"`
}

// promRule is an alerting (Alert) or recording (Record) rule.
type promRule struct {
	Record      string            `yaml:"record,omitempty"`
	Alert       string            `yaml:"alert,omitempty"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type promGroup struct {
	Name  string     `yaml:"name"`
	Rules []promRule `yaml:"rules"`
}

// alert is one alerting rule; description carries the runbook hints.
type alert struct {
	name, severity, forDur, expr string
	short, summary, description  string
}

func (a alert) rule(labels map[string]string) promRule {
	l := map[string]string{"severity": a.severity}
	maps.Copy(l, labels)
	return promRule{Alert: a.name, Expr: a.expr, For: a.forDur, Labels: l,
		Annotations: map[string]string{"summary": a.summary, "description": a.description}}
}

func encodeDocs(header string, docs ...k8sObject) []byte {
	vs := make([]any, len(docs))
	for i, d := range docs {
		vs[i] = d
	}
	return encodeYAML(header, vs...)
}

// encodeYAML writes values as a multi-document YAML stream after header.
func encodeYAML(header string, docs ...any) []byte {
	var b bytes.Buffer
	b.WriteString(header)
	for i, d := range docs {
		if i > 0 {
			b.WriteString("---\n")
		}
		e := yaml.NewEncoder(&b)
		e.SetIndent(2)
		if err := e.Encode(d); err != nil {
			panic(fmt.Sprintf("monitoring: encode: %v", err)) // built here: a failure is a bug
		}
		_ = e.Close()
	}
	return b.Bytes()
}

// PromQL building blocks (kube-prometheus-stack's job names and the
// kubernetes-mixin pod-to-workload recording rule).
const (
	cadvisor = `job="kubelet", metrics_path="/metrics/cadvisor"`
	ksm      = `job="kube-state-metrics"`
)

func ownerOf(ns, workload, typ string) string {
	return fmt.Sprintf(`namespace_workload_pod:kube_pod_owner:relabel{namespace=%q, workload=%q, workload_type=%q}`, ns, workload, typ)
}

// workloadAlerts are the alerts of one workload. They complement the
// chart's kubernetes-mixin rules (KubePodCrashLooping,
// KubeDeploymentReplicasMismatch, KubeHpaMaxedOut, CPUThrottlingHigh at
// info…) with workload-scoped thresholds and what to do about them.
func workloadAlerts(w *kubeWorkload) []alert {
	ns, own := fmt.Sprintf("namespace=%q", w.namespace), ownerOf(w.namespace, w.name, w.typ())
	ref := w.namespace + "/" + w.name
	var out []alert
	if w.memoryLimit {
		out = append(out, alert{name: "WorkloadMemoryNearLimit", severity: "warning", forDur: "10m",
			expr: fmt.Sprintf(`max by (namespace, pod, container) (container_memory_working_set_bytes{%s, %s, container!="", image!=""})
  / on (namespace, pod, container)
max by (namespace, pod, container) (kube_pod_container_resource_limits{%s, %s, resource="memory"})
  * on (namespace, pod) group_left (workload) %s
  > 0.9`, cadvisor, ns, ksm, ns, own),
			short:   "Memory above 90% of the limit for 10m",
			summary: ref + ": {{ $labels.pod }}/{{ $labels.container }} uses {{ $value | humanizePercentage }} of its memory limit",
			description: "The working set has stayed above 90% of resources.limits.memory for 10 minutes; at 100% the kernel OOM-kills the container. " +
				"What to do: compare with the peak of a load test and raise the limit with headroom (~30%), or look for a leak if memory only grows between restarts."})
	}
	if w.cpuLimit {
		out = append(out, alert{name: "WorkloadCPUThrottled", severity: "warning", forDur: "10m",
			expr: fmt.Sprintf(`sum by (namespace, pod, container) (increase(container_cpu_cfs_throttled_periods_total{%s, %s, container!=""}[5m]))
  / on (namespace, pod, container)
sum by (namespace, pod, container) (increase(container_cpu_cfs_periods_total{%s, %s, container!=""}[5m]))
  * on (namespace, pod) group_left (workload) %s
  > 0.5`, cadvisor, ns, cadvisor, ns, own),
			short:   "CPU throttled in over 50% of periods for 10m",
			summary: ref + ": {{ $labels.pod }}/{{ $labels.container }} is CPU-throttled in {{ $value | humanizePercentage }} of its scheduling periods",
			description: "The container hits resources.limits.cpu in most 100 ms periods: requests queue and latency grows even when average CPU looks low. " +
				"What to do: raise the CPU limit (or drop it and keep the request), and let the HPA add replicas earlier (lower its CPU target)."})
	}
	out = append(out,
		alert{name: "WorkloadOOMKilled", severity: "warning",
			expr: fmt.Sprintf(`max by (namespace, pod, container) (kube_pod_container_status_last_terminated_reason{%s, %s, reason="OOMKilled"})
  * on (namespace, pod) group_left (workload) %s
and on (namespace, pod, container)
increase(kube_pod_container_status_restarts_total{%s, %s}[15m]) > 0`, ksm, ns, own, ksm, ns),
			short:   "A container was OOM-killed in the last 15m",
			summary: ref + ": {{ $labels.pod }}/{{ $labels.container }} was OOM-killed",
			description: "The container went over its memory limit and the kernel killed it; in-flight requests failed. " +
				"What to do: raise resources.limits.memory from the memory peak of a load test, or cap the app's own heap/cache below the limit."},
		alert{name: "WorkloadRestarting", severity: "warning",
			expr: fmt.Sprintf(`sum by (namespace, pod, container) (increase(kube_pod_container_status_restarts_total{%s, %s}[15m]))
  * on (namespace, pod) group_left (workload) %s
  > 2`, ksm, ns, own),
			short:   "More than 2 restarts in 15m",
			summary: ref + ": {{ $labels.pod }}/{{ $labels.container }} restarted {{ $value | humanize }} times in 15 minutes",
			description: "Repeated restarts drop connections and lose in-memory state. " +
				"What to do: read the previous container's logs (kubectl logs --previous), check the liveness probe timing and the exit reason (OOMKilled, Error)."})
	var down, unavailable string
	switch w.kind {
	case "Deployment":
		sel := fmt.Sprintf(`%s, %s, deployment=%q`, ksm, ns, w.name)
		down = fmt.Sprintf("kube_deployment_status_replicas_available{%s} == 0\nand on (namespace, deployment)\nkube_deployment_spec_replicas{%s} > 0", sel, sel)
		unavailable = fmt.Sprintf("kube_deployment_status_replicas_unavailable{%s} > 0", sel)
	case "StatefulSet":
		sel := fmt.Sprintf(`%s, %s, statefulset=%q`, ksm, ns, w.name)
		down = fmt.Sprintf("kube_statefulset_status_replicas_ready{%s} == 0\nand on (namespace, statefulset)\nkube_statefulset_replicas{%s} > 0", sel, sel)
		unavailable = fmt.Sprintf("kube_statefulset_replicas{%s}\n  - on (namespace, statefulset)\nkube_statefulset_status_replicas_ready{%s}\n  > 0", sel, sel)
	case "DaemonSet":
		sel := fmt.Sprintf(`%s, %s, daemonset=%q`, ksm, ns, w.name)
		down = fmt.Sprintf("kube_daemonset_status_number_available{%s} == 0\nand on (namespace, daemonset)\nkube_daemonset_status_desired_number_scheduled{%s} > 0", sel, sel)
		unavailable = fmt.Sprintf("kube_daemonset_status_number_unavailable{%s} > 0", sel)
	}
	out = append(out,
		alert{name: "WorkloadDown", severity: "critical", forDur: "2m", expr: down,
			short:   "No available replica for 2m",
			summary: ref + " has no available replica",
			description: "Every replica is down or not ready: the workload serves no traffic. " +
				"What to do: check the pods' events and logs (image pull errors, failing readiness probes, crash loops, unschedulable pods)."},
		alert{name: "WorkloadReplicasUnavailable", severity: "warning", forDur: "10m", expr: unavailable,
			short:   "Replicas unavailable for 10m",
			summary: ref + " has {{ $value | humanize }} unavailable replica(s) for 10 minutes",
			description: "Fewer replicas than desired serve traffic, so the others carry the load and a node failure may take the workload down. " +
				"What to do: look for pods stuck Pending (not enough CPU/memory on the nodes), failing readiness probes or a rollout that does not progress."})
	if w.hpa != "" {
		hsel := fmt.Sprintf(`%s, %s, horizontalpodautoscaler=%q`, ksm, ns, w.hpa)
		out = append(out, alert{name: "WorkloadHPASaturated", severity: "warning", forDur: "15m",
			expr: fmt.Sprintf(`(
  kube_horizontalpodautoscaler_status_current_replicas{%s}
    >= on (namespace, horizontalpodautoscaler)
  kube_horizontalpodautoscaler_spec_max_replicas{%s}
)
and on (namespace)
(
  100 * sum by (namespace) (rate(container_cpu_usage_seconds_total{%s, %s, container!=""}[5m]) * on (namespace, pod) group_left (workload) %s)
    / sum by (namespace) (kube_pod_container_resource_requests{%s, %s, resource="cpu"} * on (namespace, pod) group_left (workload) %s)
  > on (namespace)
  kube_horizontalpodautoscaler_spec_target_metric{%s, metric_name="cpu", metric_target_type="utilization"}
)`, hsel, hsel, cadvisor, ns, own, ksm, ns, own, hsel),
			short:   "HPA at maxReplicas with CPU above target for 15m",
			summary: ref + ": HPA " + w.hpa + " is at maxReplicas and CPU is still above its target",
			description: "The autoscaler can't add replicas and the replicas it has run hotter than the target: latency grows with traffic from here. " +
				"What to do: raise maxReplicas (check the nodes have room), make each replica cheaper, or size requests from a load test so the target reflects real capacity."})
	}
	return out
}

// kubeDashboard is the per-workload overview: one row per workload with
// replicas vs the HPA, CPU and memory vs requests/limits, restarts and
// network (kube-state-metrics and cAdvisor metrics).
func kubeDashboard(workloads []*kubeWorkload, http bool) []byte {
	nsSet := map[string]bool{}
	var names []string
	for _, w := range workloads {
		nsSet[w.namespace] = true
		names = append(names, regexp.QuoteMeta(w.name))
	}
	namespaces := slices.Sorted(maps.Keys(nsSet))
	if len(namespaces) == 0 {
		namespaces = []string{"default"}
	}
	nameRe := "/^(" + strings.Join(slices.Compact(slices.Sorted(slices.Values(names))), "|") + ")$/"
	if len(names) == 0 {
		nameRe = ""
	}
	own := `namespace_workload_pod:kube_pod_owner:relabel{namespace=~"$namespace", workload="$workload"}`
	running := `(kube_pod_status_phase{job="kube-state-metrics", namespace=~"$namespace", phase=~"Pending|Running"} == 1)`
	byWorkload := func(metric string) string {
		return metric + " * on (namespace, pod) group_left (workload) " + own
	}
	resource := func(kind, res string) string {
		return fmt.Sprintf(`sum(kube_pod_container_resource_%s{job="kube-state-metrics", namespace=~"$namespace", resource=%q} * on (namespace, pod) group_left (workload) %s * on (namespace, pod) group_left () %s)`, kind, res, own, running)
	}
	// hpa reads an HPA metric (with extra label matchers) for the HPA scaling $workload.
	hpa := func(metric, matchers string) string {
		return fmt.Sprintf(`max(%s{job="kube-state-metrics", namespace=~"$namespace"%s} * on (namespace, horizontalpodautoscaler) group_left () kube_horizontalpodautoscaler_info{namespace=~"$namespace", scaletargetref_name="$workload"})`, metric, matchers)
	}
	cpu := fmt.Sprintf(`sum(%s)`, byWorkload(`rate(container_cpu_usage_seconds_total{job="kubelet", metrics_path="/metrics/cadvisor", namespace=~"$namespace", container!=""}[$__rate_interval])`))
	panels := []map[string]any{row(1, "$workload", "workload", 0)}
	panels = append(panels,
		timeseries(2, "Replicas vs autoscaler", "Ready pods against the replicas the HPA wants and its min/max bounds.", "short", gridPos{0, 1, 8, 8},
			map[string]string{"HPA max": "red", "HPA min": "blue", "HPA desired": "orange"},
			target{"ready pods", fmt.Sprintf(`sum(%s)`, byWorkload(`kube_pod_status_ready{job="kube-state-metrics", namespace=~"$namespace", condition="true"}`))},
			target{"HPA desired", hpa("kube_horizontalpodautoscaler_status_desired_replicas", "")},
			target{"HPA max", hpa("kube_horizontalpodautoscaler_spec_max_replicas", "")},
			target{"HPA min", hpa("kube_horizontalpodautoscaler_spec_min_replicas", "")}),
		timeseries(3, "CPU vs request and limit", "CPU cores used by every replica against the sum of their requests and limits.", "short", gridPos{8, 1, 8, 8},
			map[string]string{"limit": "red", "request": "blue"},
			target{"used", cpu}, target{"request", resource("requests", "cpu")}, target{"limit", resource("limits", "cpu")}),
		timeseries(4, "CPU % of request (HPA target)", "The utilization the HPA scales on, against its target.", "percent", gridPos{16, 1, 8, 8},
			map[string]string{"HPA target": "red"},
			target{"utilization", "100 * " + cpu + " / " + resource("requests", "cpu")},
			target{"HPA target", hpa("kube_horizontalpodautoscaler_spec_target_metric", `, metric_name="cpu", metric_target_type="utilization"`)}),
		timeseries(5, "Memory vs request and limit", "Working set of every replica against the sum of their requests and limits (the kernel OOM-kills a container at its limit).", "bytes", gridPos{0, 9, 8, 8},
			map[string]string{"limit": "red", "request": "blue"},
			target{"working set", fmt.Sprintf(`sum(%s)`, byWorkload(`container_memory_working_set_bytes{job="kubelet", metrics_path="/metrics/cadvisor", namespace=~"$namespace", container!="", image!=""}`))},
			target{"request", resource("requests", "memory")}, target{"limit", resource("limits", "memory")}),
		timeseries(6, "Restarts", "Container restarts per interval (crashes, OOM kills, failed liveness probes).", "short", gridPos{8, 9, 8, 8}, nil,
			target{"restarts", fmt.Sprintf(`sum(%s)`, byWorkload(`increase(kube_pod_container_status_restarts_total{job="kube-state-metrics", namespace=~"$namespace"}[$__rate_interval])`))}),
		timeseries(7, "Network", "Bytes per second received and sent by the pods.", "Bps", gridPos{16, 9, 8, 8}, nil,
			target{"received", fmt.Sprintf(`sum(%s)`, byWorkload(`rate(container_network_receive_bytes_total{job="kubelet", metrics_path="/metrics/cadvisor", namespace=~"$namespace"}[$__rate_interval])`))},
			target{"sent", fmt.Sprintf(`sum(%s)`, byWorkload(`rate(container_network_transmit_bytes_total{job="kubelet", metrics_path="/metrics/cadvisor", namespace=~"$namespace"}[$__rate_interval])`))}),
	)
	if http {
		// Request metrics (recorded per source: traefik, ingress-nginx, otel).
		rec := func(name string) string {
			return fmt.Sprintf(`%s{namespace=~"$namespace", workload="$workload"}`, name)
		}
		panels = append(panels,
			timeseries(8, "Requests/s", "Requests per second the workload served, by where they are measured (ingress controller or the app's OpenTelemetry metrics).", "reqps", gridPos{0, 17, 8, 8}, nil,
				target{"{{source}}", rec(workloadRecs.requests)}),
			timeseries(9, "5xx ratio", "Share of requests answered 5xx (5m rate).", "percentunit", gridPos{8, 17, 8, 8}, nil,
				target{"{{source}}", rec(workloadRecs.errors) + " / " + rec(workloadRecs.requests)}),
			timeseries(10, "p95 latency", "95th percentile of the request duration (5m window).", "s", gridPos{16, 17, 8, 8}, nil,
				target{"{{source}}", rec(workloadRecs.p95)}),
		)
	}
	vars := []map[string]any{
		customVar("namespace", "Namespace", namespaces),
		queryVar("workload", "Workload", `label_values(namespace_workload_pod:kube_pod_owner:relabel{namespace=~"$namespace"}, workload)`, nameRe),
	}
	return dashboard("nimbo-workloads", "Workloads overview", "Per-workload replicas, autoscaling, CPU, memory, restarts and network (generated by Nimbo).",
		[]string{"nimbo", "kubernetes"}, vars, panels)
}

// KubeSummary completes a summary with the stack's components and cost.
func KubeSummary(out KubeOutput, files []string, enabled bool) model.MonitoringSummary {
	return model.MonitoringSummary{
		Kind: model.WorkflowKubernetes, Enabled: enabled,
		Components: []model.MonitoringComponent{
			{Name: "kube-prometheus-stack", Image: "chart " + KubeChartName + " " + KubeChart().Version, Purpose: "Prometheus Operator, Prometheus (7d retention, 5Gi volume), Alertmanager, Grafana, kube-state-metrics and node-exporter, with the Kubernetes default alerts and dashboards"},
			{Name: "Workload alerts", Image: "PrometheusRule", Purpose: "memory near the limit, CPU throttling, OOM kills, restarts, unavailable replicas, HPA saturation, and HTTP 5xx ratio / p95 latency where request metrics exist"},
			{Name: "Scrape targets", Image: "ServiceMonitor/PodMonitor", Purpose: "Services with a metrics port or prometheus.io annotations, and the cluster's Traefik for Ingress request metrics"},
			{Name: "Dashboard", Image: "ConfigMap", Purpose: "Workloads overview: replicas vs HPA, CPU and memory vs requests/limits, restarts, network, and requests/s, 5xx ratio and p95 when measured"},
		},
		Files: files, Alerts: out.Alerts, Scrapes: out.Scrapes, Dashboards: append([]string{}, out.Dashboards...),
		HTTP: out.HTTP, Thresholds: out.Thresholds,
		Links: []model.MonitoringLink{}, Cost: kubeCost, Notes: out.Notes,
	}
}
