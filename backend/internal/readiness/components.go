package readiness

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Column is a rules-backed component column: the codes it holds and its
// message when none of them fires.
type Column struct {
	Codes []string
	Pass  string
	// Off, when set, skips the column with this message (rules turned
	// off, or not applicable to the component).
	Off string
}

// ComponentSpec is one service or workload to build a row for.
type ComponentSpec struct {
	NodeID, Name, Kind string
	// Role is "app" or "data".
	Role string
	// Diagnostics are the findings whose NodeID is the component.
	Diagnostics []model.Diagnostic
	Health      Column
	Resources   Column
}

// Component roles.
const (
	RoleApp  = "app"
	RoleData = "data"
)

// Component builds a component row: its findings split into the rules,
// health and resources columns, the environment's monitoring item, and the
// evidence of the runs that measured or targeted it.
func (ev *Evidence) Component(spec ComponentSpec, monitoring model.ReadinessItem) model.ReadinessComponent {
	var health, resources, rest []model.Diagnostic
	for _, d := range spec.Diagnostics {
		switch {
		case slices.Contains(spec.Health.Codes, d.Code):
			health = append(health, d)
		case slices.Contains(spec.Resources.Codes, d.Code):
			resources = append(resources, d)
		default:
			rest = append(rest, d)
		}
	}
	c := model.ReadinessComponent{NodeID: spec.NodeID, Name: spec.Name, Kind: spec.Kind, Role: spec.Role}
	c.Cells = []model.ReadinessCell{
		findingsCell(model.CellRules, rest, Column{Pass: "No findings."}),
		findingsCell(model.CellHealth, health, spec.Health),
		findingsCell(model.CellResources, resources, spec.Resources),
		monitoringCell(monitoring),
		ev.loadCell(spec.NodeID),
		ev.resilienceCell(spec.NodeID),
		ev.capacityCell(spec.NodeID),
	}
	return finishComponent(c)
}

var severityStatus = map[model.Severity]model.ReadinessItemStatus{
	model.SeverityError: model.ItemFail, model.SeverityWarning: model.ItemWarn, model.SeverityInfo: model.ItemInfo,
}

func findingsCell(id string, ds []model.Diagnostic, col Column) model.ReadinessCell {
	cell := model.ReadinessCell{ID: id}
	if len(ds) == 0 {
		cell.Status, cell.Message = model.ItemPass, col.Pass
		if col.Off != "" {
			cell.Status, cell.Message = model.ItemSkip, col.Off
		}
		return cell
	}
	SortDiagnostics(ds)
	cell.Status, cell.Diagnostics = severityStatus[ds[0].Severity], ds
	cell.Message = ds[0].Message
	if n := len(ds) - 1; n > 0 {
		cell.Message += fmt.Sprintf(" (+%d more)", n)
	}
	cell.Action = &model.ReadinessAction{Kind: model.ActionOpenProblem, Label: "Open problem"}
	return cell
}

var severityRank = map[model.Severity]int{model.SeverityError: 0, model.SeverityWarning: 1, model.SeverityInfo: 2}

// SortDiagnostics sorts findings worst first, then by file:line.
func SortDiagnostics(ds []model.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b model.Diagnostic) int {
		return cmp.Or(cmp.Compare(severityRank[a.Severity], severityRank[b.Severity]), cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
}

// monitoringCell mirrors the environment's monitoring item: the stack
// watches every container of the project.
func monitoringCell(item model.ReadinessItem) model.ReadinessCell {
	cell := model.ReadinessCell{ID: model.CellMonitoring, Status: item.Status, Action: item.Action}
	switch item.Status {
	case model.ItemPass:
		cell.Message = "Container metrics and alerts cover it."
	case model.ItemSkip:
		cell.Message = item.Message
	default:
		cell.Message = "No metrics or alerts."
	}
	return cell
}

// loadCell is the component's usage in the latest load test.
func (ev *Evidence) loadCell(node string) model.ReadinessCell {
	cell := model.ReadinessCell{ID: model.CellLoad}
	r := ev.Load
	if r == nil {
		cell.Status, cell.Message, cell.Action = model.ItemTodo, "Not load tested yet.", ev.runLoadTest("Run a load test")
		return cell
	}
	cell.Action = openRun(r)
	var svc *model.LoadServiceResources
	var verdict *model.ResourceVerdict
	var ws *model.WorkloadScaling
	if res := r.Resources; res != nil {
		for i := range res.Services {
			if res.Services[i].NodeID == node {
				svc = &res.Services[i]
				break
			}
		}
		if svc != nil {
			for i := range res.Verdicts {
				if res.Verdicts[i].Service == svc.Service {
					verdict = &res.Verdicts[i]
					break
				}
			}
		}
		if res.Scaling != nil {
			for i := range res.Scaling.Workloads {
				if res.Scaling.Workloads[i].NodeID == node {
					ws = &res.Scaling.Workloads[i]
					break
				}
			}
		}
	}
	if svc == nil && ws == nil {
		cell.Status, cell.Message = model.ItemTodo, "Not measured by the last load test."
		return cell
	}
	cell.Status = model.ItemPass
	var msgs []string
	if svc != nil {
		switch {
		case verdict != nil && (verdict.Kind == verdictOOM || verdict.Kind == verdictRestarted):
			cell.Status = model.ItemWarn
			msgs = append(msgs, verdict.Message)
		case verdict != nil && saturated(verdict.Kind):
			cell.Status = model.ItemWarn
			if ev.planned(r) {
				cell.Status = model.ItemInfo
			}
			msgs = append(msgs, verdict.Message)
		default:
			msgs = append(msgs, Headroom(*svc))
		}
	}
	if ws != nil {
		switch ws.Kind {
		case model.ScalingMaxed, model.ScalingLate, model.ScalingNoMetrics:
			cell.Status = Worst(cell.Status, model.ItemWarn)
		case model.ScalingTooShort, model.ScalingNotNeeded:
			cell.Status = Worst(cell.Status, model.ItemInfo)
		}
		if ws.Message != "" {
			msgs = append(msgs, strings.TrimSuffix(ws.Message, "."))
		}
	}
	cell.Message = strings.Join(msgs, "; ") + "."
	return cell
}

// Headroom describes how much CPU and memory a service had left at its
// peak.
func Headroom(s model.LoadServiceResources) string {
	var out string
	if s.CPULimited && s.CPULimit > 0 {
		used := pct(math.Min(s.PeakCPUOf, 1))
		out = fmt.Sprintf("Peak CPU %d%% of its %s limit (%d%% headroom)", used, cores(s.CPULimit), 100-used)
	} else {
		out = fmt.Sprintf("Peak CPU %.0f%% of a core, no limit", s.PeakCPU)
	}
	if s.MemLimited && s.MemLimit > 0 {
		out += fmt.Sprintf(", memory %d%% of its limit", pct(math.Min(s.PeakMemOf, 1)))
	}
	return out
}

func cores(c float64) string {
	if c == 1 {
		return "1 CPU"
	}
	return num(c) + " CPU"
}

// scenarioNode is the node a scenario run acted on: its target, or for a
// traffic spike the load-tested workload or service.
func scenarioNode(r *model.LoadTestRun) string {
	if r.Scenario.ID == model.ScenarioTrafficSpike {
		if res := r.Resources; res != nil {
			if res.Scaling != nil {
				for _, w := range res.Scaling.Workloads {
					if w.Target {
						return w.NodeID
					}
				}
			}
			for _, s := range res.Services {
				if s.Target {
					return s.NodeID
				}
			}
		}
		return cmp.Or(r.Scenario.Target, r.Spec.Target.NodeID)
	}
	return r.Scenario.Target
}

// resilienceCell is the latest verdict of every scenario that acted on the
// component (todo while one applies and none ran).
func (ev *Evidence) resilienceCell(node string) model.ReadinessCell {
	cell := model.ReadinessCell{ID: model.CellResilience, Status: model.ItemSkip}
	seen := map[string]bool{}
	var msgs []string
	for _, r := range ev.scenarios {
		id := r.Scenario.ID
		if seen[id] || scenarioNode(r) != node {
			continue
		}
		seen[id] = true
		st := verdictStatus(r.Scenario.Verdict)
		if cell.Action == nil || statusRank[st] < statusRank[cell.Status] {
			cell.Action = openRun(r)
		}
		cell.Status = Worst(cell.Status, st)
		msgs = append(msgs, fmt.Sprintf("%s: %s", ev.scenarioTitle(id), r.Scenario.Verdict))
	}
	if len(msgs) > 0 {
		cell.Message = strings.Join(msgs, "; ") + "."
		return cell
	}
	for _, o := range ev.Catalog.Scenarios {
		if slices.ContainsFunc(o.Targets, func(t model.ScenarioTarget) bool { return t.ID == node }) {
			cell.Status, cell.Message = model.ItemTodo, "Not tested yet: "+strings.ToLower(o.Title[:1])+o.Title[1:]+"."
			cell.Action = &model.ReadinessAction{Kind: model.ActionRunScenario, Label: "Run scenario", Scenario: o.ID, Target: node}
			return cell
		}
	}
	cell.Message = "No incident scenario acts on it."
	return cell
}

// capacityCell is the component's sizing in the capacity plan.
func (ev *Evidence) capacityCell(node string) model.ReadinessCell {
	cell := model.ReadinessCell{ID: model.CellCapacity}
	p := ev.Plan
	if p == nil {
		cell.Status, cell.Message = model.ItemTodo, "Not planned yet."
		if ev.Load == nil {
			cell.Action = ev.runLoadTest("Run a load test")
		} else {
			cell.Action = planCapacity("Plan capacity", ev.Load.ID)
		}
		return cell
	}
	cell.Action = planCapacity("Open capacity plan", p.Run.ID)
	i := slices.IndexFunc(p.Services, func(s model.CapacityService) bool { return s.NodeID == node })
	if i < 0 {
		cell.Status, cell.Message = model.ItemSkip, "Not in the capacity plan."
		return cell
	}
	s := p.Services[i]
	cell.Status = model.ItemPass
	if p.Confidence == model.ConfidenceLow || s.Caveat != "" {
		cell.Status = model.ItemInfo
	}
	switch s.Role {
	case model.CapacityApp:
		cell.Message = fmt.Sprintf("%d %s for %s req/s", s.Replicas, plural(s.Replicas, "replica", "replicas"), num(p.TargetRPS))
	case model.CapacityIdle:
		cell.Message = fmt.Sprintf("Keeps %d %s (idle under load)", s.Replicas, plural(s.Replicas, "replica", "replicas"))
	default:
		cell.Message = fmt.Sprintf("Needs %s CPU at %s req/s", num(s.ProjectedCPU), num(p.TargetRPS))
	}
	if s.CPULimit != "" || s.MemLimit != "" {
		cell.Message += fmt.Sprintf(" · limits %s CPU, %s", cmp.Or(s.CPULimit, "-"), cmp.Or(s.MemLimit, "-"))
	}
	cell.Message += "."
	if s.Caveat != "" {
		cell.Message += " " + s.Caveat
	}
	return cell
}
