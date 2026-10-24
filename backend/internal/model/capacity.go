package model

import "time"

// ---------------------------------------------------------------------------
// Capacity planning (POST /workflows/{id}/capacity-plan): replicas,
// requests/limits, nodes and monthly cost for a target request rate, sized
// from what a finished load test of a docker or Kubernetes workflow
// measured. The latest plan of every environment is kept and returned by
// GET /workflows/{id}/capacity-plan.

// CapacityRequest is the body of POST /workflows/{id}/capacity-plan.
type CapacityRequest struct {
	// TargetRPS is the peak request rate to plan for (> 0).
	TargetRPS float64 `json:"targetRps"`
	// P95Ms is an optional latency objective: per-replica throughput is
	// capped at the highest rate the run served with p95 within it.
	P95Ms float64 `json:"p95Ms,omitempty"`
	// Environment is the docker/Kubernetes environment ("" = the active
	// one); RunID the load test to size from ("" = the newest finished
	// run of that environment with resource usage).
	Environment string `json:"environment,omitempty"`
	RunID       string `json:"runId,omitempty"`
}

// Capacity plan confidence (CapacityPlan.Confidence).
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Service roles (CapacityService.Role).
const (
	// CapacityApp scales horizontally: more replicas add throughput.
	CapacityApp = "app"
	// CapacityDependency is a database, cache or broker: it does not scale
	// by adding replicas the same way, so its load is projected instead.
	CapacityDependency = "dependency"
	// CapacityIdle is a service whose CPU did not grow with the traffic
	// (not on the request path): it keeps its replicas.
	CapacityIdle = "idle"
)

// CapacityPlan sizes a workflow for TargetRPS from one load test run.
type CapacityPlan struct {
	WorkflowID  string    `json:"workflowId"`
	Kind        string    `json:"kind"` // docker | kubernetes
	Environment string    `json:"environment,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Run is the load test the plan is sized from.
	Run CapacityRun `json:"run"`

	TargetRPS float64 `json:"targetRps"`
	P95Ms     float64 `json:"p95Ms,omitempty"`
	// MeasuredRPS is the highest request rate the run served while
	// latency and errors were still healthy (before its breaking point).
	MeasuredRPS float64 `json:"measuredRps"`
	// Extrapolation is TargetRPS / MeasuredRPS (> 1: beyond what was
	// measured).
	Extrapolation float64 `json:"extrapolation"`
	// Confidence is high, medium or low; Reasons say why.
	Confidence        string   `json:"confidence"`
	ConfidenceReasons []string `json:"confidenceReasons"`

	// CPUCeiling is the default fraction of a replica's CPU the plan runs
	// it at (the rest is burst headroom); HeadroomRule explains the
	// replica rounding.
	CPUCeiling   float64 `json:"cpuCeiling"`
	HeadroomRule string  `json:"headroomRule"`

	Services []CapacityService `json:"services"`
	// Totals are the requests of every planned replica.
	TotalCPU    float64 `json:"totalCpu"`    // cores
	TotalMemGiB float64 `json:"totalMemGiB"` // GiB

	// Clouds is the cheapest node shape and count per cloud.
	Clouds []CloudEstimate `json:"clouds"`
	// Prices describes the pinned price table the costs come from.
	Prices PriceTableInfo `json:"prices"`

	Assumptions []string `json:"assumptions"`
	Caveats     []string `json:"caveats"`
	// Apply: whether the plan can be written into the environment with
	// POST /workflows/{id}/capacity-plan/apply, or why not.
	Applicable bool   `json:"applicable"`
	ApplyNote  string `json:"applyNote,omitempty"`
}

// CapacityRun summarizes the run a plan comes from.
type CapacityRun struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	StartedAt   time.Time `json:"startedAt"`
	DurationSec int       `json:"durationSec"`
	// HealthySec is how many seconds of load were before the breaking
	// point; PeakRPS the run's peak, BreakingPoint its breaking point.
	HealthySec    int      `json:"healthySec"`
	PeakRPS       float64  `json:"peakRps"`
	BreakingPoint *float64 `json:"breakingPoint,omitempty"`
	Stability     float64  `json:"stability"`
	ErrorRate     float64  `json:"errorRate"`
	P95Ms         float64  `json:"p95Ms"`
}

// CapacityService is the plan of one compose service / workload.
type CapacityService struct {
	NodeID  string `json:"nodeId"`
	Service string `json:"service"`
	Role    string `json:"role"` // app | dependency | idle
	Target  bool   `json:"target,omitempty"`

	// Measured: replicas during the run, CPU cores per replica at idle
	// and at MeasuredRPS (healthy seconds only), CPU cost of one req/s,
	// peak memory per replica (whole run) and the limits it ran with.
	MeasuredReplicas int        `json:"measuredReplicas"`
	MeasuredRPS      float64    `json:"measuredRps"`
	IdleCPU          float64    `json:"idleCpu"`
	PeakCPU          float64    `json:"peakCpu"`
	CPUPerRPS        float64    `json:"cpuPerRps"`
	PeakMemBytes     int64      `json:"peakMemBytes"`
	CurrentCPULimit  float64    `json:"currentCpuLimit,omitempty"` // cores per replica, 0 = none
	CurrentMemLimit  int64      `json:"currentMemLimit,omitempty"` // bytes per replica, 0 = none
	CurrentReplicas  int        `json:"currentReplicas,omitempty"`
	CurrentHPA       *HPAPolicy `json:"currentHpa,omitempty"`

	// Plan: requests/limits per replica (Kubernetes quantities; docker
	// uses the limits), the CPU ceiling, per-replica throughput at it,
	// replicas needed for the target (raw and with headroom).
	CPURequest    string  `json:"cpuRequest"`
	CPULimit      string  `json:"cpuLimit"`
	MemRequest    string  `json:"memRequest"`
	MemLimit      string  `json:"memLimit"`
	CPUCeiling    float64 `json:"cpuCeiling"`
	PerReplicaRPS float64 `json:"perReplicaRps,omitempty"`
	ReplicasRaw   float64 `json:"replicasRaw"`
	Replicas      int     `json:"replicas"`
	// HPA: Kubernetes app workloads only.
	HPA *HPAPolicy `json:"hpa,omitempty"`
	// ProjectedCPU is the CPU cores the service needs at the target in
	// total (dependencies: on their single instance).
	ProjectedCPU float64 `json:"projectedCpu"`
	TotalCPU     float64 `json:"totalCpu"`    // requested cores, all replicas
	TotalMemGiB  float64 `json:"totalMemGiB"` // requested GiB, all replicas
	// Limit: what bounds the per-replica throughput ("cpu", "latency",
	// "none" when CPU did not grow with traffic).
	Limit  string `json:"limit,omitempty"`
	Note   string `json:"note"`
	Caveat string `json:"caveat,omitempty"`
	// Apply is what POST .../capacity-plan/apply writes for this service
	// in the plan's environment (nil when nothing can be written).
	Apply *CapacityApply `json:"apply,omitempty"`
}

// CapacityApply is one service's environment settings (PATCH
// /environments/{name}/settings keys and values).
type CapacityApply struct {
	Target string            `json:"target"`
	Values map[string]string `json:"values"`
	// Changes are the human-readable changes ("Replicas 1 → 3").
	Changes []string `json:"changes"`
	// Skipped are planned settings the environment can't take here.
	Skipped []string `json:"skipped,omitempty"`
}

// CloudEstimate is the node shape, count and monthly cost of one cloud.
type CloudEstimate struct {
	Cloud  string `json:"cloud"` // aws | azure | gcp | oci
	Region string `json:"region"`
	// Recommended is the cheapest option; Options every shape that fits,
	// cheapest first.
	Recommended NodeOption   `json:"recommended"`
	Options     []NodeOption `json:"options"`
}

// NodeOption is a node shape and how many of them the plan needs.
type NodeOption struct {
	Shape  string  `json:"shape"`
	Arch   string  `json:"arch"` // arm64 | x86_64
	VCPU   float64 `json:"vcpu"`
	MemGiB float64 `json:"memGiB"`
	// Allocatable is what is left for pods after the system reserve.
	AllocCPU    float64 `json:"allocCpu"`
	AllocMemGiB float64 `json:"allocMemGiB"`
	HourlyUSD   float64 `json:"hourlyUsd"`
	Nodes       int     `json:"nodes"`
	MonthlyUSD  float64 `json:"monthlyUsd"`
	// CPUUsed and MemUsed are the fractions of the nodes' allocatable
	// capacity the packed replicas request.
	CPUUsed float64 `json:"cpuUsed"`
	MemUsed float64 `json:"memUsed"`
}

// PriceTableInfo describes the pinned on-demand price table.
type PriceTableInfo struct {
	AsOf    string            `json:"asOf"` // YYYY-MM-DD
	Regions map[string]string `json:"regions"`
	Note    string            `json:"note"`
	Sources []string          `json:"sources"`
}
