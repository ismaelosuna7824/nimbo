package readiness

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Sources are the evidence of a workflow its environments share.
type Sources struct {
	// Runs are the workflow's load test runs, newest first.
	Runs []model.LoadTestRun
	// Plan returns the latest capacity plan of an environment (nil: none).
	Plan func(env string) *model.CapacityPlan
}

// Evidence is what real runs say about one environment. Catalog is the
// incident scenarios that apply to that environment's own graph.
type Evidence struct {
	Env      string
	ProdLike bool
	// Label names the environment in messages ("prod", "the base").
	Label   string
	Catalog model.ScenarioCatalog
	Plan    *model.CapacityPlan
	// Load is the latest finished load test without a scenario (nil:
	// none); Running a run of the environment still running.
	Load    *model.LoadTestRun
	Running bool
	// scenarios are the graded scenario runs of the environment, newest
	// first; ungraded ones (stopped, or the target already failing) neither
	// pass nor fail anything and only explain a todo.
	scenarios []*model.LoadTestRun
	ungraded  []*model.LoadTestRun
}

// For collects the evidence of environment env; catalog is the scenarios
// that apply to it (DockerCatalog, KubeCatalog).
func (s Sources) For(env string, prodLike bool, catalog model.ScenarioCatalog) *Evidence {
	ev := &Evidence{Env: env, ProdLike: prodLike, Label: envLabel(env), Catalog: catalog}
	if s.Plan != nil {
		ev.Plan = s.Plan(env)
	}
	for i := range s.Runs {
		r := &s.Runs[i]
		if r.Environment != env || r.Status == model.LoadErrored {
			continue
		}
		switch {
		case r.Status == model.LoadRunning:
			ev.Running = true
		case r.Scenario != nil && model.ScenarioGraded(r.Scenario.Verdict):
			ev.scenarios = append(ev.scenarios, r)
		case r.Scenario != nil:
			ev.ungraded = append(ev.ungraded, r)
		case r.Spec.Scenario == nil && ev.Load == nil:
			ev.Load = r
		}
	}
	return ev
}

func envLabel(env string) string {
	if env == "" {
		return "the base"
	}
	return env
}

func when(r *model.LoadTestRun) string { return r.StartedAt.UTC().Format("Jan 2 15:04 UTC") }

func openRun(r *model.LoadTestRun) *model.ReadinessAction {
	return &model.ReadinessAction{Kind: model.ActionOpenLoadTest, Label: "Open load test", RunID: r.ID}
}

func (ev *Evidence) runLoadTest(label string) *model.ReadinessAction {
	return &model.ReadinessAction{Kind: model.ActionRunLoadTest, Label: label + " in " + ev.Label}
}

// planCapacity opens the capacity planner on run runID ("" = its default).
func planCapacity(label, runID string) *model.ReadinessAction {
	return &model.ReadinessAction{Kind: model.ActionPlanCapacity, Label: label, RunID: runID}
}

// scalingRank orders autoscaling verdicts worst first.
var scalingRank = map[string]int{
	model.ScalingMaxed: 0, model.ScalingLate: 1, model.ScalingNoMetrics: 2, model.ScalingNoHPA: 3,
	model.ScalingTooShort: 4, model.ScalingScaled: 5, model.ScalingNotNeeded: 6,
}

// Resource verdict kinds (model.ResourceVerdict.Kind).
const (
	verdictOOM       = "oom"
	verdictRestarted = "restarted"
)

// saturated reports a verdict that found a CPU or memory ceiling (not a
// crash): expected when a test looks for the limit.
func saturated(kind string) bool {
	return kind == "cpu-bound" || kind == "single-core" || kind == "memory-bound"
}

// planned reports whether the environment's capacity plan was sized from
// run (so the ceiling it found is accounted for).
func (ev *Evidence) planned(r *model.LoadTestRun) bool {
	return ev.Plan != nil && r != nil && ev.Plan.Run.ID == r.ID
}

// LoadItem is the "Load tested" item: the latest load test of the
// environment. scaling (Kubernetes) judges its autoscaling verdict and
// requires one; otherwise the resource verdicts are judged.
func (ev *Evidence) LoadItem(scaling bool) model.ReadinessItem {
	item := model.ReadinessItem{ID: "load_test", Title: "Load tested"}
	last := ev.Load
	if last == nil {
		item.Status, item.Action = model.ItemTodo, ev.runLoadTest("Run a load test")
		item.Message = fmt.Sprintf("Not tested yet: run a load test in %s to see how it holds under traffic.", ev.Label)
		if ev.Running {
			item.Message = fmt.Sprintf("A load test is running in %s.", ev.Label)
		}
		return item
	}
	item.Action = openRun(last)
	res := last.Resources
	if scaling {
		var sc *model.LoadScaling
		if res != nil {
			sc = res.Scaling
		}
		if sc == nil || len(sc.Workloads) == 0 {
			item.Status, item.Action = model.ItemTodo, ev.runLoadTest("Run a load test")
			item.Message = fmt.Sprintf("The last load test (%s) has no autoscaling data: run one again in %s.", when(last), ev.Label)
			return item
		}
		kind := sc.Workloads[0].Kind
		for _, w := range sc.Workloads[1:] {
			if rk, ok := scalingRank[w.Kind]; ok && rk < scalingRank[kind] {
				kind = w.Kind
			}
		}
		item.Message = fmt.Sprintf("%s (load test of %s)", strings.TrimSuffix(sc.Verdict, "."), when(last))
		switch kind {
		case model.ScalingMaxed, model.ScalingLate, model.ScalingNoMetrics:
			item.Status = model.ItemWarn
		case model.ScalingNoHPA:
			item.Status = model.ItemInfo
			if ev.ProdLike {
				item.Status = model.ItemWarn
			}
		case model.ScalingTooShort:
			item.Status, item.Action = model.ItemTodo, ev.runLoadTest("Run a longer load test")
			return item
		case model.ScalingNotNeeded:
			item.Status = model.ItemInfo // held, but the HPA was never exercised
		default:
			item.Status = model.ItemPass
		}
	} else {
		if res == nil || len(res.Verdicts) == 0 {
			item.Status, item.Action = model.ItemTodo, ev.runLoadTest("Run a load test")
			item.Message = fmt.Sprintf("The last load test (%s) has no container usage: run one against a service of the workflow in %s.", when(last), ev.Label)
			return item
		}
		item.Status = model.ItemPass
		item.Message = fmt.Sprintf("%s (load test of %s)", strings.TrimSuffix(res.Verdict, "."), when(last))
		for _, v := range res.Verdicts {
			switch {
			case v.Kind == verdictOOM || v.Kind == verdictRestarted:
				item.Status = Worst(item.Status, model.ItemWarn)
			case saturated(v.Kind) && ev.planned(last):
				item.Status = Worst(item.Status, model.ItemInfo)
			case saturated(v.Kind):
				item.Status = Worst(item.Status, model.ItemWarn)
			}
		}
		if item.Status == model.ItemWarn && !ev.planned(last) && slices.ContainsFunc(res.Verdicts, func(v model.ResourceVerdict) bool { return saturated(v.Kind) }) {
			item.Message += "; plan capacity from it to size for your target traffic"
		}
		// A breaking point no container explains: the limit is in the app.
		if last.Summary != nil && last.Summary.BreakingPoint != nil {
			item.Status = Worst(item.Status, model.ItemInfo)
		}
	}
	// Crashes during the run count on every kind.
	if res != nil && slices.ContainsFunc(res.Verdicts, func(v model.ResourceVerdict) bool { return v.Kind == verdictOOM || v.Kind == verdictRestarted }) {
		item.Status = Worst(item.Status, model.ItemWarn)
	}
	if last.Status == model.LoadFailed {
		item.Status = Worst(item.Status, model.ItemWarn)
		item.Message += "; it failed its thresholds"
	}
	return item
}

// CapacityItem is the "Capacity planned" item: the latest capacity plan of
// the environment, sized from a load test.
func (ev *Evidence) CapacityItem() model.ReadinessItem {
	item := model.ReadinessItem{ID: "capacity", Title: "Capacity planned"}
	p := ev.Plan
	switch {
	case p == nil && ev.Load == nil:
		item.Status, item.Action = model.ItemTodo, ev.runLoadTest("Run a load test")
		item.Message = fmt.Sprintf("Not planned yet: a capacity plan is sized from a load test of %s.", ev.Label)
		return item
	case p == nil:
		item.Status, item.Action = model.ItemTodo, planCapacity("Plan capacity", ev.Load.ID)
		item.Message = "Not planned yet: size replicas, requests/limits and nodes for your target traffic from the last load test."
		return item
	}
	item.Action = planCapacity("Open capacity plan", p.Run.ID)
	var parts []string
	for _, s := range p.Services {
		if s.Role == model.CapacityApp {
			parts = append(parts, fmt.Sprintf("%s ×%d", s.Service, s.Replicas))
		}
	}
	item.Message = fmt.Sprintf("Planned for %s req/s (%s confidence)", num(p.TargetRPS), p.Confidence)
	if len(parts) > 0 {
		item.Message += ": " + List(parts)
	}
	if c := cheapest(p.Clouds); c != nil {
		item.Message += fmt.Sprintf("; from $%s/month on %s (prices of %s)", num(c.Recommended.MonthlyUSD), strings.ToUpper(c.Cloud), p.Prices.AsOf)
	}
	item.Message += "."
	item.Status = model.ItemPass
	if p.Confidence == model.ConfidenceLow {
		item.Status = model.ItemInfo
	}
	if l := ev.Load; l != nil && p.Run.ID != l.ID && l.StartedAt.After(p.Run.StartedAt) {
		item.Status = model.ItemInfo
		item.Message += " It comes from an older load test: plan again from the latest one."
		item.Action = planCapacity("Plan again", l.ID)
	}
	return item
}

func cheapest(clouds []model.CloudEstimate) *model.CloudEstimate {
	var out *model.CloudEstimate
	for i := range clouds {
		if c := &clouds[i]; out == nil || c.Recommended.MonthlyUSD < out.Recommended.MonthlyUSD {
			out = c
		}
	}
	return out
}

// num formats a quantity for messages: whole numbers and values from 100
// up without decimals, values below 1 with two (0.03 CPU), else one.
func num(f float64) string {
	if f == math.Trunc(f) || f >= 100 {
		return fmt.Sprintf("%.0f", f)
	}
	format := "%.1f"
	if math.Abs(f) < 1 {
		format = "%.2f"
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf(format, f), "0"), ".")
}

// verdictStatus maps a scenario verdict to an item status.
func verdictStatus(v string) model.ReadinessItemStatus {
	switch v {
	case model.ScenarioSurvived:
		return model.ItemPass
	case model.ScenarioDegraded:
		return model.ItemWarn
	}
	return model.ItemFail
}

// ScenarioTitles are fallback names of the scenarios (the catalog's titles
// win).
var ScenarioTitles = map[string]string{
	model.ScenarioKillReplica:       "Replica loss",
	model.ScenarioDependencyDown:    "Dependency outage",
	model.ScenarioDependencyLatency: "Slow dependency",
	model.ScenarioCacheFlush:        "Cache flush",
	model.ScenarioTrafficSpike:      "Traffic spike",
}

func (ev *Evidence) scenarioTitle(id string) string {
	for _, o := range ev.Catalog.Scenarios {
		if o.ID == id {
			return o.Title
		}
	}
	return cmp.Or(ScenarioTitles[id], id)
}

// latestScenario is the newest run of scenario id (nil: none).
func (ev *Evidence) latestScenario(id string) *model.LoadTestRun {
	for _, r := range ev.scenarios {
		if r.Scenario.ID == id {
			return r
		}
	}
	return nil
}

// latestUngraded is the newest ungraded run of scenario id (nil: none).
func (ev *Evidence) latestUngraded(id string) *model.LoadTestRun {
	for _, r := range ev.ungraded {
		if r.Scenario.ID == id {
			return r
		}
	}
	return nil
}

// ResilienceItems are one item per scenario: applicable ones are verified
// by their latest run in the environment (todo until one ran), the others
// are skipped with the catalog's reason.
func (ev *Evidence) ResilienceItems() []model.ReadinessItem {
	out := []model.ReadinessItem{}
	seen := map[string]bool{}
	add := func(id, title string, target string) {
		seen[id] = true
		item := model.ReadinessItem{ID: id, Title: title}
		r := ev.latestScenario(id)
		if r == nil {
			item.Status = model.ItemTodo
			item.Message = fmt.Sprintf("Not tested yet: run a load test with this incident in %s.", ev.Label)
			if u := ev.latestUngraded(id); u != nil {
				item.Message = fmt.Sprintf("No verdict yet: %s (%s).", strings.TrimSuffix(u.Scenario.Message, "."), when(u))
			}
			item.Action = &model.ReadinessAction{Kind: model.ActionRunScenario, Label: "Run scenario", Scenario: id, Target: target}
			out = append(out, item)
			return
		}
		res := r.Scenario
		item.Status, item.Action = verdictStatus(res.Verdict), openRun(r)
		name := cmp.Or(res.TargetName, res.Target)
		item.Message = strings.TrimSuffix(res.Message, ".")
		if name != "" {
			item.Message += fmt.Sprintf(" (%s, %s)", name, when(r))
		} else {
			item.Message += fmt.Sprintf(" (%s)", when(r))
		}
		out = append(out, item)
	}
	for _, o := range ev.Catalog.Scenarios {
		target := ""
		if len(o.Targets) > 0 {
			target = o.Targets[0].ID
		}
		add(o.ID, o.Title, target)
	}
	// Runs of scenarios the catalog no longer offers still count.
	for _, r := range ev.scenarios {
		if id := r.Scenario.ID; !seen[id] {
			add(id, ev.scenarioTitle(id), "")
		}
	}
	for _, u := range ev.Catalog.Unavailable {
		if !seen[u.ID] {
			out = append(out, model.ReadinessItem{ID: u.ID, Title: u.Title, Status: model.ItemSkip, Message: u.Reason})
		}
	}
	return out
}

// List joins names: "a", "a and b", "a, b and c" (at most 4, then "+N").
func List(names []string) string {
	if len(names) > 4 {
		names = append(slices.Clone(names[:3]), fmt.Sprintf("%d more", len(names)-3))
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// Plural picks one or many by n.
func Plural(n int, one, many string) string { return plural(n, one, many) }
