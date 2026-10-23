package kuberules

import (
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Workload is a Deployment or StatefulSet of a rendered project as the
// production readiness report sees it.
type Workload struct {
	NodeID, Kind, Namespace, Name string
	// MinReplicas is the fewest pods it runs (the HPA's minReplicas when
	// one scales it).
	MinReplicas int
	// HPA names the HorizontalPodAutoscaler scaling it ("" = none).
	HPA string
	// Stateful: it keeps state on its own volume (claim templates, a
	// ReadWriteOnce claim or a Recreate strategy) and can't simply scale.
	Stateful bool
}

// Workloads lists the Deployments and StatefulSets of a rendered project,
// leaving out the ones vendored palette charts render (their replicas are
// the chart's values, not the workflow's design).
func Workloads(p *kube.Project, wf *model.Workflow) []Workload {
	in := newInput(p, wf)
	c := &checker{in: in, byKey: map[string][]*object{}}
	for _, o := range in.objects {
		c.byKey[o.kind+"/"+o.namespace+"/"+o.name] = append(c.byKey[o.kind+"/"+o.namespace+"/"+o.name], o)
	}
	var out []Workload
	for _, o := range in.objects {
		w := newWorkload(o)
		if w == nil || o.kind != "Deployment" && o.kind != "StatefulSet" || w.pod == nil || o.vendored {
			continue
		}
		f := Workload{NodeID: o.id, Kind: o.kind, Namespace: o.namespace, Name: o.name, MinReplicas: c.minReplicas(w), Stateful: c.stateful(w)}
		if h := c.hpaFor(w); h != nil {
			f.HPA = h.name
		}
		out = append(out, f)
	}
	return out
}

// ProductionLike reports whether an environment name is production-like
// (prod, production, staging, live… and prefixed/suffixed variants): the
// production-only rules run there.
func ProductionLike(env string) bool { return prodLike(env) }

// RuleCategory is the Settings category of a k8s_* rule ("" if unknown).
func RuleCategory(code string) string { return ruleByCode[code].Category }
