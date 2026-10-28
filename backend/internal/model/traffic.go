package model

// Live traffic status (LiveTraffic.Status).
const (
	// TrafficOK: Prometheus answered; Nodes are the measured nodes.
	TrafficOK = "ok"
	// TrafficNoMonitoring: Add monitoring never ran on the workflow.
	TrafficNoMonitoring = "no_monitoring"
	// TrafficNotRunning: monitoring is added but its Prometheus is not
	// running (not deployed, stopped, cluster down).
	TrafficNotRunning = "not_running"
	// TrafficUnreachable: Prometheus runs but the query failed (Message).
	TrafficUnreachable = "unreachable"
)

// LiveTraffic is GET /workflows/{id}/traffic: the requests each service
// (docker) or workload (Kubernetes) receives now, measured by the
// workflow's own Prometheus (Add monitoring). Only nodes whose requests are
// measured are listed: the request metrics of scraped services, and
// Kubernetes ingress metrics.
type LiveTraffic struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// Window says how the rates are computed.
	Window string        `json:"window"`
	Nodes  []NodeTraffic `json:"nodes"`
}

// NodeTraffic is one node's measured requests.
type NodeTraffic struct {
	// NodeID is the canvas node: "service:api", "Deployment/default/web".
	NodeID string `json:"nodeId"`
	// Source: "otel" (the app's own HTTP metrics), "traefik" or
	// "ingress-nginx" (the ingress controller's).
	Source string `json:"source"`
	// RPS is requests per second; ErrorRate the share answered 5xx (0-1).
	RPS       float64 `json:"rps"`
	ErrorRate float64 `json:"errorRate"`
	// P95Ms is the 95th percentile latency; 0 while no request completed.
	P95Ms float64 `json:"p95Ms,omitempty"`
}
