package model

// ---------------------------------------------------------------------------
// Load test autoscaling (Kubernetes workflows)

// Scaling verdict kinds (WorkloadScaling.Kind).
const (
	// ScalingScaled: the HPA added pods and they were Ready before latency
	// degraded (or it degraded only for a short grace window).
	ScalingScaled = "scaled"
	// ScalingLate: latency degraded for a while before new pods were Ready,
	// or the HPA never scaled up although the workload was under pressure.
	ScalingLate = "late"
	// ScalingMaxed: the HPA reached maxReplicas and the workload stayed
	// saturated.
	ScalingMaxed = "maxed"
	// ScalingNoHPA: the targeted workload has no HPA in the environment.
	ScalingNoHPA = "no_hpa"
	// ScalingNoMetrics: the HPA could not read the metrics it scales on.
	ScalingNoMetrics = "no_metrics"
	// ScalingNotNeeded: CPU stayed under the HPA target and latency held.
	ScalingNotNeeded = "not_needed"
	// ScalingTooShort: the run ended before an HPA could react (metrics
	// resolution, sync period and pod startup take about a minute).
	ScalingTooShort = "too_short"
)

// LoadScaling is the autoscaling analysis of a load test run against a
// Kubernetes workflow (LoadResources.Scaling): for every workload in the
// run's scope (the targeted workload and every workload with a
// HorizontalPodAutoscaler) its replicas and HPA CPU utilization over time
// and a verdict.
type LoadScaling struct {
	Workloads []WorkloadScaling `json:"workloads"`
	// Verdict is the headline sentence (the targeted workload's verdict
	// first).
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// WorkloadScaling is one workload's replicas over a load test and its
// autoscaling verdict. T values share LoadSample.T's axis (seconds since
// load started; 0 is the baseline).
type WorkloadScaling struct {
	NodeID   string         `json:"nodeId"`
	Workload string         `json:"workload"`
	Target   bool           `json:"target,omitempty"`
	HPA      *HPAPolicy     `json:"hpa,omitempty"`
	Timeline []ScalingPoint `json:"timeline"`
	Kind     string         `json:"kind"`
	Message  string         `json:"message"`
	// ScaleUpAt is the first second desired replicas grew; ReadyAt the
	// first second a new pod was Ready; DegradedAt the first second
	// latency or errors degraded and DegradedFor how many seconds they
	// stayed degraded before ReadyAt (until the end without new pods).
	ScaleUpAt   *int `json:"scaleUpAt,omitempty"`
	ReadyAt     *int `json:"readyAt,omitempty"`
	DegradedAt  *int `json:"degradedAt,omitempty"`
	DegradedFor int  `json:"degradedFor,omitempty"`
	// PeakReplicas is the most Ready replicas; PeakCPU the highest CPU
	// utilization (% of requests) the HPA reported.
	PeakReplicas int  `json:"peakReplicas"`
	PeakCPU      *int `json:"peakCpuUtilization,omitempty"`
}

// HPAPolicy is a HorizontalPodAutoscaler's bounds and CPU target.
type HPAPolicy struct {
	Name string `json:"name"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	// TargetCPU is the average CPU utilization target (% of the pods'
	// requests); 0 when the HPA does not scale on CPU.
	TargetCPU int `json:"targetCpu,omitempty"`
}

// ScalingPoint is one second of a workload's replicas: Desired is the
// workload's spec.replicas (what the HPA asked for), Ready its Ready pods;
// CPUUtilization the HPA's current average CPU utilization (% of requests,
// refreshed by metrics-server about every 15 s), absent without a reading.
type ScalingPoint struct {
	T              int  `json:"t"`
	Desired        int  `json:"desired"`
	Ready          int  `json:"ready"`
	CPUUtilization *int `json:"cpuUtilization,omitempty"`
}

// WorkloadScale is one instant of a workload's replicas and HPA status, as
// sampled once a second during a Kubernetes load test.
type WorkloadScale struct {
	NodeID   string
	Workload string // display label (the load test probe's service label)
	Desired  int
	Ready    int
	HPA      *HPAStatus // nil without an HPA
}

// HPAStatus is a HorizontalPodAutoscaler's spec bounds and live status.
type HPAStatus struct {
	HPAPolicy
	CPUUtilization  *int // status.currentMetrics cpu averageUtilization
	DesiredReplicas int
	Conditions      []HPACondition
}

// HPACondition is one of an HPA's status conditions (AbleToScale,
// ScalingActive, ScalingLimited).
type HPACondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}
