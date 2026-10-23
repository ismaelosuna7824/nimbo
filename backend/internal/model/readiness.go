package model

// ---------------------------------------------------------------------------
// Production readiness (Kubernetes workflows)

// ReadinessStatus is an environment's overall verdict.
type ReadinessStatus string

const (
	ReadinessReady     ReadinessStatus = "ready"
	ReadinessNeedsWork ReadinessStatus = "needs_work"
	// ReadinessBlocked: the manifests don't render or the API server would
	// refuse them.
	ReadinessBlocked ReadinessStatus = "blocked"
)

// ReadinessItemStatus is one checklist item's state.
type ReadinessItemStatus string

const (
	ItemPass ReadinessItemStatus = "pass"
	// ItemInfo passes with suggestions (info findings).
	ItemInfo ReadinessItemStatus = "info"
	ItemWarn ReadinessItemStatus = "warn"
	ItemFail ReadinessItemStatus = "fail"
	// ItemTodo: not verified yet (no load test of the environment).
	ItemTodo ReadinessItemStatus = "todo"
	// ItemSkip: does not apply (production-only check in a dev
	// environment, nothing to check, or turned off in Settings).
	ItemSkip ReadinessItemStatus = "skip"
)

// Readiness actions (ReadinessAction.Kind).
const (
	ActionOpenProblem   = "open_problem"   // jump to the first diagnostic's file:line
	ActionRunLoadTest   = "run_load_test"  // open the Load test tab to start a run in the environment
	ActionOpenLoadTest  = "open_load_test" // open run RunID in the Load test tab
	ActionAddMonitoring = "add_monitoring" // POST /workflows/{id}/monitoring
)

// KubeReadiness is GET /workflows/{id}/kubernetes/readiness: the production
// readiness of every environment of a Kubernetes workflow.
type KubeReadiness struct {
	WorkflowID string `json:"workflowId"`
	// ActiveEnvironment is the environment the canvas shows ("" = base).
	ActiveEnvironment string                 `json:"activeEnvironment"`
	Environments      []EnvironmentReadiness `json:"environments"`
}

// EnvironmentReadiness is one environment's checklist. Name "" is the base
// of a workflow without environments; it is checked as production.
type EnvironmentReadiness struct {
	Name string `json:"name"`
	// ProductionLike: production-only rules (replicas, spread, rollouts,
	// quotas, digests) apply.
	ProductionLike bool            `json:"productionLike"`
	Status         ReadinessStatus `json:"status"`
	// Score is 0-100: passed items count fully, warnings and untested
	// items half, failures nothing; skipped items are left out.
	Score   int              `json:"score"`
	Summary string           `json:"summary"`
	Groups  []ReadinessGroup `json:"groups"`
}

// ReadinessGroup is a checklist section: reliability, scaling, monitoring,
// security or hygiene.
type ReadinessGroup struct {
	ID    string          `json:"id"`
	Title string          `json:"title"`
	Items []ReadinessItem `json:"items"`
}

// ReadinessItem is one checklist entry with the diagnostics behind it.
type ReadinessItem struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Status  ReadinessItemStatus `json:"status"`
	Message string              `json:"message"`
	// Codes are the k8s_* rules the item is made of.
	Codes       []string         `json:"codes,omitempty"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
	Action      *ReadinessAction `json:"action,omitempty"`
}

// ReadinessAction is what fixes or verifies an item.
type ReadinessAction struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// RunID is the load test run (open_load_test).
	RunID string `json:"runId,omitempty"`
}
