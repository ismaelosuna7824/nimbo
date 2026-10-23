// Package kubeready builds the production readiness report of a Kubernetes
// workflow: for every environment it renders the project (without
// touching the workflow's active environment), runs the k8s_* rules and
// folds them, the autoscalers, the latest load test of the environment and
// the monitoring stack into a checklist with a verdict.
package kubeready

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// baseEnvironment is the environment name the rules see for the base of a
// workflow without environments: what it deploys is what production runs.
const baseEnvironment = "production"

// Report checks every environment of the Kubernetes workflow wf, whose
// files are in dir (only: just that environment). runs are the workflow's
// load test runs, newest first. cfg are the best-practice settings: rules
// turned off stay off; opt-in rules are all evaluated (they only add info
// items).
func Report(ctx context.Context, dir string, wf *model.Workflow, cfg model.BestPracticeSettings, runs []model.LoadTestRun, only *string) model.KubeReadiness {
	out := model.KubeReadiness{WorkflowID: wf.ID, ActiveEnvironment: wf.ActiveEnvironment, Environments: []model.EnvironmentReadiness{}}
	envs := []string{}
	for _, e := range wf.Environments {
		envs = append(envs, e.Name)
	}
	if len(envs) == 0 {
		envs = []string{""}
	}
	for _, env := range envs {
		if only != nil && *only != env {
			continue
		}
		p := kube.Open(ctx, dir, wf, kube.Options{Environment: env})
		out.Environments = append(out.Environments, Environment(Input{Env: env, Project: p, Workflow: wf, Settings: cfg, Runs: runs}))
	}
	return out
}

// Input is one environment to check.
type Input struct {
	// Env is the rendered environment ("" = the base of a workflow
	// without environments, checked as production).
	Env      string
	Project  *kube.Project
	Workflow *model.Workflow
	Settings model.BestPracticeSettings
	// Runs are the workflow's load test runs, newest first.
	Runs []model.LoadTestRun
}

// Settings are the rule settings of the report: every opt-in rule is on
// (they are info-only), rules turned off in Settings stay off.
func Settings(cfg model.BestPracticeSettings) model.BestPracticeSettings {
	out := model.BestPracticeSettings{DisabledRules: slices.Clone(cfg.DisabledRules)}
	for _, r := range kuberules.Rules() {
		if r.DefaultOff {
			out.EnabledRules = append(out.EnabledRules, r.Code)
		}
	}
	return out
}

// report is the state of one environment's checklist.
type report struct {
	in       Input
	prodLike bool
	label    string // environment name for messages
	cfg      model.BestPracticeSettings
	byCode   map[string][]model.Diagnostic
	used     map[string]bool
}

// Environment builds one environment's checklist.
func Environment(in Input) model.EnvironmentReadiness {
	r := &report{in: in, cfg: Settings(in.Settings), byCode: map[string][]model.Diagnostic{}, used: map[string]bool{}, label: in.Env}
	if in.Env == "" {
		in.Project.Environment = baseEnvironment
		r.label = "the base"
	}
	r.prodLike = kuberules.ProductionLike(in.Project.Environment)
	for _, d := range slices.Concat(in.Project.Diagnostics, kuberules.Check(in.Project, in.Workflow, r.cfg)) {
		r.byCode[d.Code] = append(r.byCode[d.Code], d)
	}
	groups := []model.ReadinessGroup{
		{ID: "reliability", Title: "Reliability", Items: r.all(reliability)},
		{ID: "scaling", Title: "Scaling", Items: []model.ReadinessItem{r.autoscaling(), r.loadTest()}},
		{ID: "monitoring", Title: "Monitoring", Items: []model.ReadinessItem{r.monitoring()}},
		{ID: "security", Title: "Security", Items: r.all(security)},
		{ID: "hygiene", Title: "Hygiene", Items: r.all(hygiene)},
	}
	// Findings no item claims (codes of other tools) count as manifest
	// problems so nothing is silently dropped.
	var rest []model.Diagnostic
	for code, ds := range r.byCode {
		if !r.used[code] {
			rest = append(rest, ds...)
		}
	}
	if len(rest) > 0 {
		valid := &groups[0].Items[0]
		valid.Diagnostics = append(valid.Diagnostics, rest...)
		sortDiags(valid.Diagnostics)
		*valid = r.finish(*valid, reliability[0])
	}
	return summarize(model.EnvironmentReadiness{Name: in.Env, ProductionLike: r.prodLike, Groups: groups})
}

// check is a checklist item made of k8s_* rules.
type check struct {
	id, title string
	codes     []string
	// prodOnly: the rules only run in production-like environments.
	prodOnly bool
	// pass is the message when nothing is found.
	pass string
}

var reliability = []check{
	{id: "manifests", title: "Manifests apply", pass: "Every source renders and the objects follow the Kubernetes API."},
	{id: "redundancy", title: "Replicas and disruption budgets", codes: []string{"k8s_single_replica", "k8s_pdb"}, prodOnly: true,
		pass: "Stateless workloads run 2+ replicas with a PodDisruptionBudget that still allows node drains."},
	{id: "probes", title: "Health probes", codes: []string{"k8s_no_readiness_probe", "k8s_no_liveness_probe", "k8s_probe_port"},
		pass: "Serving containers have readiness and liveness probes."},
	{id: "resources", title: "Requests and limits", codes: []string{"k8s_no_requests", "k8s_no_memory_limit"},
		pass: "Containers request cpu and memory and cap memory."},
	{id: "rollout", title: "Rollout strategy", codes: []string{"k8s_rollout_strategy"}, prodOnly: true,
		pass: "Rollouts keep pods serving (rolling updates)."},
	{id: "spread", title: "Topology spread", codes: []string{"k8s_topology_spread"}, prodOnly: true,
		pass: "Replicas are spread over nodes."},
	{id: "wiring", title: "Services and references", codes: []string{"k8s_selector_mismatch", "k8s_service_port", "k8s_ingress_backend", "k8s_unknown_ref", "k8s_deprecated_api", "k8s_pvc_storage_class", "k8s_local_image"},
		pass: "Services, Ingresses and references point at objects of the workflow."},
}

var security = []check{
	{id: "secrets", title: "Secrets", codes: []string{"k8s_secret_env", "k8s_secret_in_configmap"},
		pass: "Credentials come from Secrets."},
	{id: "privileges", title: "Pod privileges", codes: []string{"k8s_privileged", "k8s_host_namespaces", "k8s_host_path", "k8s_run_as_root", "k8s_hardening"},
		pass: "Pods run unprivileged, as non-root and without host access."},
	{id: "network_policies", title: "Network policies", codes: []string{"k8s_network_policy"},
		pass: "Every workload is selected by a NetworkPolicy."},
}

var hygiene = []check{
	{id: "labels", title: "Recommended labels", codes: []string{"k8s_recommended_labels"},
		pass: "Workloads carry the app.kubernetes.io labels."},
	{id: "images", title: "Image pinning", codes: []string{"k8s_latest_tag", "k8s_image_digest"},
		pass: "Images are pinned."},
	{id: "namespaces", title: "Namespaces and quotas", codes: []string{"k8s_resource_quota", "k8s_default_namespace", "k8s_namespace_missing"},
		pass: "Namespaces are explicit and capped by quotas."},
}

// scalingCodes are the rules the autoscaling item is made of.
var scalingCodes = []string{"k8s_hpa_no_requests"}

func init() {
	// The manifests item holds every error rule (the API server or the
	// renderer refuses them).
	for _, rl := range kuberules.Rules() {
		if rl.Severity == model.SeverityError {
			reliability[0].codes = append(reliability[0].codes, rl.Code)
		}
	}
}

func (r *report) all(checks []check) []model.ReadinessItem {
	out := make([]model.ReadinessItem, 0, len(checks))
	for _, c := range checks {
		out = append(out, r.rules(c))
	}
	return out
}

// rules builds an item from the findings of its rules.
func (r *report) rules(c check) model.ReadinessItem {
	item := model.ReadinessItem{ID: c.id, Title: c.title, Codes: c.codes}
	for _, code := range c.codes {
		r.used[code] = true
		item.Diagnostics = append(item.Diagnostics, r.byCode[code]...)
	}
	sortDiags(item.Diagnostics)
	return r.finish(item, c)
}

// finish sets an item's status, message and action from its diagnostics.
func (r *report) finish(item model.ReadinessItem, c check) model.ReadinessItem {
	switch worst(item.Diagnostics) {
	case model.SeverityError:
		item.Status = model.ItemFail
	case model.SeverityWarning:
		item.Status = model.ItemWarn
	case model.SeverityInfo:
		item.Status = model.ItemInfo
	default:
		item.Action = nil
		switch {
		case len(c.codes) > 0 && r.allDisabled(c.codes):
			item.Status, item.Message = model.ItemSkip, "Turned off in Settings."
		case c.prodOnly && !r.prodLike:
			item.Status, item.Message = model.ItemSkip, "Checked in production-like environments only."
		default:
			item.Status, item.Message = model.ItemPass, c.pass
		}
		return item
	}
	item.Message = item.Diagnostics[0].Message
	if n := len(item.Diagnostics) - 1; n > 0 {
		item.Message += fmt.Sprintf(" (+%d more)", n)
	}
	item.Action = &model.ReadinessAction{Kind: model.ActionOpenProblem, Label: "Open problem"}
	return item
}

func (r *report) allDisabled(codes []string) bool {
	for _, c := range codes {
		if !slices.Contains(r.cfg.DisabledRules, c) {
			return false
		}
	}
	return true
}

var severityRank = map[model.Severity]int{model.SeverityError: 0, model.SeverityWarning: 1, model.SeverityInfo: 2}

func worst(ds []model.Diagnostic) model.Severity {
	if len(ds) == 0 {
		return ""
	}
	return ds[0].Severity // sorted worst first
}

func sortDiags(ds []model.Diagnostic) {
	slices.SortStableFunc(ds, func(a, b model.Diagnostic) int {
		return cmp.Or(cmp.Compare(severityRank[a.Severity], severityRank[b.Severity]), cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
}

// autoscaling: stateless Deployments of production-like environments
// should be scaled by a HorizontalPodAutoscaler whose metrics can work.
func (r *report) autoscaling() model.ReadinessItem {
	item := r.rules(check{id: "autoscaling", title: "Autoscaling", codes: scalingCodes})
	if item.Status == model.ItemFail || item.Status == model.ItemWarn {
		return item
	}
	var scaled, fixed []string
	for _, w := range kuberules.Workloads(r.in.Project, r.in.Workflow) {
		if w.Kind != "Deployment" || w.Stateful {
			continue
		}
		if w.HPA != "" {
			scaled = append(scaled, w.Name)
		} else {
			fixed = append(fixed, w.Name)
		}
	}
	item.Action, item.Diagnostics = nil, nil
	switch {
	case len(scaled) == 0 && len(fixed) == 0:
		item.Status, item.Message = model.ItemSkip, "No stateless Deployments to autoscale."
	case len(scaled) == 0 && r.prodLike:
		item.Status = model.ItemWarn
		item.Message = fmt.Sprintf("No HorizontalPodAutoscaler: %s %s a fixed number of pods, so traffic spikes queue up instead of adding pods. Add one from the palette (Autoscaler).", list(fixed), plural(len(fixed), "runs", "run"))
	case len(scaled) == 0:
		item.Status, item.Message = model.ItemSkip, "No HorizontalPodAutoscaler (required in production-like environments only)."
	case len(fixed) > 0:
		item.Status = model.ItemInfo
		item.Message = fmt.Sprintf("%s %s; %s %s fixed replicas.", list(scaled), plural(len(scaled), "autoscales", "autoscale"), list(fixed), plural(len(fixed), "runs", "run"))
	default:
		item.Status = model.ItemPass
		item.Message = fmt.Sprintf("%s %s with a HorizontalPodAutoscaler.", list(scaled), plural(len(scaled), "autoscales", "autoscale"))
	}
	return item
}

// scalingRank orders scaling verdicts worst first.
var scalingRank = map[string]int{
	model.ScalingMaxed: 0, model.ScalingLate: 1, model.ScalingNoMetrics: 2, model.ScalingNoHPA: 3,
	model.ScalingTooShort: 4, model.ScalingScaled: 5, model.ScalingNotNeeded: 6,
}

// loadTest: the latest load test of the environment and its autoscaling
// verdict (LoadTestRun.Environment, LoadResources.Scaling).
func (r *report) loadTest() model.ReadinessItem {
	item := model.ReadinessItem{ID: "load_test", Title: "Load tested"}
	run := &model.ReadinessAction{Kind: model.ActionRunLoadTest, Label: "Run a load test in " + r.envName()}
	var last *model.LoadTestRun
	running := false
	for i := range r.in.Runs {
		lt := &r.in.Runs[i]
		if lt.Environment != r.in.Env || lt.Status == model.LoadErrored {
			continue
		}
		if lt.Status == model.LoadRunning {
			running = true
			continue
		}
		last = lt
		break
	}
	if last == nil {
		// Load tests gate production-like environments; elsewhere one is a suggestion.
		item.Status, item.Action = model.ItemTodo, run
		if !r.prodLike {
			item.Status = model.ItemInfo
		}
		item.Message = fmt.Sprintf("Not tested yet: run a load test in %s to see how it scales under traffic.", r.label)
		if running {
			item.Status = model.ItemTodo
			item.Message = fmt.Sprintf("A load test is running in %s.", r.label)
		}
		return item
	}
	when := last.StartedAt.UTC().Format("Jan 2 15:04 UTC")
	item.Action = &model.ReadinessAction{Kind: model.ActionOpenLoadTest, Label: "Open load test", RunID: last.ID}
	var sc *model.LoadScaling
	if last.Resources != nil {
		sc = last.Resources.Scaling
	}
	if sc == nil || len(sc.Workloads) == 0 {
		item.Status, item.Action = model.ItemTodo, run
		item.Message = fmt.Sprintf("The last load test (%s) has no autoscaling data: run one again in %s.", when, r.label)
		return item
	}
	kind := sc.Workloads[0].Kind
	for _, w := range sc.Workloads[1:] {
		if rk, ok := scalingRank[w.Kind]; ok && rk < scalingRank[kind] {
			kind = w.Kind
		}
	}
	item.Message = fmt.Sprintf("%s (load test of %s)", strings.TrimSuffix(sc.Verdict, "."), when)
	switch kind {
	case model.ScalingMaxed, model.ScalingLate, model.ScalingNoMetrics:
		item.Status = model.ItemWarn
	case model.ScalingNoHPA:
		item.Status = model.ItemInfo
		if r.prodLike {
			item.Status = model.ItemWarn
		}
	case model.ScalingTooShort:
		item.Status = model.ItemTodo
		item.Action = &model.ReadinessAction{Kind: model.ActionRunLoadTest, Label: "Run a longer load test in " + r.envName()}
	default:
		item.Status = model.ItemPass
		if kind == model.ScalingNotNeeded {
			item.Status = model.ItemInfo // held, but the HPA was never exercised
		}
		if last.Status == model.LoadFailed {
			item.Status = model.ItemWarn
			item.Message += "; it failed its thresholds"
		}
	}
	return item
}

// Monitoring returns what monitors the rendered environment: the
// kube-prometheus-stack chart and ServiceMonitors / PrometheusRules
// (monitoring.coreos.com). Empty means no monitoring.
func Monitoring(p *kube.Project) []string {
	var out []string
	for _, r := range p.Sources {
		if c := r.Source.Chart; c != nil && r.Source.Kind == model.KubeHelm {
			if c.Name == "kube-prometheus-stack" || slices.ContainsFunc(c.Dependencies, func(d model.KubeChartDependency) bool { return d.Name == "kube-prometheus-stack" }) {
				out = append(out, "kube-prometheus-stack chart")
				break
			}
		}
	}
	counts := map[string]int{}
	for _, o := range p.Objects {
		if strings.HasPrefix(o.APIVersion, "monitoring.coreos.com/") && (o.Kind == "ServiceMonitor" || o.Kind == "PrometheusRule") {
			counts[o.Kind]++
		}
	}
	for _, k := range []string{"ServiceMonitor", "PrometheusRule"} {
		if n := counts[k]; n > 0 {
			out = append(out, fmt.Sprintf("%d %s", n, plural(n, k, k+"s")))
		}
	}
	return out
}

func (r *report) monitoring() model.ReadinessItem {
	item := model.ReadinessItem{ID: "monitoring", Title: "Metrics, dashboards and alerts"}
	if found := Monitoring(r.in.Project); len(found) > 0 {
		item.Status, item.Message = model.ItemPass, "Monitored: "+strings.Join(found, ", ")+"."
		return item
	}
	item.Status = model.ItemInfo
	if r.prodLike {
		item.Status = model.ItemWarn
	}
	item.Message = "No Prometheus metrics or alerts: saturation, restarts and error spikes reach users before you see them."
	item.Action = &model.ReadinessAction{Kind: model.ActionAddMonitoring, Label: "Add monitoring"}
	return item
}

func (r *report) envName() string {
	if r.in.Env == "" {
		return "the base"
	}
	return r.in.Env
}

// summarize sets an environment's score, status and summary.
func summarize(e model.EnvironmentReadiness) model.EnvironmentReadiness {
	var total, got float64
	var fail *model.ReadinessItem
	open := 0
	for gi := range e.Groups {
		for ii := range e.Groups[gi].Items {
			it := &e.Groups[gi].Items[ii]
			switch it.Status {
			case model.ItemSkip:
				continue
			case model.ItemPass, model.ItemInfo:
				got++
			case model.ItemWarn, model.ItemTodo:
				got += 0.5
				open++
			case model.ItemFail:
				if fail == nil {
					fail = it
				}
				open++
			}
			total++
		}
	}
	e.Score = 100
	if total > 0 {
		e.Score = int(math.Round(100 * got / total))
	}
	switch {
	case fail != nil:
		e.Status, e.Summary = model.ReadinessBlocked, "Blocked: "+fail.Message
	case open > 0:
		e.Status, e.Summary = model.ReadinessNeedsWork, fmt.Sprintf("%d %s.", open, plural(open, "check needs work", "checks need work"))
	case e.ProductionLike:
		e.Status, e.Summary = model.ReadinessReady, "Ready for production."
	default:
		e.Status, e.Summary = model.ReadinessReady, "Ready; production-only checks run in production-like environments."
	}
	return e
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// list joins names: "a", "a and b", "a, b and c" (at most 4, then "+N").
func list(names []string) string {
	if len(names) > 4 {
		names = append(slices.Clone(names[:3]), fmt.Sprintf("%d more", len(names)-3))
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
