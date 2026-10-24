package mcp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// production_readiness: GET /workflows/{id}/readiness for docker and
// Kubernetes workflows.

const groupReadiness = "Production readiness"

func registerReadinessTools(r *registry, t *tools) {
	r.group = groupReadiness
	addTool(r, "production_readiness", "Production readiness",
		"Production readiness of a docker or Kubernetes workflow per environment (each opened/rendered on its own; the active environment is not changed): status ready | needs_work | blocked, a 0-100 score, a 0-100 confidence and per-category scores. "+
			"Categories and weights: reliability 30 (Kubernetes: manifests apply, replicas + PodDisruptionBudgets, probes, requests/limits, rollout, topology spread, wiring; docker: project loads, configuration, healthchecks, restart policies, resource limits, persistent data), performance 20 (Kubernetes HPAs; the latest load test of that environment: autoscaling or container verdicts; the latest capacity plan), resilience 15 (the latest incident scenario run per scenario id: survived / degraded / failed), monitoring 10, security 15 and hygiene 10. "+
			"Scoring: pass/info items earn 1, warn 0.5, fail 0; not-tested items (no load test, scenario or capacity plan yet) are left out of the score and lower the confidence instead; a failing rule item blocks; a failed scenario scores 0 in resilience and needs work (it does not block); untested items keep production-like environments at needs_work. Each environment lists the scenarios of its own services/workloads. "+
			"components has one row per compose service / workload with its rules, health, resources, monitoring, load (headroom in the latest load test), resilience and capacity cells. Each open item names the tool that resolves or verifies it. Call it before declaring a workflow production-ready.",
		t.productionReadiness)
}

type productionReadinessIn struct {
	Workflow    string  `json:"workflow,omitempty" jsonschema:"docker or Kubernetes workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment *string `json:"environment,omitempty" jsonschema:"only this environment (e.g. prod); omit for every environment. \"\" is the base (docker workflows always list it; Kubernetes ones only without environments, checked as production)"`
}

// readinessItemView is one checklist item, compact.
type readinessItemView struct {
	Group    string   `json:"group"`
	ID       string   `json:"id"`
	Status   string   `json:"status"`
	Message  string   `json:"message"`
	Findings []string `json:"findings,omitempty"`
	Action   string   `json:"action,omitempty"`
	RunID    string   `json:"run_id,omitempty"`
}

type readinessCategoryView struct {
	ID       string `json:"id"`
	Weight   int    `json:"weight"`
	Score    *int   `json:"score"` // null: nothing verified yet
	Untested int    `json:"untested,omitempty"`
}

// readinessComponentView is one service/workload row: its open cells.
type readinessComponentView struct {
	Node   string   `json:"node"`
	Name   string   `json:"name"`
	Role   string   `json:"role"`
	Status string   `json:"status"`
	Score  *int     `json:"score"`
	Open   []string `json:"open,omitempty"`
}

type readinessEnvView struct {
	Environment    string                   `json:"environment"`
	ProductionLike bool                     `json:"production_like"`
	Status         string                   `json:"status"`
	Score          int                      `json:"score"`
	Confidence     int                      `json:"confidence"`
	Summary        string                   `json:"summary"`
	Categories     []readinessCategoryView  `json:"categories"`
	Open           []readinessItemView      `json:"open"`
	Passed         []string                 `json:"passed"`
	Skipped        []string                 `json:"skipped,omitempty"`
	Components     []readinessComponentView `json:"components"`
	LoadTestRunID  string                   `json:"load_test_run_id,omitempty"`
}

// maxReadinessFindings caps the findings listed per item.
const maxReadinessFindings = 5

func (t *tools) productionReadiness(ctx context.Context, in productionReadinessIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return nil, fmt.Errorf("%s is a %s workflow; production_readiness is for docker and Kubernetes workflows", wf.Name, model.KindLabel(wf.Kind))
	}
	path := "/workflows/" + pathEscape(wf.ID) + "/readiness"
	if in.Environment != nil {
		path += "?environment=" + url.QueryEscape(*in.Environment)
	}
	var rep model.WorkflowReadiness
	if err := t.c.get(ctx, path, &rep); err != nil {
		return nil, err
	}
	out := []readinessEnvView{}
	for _, e := range rep.Environments {
		v := readinessEnvView{Environment: e.Name, ProductionLike: e.ProductionLike, Status: string(e.Status), Score: e.Score, Confidence: e.Confidence,
			Summary: e.Summary, Categories: []readinessCategoryView{}, Open: []readinessItemView{}, Passed: []string{}, Components: []readinessComponentView{}, LoadTestRunID: e.LoadTestRunID}
		for _, g := range e.Groups {
			v.Categories = append(v.Categories, readinessCategoryView{ID: g.ID, Weight: g.Weight, Score: g.Score, Untested: g.Untested})
			for _, it := range g.Items {
				switch it.Status {
				case model.ItemPass:
					v.Passed = append(v.Passed, g.ID+"/"+it.ID)
					continue
				case model.ItemSkip:
					v.Skipped = append(v.Skipped, g.ID+"/"+it.ID+": "+it.Message)
					continue
				}
				iv := readinessItemView{Group: g.ID, ID: it.ID, Status: string(it.Status), Message: it.Message}
				for i, d := range it.Diagnostics {
					if i == maxReadinessFindings {
						iv.Findings = append(iv.Findings, fmt.Sprintf("… %d more (%s lists them)", len(it.Diagnostics)-i, reviewTool(wf)))
						break
					}
					iv.Findings = append(iv.Findings, fmt.Sprintf("%s %s %s:%d %s — fix: %s", d.Severity, d.Code, d.File, d.Line, d.Message, d.Fix))
				}
				if it.Action != nil {
					iv.Action, iv.RunID = readinessActionHint(wf, it.Action), it.Action.RunID
				}
				v.Open = append(v.Open, iv)
			}
		}
		for _, c := range e.Components {
			cv := readinessComponentView{Node: c.NodeID, Name: c.Name, Role: c.Role, Status: string(c.Status), Score: c.Score}
			for _, cell := range c.Cells {
				if cell.Status == model.ItemPass || cell.Status == model.ItemSkip || cell.Status == model.ItemInfo {
					continue
				}
				cv.Open = append(cv.Open, fmt.Sprintf("%s %s: %s", cell.ID, cell.Status, cell.Message))
			}
			v.Components = append(v.Components, cv)
		}
		out = append(out, v)
	}
	return map[string]any{"workflow": wf.ID, "kind": rep.Kind, "active_environment": rep.ActiveEnvironment, "environments": out}, nil
}

func reviewTool(wf *model.Workflow) string {
	if wf.IsDocker() {
		return "review_docker"
	}
	return "review_kubernetes"
}

// readinessActionHint names the MCP tool that resolves an item.
func readinessActionHint(wf *model.Workflow, a *model.ReadinessAction) string {
	switch a.Kind {
	case model.ActionAddMonitoring:
		return "add_monitoring"
	case model.ActionRunLoadTest:
		if wf.IsDocker() {
			return "update_workflow environment=<env>, deploy, then run_load_test against a service of the workflow"
		}
		return "update_workflow environment=<env>, deploy, then run_load_test (60 s or more so the HPA can react)"
	case model.ActionOpenLoadTest:
		return "get_load_test run_id for the timeline, container usage and verdicts"
	case model.ActionRunScenario:
		if a.Target != "" {
			return fmt.Sprintf("run_load_test with scenario {id: %q, target: %q} in that environment (list_scenarios shows targets and parameters)", a.Scenario, a.Target)
		}
		return fmt.Sprintf("run_load_test with scenario {id: %q} in that environment (list_scenarios shows targets and parameters)", a.Scenario)
	case model.ActionPlanCapacity:
		return "plan_capacity target_rps=<peak req/s> environment=<env>"
	case model.ActionOpenProblem:
		if wf.IsDocker() {
			return "fix the findings (update_service or apply_terraform_code), then review_docker"
		}
		return "fix the findings (update_kubernetes_object or apply_terraform_code)"
	}
	return a.Label
}
