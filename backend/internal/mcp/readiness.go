package mcp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type productionReadinessIn struct {
	Workflow    string  `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment *string `json:"environment,omitempty" jsonschema:"only this environment (e.g. prod); omit for every environment. A workflow without environments has one entry, \"\" (its base, checked as production)"`
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

type readinessEnvView struct {
	Environment    string              `json:"environment"`
	ProductionLike bool                `json:"production_like"`
	Status         string              `json:"status"`
	Score          int                 `json:"score"`
	Summary        string              `json:"summary"`
	Open           []readinessItemView `json:"open"`
	Passed         []string            `json:"passed"`
	Skipped        []string            `json:"skipped,omitempty"`
}

// maxReadinessFindings caps the findings listed per item.
const maxReadinessFindings = 5

func (t *tools) productionReadiness(ctx context.Context, in productionReadinessIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	path := kubePath(wf, "/readiness")
	if in.Environment != nil {
		path += "?environment=" + url.QueryEscape(*in.Environment)
	}
	var rep model.KubeReadiness
	if err := t.c.get(ctx, path, &rep); err != nil {
		return nil, err
	}
	out := []readinessEnvView{}
	for _, e := range rep.Environments {
		v := readinessEnvView{Environment: e.Name, ProductionLike: e.ProductionLike, Status: string(e.Status), Score: e.Score, Summary: e.Summary, Open: []readinessItemView{}, Passed: []string{}}
		for _, g := range e.Groups {
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
						iv.Findings = append(iv.Findings, fmt.Sprintf("… %d more (review_kubernetes lists them)", len(it.Diagnostics)-i))
						break
					}
					iv.Findings = append(iv.Findings, fmt.Sprintf("%s %s %s:%d %s — fix: %s", d.Severity, d.Code, d.File, d.Line, d.Message, d.Fix))
				}
				if it.Action != nil {
					iv.Action, iv.RunID = readinessActionHint(it.Action), it.Action.RunID
				}
				v.Open = append(v.Open, iv)
			}
		}
		out = append(out, v)
	}
	return map[string]any{"workflow": wf.ID, "active_environment": rep.ActiveEnvironment, "environments": out}, nil
}

// readinessActionHint names the MCP tool that resolves an item.
func readinessActionHint(a *model.ReadinessAction) string {
	switch a.Kind {
	case model.ActionAddMonitoring:
		return "add_monitoring"
	case model.ActionRunLoadTest:
		return "update_workflow environment=<env>, deploy, then run_load_test (60 s or more so the HPA can react)"
	case model.ActionOpenLoadTest:
		return "get_load_test run_id for the replicas/latency timeline"
	case model.ActionOpenProblem:
		return "fix the findings (update_kubernetes_object or apply_terraform_code)"
	}
	return a.Label
}
