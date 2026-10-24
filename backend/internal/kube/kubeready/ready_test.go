package kubeready

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/readiness"
)

// fromTemplate writes a Kubernetes template into a fresh directory and
// derives the workflow (environments, chart facts) like the daemon does.
func fromTemplate(t *testing.T, id string, extra map[string]string) (string, *model.Workflow) {
	t.Helper()
	files, k, ok := kubecatalog.TemplateFiles(id)
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
	wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: k}
	kube.Derive(context.Background(), dir, wf)
	return dir, wf
}

func env(t *testing.T, r model.WorkflowReadiness, name string) model.EnvironmentReadiness {
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

func audit(dir string, wf *model.Workflow, runs []model.LoadTestRun) model.WorkflowReadiness {
	return auditWith(dir, wf, readiness.Sources{Runs: runs})
}

func auditWith(dir string, wf *model.Workflow, src readiness.Sources) model.WorkflowReadiness {
	return Report(context.Background(), dir, wf, model.BestPracticeSettings{}, src, nil)
}

func scalingRun(id, envName, kind string, at time.Time) model.LoadTestRun {
	return model.LoadTestRun{ID: id, WorkflowID: "t", Environment: envName, Status: model.LoadPassed, StartedAt: at,
		Resources: &model.LoadResources{Scaling: &model.LoadScaling{
			Verdict:   "web: " + kind + ".",
			Workloads: []model.WorkloadScaling{{NodeID: "Deployment/hello-prod/web", Workload: "web", Kind: kind, Timeline: []model.ScalingPoint{}}},
		}}}
}

const serviceMonitor = `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: web
  namespace: hello-prod
spec:
  selector:
    matchLabels: {app: web}
  endpoints:
    - port: http
`

// The kustomize template differs per environment: production rules,
// monitoring and the load test are judged for prod only.
func TestReportPerEnvironment(t *testing.T) {
	dir, wf := fromTemplate(t, "k8s-kustomize-app", nil)
	wf.ActiveEnvironment = "dev"
	r := audit(dir, wf, nil)
	if len(r.Environments) != 2 || r.ActiveEnvironment != "dev" {
		t.Fatalf("environments %+v", r.Environments)
	}
	dev, prod := env(t, r, "dev"), env(t, r, "prod")
	if dev.ProductionLike || !prod.ProductionLike {
		t.Errorf("production-like dev=%v prod=%v", dev.ProductionLike, prod.ProductionLike)
	}
	for _, id := range []string{"redundancy", "spread", "rollout"} {
		if it := item(t, dev, id); it.Status != model.ItemSkip {
			t.Errorf("dev %s: %s %s", id, it.Status, it.Message)
		}
		if it := item(t, prod, id); it.Status != model.ItemPass {
			t.Errorf("prod %s: %s %s %v", id, it.Status, it.Message, it.Diagnostics)
		}
	}
	if it := item(t, prod, "autoscaling"); it.Status != model.ItemPass || !strings.Contains(it.Message, "web") {
		t.Errorf("prod autoscaling: %+v", it)
	}
	if it := item(t, dev, "autoscaling"); it.Status != model.ItemSkip {
		t.Errorf("dev autoscaling: %+v", it)
	}
	// No monitoring: a warning with the action in prod, a hint in dev.
	mon := item(t, prod, "monitoring")
	if mon.Status != model.ItemWarn || mon.Action == nil || mon.Action.Kind != model.ActionAddMonitoring {
		t.Errorf("prod monitoring: %+v", mon)
	}
	if it := item(t, dev, "monitoring"); it.Status != model.ItemInfo {
		t.Errorf("dev monitoring: %+v", it)
	}
	lt := item(t, prod, "load_test")
	if lt.Status != model.ItemTodo || lt.Action == nil || lt.Action.Kind != model.ActionRunLoadTest || !strings.Contains(lt.Action.Label, "prod") {
		t.Errorf("prod load test: %+v", lt)
	}
	if prod.Status != model.ReadinessNeedsWork || prod.Summary != "1 check needs work; 4 not tested yet." || prod.Score >= 100 || prod.Confidence >= 100 {
		t.Errorf("prod %s %d %d %q", prod.Status, prod.Score, prod.Confidence, prod.Summary)
	}
	// Dev: production-only checks skipped; untested evidence lowers the
	// confidence but neither the score nor the status there.
	if it := item(t, dev, "load_test"); it.Status != model.ItemTodo || it.Action == nil {
		t.Errorf("dev load test: %+v", it)
	}
	if dev.Status != model.ReadinessReady || dev.Score != 100 || dev.Confidence == 100 || dev.Summary != "Ready; 4 not tested yet." {
		t.Errorf("dev %s %d %d %q", dev.Status, dev.Score, dev.Confidence, dev.Summary)
	}
	// Hygiene opt-ins are evaluated (info, never blocking).
	if it := item(t, prod, "images"); it.Status != model.ItemInfo || !slices.ContainsFunc(it.Diagnostics, func(d model.Diagnostic) bool { return d.Code == "k8s_image_digest" }) {
		t.Errorf("prod images: %+v", it)
	}
	if it := item(t, dev, "images"); slices.ContainsFunc(it.Diagnostics, func(d model.Diagnostic) bool { return d.Code == "k8s_image_digest" }) {
		t.Errorf("digest flagged in dev: %+v", it)
	}
	// The workflow's active environment is untouched.
	if wf.ActiveEnvironment != "dev" {
		t.Errorf("active environment changed to %q", wf.ActiveEnvironment)
	}
}

// Monitoring, a load test whose HPA scaled, a capacity plan sized from it
// and the applicable incidents survived make prod ready.
func TestReportMonitoringAndScaling(t *testing.T) {
	dir, wf := fromTemplate(t, "k8s-kustomize-app", map[string]string{"monitoring/web.yaml": serviceMonitor})
	wf.Kubernetes.Sources = append(wf.Kubernetes.Sources, model.KubeSource{Kind: model.KubeManifests, Path: "monitoring"})
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	runs := []model.LoadTestRun{
		scalingRun("dev1", "dev", model.ScalingMaxed, now.Add(time.Hour)), // other environment
		scalingRun("p2", "prod", model.ScalingScaled, now),
		scalingRun("p1", "prod", model.ScalingMaxed, now.Add(-time.Hour)), // older
	}
	// The scenarios that apply to prod's own graph: a replica loss and a
	// traffic spike, both survived.
	for i, id := range []string{model.ScenarioKillReplica, model.ScenarioTrafficSpike} {
		runs = append([]model.LoadTestRun{{ID: "s" + id, Environment: "prod", Status: model.LoadPassed, StartedAt: now.Add(time.Duration(i+1) * time.Minute),
			Spec:     model.LoadTestSpec{Scenario: &model.ScenarioSpec{ID: id, Target: "Deployment/hello-prod/web"}},
			Scenario: &model.ScenarioResult{ID: id, Target: "Deployment/hello-prod/web", TargetName: "web", Verdict: model.ScenarioSurvived, Message: "Survived"}}}, runs...)
	}
	plans := map[string]*model.CapacityPlan{"prod": {Environment: "prod", Run: model.CapacityRun{ID: "p2", StartedAt: now}, TargetRPS: 200, Confidence: model.ConfidenceHigh,
		Services: []model.CapacityService{{NodeID: "Deployment/hello-prod/web", Service: "web", Role: model.CapacityApp, Replicas: 3}}}}
	r := auditWith(dir, wf, readiness.Sources{Runs: runs, Plan: func(env string) *model.CapacityPlan { return plans[env] }})
	prod := env(t, r, "prod")
	if it := item(t, prod, "monitoring"); it.Status != model.ItemPass || !strings.Contains(it.Message, "1 ServiceMonitor") {
		t.Errorf("monitoring: %+v", it)
	}
	lt := item(t, prod, "load_test")
	if lt.Status != model.ItemPass || lt.Action == nil || lt.Action.Kind != model.ActionOpenLoadTest || lt.Action.RunID != "p2" || !strings.Contains(lt.Message, "scaled") {
		t.Errorf("load test: %+v", lt)
	}
	if prod.Status != model.ReadinessReady || prod.Score != 100 || prod.Confidence != 100 || prod.Summary != "Ready for production." {
		for _, g := range prod.Groups {
			for _, it := range g.Items {
				t.Logf("%s %s %s", it.ID, it.Status, it.Message)
			}
		}
		t.Errorf("prod %s %d %q", prod.Status, prod.Score, prod.Summary)
	}
	// The dev run is dev's: maxed is a warning there.
	if it := item(t, env(t, r, "dev"), "load_test"); it.Status != model.ItemWarn || it.Action.RunID != "dev1" {
		t.Errorf("dev load test: %+v", it)
	}
	// The web workload's row: measured by the run, sized by the plan.
	web := component(t, prod, "Deployment/hello-prod/web")
	if c := cell(t, web, model.CellLoad); c.Status != model.ItemPass || c.Action == nil || c.Action.RunID != "p2" {
		t.Errorf("web load: %+v", c)
	}
	if c := cell(t, web, model.CellCapacity); c.Status != model.ItemPass || !strings.HasPrefix(c.Message, "3 replicas for 200 req/s") {
		t.Errorf("web capacity: %+v", c)
	}
	if c := cell(t, web, model.CellMonitoring); c.Status != model.ItemPass {
		t.Errorf("web monitoring: %+v", c)
	}
	// Without evidence the same row is untested, not failing.
	bare := component(t, env(t, audit(dir, wf, nil), "prod"), "Deployment/hello-prod/web")
	for _, id := range []string{model.CellLoad, model.CellCapacity} {
		if c := cell(t, bare, id); c.Status != model.ItemTodo || c.Action == nil {
			t.Errorf("bare %s: %+v", id, c)
		}
	}
	if bare.Score == nil || *bare.Score != 100 || bare.Status != model.ItemTodo || len(bare.Cells) != 7 {
		t.Errorf("bare row: %+v", bare)
	}

	// Verdicts: the worst workload of the latest run counts.
	for kind, want := range map[string]model.ReadinessItemStatus{
		model.ScalingLate: model.ItemWarn, model.ScalingMaxed: model.ItemWarn, model.ScalingNoMetrics: model.ItemWarn,
		model.ScalingNoHPA: model.ItemWarn, model.ScalingTooShort: model.ItemTodo, model.ScalingNotNeeded: model.ItemInfo,
		model.ScalingScaled: model.ItemPass,
	} {
		// The worst workload of the run counts; a lone not_needed held but never exercised the HPA.
		run := scalingRun("x", "prod", model.ScalingNotNeeded, now)
		run.Resources.Scaling.Workloads = append(run.Resources.Scaling.Workloads, model.WorkloadScaling{Workload: "api", Kind: kind})
		if it := item(t, env(t, audit(dir, wf, []model.LoadTestRun{run}), "prod"), "load_test"); it.Status != want {
			t.Errorf("%s: %s, want %s", kind, it.Status, want)
		}
	}
	// A run without scaling data, a failed-threshold run, a running one.
	plain := model.LoadTestRun{ID: "old", Environment: "prod", Status: model.LoadPassed, StartedAt: now}
	if it := item(t, env(t, audit(dir, wf, []model.LoadTestRun{plain}), "prod"), "load_test"); it.Status != model.ItemTodo || it.Action.Kind != model.ActionRunLoadTest {
		t.Errorf("run without scaling: %+v", it)
	}
	failed := scalingRun("f", "prod", model.ScalingScaled, now)
	failed.Status = model.LoadFailed
	if it := item(t, env(t, audit(dir, wf, []model.LoadTestRun{failed}), "prod"), "load_test"); it.Status != model.ItemWarn || !strings.Contains(it.Message, "thresholds") {
		t.Errorf("failed thresholds: %+v", it)
	}
	running := model.LoadTestRun{ID: "r", Environment: "prod", Status: model.LoadRunning, StartedAt: now}
	if it := item(t, env(t, audit(dir, wf, []model.LoadTestRun{running}), "prod"), "load_test"); it.Status != model.ItemTodo || !strings.Contains(it.Message, "running") {
		t.Errorf("running: %+v", it)
	}
}

func TestMonitoringChart(t *testing.T) {
	p := &kube.Project{Sources: []*kube.Rendered{{Source: model.KubeSource{Kind: model.KubeHelm, Path: "charts/monitoring",
		Chart: &model.KubeChart{Name: "monitoring", Dependencies: []model.KubeChartDependency{{Name: "kube-prometheus-stack"}}}}}}}
	if got := Monitoring(p); len(got) != 1 || got[0] != "kube-prometheus-stack chart" {
		t.Errorf("wrapper chart: %v", got)
	}
	p.Sources[0].Source.Chart = &model.KubeChart{Name: "grafana"}
	if got := Monitoring(p); len(got) != 0 {
		t.Errorf("grafana alone counted as monitoring: %v", got)
	}
}

// A workflow without environments is checked as production; a broken
// manifest blocks it.
func TestReportBaseAndBlocked(t *testing.T) {
	dir, wf := fromTemplate(t, "k8s-microservices", nil)
	r := audit(dir, wf, nil)
	if len(r.Environments) != 1 {
		t.Fatalf("environments %+v", r.Environments)
	}
	base := r.Environments[0]
	if base.Name != "" || !base.ProductionLike {
		t.Errorf("base %+v", base)
	}
	if it := item(t, base, "redundancy"); it.Status != model.ItemWarn || it.Action == nil || it.Action.Kind != model.ActionOpenProblem || len(it.Diagnostics) == 0 {
		t.Errorf("base redundancy: %+v", it)
	}
	if !strings.Contains(item(t, base, "load_test").Action.Label, "the base") {
		t.Errorf("base load test action %+v", item(t, base, "load_test").Action)
	}

	broken := filepath.Join(dir, "k8s", "broken.yaml")
	if err := os.WriteFile(broken, []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: x, namespace: shop}\nspec: {replicas: two}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base = audit(dir, wf, nil).Environments[0]
	if it := item(t, base, "manifests"); it.Status != model.ItemFail || base.Status != model.ReadinessBlocked || !strings.HasPrefix(base.Summary, "Blocked: ") {
		t.Errorf("broken manifest: %s %q item %+v", base.Status, base.Summary, it)
	}
}

func TestReportSettings(t *testing.T) {
	dir, wf := fromTemplate(t, "k8s-microservices", nil)
	cfg := model.BestPracticeSettings{Disabled: true, DisabledRules: []string{"k8s_single_replica", "k8s_pdb"}}
	base := Report(context.Background(), dir, wf, cfg, readiness.Sources{}, nil).Environments[0]
	if it := item(t, base, "redundancy"); it.Status != model.ItemSkip || it.Message != "Turned off in Settings." {
		t.Errorf("disabled rules: %+v", it)
	}
	// Best practices off globally still audits the rest.
	if it := item(t, base, "spread"); it.Status == model.ItemSkip {
		t.Errorf("spread skipped: %+v", it)
	}
	only := ""
	if got := Report(context.Background(), dir, wf, cfg, readiness.Sources{}, &only); len(got.Environments) != 1 {
		t.Errorf("only base: %+v", got.Environments)
	}
}

// Every k8s_* rule lands in a checklist item, so readiness never hides a
// finding.
func TestEveryRuleHasAnItem(t *testing.T) {
	mapped := map[string]bool{}
	for _, list := range [][]readiness.Check{reliability, security, hygiene, {{Codes: scalingCodes}}} {
		for _, c := range list {
			for _, code := range c.Codes {
				if mapped[code] {
					t.Errorf("%s in two items", code)
				}
				mapped[code] = true
			}
		}
	}
	for _, r := range kuberules.Rules() {
		if !mapped[r.Code] {
			t.Errorf("%s (%s) is in no readiness item", r.Code, r.Category)
		}
	}
}
