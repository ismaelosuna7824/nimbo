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

func env(t *testing.T, r model.KubeReadiness, name string) model.EnvironmentReadiness {
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

func audit(dir string, wf *model.Workflow, runs []model.LoadTestRun) model.KubeReadiness {
	return Report(context.Background(), dir, wf, model.BestPracticeSettings{}, runs, nil)
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
	if prod.Status != model.ReadinessNeedsWork || prod.Summary != "2 checks need work." || prod.Score >= 100 {
		t.Errorf("prod %s %d %q", prod.Status, prod.Score, prod.Summary)
	}
	// Dev: production-only checks skipped, a load test is only a suggestion there.
	if it := item(t, dev, "load_test"); it.Status != model.ItemInfo || it.Action == nil {
		t.Errorf("dev load test: %+v", it)
	}
	if dev.Status != model.ReadinessReady || dev.Score != 100 {
		t.Errorf("dev %s %d %q", dev.Status, dev.Score, dev.Summary)
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

// Monitoring plus a load test whose HPA scaled make prod ready.
func TestReportMonitoringAndScaling(t *testing.T) {
	dir, wf := fromTemplate(t, "k8s-kustomize-app", map[string]string{"monitoring/web.yaml": serviceMonitor})
	wf.Kubernetes.Sources = append(wf.Kubernetes.Sources, model.KubeSource{Kind: model.KubeManifests, Path: "monitoring"})
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	runs := []model.LoadTestRun{
		scalingRun("dev1", "dev", model.ScalingMaxed, now.Add(time.Hour)), // other environment
		scalingRun("p2", "prod", model.ScalingScaled, now),
		scalingRun("p1", "prod", model.ScalingMaxed, now.Add(-time.Hour)), // older
	}
	r := audit(dir, wf, runs)
	prod := env(t, r, "prod")
	if it := item(t, prod, "monitoring"); it.Status != model.ItemPass || !strings.Contains(it.Message, "1 ServiceMonitor") {
		t.Errorf("monitoring: %+v", it)
	}
	lt := item(t, prod, "load_test")
	if lt.Status != model.ItemPass || lt.Action == nil || lt.Action.Kind != model.ActionOpenLoadTest || lt.Action.RunID != "p2" || !strings.Contains(lt.Message, "scaled") {
		t.Errorf("load test: %+v", lt)
	}
	if prod.Status != model.ReadinessReady || prod.Score != 100 || prod.Summary != "Ready for production." {
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
	base := Report(context.Background(), dir, wf, cfg, nil, nil).Environments[0]
	if it := item(t, base, "redundancy"); it.Status != model.ItemSkip || it.Message != "Turned off in Settings." {
		t.Errorf("disabled rules: %+v", it)
	}
	// Best practices off globally still audits the rest.
	if it := item(t, base, "spread"); it.Status == model.ItemSkip {
		t.Errorf("spread skipped: %+v", it)
	}
	only := ""
	if got := Report(context.Background(), dir, wf, cfg, nil, &only); len(got.Environments) != 1 {
		t.Errorf("only base: %+v", got.Environments)
	}
}

// Every k8s_* rule lands in a checklist item, so readiness never hides a
// finding.
func TestEveryRuleHasAnItem(t *testing.T) {
	mapped := map[string]bool{}
	for _, list := range [][]check{reliability, security, hygiene, {{codes: scalingCodes}}} {
		for _, c := range list {
			for _, code := range c.codes {
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
