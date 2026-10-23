package model

import "time"

// ---------------------------------------------------------------------------
// Docker workflow performance: build metrics (per compose build), live
// container usage (`docker stats`), container problems and the resource
// correlation of load tests against docker services.

// Event types of docker performance data.
const (
	EventComposeStats = "compose.stats" // Data: ComposeStatsEvent; about once a second while containers run
	EventComposeBuild = "compose.build" // Data: ComposeBuild; after a build/up run that built images
)

// ServiceUsage is one sample of a service's running containers (summed over
// replicas), read from `docker stats`. CPUPercent follows docker stats: 100
// is one full core. CPULimit is the cores the containers may use: the sum of
// their limits, or the host's CPUs (CPULimited false) when they have none;
// MemLimit likewise (the Docker host's memory when MemLimited is false).
type ServiceUsage struct {
	Service    string    `json:"service"`
	T          time.Time `json:"t"`
	Containers int       `json:"containers"`
	CPUPercent float64   `json:"cpuPercent"`
	CPULimit   float64   `json:"cpuLimit"`
	CPULimited bool      `json:"cpuLimited"`
	MemBytes   int64     `json:"memBytes"`
	MemLimit   int64     `json:"memLimit"`
	MemLimited bool      `json:"memLimited"`
	NetRxBytes int64     `json:"netRxBytes"`
	NetTxBytes int64     `json:"netTxBytes"`
	BlockRead  int64     `json:"blockReadBytes"`
	BlockWrite int64     `json:"blockWriteBytes"`
	PIDs       int       `json:"pids"`
}

// CPUOfLimit is CPU use as a fraction of CPULimit (1 = saturated).
func (u ServiceUsage) CPUOfLimit() float64 {
	if u.CPULimit <= 0 {
		return 0
	}
	return u.CPUPercent / (u.CPULimit * 100)
}

// MemOfLimit is memory use as a fraction of MemLimit.
func (u ServiceUsage) MemOfLimit() float64 {
	if u.MemLimit <= 0 {
		return 0
	}
	return float64(u.MemBytes) / float64(u.MemLimit)
}

// ComposeStats is the live resource usage of a docker workflow's project
// (GET /workflows/{id}/compose/stats). History holds the last samples of
// every service (one a second, oldest first) for sparklines.
type ComposeStats struct {
	WorkflowID string               `json:"workflowId"`
	Streaming  bool                 `json:"streaming"` // a `docker stats` stream is running
	HostCPUs   int                  `json:"hostCpus"`
	HostMemory int64                `json:"hostMemory"`
	IntervalMs int                  `json:"intervalMs"`
	Services   []ServiceStatsSeries `json:"services"`
	Message    string               `json:"message,omitempty"`
	UpdatedAt  time.Time            `json:"updatedAt"`
}

// ServiceStatsSeries is one service's current usage and recent history.
type ServiceStatsSeries struct {
	Service string         `json:"service"`
	NodeID  string         `json:"nodeId"`
	Current *ServiceUsage  `json:"current,omitempty"`
	History []ServiceUsage `json:"history"`
}

// ComposeStatsEvent is the payload of compose.stats: the newest sample of
// every service with running containers.
type ComposeStatsEvent struct {
	WorkflowID string         `json:"workflowId"`
	Services   []ServiceUsage `json:"services"`
}

// ComposeProblem is a container condition worth the user's attention:
// kind "oom" (killed for exceeding its memory limit), "restarts" (restarted
// by its restart policy), "exit" (exited with a non-zero code), "unhealthy".
type ComposeProblem struct {
	Service   string `json:"service"`
	NodeID    string `json:"nodeId"`
	Container string `json:"container"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"` // error | warning
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	Restarts  int    `json:"restarts,omitempty"`
}

// ---------------------------------------------------------------------------
// Builds

// ComposeBuild is what one compose build (a build run, or the build step of
// an up) did for each service it built, parsed from BuildKit's plain
// progress output and the built images (GET /workflows/{id}/compose/builds).
type ComposeBuild struct {
	RunID      string              `json:"runId"`
	Action     RunAction           `json:"action"`
	At         time.Time           `json:"at"`
	DurationMs int64               `json:"durationMs"` // whole BuildKit build (all services in parallel)
	Succeeded  bool                `json:"succeeded"`
	Services   []ServiceBuildStats `json:"services"`
}

// ServiceBuildStats are one service's build time, cache use and image size.
// DurationMs spans from the start of its first step to the end of its last
// (BuildKit builds services in parallel; steps shared by several services
// are reported once, under one of them). CacheHits/CacheMisses count the
// Dockerfile instructions (FROM excluded) BuildKit reused or executed.
// BaseSizeBytes is the part of the image coming from the final stage's base
// image (0 with BaseImage unknown when it could not be told apart).
type ServiceBuildStats struct {
	Service       string         `json:"service"`
	NodeID        string         `json:"nodeId"`
	Image         string         `json:"image,omitempty"`
	ImageID       string         `json:"imageId,omitempty"`
	Stack         string         `json:"stack,omitempty"` // node | go | python | java | dotnet | rust | ruby | php | static
	DurationMs    int64          `json:"durationMs"`
	CacheHits     int            `json:"cacheHits"`
	CacheMisses   int            `json:"cacheMisses"`
	Steps         []BuildStep    `json:"steps"`
	SizeBytes     int64          `json:"sizeBytes"`
	BaseImage     string         `json:"baseImage,omitempty"`
	BaseSizeBytes int64          `json:"baseSizeBytes"`
	Layers        []ImageLayer   `json:"layers"` // biggest layers of the image, largest first
	Failed        bool           `json:"failed,omitempty"`
	Delta         *BuildDelta    `json:"delta,omitempty"`
	Findings      []BuildFinding `json:"findings"`
}

// BuildStep is one BuildKit vertex of a service's build.
type BuildStep struct {
	Name        string `json:"name"`            // as BuildKit prints it: "[builder 3/5] RUN npm ci"
	Stage       string `json:"stage,omitempty"` // Dockerfile stage ("" for internal steps)
	Instruction string `json:"instruction"`     // "RUN npm ci", "exporting to image", ...
	Internal    bool   `json:"internal,omitempty"`
	Cached      bool   `json:"cached"`
	DurationMs  int64  `json:"durationMs"`
	Error       bool   `json:"error,omitempty"`
}

// ImageLayer is one entry of `docker image history`; Base marks layers of
// the base image.
type ImageLayer struct {
	CreatedBy string `json:"createdBy"`
	SizeBytes int64  `json:"sizeBytes"`
	Base      bool   `json:"base"`
}

// BuildDelta compares a service's build with its previous recorded build.
type BuildDelta struct {
	PrevRunID   string `json:"prevRunId"`
	DurationMs  int64  `json:"durationMs"`  // this minus previous
	SizeBytes   int64  `json:"sizeBytes"`   // this minus previous
	CacheHits   int    `json:"cacheHits"`   // this minus previous
	CacheMisses int    `json:"cacheMisses"` // this minus previous
}

// BuildFinding flags a built image, e.g. too big for its stack; Rules are
// the best-practice rule IDs (GET /best-practices/rules) explaining the fix.
type BuildFinding struct {
	Code     string   `json:"code"` // image_too_big | base_dominates | slow_uncached_step
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Rules    []string `json:"rules,omitempty"`
}

// ComposeBuilds lists a workflow's recorded builds, newest first.
type ComposeBuilds struct {
	Builds []ComposeBuild `json:"builds"`
}

// ---------------------------------------------------------------------------
// Load test correlation

// ResourceProbe samples the resources of a load test's target while it
// runs (see loadtest.EngineConfig.Monitor).
type ResourceProbe interface {
	// Target is the compose service the load test hits ("" when unknown).
	Target() string
	// Sample returns the newest usage of every service with running
	// containers.
	Sample() []ServiceUsage
	// Containers returns restarts, OOM kills and startup times per service.
	Containers() map[string]ContainerFacts
}

// ContainerFacts summarize a service's containers: restarts summed over
// replicas, whether one was OOM-killed, and the slowest startup.
type ContainerFacts struct {
	Restarts  int    `json:"restarts"`
	OOMKilled bool   `json:"oomKilled"`
	StartupMs *int64 `json:"startupMs,omitempty"`
}

// LoadResources is the per-container usage recorded during a load test of
// a docker workflow, aligned with LoadTestRun.Timeline (T in seconds since
// load started; T 0 is the baseline), and the bottleneck analysis.
type LoadResources struct {
	Service  string                 `json:"service,omitempty"` // targeted service
	Services []LoadServiceResources `json:"services"`
	Verdicts []ResourceVerdict      `json:"verdicts"`
	Verdict  string                 `json:"verdict"` // one-line headline
	Note     string                 `json:"note,omitempty"`
}

// LoadServiceResources is one service's usage during a load test.
type LoadServiceResources struct {
	Service    string          `json:"service"`
	NodeID     string          `json:"nodeId"`
	Target     bool            `json:"target,omitempty"`
	CPULimit   float64         `json:"cpuLimit"`
	CPULimited bool            `json:"cpuLimited"`
	MemLimit   int64           `json:"memLimit"`
	MemLimited bool            `json:"memLimited"`
	StartupMs  *int64          `json:"startupMs,omitempty"`
	Timeline   []ResourcePoint `json:"timeline"`
	PeakCPU    float64         `json:"peakCpuPercent"`
	PeakCPUOf  float64         `json:"peakCpuOfLimit"` // 0..1 (sustained over 2 s)
	PeakMem    int64           `json:"peakMemBytes"`
	PeakMemOf  float64         `json:"peakMemOfLimit"`
	Restarts   int             `json:"restarts,omitempty"`  // during the run
	OOMKilled  bool            `json:"oomKilled,omitempty"` // during the run
}

// ResourcePoint is one second of a service's usage during a load test.
type ResourcePoint struct {
	T          float64 `json:"t"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   int64   `json:"memBytes"`
	Containers int     `json:"containers"`
}

// ResourceVerdict is the analysis of one service: kind cpu-bound,
// single-core, memory-bound, oom, restarted or ok.
type ResourceVerdict struct {
	Service    string  `json:"service"`
	Kind       string  `json:"kind"`
	Bottleneck bool    `json:"bottleneck"`
	Message    string  `json:"message"`
	AtRPS      float64 `json:"atRps,omitempty"`
	AtT        float64 `json:"atT,omitempty"`
}
