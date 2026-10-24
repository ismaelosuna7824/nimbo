package model

// ---------------------------------------------------------------------------
// Load test incident scenarios: a real, reversible fault injected into a
// docker or Kubernetes workflow's local runtime while a load test runs.

// Scenario ids (ScenarioSpec.ID).
const (
	// ScenarioKillReplica kills one replica of the target: one container of
	// a compose service (its restart policy or replicas bring it back) or one
	// pod of a Kubernetes workload (its controller replaces it).
	ScenarioKillReplica = "kill_replica"
	// ScenarioDependencyDown makes a dependency unavailable for DurationSec:
	// docker pause/unpause of its containers, or scaling its Kubernetes
	// workload to 0 and back.
	ScenarioDependencyDown = "dependency_down"
	// ScenarioDependencyLatency adds network delay, jitter and loss to a
	// dependency for DurationSec (tc netem in its network namespace).
	// Params: delayMs, jitterMs, lossPct.
	ScenarioDependencyLatency = "dependency_latency"
	// ScenarioCacheFlush empties a cache (Redis/Valkey FLUSHALL, Memcached
	// flush_all) once, at AtSec.
	ScenarioCacheFlush = "cache_flush"
	// ScenarioTrafficSpike multiplies the load generator's target by the
	// "factor" param (default 3) from AtSec for DurationSec.
	ScenarioTrafficSpike = "traffic_spike"
)

// Scenario verdicts (ScenarioResult.Verdict).
const (
	ScenarioSurvived = "survived" // errors and latency stayed within bounds
	ScenarioDegraded = "degraded" // the system recovered, with a visible impact
	ScenarioFailed   = "failed"   // errors stayed high or it never recovered
)

// ScenarioSpec asks a load test to inject a fault. Target is the graph node
// id of the compose service or Kubernetes workload acted on (unused by
// traffic_spike). AtSec is when, in seconds after load starts, the fault is
// injected; it is reverted at AtSec+DurationSec (kill_replica and
// cache_flush are instantaneous: DurationSec is the observation window).
type ScenarioSpec struct {
	ID          string            `json:"id"`
	Target      string            `json:"target"`
	AtSec       int               `json:"atSec"`
	DurationSec int               `json:"durationSec"`
	Params      map[string]string `json:"params,omitempty"`
}

// ScenarioResult is the outcome of a run's scenario. InjectedAtSec and
// RevertedAtSec share LoadSample.T's axis; RecoveredAtSec is when the
// load-tested service was healthy again (errors and p95 back near the
// baseline), nil when it never recovered before the run ended.
type ScenarioResult struct {
	ID             string          `json:"id"`
	Target         string          `json:"target"`
	TargetName     string          `json:"targetName,omitempty"`
	InjectedAtSec  int             `json:"injectedAtSec"`
	RevertedAtSec  *int            `json:"revertedAtSec,omitempty"`
	RecoveredAtSec *int            `json:"recoveredAtSec,omitempty"`
	Verdict        string          `json:"verdict"`
	Message        string          `json:"message"`
	Checks         []ScenarioCheck `json:"checks"`
	// Detail is what was done to the runtime (e.g. "killed container
	// nimbo-dk-x-api-2"), for the record.
	Detail string `json:"detail,omitempty"`
}

// ScenarioCheck is one criterion of a scenario verdict.
type ScenarioCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// ScenarioOption is a scenario applicable to a workflow
// (GET /workflows/{id}/scenarios): its targets and parameters.
type ScenarioOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// NeedsTarget is false for traffic_spike.
	NeedsTarget bool             `json:"needsTarget"`
	Targets     []ScenarioTarget `json:"targets"`
	Params      []ScenarioParam  `json:"params,omitempty"`
	// DefaultDurationSec is a sensible window for the scenario.
	DefaultDurationSec int `json:"defaultDurationSec"`
}

// ScenarioTarget is a node a scenario can act on.
type ScenarioTarget struct {
	ID     string `json:"id"`     // graph node id
	Name   string `json:"name"`   // compose service / workload name
	Detail string `json:"detail"` // why it qualifies, e.g. "2 replicas", "redis cache"
}

// ScenarioParam is a numeric scenario parameter.
type ScenarioParam struct {
	Name    string  `json:"name"`
	Label   string  `json:"label"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Unit    string  `json:"unit,omitempty"`
}

// ScenarioUnavailable explains why a scenario does not apply.
type ScenarioUnavailable struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// ScenarioCatalog is the response of GET /workflows/{id}/scenarios.
type ScenarioCatalog struct {
	Kind        string                `json:"kind"` // docker | kubernetes | "" (no scenario support)
	Environment string                `json:"environment,omitempty"`
	Scenarios   []ScenarioOption      `json:"scenarios"`
	Unavailable []ScenarioUnavailable `json:"unavailable"`
}
