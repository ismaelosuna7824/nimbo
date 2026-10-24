// Package kubeready builds the production readiness report of a Kubernetes
// workflow: for every environment it renders the project (without
// touching the workflow's active environment), runs the k8s_* rules and
// folds them, the autoscalers and the monitoring stack into the readiness
// categories, plus the evidence of real runs (load tests, incident
// scenarios, capacity plans) and one row per workload. Scoring is
// package readiness's.
package kubeready

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/readiness"
	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

// baseEnvironment is the environment name the rules see for the base of a
// workflow without environments: what it deploys is what production runs.
const baseEnvironment = "production"

// Report checks every environment of the Kubernetes workflow wf, whose
// files are in dir (only: just that environment). src is the evidence of
// the workflow's runs. cfg are the best-practice settings: rules turned
// off stay off; opt-in rules are all evaluated (they only add info items).
func Report(ctx context.Context, dir string, wf *model.Workflow, cfg model.BestPracticeSettings, src readiness.Sources, only *string) model.WorkflowReadiness {
	out := model.WorkflowReadiness{WorkflowID: wf.ID, Kind: string(model.WorkflowKubernetes), ActiveEnvironment: wf.ActiveEnvironment, Environments: []model.EnvironmentReadiness{}}
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
		out.Environments = append(out.Environments, Environment(Input{Env: env, Project: p, Workflow: wf, Settings: cfg, Sources: src}))
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
	Sources  readiness.Sources
}

// Environment builds one environment's checklist and workload rows.
func Environment(in Input) model.EnvironmentReadiness {
	if in.Env == "" {
		in.Project.Environment = baseEnvironment
	}
	prodLike := kuberules.ProductionLike(in.Project.Environment)
	cfg := readiness.Settings(in.Settings, kuberules.Rules())
	f := readiness.NewFindings(slices.Concat(in.Project.Diagnostics, kuberules.Check(in.Project, in.Workflow, cfg)), cfg.DisabledRules, prodLike)
	ev := in.Sources.For(in.Env, prodLike, catalog(in))
	rel := f.Items(reliability)
	perf := []model.ReadinessItem{autoscaling(f, in, prodLike), ev.LoadItem(true), ev.CapacityItem()}
	sec, hyg := f.Items(security), f.Items(hygiene)
	f.Claim(&rel[0], reliability[0])
	mon := monitoring(in.Project, prodLike)
	e := model.EnvironmentReadiness{Name: in.Env, ProductionLike: prodLike, Groups: []model.ReadinessGroup{
		readiness.Group(model.ReadinessReliability, rel),
		readiness.Group(model.ReadinessPerformance, perf),
		readiness.Group(model.ReadinessResilience, ev.ResilienceItems()),
		readiness.Group(model.ReadinessMonitoring, []model.ReadinessItem{mon}),
		readiness.Group(model.ReadinessSecurity, sec),
		readiness.Group(model.ReadinessHygiene, hyg),
	}, Components: []model.ReadinessComponent{}}
	if ev.Load != nil {
		e.LoadTestRunID = ev.Load.ID
	}
	health := readiness.Column{Codes: probeCodes, Pass: "Readiness and liveness probes set."}
	if f.AllDisabled(probeCodes) {
		health.Off = "Turned off in Settings."
	}
	resources := readiness.Column{Codes: resourceCodes, Pass: "Requests and memory limits set."}
	if f.AllDisabled(resourceCodes) {
		resources.Off = "Turned off in Settings."
	}
	for _, w := range kuberules.Workloads(in.Project, in.Workflow) {
		role := readiness.RoleApp
		if w.Stateful {
			role = readiness.RoleData
		}
		e.Components = append(e.Components, ev.Component(readiness.ComponentSpec{
			NodeID: w.NodeID, Name: w.Name, Kind: w.Kind, Role: role,
			Diagnostics: f.Node(w.NodeID), Health: health, Resources: resources,
		}, mon))
	}
	return readiness.Summarize(e)
}

// Component columns of workloads.
var (
	probeCodes    = []string{"k8s_no_readiness_probe", "k8s_no_liveness_probe", "k8s_probe_port"}
	resourceCodes = []string{"k8s_no_requests", "k8s_no_memory_limit"}
)

// catalog is the incident scenarios that apply to the rendered environment:
// scenario.Catalog on its own graph (not the active environment's).
func catalog(in Input) model.ScenarioCatalog {
	if in.Workflow.Kubernetes == nil {
		return scenario.Catalog(in.Workflow)
	}
	k := *in.Workflow.Kubernetes
	k.Graph, k.Environment = in.Project.Graph(nil), in.Env
	w := *in.Workflow
	w.Kubernetes = &k
	return scenario.Catalog(&w)
}

var reliability = []readiness.Check{
	{ID: "manifests", Title: "Manifests apply", Pass: "Every source renders and the objects follow the Kubernetes API."},
	{ID: "redundancy", Title: "Replicas and disruption budgets", Codes: []string{"k8s_single_replica", "k8s_pdb"}, ProdOnly: true,
		Pass: "Stateless workloads run 2+ replicas with a PodDisruptionBudget that still allows node drains."},
	{ID: "probes", Title: "Health probes", Codes: probeCodes,
		Pass: "Serving containers have readiness and liveness probes."},
	{ID: "resources", Title: "Requests and limits", Codes: resourceCodes,
		Pass: "Containers request cpu and memory and cap memory."},
	{ID: "rollout", Title: "Rollout strategy", Codes: []string{"k8s_rollout_strategy"}, ProdOnly: true,
		Pass: "Rollouts keep pods serving (rolling updates)."},
	{ID: "spread", Title: "Topology spread", Codes: []string{"k8s_topology_spread"}, ProdOnly: true,
		Pass: "Replicas are spread over nodes."},
	{ID: "wiring", Title: "Services and references", Codes: []string{"k8s_selector_mismatch", "k8s_service_port", "k8s_ingress_backend", "k8s_unknown_ref", "k8s_deprecated_api", "k8s_pvc_storage_class", "k8s_local_image"},
		Pass: "Services, Ingresses and references point at objects of the workflow."},
}

var security = []readiness.Check{
	{ID: "secrets", Title: "Secrets", Codes: []string{"k8s_secret_env", "k8s_secret_in_configmap"},
		Pass: "Credentials come from Secrets."},
	{ID: "privileges", Title: "Pod privileges", Codes: []string{"k8s_privileged", "k8s_host_namespaces", "k8s_host_path", "k8s_run_as_root", "k8s_hardening"},
		Pass: "Pods run unprivileged, as non-root and without host access."},
	{ID: "network_policies", Title: "Network policies", Codes: []string{"k8s_network_policy"},
		Pass: "Every workload is selected by a NetworkPolicy."},
}

var hygiene = []readiness.Check{
	{ID: "labels", Title: "Recommended labels", Codes: []string{"k8s_recommended_labels"},
		Pass: "Workloads carry the app.kubernetes.io labels."},
	{ID: "images", Title: "Image pinning", Codes: []string{"k8s_latest_tag", "k8s_image_digest"},
		Pass: "Images are pinned."},
	{ID: "namespaces", Title: "Namespaces and quotas", Codes: []string{"k8s_resource_quota", "k8s_default_namespace", "k8s_namespace_missing"},
		Pass: "Namespaces are explicit and capped by quotas."},
}

// scalingCodes are the rules the autoscaling item is made of.
var scalingCodes = []string{"k8s_hpa_no_requests"}

func init() {
	// The manifests item holds every error rule (the API server or the
	// renderer refuses them).
	for _, rl := range kuberules.Rules() {
		if rl.Severity == model.SeverityError {
			reliability[0].Codes = append(reliability[0].Codes, rl.Code)
		}
	}
}

// autoscaling: stateless Deployments of production-like environments
// should be scaled by a HorizontalPodAutoscaler whose metrics can work.
func autoscaling(f *readiness.Findings, in Input, prodLike bool) model.ReadinessItem {
	item := f.Item(readiness.Check{ID: "autoscaling", Title: "Autoscaling", Codes: scalingCodes})
	if item.Status == model.ItemFail || item.Status == model.ItemWarn {
		return item
	}
	var scaled, fixed []string
	for _, w := range kuberules.Workloads(in.Project, in.Workflow) {
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
	case len(scaled) == 0 && prodLike:
		item.Status = model.ItemWarn
		item.Message = fmt.Sprintf("No HorizontalPodAutoscaler: %s %s a fixed number of pods, so traffic spikes queue up instead of adding pods. Add one from the palette (Autoscaler).", readiness.List(fixed), readiness.Plural(len(fixed), "runs", "run"))
	case len(scaled) == 0:
		item.Status, item.Message = model.ItemSkip, "No HorizontalPodAutoscaler (required in production-like environments only)."
	case len(fixed) > 0:
		item.Status = model.ItemInfo
		item.Message = fmt.Sprintf("%s %s; %s %s fixed replicas.", readiness.List(scaled), readiness.Plural(len(scaled), "autoscales", "autoscale"), readiness.List(fixed), readiness.Plural(len(fixed), "runs", "run"))
	default:
		item.Status = model.ItemPass
		item.Message = fmt.Sprintf("%s %s with a HorizontalPodAutoscaler.", readiness.List(scaled), readiness.Plural(len(scaled), "autoscales", "autoscale"))
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
			out = append(out, fmt.Sprintf("%d %s", n, readiness.Plural(n, k, k+"s")))
		}
	}
	return out
}

func monitoring(p *kube.Project, prodLike bool) model.ReadinessItem {
	item := model.ReadinessItem{ID: "monitoring", Title: "Metrics, dashboards and alerts"}
	if found := Monitoring(p); len(found) > 0 {
		item.Status, item.Message = model.ItemPass, "Monitored: "+strings.Join(found, ", ")+"."
		return item
	}
	item.Status = model.ItemInfo
	if prodLike {
		item.Status = model.ItemWarn
	}
	item.Message = "No Prometheus metrics or alerts: saturation, restarts and error spikes reach users before you see them."
	item.Action = &model.ReadinessAction{Kind: model.ActionAddMonitoring, Label: "Add monitoring"}
	return item
}
