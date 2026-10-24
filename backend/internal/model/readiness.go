package model

// ---------------------------------------------------------------------------
// Production readiness (docker and Kubernetes workflows)

// ReadinessStatus is an environment's overall verdict.
type ReadinessStatus string

const (
	ReadinessReady     ReadinessStatus = "ready"
	ReadinessNeedsWork ReadinessStatus = "needs_work"
	// ReadinessBlocked: a rule or runtime item failed: the files don't load
	// or render, or the runtime would refuse them. A failed incident
	// scenario scores 0 in resilience but only needs work.
	ReadinessBlocked ReadinessStatus = "blocked"
)

// ReadinessItemStatus is one checklist item's (or component cell's) state.
type ReadinessItemStatus string

const (
	ItemPass ReadinessItemStatus = "pass"
	// ItemInfo passes with suggestions (info findings).
	ItemInfo ReadinessItemStatus = "info"
	ItemWarn ReadinessItemStatus = "warn"
	ItemFail ReadinessItemStatus = "fail"
	// ItemTodo: not verified yet (no load test, scenario run or capacity
	// plan of the environment). It never counts as passing or failing: it
	// lowers the confidence instead.
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
	// ActionRunScenario opens the Load test tab with incident Scenario
	// (and Target, when it acts on one) picked.
	ActionRunScenario = "run_scenario"
	// ActionPlanCapacity opens the capacity planner of the environment
	// (POST /workflows/{id}/capacity-plan).
	ActionPlanCapacity = "plan_capacity"
)

// Readiness categories (ReadinessGroup.ID).
const (
	ReadinessReliability = "reliability"
	ReadinessPerformance = "performance"
	ReadinessResilience  = "resilience"
	ReadinessMonitoring  = "monitoring"
	ReadinessSecurity    = "security"
	ReadinessHygiene     = "hygiene"
)

// Component columns (ReadinessCell.ID), in display order.
const (
	CellRules      = "rules"      // findings of the component not in another column
	CellHealth     = "health"     // healthchecks and restart policies / probes
	CellResources  = "resources"  // limits / requests and limits
	CellMonitoring = "monitoring" // metrics and alerts cover it
	CellLoad       = "load"       // headroom in the latest load test
	CellResilience = "resilience" // latest scenario verdicts that targeted it
	CellCapacity   = "capacity"   // the latest capacity plan's sizing of it
)

// WorkflowReadiness is GET /workflows/{id}/readiness: the production
// readiness of every environment of a docker or Kubernetes workflow.
type WorkflowReadiness struct {
	WorkflowID string `json:"workflowId"`
	// Kind is the workflow kind: "docker" or "kubernetes".
	Kind string `json:"kind"`
	// ActiveEnvironment is the environment the canvas shows ("" = base).
	ActiveEnvironment string                 `json:"activeEnvironment"`
	Environments      []EnvironmentReadiness `json:"environments"`
}

// EnvironmentReadiness is one environment's checklist. Name "" is the base;
// a workflow without environments checks it as production.
type EnvironmentReadiness struct {
	Name string `json:"name"`
	// ProductionLike: production-only checks apply and untested evidence
	// keeps the status at needs_work.
	ProductionLike bool            `json:"productionLike"`
	Status         ReadinessStatus `json:"status"`
	// Score is 0-100: the weighted mean of the category scores that have
	// evidence (see ReadinessGroup.Score). Untested items are left out.
	Score int `json:"score"`
	// Confidence is 0-100: the weighted share of the checks that were
	// verified (100 = nothing left untested).
	Confidence int              `json:"confidence"`
	Summary    string           `json:"summary"`
	Groups     []ReadinessGroup `json:"groups"`
	// Components are the services (docker) or workloads (Kubernetes), one
	// row each, with a cell per column (Cell* ids, always all of them).
	Components []ReadinessComponent `json:"components"`
	// LoadTestRunID is the latest finished load test of the environment the
	// evidence comes from ("" = none).
	LoadTestRunID string `json:"loadTestRunId,omitempty"`
}

// ReadinessGroup is a scoring category: reliability, performance,
// resilience, monitoring, security or hygiene.
type ReadinessGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Weight is the category's share of the environment score (the weights
	// of all categories add up to 100).
	Weight int `json:"weight"`
	// Score is the mean of the scored items (pass/info 100, warn 50, fail
	// 0); nil when no item has evidence (all untested or skipped).
	Score *int `json:"score"`
	// Untested counts the todo items.
	Untested int             `json:"untested"`
	Items    []ReadinessItem `json:"items"`
}

// ReadinessItem is one checklist entry with the diagnostics behind it.
type ReadinessItem struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Status  ReadinessItemStatus `json:"status"`
	Message string              `json:"message"`
	// Codes are the rules (k8s_*, dk_*) the item is made of.
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
	// Scenario and Target pick the incident to run (run_scenario).
	Scenario string `json:"scenario,omitempty"`
	Target   string `json:"target,omitempty"`
}

// ReadinessComponent is one service or workload of the environment.
type ReadinessComponent struct {
	// NodeID is its graph node (service:<name>, <Kind>/<ns>/<name>).
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
	// Kind is "service" (compose) or the workload kind.
	Kind string `json:"kind"`
	// Role is "app" (serves requests) or "data" (database, cache, queue,
	// stateful workload).
	Role   string              `json:"role"`
	Status ReadinessItemStatus `json:"status"` // worst cell
	// Score is the mean of the scored cells (like a category).
	Score *int            `json:"score"`
	Cells []ReadinessCell `json:"cells"`
}

// ReadinessCell is one column of a component row.
type ReadinessCell struct {
	ID          string              `json:"id"`
	Status      ReadinessItemStatus `json:"status"`
	Message     string              `json:"message"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
	Action      *ReadinessAction    `json:"action,omitempty"`
}
