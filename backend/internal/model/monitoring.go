package model

// Monitoring (GET/POST /workflows/{id}/monitoring): one click adds a
// metrics, alerting and dashboards stack to a docker or Kubernetes
// workflow — Prometheus, Grafana and cAdvisor services for docker; the
// kube-prometheus-stack chart plus workload-scoped ServiceMonitors,
// PrometheusRules and dashboards for Kubernetes. Running it again
// regenerates the generated files (new workloads get their alerts) and
// never duplicates services or sources.

// MonitoringSummary describes the monitoring of a workflow: what Add
// monitoring adds (GET, a preview) or added (POST).
type MonitoringSummary struct {
	// Kind is the workflow kind: "docker" or "kubernetes".
	Kind string `json:"kind"`
	// Enabled: the workflow has the generated monitoring (POST updates it).
	Enabled    bool                  `json:"enabled"`
	Components []MonitoringComponent `json:"components"`
	// Files are the generated files (slash paths relative to the workflow files).
	Files  []string          `json:"files"`
	Alerts []MonitoringAlert `json:"alerts"`
	// Scrapes are the application metrics endpoints scraped besides the
	// container metrics (services that declare a metrics port or label).
	Scrapes    []MonitoringScrape `json:"scrapes"`
	Dashboards []string           `json:"dashboards"`
	// HTTP says, per workload (or compose service), which request metrics
	// its HTTP alerts read (no sources = no request metrics, no alert).
	HTTP []MonitoringHTTP `json:"http"`
	// Thresholds are the HTTP alert thresholds in use.
	Thresholds MonitoringThresholds `json:"thresholds"`
	// Links are the local URLs of the UIs (Running once deployed).
	Links       []MonitoringLink       `json:"links"`
	Credentials *MonitoringCredentials `json:"credentials,omitempty"`
	// Cost is what the monitoring stack reserves.
	Cost  MonitoringCost `json:"cost"`
	Notes []string       `json:"notes,omitempty"`
}

// MonitoringComponent is one piece of the monitoring stack.
type MonitoringComponent struct {
	Name string `json:"name"`
	// Image is the pinned image or chart ("prom/prometheus:v2.54.1").
	Image   string `json:"image"`
	Purpose string `json:"purpose"`
}

// MonitoringAlert is one generated alerting rule.
type MonitoringAlert struct {
	Name     string `json:"name"`
	Severity string `json:"severity"` // warning | critical
	// Target is the workload ("shop/Deployment/backend") or compose
	// service the alert watches.
	Target  string `json:"target"`
	Summary string `json:"summary"`
}

// MonitoringScrape is one application metrics endpoint.
type MonitoringScrape struct {
	Target string `json:"target"` // Service "shop/api" or compose service "api"
	Port   string `json:"port"`
	Path   string `json:"path"`
	// Via is how it is scraped: ServiceMonitor, PodMonitor or a static
	// scrape config (docker).
	Via string `json:"via"`
}

// MonitoringLink is the local URL of a monitoring UI.
type MonitoringLink struct {
	Name string `json:"name"` // Grafana | Prometheus | Alertmanager
	URL  string `json:"url,omitempty"`
	// Running: the UI is deployed and reachable at URL.
	Running bool `json:"running"`
}

// MonitoringCredentials is how to sign in to Grafana.
type MonitoringCredentials struct {
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	// Source is where the password is stored (".env GRAFANA_ADMIN_PASSWORD").
	Source string `json:"source"`
}

// MonitoringCost is what the monitoring stack requests (Kubernetes) or
// reserves (docker): CPU in millicores, memory and disk in bytes.
type MonitoringCost struct {
	CPUMillis   int   `json:"cpuMillis"`
	MemoryBytes int64 `json:"memoryBytes"`
	DiskBytes   int64 `json:"diskBytes"`
}

// MonitoringHTTP is where one workload's HTTP alerts come from.
type MonitoringHTTP struct {
	Target string `json:"target"`
	// Sources: traefik | ingress-nginx | otel.
	Sources []string `json:"sources"`
}

// MonitoringThresholds tune the HTTP alerts (POST body; kept in the
// generated rules so updates reuse them).
type MonitoringThresholds struct {
	// ErrorPercent: 5xx share of requests that fires the error alert (5m).
	ErrorPercent float64 `json:"errorPercent"`
	// LatencyP95Ms: p95 latency that fires the latency alert (10m).
	LatencyP95Ms int `json:"latencyP95Ms"`
	// MinRequestsPerSecond: below this traffic neither alert fires.
	MinRequestsPerSecond float64 `json:"minRequestsPerSecond"`
}

// MonitoringRequest is the optional body of POST /workflows/{id}/monitoring.
type MonitoringRequest struct {
	// Thresholds override the HTTP alert thresholds (zero fields keep the
	// current ones, else the defaults).
	Thresholds *MonitoringThresholds `json:"thresholds,omitempty"`
}

// MonitoringResult answers POST /workflows/{id}/monitoring.
type MonitoringResult struct {
	Workflow *Workflow         `json:"workflow"`
	Summary  MonitoringSummary `json:"summary"`
}
