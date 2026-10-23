package model

import "time"

// Converting a docker workflow into a Kubernetes one
// (POST /workflows/{id}/convert/kubernetes).

// Convert formats (KubeConvertReport.Format): what the new workflow's
// manifests are written as.
const (
	ConvertManifests = KubeManifests // k8s/*.yaml
	ConvertKustomize = KubeKustomize // k8s/base + k8s/overlays/{dev,prod}
	ConvertHelm      = KubeHelm      // chart/ with values-dev/prod.yaml
)

// KubeConvertReport tells what a conversion produced and what it couldn't
// map.
type KubeConvertReport struct {
	Format    string `json:"format"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Sources and Builds are the new workflow's Kubernetes.Sources/Builds.
	Sources      []KubeSource       `json:"sources"`
	Builds       []KubeBuild        `json:"builds"`
	Environments []string           `json:"environments"`
	Objects      []KubeConvertObj   `json:"objects"`
	Mappings     []KubeConvertMap   `json:"mappings"`
	Unmapped     []KubeConvertIssue `json:"unmapped"`
	// LoadTest is the docker workflow's load test run the conversion can
	// size requests/limits from (nil when there is none usable); Used
	// tells whether it did.
	LoadTest *KubeConvertLoadTest `json:"loadTest,omitempty"`
	// Sizing is where each service's requests/limits come from.
	Sizing []KubeConvertSizing `json:"sizing"`
	// Autoscaling lists the HorizontalPodAutoscalers written, per
	// environment.
	Autoscaling []KubeConvertHPA `json:"autoscaling"`
}

// Sizing sources (KubeConvertSizing.Source).
const (
	SizingLoadTest = "load_test" // at least one value comes from a load test's measurements
	SizingCompose  = "compose"   // deploy.resources / cpus / mem_limit, the rest defaulted
	SizingDefault  = "default"   // nothing set or measured: defaults
)

// KubeConvertLoadTest is the load test run a conversion sizes from.
type KubeConvertLoadTest struct {
	RunID     string    `json:"runId"`
	Name      string    `json:"name,omitempty"`
	Target    string    `json:"target,omitempty"` // compose service the test hit
	StartedAt time.Time `json:"startedAt"`
	Seconds   float64   `json:"seconds"` // load duration
	RPS       float64   `json:"rps"`
	PeakRPS   float64   `json:"peakRps"`
	Services  []string  `json:"services"` // compose services it measured that still exist
	Used      bool      `json:"used"`     // false: sizeFromLoadTest was turned off
}

// KubeResourceQty is a pair of Kubernetes quantities ("250m", "192Mi").
type KubeResourceQty struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// KubeConvertSizing is the requests/limits of one service and their
// provenance.
type KubeConvertSizing struct {
	Service  string          `json:"service"`
	Source   string          `json:"source"` // load_test | compose | default
	Requests KubeResourceQty `json:"requests"`
	Limits   KubeResourceQty `json:"limits"`
	// Measured is the per-container usage the load test recorded.
	Measured *KubeMeasuredUsage `json:"measured,omitempty"`
	// Censored: the service hit its Docker limit during the test (cpu,
	// memory or single-core), so the measurement is a lower bound.
	Censored string `json:"censored,omitempty"`
	Detail   string `json:"detail"` // where the numbers come from, one line
}

// KubeMeasuredUsage is one container's usage during a load test.
type KubeMeasuredUsage struct {
	CPUP90  float64 `json:"cpuP90"`  // cores
	CPUPeak float64 `json:"cpuPeak"` // cores
	MemPeak int64   `json:"memPeak"` // bytes
}

// KubeConvertHPA is a HorizontalPodAutoscaler the conversion wrote.
type KubeConvertHPA struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	MinReplicas int    `json:"minReplicas"`
	MaxReplicas int    `json:"maxReplicas"`
	TargetCPU   int    `json:"targetCpu"` // averageUtilization, % of the CPU request
}

// KubeConvertObj is one object written (for Helm: rendered with the
// default values).
type KubeConvertObj struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Service string `json:"service,omitempty"` // the compose service it comes from
}

// KubeConvertMap is one compose feature and what it became.
type KubeConvertMap struct {
	Service string `json:"service,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// KubeConvertIssue is a compose feature that couldn't be mapped, or was
// mapped approximately: why, and what to do.
type KubeConvertIssue struct {
	Service  string `json:"service,omitempty"`
	Feature  string `json:"feature"`
	Detail   string `json:"detail"`
	Severity string `json:"severity"` // warning | info
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}
