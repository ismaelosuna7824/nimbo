package model

import "time"

// ---------------------------------------------------------------------------
// Learn challenges: a problem, a starter workflow and checks Nimbo grades
// for real (static analysis of the workflow, deploys, HTTP calls, load
// tests and incident scenarios). Content lives in the Learn database
// (internal/learn migrations); the grading engine is internal/challenge.

// Challenge is one Learn challenge.
type Challenge struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	Level         string   `json:"level"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Minutes       int      `json:"minutes"`
	Tags          []string `json:"tags"`
	Prerequisites []string `json:"prerequisites,omitempty"`
	Runtime       string   `json:"runtime"`
	// Problem is markdown: the story and its context.
	Problem      string   `json:"problem"`
	Requirements []string `json:"requirements"`
	// Hints are revealed one at a time, the last one closest to the answer.
	Hints []string `json:"hints"`
	// Solution is markdown: the steps and why.
	Solution string `json:"solution"`
	// SolutionFiles are reference files (slash paths) written over the
	// starter in a copy of the workflow ("Show me the solution").
	SolutionFiles map[string]string `json:"solutionFiles,omitempty"`
	Starter       ChallengeStarter  `json:"starter"`
	Checks        []ChallengeCheck  `json:"checks"`
	// Adaptations (global challenges): how to solve it on each provider.
	Adaptations map[string]string `json:"adaptations,omitempty"`
	Sources     []LearnSource     `json:"sources"`
}

// LearnSource is an official documentation page.
type LearnSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ChallengeStarter is the workflow a challenge starts from: a template
// (catalog, docker, Kubernetes or challenge starter template), or inline
// files of a kind (Cloud for Terraform; Kubernetes sources and builds for
// Kubernetes, default: manifests in k8s/).
type ChallengeStarter struct {
	TemplateID string            `json:"templateId,omitempty"`
	Kind       string            `json:"kind,omitempty"` // terraform | docker | kubernetes
	Cloud      Cloud             `json:"cloud,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	Kubernetes *KubernetesModel  `json:"kubernetes,omitempty"`
}

// Check types (ChallengeCheck.Type).
const (
	CheckValidate   = "validate"
	CheckRulesClean = "rules_clean"
	CheckHas        = "has"
	CheckDeploy     = "deploy"
	CheckHTTP       = "http"
	CheckLoad       = "load"
	CheckScenario   = "scenario"
	CheckReadiness  = "readiness"
)

// ChallengeCheck is one graded goal. Type selects the parameter object of
// the same name; validate and deploy take none.
type ChallengeCheck struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Type  string `json:"type"`
	// Environment (docker, Kubernetes) opens the workflow in that
	// environment for validate, rules_clean and has ("" = the active one).
	Environment string                   `json:"environment,omitempty"`
	RulesClean  *ChallengeRulesClean     `json:"rules_clean,omitempty"`
	Has         *ChallengeHas            `json:"has,omitempty"`
	HTTP        *ChallengeHTTP           `json:"http,omitempty"`
	Load        *ChallengeLoad           `json:"load,omitempty"`
	Scenario    *ChallengeScenario       `json:"scenario,omitempty"`
	Readiness   *ChallengeReadinessCheck `json:"readiness,omitempty"`
}

// ChallengeRulesClean passes when no finding matches: its code is in
// Codes or starts with one of Prefixes (neither: every finding) and its
// severity is at least MinSeverity (info | warning | error). Every rule
// runs, whatever the user's settings turn off.
type ChallengeRulesClean struct {
	Codes       []string `json:"codes,omitempty"`
	Prefixes    []string `json:"prefixes,omitempty"`
	MinSeverity Severity `json:"minSeverity"`
}

// ChallengeHas asserts the workflow contains something. Exactly one of
// Terraform, Compose, Kubernetes, Connected.
type ChallengeHas struct {
	Terraform  *HasTerraform  `json:"terraform,omitempty"`
	Compose    *HasCompose    `json:"compose,omitempty"`
	Kubernetes *HasKubernetes `json:"kubernetes,omitempty"`
	Connected  *HasConnected  `json:"connected,omitempty"`
}

// Where maps a dotted path to an expected value: a scalar (equal), or an
// object of matchers: exists (bool), in (list), notIn (list), min, max
// (numbers), contains (substring or list element), ref (a Terraform
// expression referencing that address prefix). Paths walk objects,
// Terraform nested blocks (any instance), lists (an index, "*" = any
// element, or an element whose name/key equals the segment).
type Where map[string]any

// Count bounds how many objects match (default: at least one).
type Count struct {
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
}

// HasTerraform matches resources (or data sources, "data.<type>") by type
// and optional local name; Where reads their arguments and nested blocks.
type HasTerraform struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Where Where  `json:"where,omitempty"`
	Count *Count `json:"count,omitempty"`
}

// HasCompose matches services (by name, image prefix or Nimbo image
// category: database, cache, queue, …); Where reads the effective compose
// service (compose spec keys: deploy.resources.limits.memory,
// healthcheck.test, depends_on.db.condition, …). With none of Service,
// Image, Category, Where reads the whole project (networks.x.internal).
type HasCompose struct {
	Service  string `json:"service,omitempty"`
	Image    string `json:"image,omitempty"`
	Category string `json:"category,omitempty"`
	Where    Where  `json:"where,omitempty"`
	Count    *Count `json:"count,omitempty"`
}

// HasKubernetes matches rendered objects by kind and optional name and
// namespace; Where reads the object (spec.replicas, spec.template.spec…).
type HasKubernetes struct {
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Where     Where  `json:"where,omitempty"`
	Count     *Count `json:"count,omitempty"`
}

// HasConnected asserts a graph edge, either direction, between a node
// matching From and one matching To: a Terraform type or address, a
// compose service name, or a Kubernetes "Kind/name" or name.
type HasConnected struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ChallengeTarget is an HTTP endpoint of the deployed workflow: a compose
// service's published container port, a Kubernetes Service port
// (daemon port-forward) or its Ingress rule, or a Terraform node (address
// or node ID) whose endpoint the Load test tab lists.
type ChallengeTarget struct {
	Service string `json:"service"`
	Port    int    `json:"port,omitempty"`
	// Ingress (Kubernetes): go through the Ingress rule routing to the
	// Service, so the ingress controller spreads every request over the
	// ready pods (the port-forward picks a pod per connection).
	Ingress bool   `json:"ingress,omitempty"`
	Method  string `json:"method,omitempty"` // default GET
	// Path may use load test placeholders ({{rand}}, {{seq}}, …).
	Path string `json:"path"`
}

// ChallengeHTTP calls the target once it answers and expects Status
// (default 200) and, optionally, a body containing Contains.
type ChallengeHTTP struct {
	ChallengeTarget
	Status   int    `json:"status,omitempty"`
	Contains string `json:"contains,omitempty"`
}

// ChallengeLoadSpec is the traffic of a load or scenario check: a preset
// or custom stages.
type ChallengeLoadSpec struct {
	Preset     string      `json:"preset,omitempty"`
	Mode       LoadMode    `json:"mode,omitempty"` // default rps
	Stages     []LoadStage `json:"stages,omitempty"`
	TimeoutMs  int         `json:"timeoutMs,omitempty"` // default 2000
	MaxWorkers int         `json:"maxWorkers,omitempty"`
}

// Challenge metrics (ChallengeMetric.Metric).
const (
	MetricP50       = "p50_ms"
	MetricP90       = "p90_ms"
	MetricP95       = "p95_ms"
	MetricP99       = "p99_ms"
	MetricMax       = "max_ms"
	MetricErrorRate = "error_rate" // fraction 0..1
	MetricRPS       = "rps_min"    // achieved requests per second
	// MetricReplicasMax is the most Ready replicas of a Kubernetes
	// workload during the run (Workload, default the target's).
	MetricReplicasMax = "replicas_max"
)

// ChallengeMetric is one measured expectation on a run.
type ChallengeMetric struct {
	Metric   string  `json:"metric"`
	Op       string  `json:"op"` // lt | lte | gt | gte
	Value    float64 `json:"value"`
	Workload string  `json:"workload,omitempty"`
}

// ChallengeLoad runs a load test against Target.
type ChallengeLoad struct {
	Target ChallengeTarget   `json:"target"`
	Spec   ChallengeLoadSpec `json:"spec"`
	Expect []ChallengeMetric `json:"expect"`
}

// ChallengeScenario runs a load test with an incident scenario.
// Scenario.Target names the compose service or Kubernetes workload.
type ChallengeScenario struct {
	Target   ChallengeTarget         `json:"target"`
	Spec     ChallengeLoadSpec       `json:"spec"`
	Scenario ScenarioSpec            `json:"scenario"`
	Expect   ChallengeScenarioExpect `json:"expect"`
}

// ChallengeScenarioExpect: the verdict must be Verdict or better
// (survived > degraded > failed), and every metric must hold.
type ChallengeScenarioExpect struct {
	Verdict string            `json:"verdict"` // survived | degraded
	Metrics []ChallengeMetric `json:"metrics,omitempty"`
}

// ChallengeReadinessCheck passes when the production readiness score of
// Environment ("" = the active one) is at least MinScore.
type ChallengeReadinessCheck struct {
	MinScore    int    `json:"minScore"`
	Environment string `json:"environment,omitempty"`
}

// Check states (ChallengeResult.Status, ChallengeGoalResult.Status).
const (
	ChallengePending  = "pending"
	ChallengeRunning  = "running"
	ChallengePassed   = "passed"
	ChallengeFailed   = "failed"
	ChallengeError    = "error"
	ChallengeCanceled = "canceled"
)

// ChallengeResult is one "Check my solution": every check in order
// (deploying before the first that needs the runtime). Score is 0–100:
// every check weighs the same, and inside one every measurement does.
type ChallengeResult struct {
	Challenge   string `json:"challenge"`
	Status      string `json:"status"`
	Environment string `json:"environment,omitempty"`
	// Phase says what runs now ("Deploying your changes", a check label).
	Phase     string                `json:"phase,omitempty"`
	StartedAt time.Time             `json:"startedAt"`
	EndedAt   *time.Time            `json:"endedAt,omitempty"`
	DeployRun string                `json:"deployRunId,omitempty"`
	Goals     []ChallengeGoalResult `json:"goals"`
	Passed    bool                  `json:"passed"`
	Score     int                   `json:"score"`
	Error     string                `json:"error,omitempty"`
}

// ChallengeGoalResult is one check's outcome (ID = ChallengeCheck.ID).
// RunID is its load test run (load, scenario) or deploy run (deploy).
type ChallengeGoalResult struct {
	ID     string                 `json:"id"`
	Label  string                 `json:"label"`
	Type   string                 `json:"type"`
	Status string                 `json:"status"`
	Passed bool                   `json:"passed"`
	Detail string                 `json:"detail"`
	RunID  string                 `json:"runId,omitempty"`
	Checks []ChallengeCheckResult `json:"checks"`
}

// ChallengeCheckResult is one measurement of a check.
type ChallengeCheckResult struct {
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Actual   string `json:"actual"`   // "312 ms", "4.2%", "degraded", "2 found"
	Expected string `json:"expected"` // "< 100 ms", "survived"
}

// ChallengeView is GET /workflows/{id}/challenge: the challenge and the
// latest check (running or finished; nil before the first).
type ChallengeView struct {
	Challenge Challenge        `json:"challenge"`
	Last      *ChallengeResult `json:"last,omitempty"`
}
