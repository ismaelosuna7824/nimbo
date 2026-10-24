package scenario

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Parameter defaults and bounds.
const (
	DefaultDelayMs     = 300
	DefaultJitterMs    = 0
	DefaultLossPct     = 0
	DefaultSpikeFactor = 3
	// MinAtSec leaves a baseline before the injection.
	MinAtSec = 5
	// MaxDurationSec bounds how long a fault stays injected.
	MaxDurationSec = 600
)

var (
	paramDelay  = model.ScenarioParam{Name: "delayMs", Label: "Delay", Default: DefaultDelayMs, Min: 1, Max: 10_000, Unit: "ms"}
	paramJitter = model.ScenarioParam{Name: "jitterMs", Label: "Jitter", Default: DefaultJitterMs, Min: 0, Max: 5_000, Unit: "ms"}
	paramLoss   = model.ScenarioParam{Name: "lossPct", Label: "Packet loss", Default: DefaultLossPct, Min: 0, Max: 50, Unit: "%"}
	paramFactor = model.ScenarioParam{Name: "factor", Label: "Load multiplier", Default: DefaultSpikeFactor, Min: 1.5, Max: 20, Unit: "x"}
)

// scenario titles and plain explanations, in display order.
var defs = []model.ScenarioOption{
	{ID: model.ScenarioKillReplica, Title: "Kill a replica", NeedsTarget: true, DefaultDurationSec: 30,
		Description: "Crashes one replica of the service mid-test. Shows whether traffic moves to the healthy replicas and how fast the crashed one comes back."},
	{ID: model.ScenarioDependencyDown, Title: "Dependency down", NeedsTarget: true, DefaultDurationSec: 20,
		Description: "Makes a dependency unreachable for a while, then brings it back. Shows whether requests fail fast or hang, and how fast the app recovers."},
	{ID: model.ScenarioDependencyLatency, Title: "Slow dependency", NeedsTarget: true, DefaultDurationSec: 30, Params: []model.ScenarioParam{paramDelay, paramJitter, paramLoss},
		Description: "Adds network delay (and optional jitter and packet loss) to a dependency. Shows how latency propagates, retry pile-ups and timeouts."},
	{ID: model.ScenarioCacheFlush, Title: "Flush the cache", NeedsTarget: true, DefaultDurationSec: 30,
		Description: "Empties a Redis/Valkey or Memcached cache at once. Shows the latency spike and database load while the cache warms up again."},
	{ID: model.ScenarioTrafficSpike, Title: "Traffic spike", NeedsTarget: false, DefaultDurationSec: 30, Params: []model.ScenarioParam{paramFactor},
		Description: "Multiplies the load suddenly for a while. Shows errors, latency and, on Kubernetes, whether autoscaling reacts in time."},
}

// Catalog lists the scenarios that apply to the workflow in its active
// environment, from its derived graph alone (no runtime state): docker
// workflows from Compose.Graph, Kubernetes ones from Kubernetes.Graph.
// Other workflows get Kind "" and no scenarios.
func Catalog(wf *model.Workflow) model.ScenarioCatalog {
	out := model.ScenarioCatalog{Scenarios: []model.ScenarioOption{}, Unavailable: []model.ScenarioUnavailable{}}
	var targets map[string][]model.ScenarioTarget
	var reasons map[string]string
	switch {
	case wf.IsDocker() && wf.Compose != nil:
		out.Kind, out.Environment = model.WorkflowDocker, wf.Compose.Environment
		targets, reasons = composeTargets(wf.Compose.Graph)
	case wf.IsKubernetes() && wf.Kubernetes != nil:
		out.Kind, out.Environment = model.WorkflowKubernetes, wf.Kubernetes.Environment
		targets, reasons = kubeTargets(wf.Kubernetes.Graph)
	default:
		return out
	}
	for _, d := range defs {
		opt := d
		opt.Params = slices.Clone(d.Params)
		opt.Targets = targets[d.ID]
		if opt.Targets == nil {
			opt.Targets = []model.ScenarioTarget{}
		}
		if d.NeedsTarget && len(opt.Targets) == 0 {
			out.Unavailable = append(out.Unavailable, model.ScenarioUnavailable{ID: d.ID, Title: d.Title, Reason: reasons[d.ID]})
			continue
		}
		if !d.NeedsTarget && reasons[d.ID] != "" {
			out.Unavailable = append(out.Unavailable, model.ScenarioUnavailable{ID: d.ID, Title: d.Title, Reason: reasons[d.ID]})
			continue
		}
		out.Scenarios = append(out.Scenarios, opt)
	}
	return out
}

// Option returns the catalog's option for id, nil when not applicable.
func (c catalog) option(id string) *model.ScenarioOption {
	for i := range c.Scenarios {
		if c.Scenarios[i].ID == id {
			return &c.Scenarios[i]
		}
	}
	return nil
}

type catalog model.ScenarioCatalog

// Validate checks spec against the workflow's catalog and the run's
// length (profileSec): the scenario must apply, its target be listed,
// params be numbers within bounds, and the fault window fit in the run.
func Validate(wf *model.Workflow, spec *model.ScenarioSpec, profileSec int) error {
	cat := catalog(Catalog(wf))
	if cat.Kind == "" {
		return fmt.Errorf("scenarios need a docker or Kubernetes workflow")
	}
	opt := cat.option(spec.ID)
	if opt == nil {
		for _, u := range cat.Unavailable {
			if u.ID == spec.ID {
				return fmt.Errorf("scenario %s does not apply to this workflow: %s", spec.ID, u.Reason)
			}
		}
		return fmt.Errorf("unknown scenario %q (use one of %s)", spec.ID, strings.Join(IDs(), ", "))
	}
	if opt.NeedsTarget {
		ok := false
		for _, t := range opt.Targets {
			ok = ok || t.ID == spec.Target
		}
		if !ok {
			names := make([]string, 0, len(opt.Targets))
			for _, t := range opt.Targets {
				names = append(names, t.ID)
			}
			return fmt.Errorf("scenario %s cannot target %q (targets: %s)", spec.ID, spec.Target, strings.Join(names, ", "))
		}
	} else {
		spec.Target = ""
	}
	for k, v := range spec.Params {
		var p *model.ScenarioParam
		for i := range opt.Params {
			if opt.Params[i].Name == k {
				p = &opt.Params[i]
			}
		}
		if p == nil {
			return fmt.Errorf("scenario %s has no param %q", spec.ID, k)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f < p.Min || f > p.Max {
			return fmt.Errorf("scenario param %s must be a number from %g to %g", k, p.Min, p.Max)
		}
	}
	if spec.DurationSec == 0 {
		spec.DurationSec = opt.DefaultDurationSec
	}
	switch {
	case spec.AtSec < MinAtSec:
		return fmt.Errorf("scenario atSec must be at least %d so the run has a baseline", MinAtSec)
	case spec.DurationSec < 1 || spec.DurationSec > MaxDurationSec:
		return fmt.Errorf("scenario durationSec must be from 1 to %d", MaxDurationSec)
	case spec.AtSec+spec.DurationSec > profileSec:
		return fmt.Errorf("scenario window (%d s + %d s) ends after the load profile (%d s); start it earlier or lengthen the profile", spec.AtSec, spec.DurationSec, profileSec)
	}
	return nil
}

// IDs lists every scenario id.
func IDs() []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.ID
	}
	return out
}

// Param reads a numeric param, def when absent or invalid.
func Param(params map[string]string, name string, def float64) float64 {
	return paramFloat(params, name, def)
}

func paramFloat(params map[string]string, name string, def float64) float64 {
	if v, ok := params[name]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

// CacheKind classifies an image as a cache: "redis" (Redis, Valkey,
// KeyDB) or "memcached"; "" otherwise.
func CacheKind(image string) string {
	ref := strings.ToLower(image)
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	repo := ref[strings.LastIndex(ref, "/")+1:]
	switch {
	case strings.Contains(repo, "redis"), strings.Contains(repo, "valkey"), strings.Contains(repo, "keydb"):
		return "redis"
	case strings.Contains(repo, "memcached"):
		return "memcached"
	}
	return ""
}

// composeTargets lists each scenario's targets in a compose graph.
func composeTargets(g model.ComposeGraph) (map[string][]model.ScenarioTarget, map[string]string) {
	t := map[string][]model.ScenarioTarget{}
	r := map[string]string{}
	services := map[string]*model.ComposeNode{}
	var order []string
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == model.ComposeService && !n.Inactive && n.Service != nil {
			services[n.ID] = n
			order = append(order, n.ID)
		}
	}
	slices.Sort(order)
	usedBy := map[string][]string{}
	for _, e := range g.Edges {
		switch e.Kind {
		case model.ComposeEdgeDependsOn, model.ComposeEdgeLinks, model.ComposeEdgeInferred:
		default:
			continue
		}
		dep, user := services[e.Source], services[e.Target]
		if dep == nil || user == nil || dep == user || slices.Contains(usedBy[dep.ID], user.Name) {
			continue
		}
		usedBy[dep.ID] = append(usedBy[dep.ID], user.Name)
	}
	for _, id := range order {
		n := services[id]
		f := n.Service
		replicas := 1
		if f.Replicas != nil {
			replicas = *f.Replicas
		}
		restarts := f.Restart == "always" || f.Restart == "unless-stopped" || strings.HasPrefix(f.Restart, "on-failure")
		if replicas >= 2 || restarts {
			var parts []string
			if replicas >= 2 {
				parts = append(parts, fmt.Sprintf("%d replicas", replicas))
			} else {
				parts = append(parts, "1 replica")
			}
			if restarts {
				parts = append(parts, "restart: "+f.Restart)
			}
			t[model.ScenarioKillReplica] = append(t[model.ScenarioKillReplica], model.ScenarioTarget{ID: id, Name: n.Name, Detail: strings.Join(parts, ", ")})
		}
		if users := usedBy[id]; len(users) > 0 {
			detail := "used by " + strings.Join(users, ", ")
			for _, sc := range []string{model.ScenarioDependencyDown, model.ScenarioDependencyLatency} {
				t[sc] = append(t[sc], model.ScenarioTarget{ID: id, Name: n.Name, Detail: detail})
			}
		}
		image := ""
		if f.Image != nil {
			image = f.Image.Ref
		}
		if k := CacheKind(image); k != "" {
			t[model.ScenarioCacheFlush] = append(t[model.ScenarioCacheFlush], model.ScenarioTarget{ID: id, Name: n.Name, Detail: k + " cache"})
		}
	}
	r[model.ScenarioKillReplica] = "no service has 2+ replicas or a restart policy that would bring a crashed container back"
	r[model.ScenarioDependencyDown] = "no service is a dependency of another (depends_on, links or a host in its environment)"
	r[model.ScenarioDependencyLatency] = r[model.ScenarioDependencyDown]
	r[model.ScenarioCacheFlush] = "no Redis, Valkey, KeyDB or Memcached service"
	if len(order) == 0 {
		r[model.ScenarioTrafficSpike] = "the workflow has no services"
	}
	return t, r
}

// scalable are the workload kinds a scenario can act on.
func scalable(kind string) bool { return kind == "Deployment" || kind == "StatefulSet" }

// kubeTargets lists each scenario's targets in a Kubernetes graph: the
// workflow's own Deployments and StatefulSets (vendored charts included).
func kubeTargets(g model.KubeGraph) (map[string][]model.ScenarioTarget, map[string]string) {
	t := map[string][]model.ScenarioTarget{}
	r := map[string]string{}
	nodes := map[string]*model.KubeNode{}
	var order []string
	for i := range g.Nodes {
		n := &g.Nodes[i]
		nodes[n.ID] = n
		if scalable(n.Kind) && n.Workload != nil {
			order = append(order, n.ID)
		}
	}
	slices.Sort(order)
	// Service -> workloads it selects; workload -> Services it calls.
	selects := map[string][]string{}
	hpa := map[string]*model.KubeScaler{}
	for _, n := range g.Nodes {
		if n.Scaler != nil {
			hpa[n.Scaler.Target] = n.Scaler
		}
	}
	for _, e := range g.Edges {
		if e.Kind == model.KubeEdgeSelects {
			selects[e.Source] = append(selects[e.Source], e.Target)
		}
	}
	usedBy := map[string][]string{}
	for _, e := range g.Edges {
		if e.Kind != model.KubeEdgeInferred {
			continue
		}
		user := nodes[e.Source]
		if user == nil {
			continue
		}
		for _, w := range selects[e.Target] {
			if w != e.Source && !slices.Contains(usedBy[w], user.Name) {
				usedBy[w] = append(usedBy[w], user.Name)
			}
		}
	}
	for _, id := range order {
		n := nodes[id]
		replicas := 1
		if n.Workload.Replicas != nil {
			replicas = *n.Workload.Replicas
		}
		detail := fmt.Sprintf("%s, %d replica", n.Kind, replicas)
		if replicas != 1 {
			detail += "s"
		}
		if h := hpa[id]; h != nil {
			detail += fmt.Sprintf(", HPA %d-%d", h.MinReplicas, h.MaxReplicas)
		}
		if replicas >= 1 {
			t[model.ScenarioKillReplica] = append(t[model.ScenarioKillReplica], model.ScenarioTarget{ID: id, Name: n.Name, Detail: detail})
		}
		if users := usedBy[id]; len(users) > 0 {
			d := "used by " + strings.Join(users, ", ")
			for _, sc := range []string{model.ScenarioDependencyDown, model.ScenarioDependencyLatency} {
				t[sc] = append(t[sc], model.ScenarioTarget{ID: id, Name: n.Name, Detail: d})
			}
		}
		for _, c := range n.Workload.Containers {
			if k := CacheKind(c.Image); k != "" {
				t[model.ScenarioCacheFlush] = append(t[model.ScenarioCacheFlush], model.ScenarioTarget{ID: id, Name: n.Name, Detail: k + " cache"})
				break
			}
		}
	}
	r[model.ScenarioKillReplica] = "no Deployment or StatefulSet with replicas"
	r[model.ScenarioDependencyDown] = "no Deployment or StatefulSet is called by another workload (through a Service named in its environment)"
	r[model.ScenarioDependencyLatency] = r[model.ScenarioDependencyDown]
	r[model.ScenarioCacheFlush] = "no Redis, Valkey, KeyDB or Memcached workload"
	if len(order) == 0 {
		r[model.ScenarioTrafficSpike] = "the workflow has no Deployments or StatefulSets"
	}
	return t, r
}
