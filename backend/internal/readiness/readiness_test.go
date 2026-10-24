package readiness

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func items(statuses ...model.ReadinessItemStatus) []model.ReadinessItem {
	out := make([]model.ReadinessItem, len(statuses))
	for i, s := range statuses {
		out[i] = model.ReadinessItem{ID: string(rune('a' + i)), Status: s, Message: "item " + string(s)}
	}
	return out
}

func env(prodLike bool, groups ...model.ReadinessGroup) model.EnvironmentReadiness {
	return Summarize(model.EnvironmentReadiness{ProductionLike: prodLike, Groups: groups})
}

func TestWeights(t *testing.T) {
	sum := 0
	for _, id := range Order {
		if Weights[id] <= 0 || titles[id] == "" {
			t.Errorf("%s: weight %d title %q", id, Weights[id], titles[id])
		}
		sum += Weights[id]
	}
	if sum != 100 || len(Order) != len(Weights) {
		t.Errorf("weights add up to %d over %d categories", sum, len(Order))
	}
}

func TestGroupScore(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []model.ReadinessItemStatus
		score    *int
		untested int
	}{
		{"all pass", []model.ReadinessItemStatus{model.ItemPass, model.ItemInfo}, new(100), 0},
		{"one warn of two", []model.ReadinessItemStatus{model.ItemPass, model.ItemWarn}, new(75), 0},
		{"warn alone is half", []model.ReadinessItemStatus{model.ItemWarn}, new(50), 0},
		{"fail alone is zero", []model.ReadinessItemStatus{model.ItemFail}, new(0), 0},
		{"thirds round", []model.ReadinessItemStatus{model.ItemPass, model.ItemPass, model.ItemFail}, new(67), 0},
		{"todo is left out", []model.ReadinessItemStatus{model.ItemPass, model.ItemTodo}, new(100), 1},
		{"skip is left out", []model.ReadinessItemStatus{model.ItemWarn, model.ItemSkip}, new(50), 0},
		{"no evidence", []model.ReadinessItemStatus{model.ItemTodo, model.ItemSkip}, nil, 1},
		{"empty", nil, nil, 0},
	} {
		g := Group(model.ReadinessReliability, items(tc.statuses...))
		if !reflect.DeepEqual(g.Score, tc.score) || g.Untested != tc.untested || g.Weight != 30 || g.Items == nil {
			t.Errorf("%s: score %v untested %d weight %d", tc.name, deref(g.Score), g.Untested, g.Weight)
		}
	}
}

func TestSummarize(t *testing.T) {
	// Weighted mean of the scored categories: reliability 30 × 0.75 and
	// security 15 × 1 over 45 → 83; performance (20) is untested: it is
	// out of the score and the confidence is 45 / 65.
	e := env(true,
		Group(model.ReadinessReliability, items(model.ItemPass, model.ItemWarn)),
		Group(model.ReadinessPerformance, items(model.ItemTodo)),
		Group(model.ReadinessSecurity, items(model.ItemPass)),
	)
	if e.Score != 83 || e.Confidence != 69 || e.Status != model.ReadinessNeedsWork || e.Summary != "1 check needs work; 1 not tested yet." {
		t.Errorf("mixed: %d %d %s %q", e.Score, e.Confidence, e.Status, e.Summary)
	}
	// Not tested yet is not failing: the score stays, the confidence drops;
	// production-like environments need the evidence to be ready.
	untested := []model.ReadinessGroup{
		Group(model.ReadinessReliability, items(model.ItemPass)),
		Group(model.ReadinessPerformance, items(model.ItemTodo, model.ItemTodo)),
	}
	if e := env(true, untested...); e.Score != 100 || e.Confidence != 60 || e.Status != model.ReadinessNeedsWork || e.Summary != "2 not tested yet." {
		t.Errorf("untested prod: %d %d %s %q", e.Score, e.Confidence, e.Status, e.Summary)
	}
	if e := env(false, untested...); e.Score != 100 || e.Status != model.ReadinessReady || e.Summary != "Ready; 2 not tested yet." {
		t.Errorf("untested dev: %d %s %q", e.Score, e.Status, e.Summary)
	}
	// Any rule or runtime failure blocks, whatever the score.
	nine := slices.Repeat([]model.ReadinessItemStatus{model.ItemPass}, 19)
	blocked := env(true, Group(model.ReadinessReliability, items(append(nine, model.ItemFail)...)))
	if blocked.Score != 95 || blocked.Status != model.ReadinessBlocked || blocked.Summary != "Blocked: item fail" {
		t.Errorf("blocked: %d %s %q", blocked.Score, blocked.Status, blocked.Summary)
	}
	// A failed scenario scores 0 in resilience but only needs work.
	failed := env(true, Group(model.ReadinessReliability, items(model.ItemPass)), Group(model.ReadinessResilience, items(model.ItemPass, model.ItemFail)))
	if failed.Score != 83 || failed.Status != model.ReadinessNeedsWork || failed.Summary != "1 check needs work." {
		t.Errorf("failed scenario: %d %s %q", failed.Score, failed.Status, failed.Summary)
	}
	// Everything verified and passing.
	if e := env(true, Group(model.ReadinessReliability, items(model.ItemPass)), Group(model.ReadinessMonitoring, items(model.ItemInfo))); e.Score != 100 || e.Confidence != 100 || e.Status != model.ReadinessReady || e.Summary != "Ready for production." {
		t.Errorf("ready: %d %d %s %q", e.Score, e.Confidence, e.Status, e.Summary)
	}
	// Nothing applicable: full marks, nothing to verify.
	if e := env(true, Group(model.ReadinessResilience, items(model.ItemSkip))); e.Score != 100 || e.Confidence != 100 {
		t.Errorf("all skipped: %d %d", e.Score, e.Confidence)
	}
	// Category means are combined unrounded: 1/3 and 2/3 weighted 30/15.
	if e := env(true, Group(model.ReadinessReliability, items(model.ItemPass, model.ItemFail, model.ItemFail)), Group(model.ReadinessSecurity, items(model.ItemPass, model.ItemPass, model.ItemFail))); e.Score != 44 {
		t.Errorf("unrounded mean: %d", e.Score)
	}
}

func TestSummarizeDeterministic(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", nil)
	src := evidenceSources(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), wf, true)
	a := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, src, nil)
	b := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, src, nil)
	if !reflect.DeepEqual(a, b) {
		t.Error("two reports of the same files and runs differ")
	}
}

// dockerTemplate writes a docker template into a fresh directory and derives
// the workflow like the daemon does.
func dockerTemplate(t *testing.T, id string, extra map[string]string) (string, *model.Workflow) {
	t.Helper()
	files, ok := compose.TemplateFiles(id)
	if !ok {
		t.Fatalf("no template %s", id)
	}
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, data := range files {
		write(rel, data)
	}
	for rel, data := range extra {
		write(rel, []byte(data))
	}
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}}}
	compose.Derive(context.Background(), dir, wf, model.BestPracticeSettings{})
	return dir, wf
}

func dockerEnv(t *testing.T, r model.WorkflowReadiness, name string) model.EnvironmentReadiness {
	t.Helper()
	for _, e := range r.Environments {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no environment %q in %+v", name, r.Environments)
	return model.EnvironmentReadiness{}
}

func item(t *testing.T, e model.EnvironmentReadiness, id string) model.ReadinessItem {
	t.Helper()
	for _, g := range e.Groups {
		for _, it := range g.Items {
			if it.ID == id {
				return it
			}
		}
	}
	t.Fatalf("no item %q", id)
	return model.ReadinessItem{}
}

func group(t *testing.T, e model.EnvironmentReadiness, id string) model.ReadinessGroup {
	t.Helper()
	for _, g := range e.Groups {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("no group %q", id)
	return model.ReadinessGroup{}
}

func component(t *testing.T, e model.EnvironmentReadiness, node string) model.ReadinessComponent {
	t.Helper()
	for _, c := range e.Components {
		if c.NodeID == node {
			return c
		}
	}
	t.Fatalf("no component %q in %+v", node, e.Components)
	return model.ReadinessComponent{}
}

func cell(t *testing.T, c model.ReadinessComponent, id string) model.ReadinessCell {
	t.Helper()
	for _, x := range c.Cells {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("no cell %q", id)
	return model.ReadinessCell{}
}

// loadRun is a finished load test of api: api was CPU-bound, db and
// cache had room.
func loadRun(id string, at time.Time) model.LoadTestRun {
	return model.LoadTestRun{ID: id, WorkflowID: "t", Status: model.LoadPassed, StartedAt: at,
		Spec: model.LoadTestSpec{Target: model.LoadTarget{NodeID: "service:api"}},
		Resources: &model.LoadResources{Service: "api", Verdict: "api reached 99% of its 1 CPU limit at 90 req/s — CPU-bound; raise limits or add replicas",
			Services: []model.LoadServiceResources{
				{Service: "api", NodeID: "service:api", Target: true, CPULimit: 1, CPULimited: true, PeakCPUOf: 0.99, MemLimit: 256 << 20, MemLimited: true, PeakMemOf: 0.4},
				{Service: "db", NodeID: "service:db", CPULimit: 1, CPULimited: true, PeakCPUOf: 0.35},
				{Service: "cache", NodeID: "service:cache", CPULimit: 0.5, CPULimited: true, PeakCPUOf: 0.1},
			},
			Verdicts: []model.ResourceVerdict{
				{Service: "api", Kind: "cpu-bound", Bottleneck: true, Message: "api reached 99% of its 1 CPU limit at 90 req/s — CPU-bound; raise limits or add replicas"},
				{Service: "db", Kind: "ok", Message: "db at 35% of its 1 CPU limit — not the bottleneck"},
				{Service: "cache", Kind: "ok", Message: "cache at 10% of its 0.5 CPU limit — not the bottleneck"},
			}}}
}

func scenarioRun(id, scenarioID, target, verdict string, at time.Time) model.LoadTestRun {
	r := loadRun(id, at)
	r.Spec.Scenario = &model.ScenarioSpec{ID: scenarioID, Target: target, AtSec: 10, DurationSec: 20}
	r.Scenario = &model.ScenarioResult{ID: scenarioID, Target: target, TargetName: strings.TrimPrefix(target, "service:"), Verdict: verdict, Message: "Scenario " + verdict, Checks: []model.ScenarioCheck{}}
	return r
}

// evidenceSources: a load test, two scenario runs after it and (planned)
// a capacity plan sized from the load test.
func evidenceSources(now time.Time, wf *model.Workflow, planned bool) Sources {
	src := Sources{Runs: []model.LoadTestRun{
		scenarioRun("s2", model.ScenarioDependencyDown, "service:db", model.ScenarioFailed, now.Add(2*time.Minute)),
		scenarioRun("s1", model.ScenarioKillReplica, "service:api", model.ScenarioSurvived, now.Add(time.Minute)),
		loadRun("l1", now),
	}}
	if planned {
		plan := &model.CapacityPlan{Run: model.CapacityRun{ID: "l1", StartedAt: now}, TargetRPS: 300, Confidence: model.ConfidenceMedium,
			Services: []model.CapacityService{
				{NodeID: "service:api", Service: "api", Role: model.CapacityApp, Replicas: 4, CPULimit: "1", MemLimit: "256M"},
				{NodeID: "service:db", Service: "db", Role: model.CapacityDependency, Replicas: 1, ProjectedCPU: 1.2},
			},
			Clouds: []model.CloudEstimate{{Cloud: "aws", Recommended: model.NodeOption{MonthlyUSD: 120}}, {Cloud: "gcp", Recommended: model.NodeOption{MonthlyUSD: 98.5}}},
			Prices: model.PriceTableInfo{AsOf: "2026-10-01"}}
		src.Plan = func(env string) *model.CapacityPlan {
			if env == "" {
				return plan
			}
			return nil
		}
	}
	return src
}

// Each environment gets the scenarios of its own graph, not the active
// environment's: a cache only prod declares is a cache_flush target there.
func TestDockerCatalogPerEnvironment(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", map[string]string{
		"compose.prod.yaml": "services:\n  sessions:\n    image: redis:7-alpine\n    restart: unless-stopped\n",
	})
	r := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, nil)
	targets := func(e model.EnvironmentReadiness) []string {
		it := item(t, e, model.ScenarioCacheFlush)
		var out []string
		for _, c := range e.Components {
			if cell(t, c, model.CellResilience).Action != nil {
				out = append(out, c.Name)
			}
		}
		if it.Status != model.ItemTodo {
			t.Errorf("cache flush: %+v", it)
		}
		return out
	}
	if got := targets(dockerEnv(t, r, "prod")); !slices.Contains(got, "sessions") {
		t.Errorf("prod scenario targets %v", got)
	}
	if got := targets(dockerEnv(t, r, "")); slices.Contains(got, "sessions") {
		t.Errorf("base scenario targets %v", got)
	}
	if wf.Compose.Environment != "" {
		t.Errorf("active environment changed to %q", wf.Compose.Environment)
	}
}

// Before any run: rules pass, monitoring is missing, every evidence item
// and cell is untested (not failing).
func TestDockerBeforeEvidence(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", nil)
	r := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, nil)
	if r.Kind != "docker" || len(r.Environments) != 1 {
		t.Fatalf("report %+v", r)
	}
	e := r.Environments[0]
	if e.Name != "" || !e.ProductionLike {
		t.Errorf("base without environments is production: %+v", e)
	}
	for _, id := range []string{"project", "configuration", "healthchecks", "restart", "resources", "data"} {
		if it := item(t, e, id); it.Status != model.ItemPass {
			t.Errorf("%s: %s %s %v", id, it.Status, it.Message, it.Diagnostics)
		}
	}
	if it := item(t, e, "monitoring"); it.Status != model.ItemWarn || it.Action == nil || it.Action.Kind != model.ActionAddMonitoring {
		t.Errorf("monitoring: %+v", it)
	}
	if it := item(t, e, "load_test"); it.Status != model.ItemTodo || it.Action.Kind != model.ActionRunLoadTest || it.Action.Label != "Run a load test in the base" {
		t.Errorf("load test: %+v", it)
	}
	if it := item(t, e, "capacity"); it.Status != model.ItemTodo || it.Action.Kind != model.ActionRunLoadTest {
		t.Errorf("capacity: %+v", it)
	}
	res := group(t, e, model.ReadinessResilience)
	if res.Score != nil || res.Untested == 0 {
		t.Errorf("resilience: %+v", res)
	}
	if it := item(t, e, model.ScenarioKillReplica); it.Status != model.ItemTodo || it.Action.Kind != model.ActionRunScenario || it.Action.Scenario != model.ScenarioKillReplica {
		t.Errorf("kill replica: %+v", it)
	}
	if e.Status != model.ReadinessNeedsWork || e.Confidence >= 70 || e.Score < 90 {
		t.Errorf("env %s score %d confidence %d %q", e.Status, e.Score, e.Confidence, e.Summary)
	}
	names := []string{}
	for _, c := range e.Components {
		names = append(names, c.Name+":"+c.Role)
		if len(c.Cells) != 7 {
			t.Errorf("%s cells %+v", c.Name, c.Cells)
		}
	}
	if !slices.Equal(names, []string{"api:app", "cache:data", "db:data"}) {
		t.Errorf("components %v", names)
	}
	api := component(t, e, "service:api")
	if c := cell(t, api, model.CellHealth); c.Status != model.ItemPass {
		t.Errorf("api health: %+v", c)
	}
	if c := cell(t, api, model.CellResources); c.Status != model.ItemPass {
		t.Errorf("api resources: %+v", c)
	}
	if c := cell(t, api, model.CellMonitoring); c.Status != model.ItemWarn || c.Action == nil {
		t.Errorf("api monitoring: %+v", c)
	}
	for _, id := range []string{model.CellLoad, model.CellResilience, model.CellCapacity} {
		if c := cell(t, api, id); c.Status != model.ItemTodo || c.Action == nil {
			t.Errorf("api %s: %+v", id, c)
		}
	}
}

// After a load test and scenario runs the items and rows carry their
// verdicts; a capacity plan from the same run accounts for the ceiling.
func TestDockerWithEvidence(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", nil)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	before := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, nil).Environments[0]

	e := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, evidenceSources(now, wf, false), nil).Environments[0]
	// The scenario runs are not the load test: the plain run is.
	lt := item(t, e, "load_test")
	if lt.Status != model.ItemWarn || lt.Action.RunID != "l1" || !strings.Contains(lt.Message, "CPU-bound") || !strings.Contains(lt.Message, "plan capacity") || e.LoadTestRunID != "l1" {
		t.Errorf("load test: %+v", lt)
	}
	if it := item(t, e, "capacity"); it.Status != model.ItemTodo || it.Action.Kind != model.ActionPlanCapacity {
		t.Errorf("capacity: %+v", it)
	}
	if it := item(t, e, model.ScenarioKillReplica); it.Status != model.ItemPass || it.Action.RunID != "s1" || !strings.Contains(it.Message, "(api, ") {
		t.Errorf("kill replica: %+v", it)
	}
	if it := item(t, e, model.ScenarioDependencyDown); it.Status != model.ItemFail || it.Action.RunID != "s2" {
		t.Errorf("dependency down: %+v", it)
	}
	// A failed incident needs work (it does not block); the confidence rose
	// with the evidence. Open: monitoring, the CPU-bound load test and the
	// failed dependency outage.
	if e.Status != model.ReadinessNeedsWork || !strings.HasPrefix(e.Summary, "3 checks need work; ") || e.Confidence <= before.Confidence || e.Score >= before.Score {
		t.Errorf("env %s %d/%d (before %d/%d) %q", e.Status, e.Score, e.Confidence, before.Score, before.Confidence, e.Summary)
	}
	api := component(t, e, "service:api")
	if c := cell(t, api, model.CellLoad); c.Status != model.ItemWarn || !strings.Contains(c.Message, "CPU-bound") || c.Action.RunID != "l1" {
		t.Errorf("api load: %+v", c)
	}
	if c := cell(t, api, model.CellResilience); c.Status != model.ItemPass || !strings.Contains(c.Message, "survived") || c.Action.RunID != "s1" {
		t.Errorf("api resilience: %+v", c)
	}
	db := component(t, e, "service:db")
	if c := cell(t, db, model.CellLoad); c.Status != model.ItemPass || c.Message != "Peak CPU 35% of its 1 CPU limit (65% headroom)." {
		t.Errorf("db load: %+v", c)
	}
	if c := cell(t, db, model.CellResilience); c.Status != model.ItemFail || db.Status != model.ItemFail {
		t.Errorf("db resilience: %+v (%s)", c, db.Status)
	}
	cache := component(t, e, "service:cache")
	if c := cell(t, cache, model.CellResilience); c.Status != model.ItemTodo || c.Action == nil || c.Action.Kind != model.ActionRunScenario || c.Action.Target != "service:cache" {
		t.Errorf("cache resilience: %+v", c)
	}

	// The plan sized from l1 accounts for api's ceiling.
	p := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, evidenceSources(now, wf, true), nil).Environments[0]
	if it := item(t, p, "load_test"); it.Status != model.ItemInfo {
		t.Errorf("planned load test: %+v", it)
	}
	if it := item(t, p, "capacity"); it.Status != model.ItemPass || it.Message != "Planned for 300 req/s (medium confidence): api ×4; from $98.5/month on GCP (prices of 2026-10-01)." {
		t.Errorf("capacity: %q %s", it.Message, it.Status)
	}
	api = component(t, p, "service:api")
	if c := cell(t, api, model.CellLoad); c.Status != model.ItemInfo {
		t.Errorf("planned api load: %+v", c)
	}
	if c := cell(t, api, model.CellCapacity); c.Status != model.ItemPass || c.Message != "4 replicas for 300 req/s · limits 1 CPU, 256M." {
		t.Errorf("api capacity: %+v", c)
	}
	if c := cell(t, component(t, p, "service:db"), model.CellCapacity); c.Message != "Needs 1.2 CPU at 300 req/s." {
		t.Errorf("db capacity: %+v", c)
	}
	if c := cell(t, component(t, p, "service:cache"), model.CellCapacity); c.Status != model.ItemSkip {
		t.Errorf("cache capacity: %+v", c)
	}
	// A newer load test makes the plan stale.
	src := evidenceSources(now, wf, true)
	src.Runs = append([]model.LoadTestRun{loadRun("l2", now.Add(time.Hour))}, src.Runs...)
	if it := item(t, Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, src, nil).Environments[0], "capacity"); it.Status != model.ItemInfo || !strings.Contains(it.Message, "older load test") {
		t.Errorf("stale plan: %+v", it)
	}
}

// Environments are checked on their own, with their own runs; the base is
// not production-like once environments exist.
func TestDockerEnvironments(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", map[string]string{
		"compose.prod.yaml": "services:\n  worker:\n    image: busybox:1.37\n    command: [\"sleep\", \"infinity\"]\n",
	})
	if len(wf.Compose.Environments) != 1 {
		t.Fatalf("environments %+v", wf.Compose.Environments)
	}
	run := loadRun("p1", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	run.Environment = "prod"
	r := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{Runs: []model.LoadTestRun{run}}, nil)
	base, prod := dockerEnv(t, r, ""), dockerEnv(t, r, "prod")
	if base.ProductionLike || !prod.ProductionLike {
		t.Errorf("production-like base=%v prod=%v", base.ProductionLike, prod.ProductionLike)
	}
	if it := item(t, prod, "restart"); it.Status == model.ItemPass {
		t.Errorf("prod-only worker without restart policy not checked: %+v", it)
	}
	if it := item(t, base, "restart"); it.Status != model.ItemPass {
		t.Errorf("base restart: %+v", it)
	}
	if item(t, base, "load_test").Status != model.ItemTodo || item(t, prod, "load_test").Action.RunID != "p1" {
		t.Error("runs not attributed to their environment")
	}
	only := "prod"
	if got := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, &only); len(got.Environments) != 1 || got.Environments[0].Name != "prod" {
		t.Errorf("only prod: %+v", got.Environments)
	}
}

// The generated monitoring stack passes and is not a component; settings
// turn rule items off.
func TestDockerMonitoringAndSettings(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", map[string]string{
		"monitoring/compose.yaml": "services:\n  prometheus:\n    image: prom/prometheus:v3.5.0\n",
	})
	wf.Compose.Files = append(wf.Compose.Files, "monitoring/compose.yaml")
	e := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, nil).Environments[0]
	if it := item(t, e, "monitoring"); it.Status != model.ItemPass || !strings.Contains(it.Message, "monitoring/compose.yaml") {
		t.Errorf("monitoring: %+v", it)
	}
	for _, c := range e.Components {
		if c.Name == "prometheus" {
			t.Error("the monitoring stack is listed as a component")
		}
		if cell(t, c, model.CellMonitoring).Status != model.ItemPass {
			t.Errorf("%s monitoring cell %+v", c.Name, cell(t, c, model.CellMonitoring))
		}
	}
	off := model.BestPracticeSettings{DisabledRules: []string{"dk_no_limits"}}
	e = Docker(context.Background(), dir, wf, off, Sources{}, nil).Environments[0]
	if it := item(t, e, "resources"); it.Status != model.ItemSkip || it.Message != "Turned off in Settings." {
		t.Errorf("disabled: %+v", it)
	}
	if c := cell(t, component(t, e, "service:api"), model.CellResources); c.Status != model.ItemSkip {
		t.Errorf("disabled resources cell: %+v", c)
	}
}

// A service without healthcheck, restart policy or limits shows it in its
// row and the matching items, with the finding to open.
func TestDockerFindingsPerComponent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:latest\n    ports: [\"8080:80\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}}}
	e := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{}, nil).Environments[0]
	web := component(t, e, "service:web")
	for _, id := range []string{model.CellHealth, model.CellResources, model.CellRules} {
		c := cell(t, web, id)
		if c.Status == model.ItemPass || c.Action == nil || c.Action.Kind != model.ActionOpenProblem || len(c.Diagnostics) == 0 {
			t.Errorf("web %s: %+v", id, c)
		}
	}
	if !slices.ContainsFunc(cell(t, web, model.CellRules).Diagnostics, func(d model.Diagnostic) bool { return d.Code == "dk_latest_tag" }) {
		t.Errorf("rules cell: %+v", cell(t, web, model.CellRules))
	}
	if it := item(t, e, "images"); it.Status != model.ItemInfo {
		t.Errorf("images: %+v", it)
	}
	if web.Score == nil || *web.Score == 100 {
		t.Errorf("web score %v", deref(web.Score))
	}
}

func TestNum(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 3: "3", 0.03: "0.03", 0.5: "0.5", 1.5: "1.5", 98.5: "98.5", 144.39: "144", 2.04: "2"} {
		if got := num(in); got != want {
			t.Errorf("num(%v) = %q, want %q", in, got, want)
		}
	}
}

// A breaking point no container explains is a suggestion, not a pass.
func TestDockerBreakingPointWithoutSaturation(t *testing.T) {
	dir, wf := dockerTemplate(t, "docker-node-postgres-redis", nil)
	run := loadRun("l1", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	run.Resources.Verdicts[0] = model.ResourceVerdict{Service: "api", Kind: "ok", Message: "api at 68% of its 1 CPU limit — not the bottleneck"}
	run.Resources.Verdict = "No container reached its CPU or memory limit, yet latency or errors degraded"
	e := Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{Runs: []model.LoadTestRun{run}}, nil).Environments[0]
	if it := item(t, e, "load_test"); it.Status != model.ItemPass {
		t.Errorf("no breaking point: %+v", it)
	}
	run.Summary = &model.LoadSummary{BreakingPoint: new(241.7)}
	e = Docker(context.Background(), dir, wf, model.BestPracticeSettings{}, Sources{Runs: []model.LoadTestRun{run}}, nil).Environments[0]
	if it := item(t, e, "load_test"); it.Status != model.ItemInfo || !strings.Contains(it.Message, "latency or errors degraded") {
		t.Errorf("breaking point: %+v", it)
	}
}

// Every dk_* rule lands in exactly one checklist item.
func TestEveryDockerRuleHasAnItem(t *testing.T) {
	mapped := map[string]bool{}
	for _, list := range [][]Check{dockerReliability, dockerSecurity, dockerHygiene} {
		for _, c := range list {
			for _, code := range c.Codes {
				if mapped[code] {
					t.Errorf("%s in two items", code)
				}
				mapped[code] = true
			}
		}
	}
	for _, r := range compose.Rules() {
		if !mapped[r.Code] {
			t.Errorf("%s (%s) is in no readiness item", r.Code, r.Category)
		}
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
