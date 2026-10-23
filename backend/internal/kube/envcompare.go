package kube

import (
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// envKey is an object's identity across environments: its kind and its
// name before Kustomize name prefixes/suffixes, hashes and namespaces
// ("Deployment/web"), prefixed with the source index ("1:Deployment/web")
// when the workflow has several sources.
func envKey(o *Object, multi bool) string {
	k := o.Kind + "/" + baseName(o)
	if multi {
		k = strconv.Itoa(o.Source) + ":" + k
	}
	return k
}

// baseName is an object's name before Kustomize name prefixes/suffixes
// and generator hashes.
func baseName(o *Object) string {
	name := o.Name
	if o.orgName != "" {
		name = o.orgName
	}
	if o.generated {
		// Generators append a content hash ("site-4h5km4h7md").
		name = generatorHash.ReplaceAllString(name, "")
	}
	return name
}

var generatorHash = regexp.MustCompile(`-[bcdfghkmntz2456789]{10}$`)

func multiSource(wf *model.Workflow) bool {
	return wf.Kubernetes != nil && len(wf.Kubernetes.Sources) > 1
}

// compareRow is one fact of a workload in one environment; detail rows
// (env vars, ConfigMap keys) are only listed when they differ.
type compareRow struct {
	key, label, value string
	detail            bool
}

// CompareEnvironments renders the base and every environment of wf on its
// own (the active one is not changed) and lays the facts that usually
// change between environments side by side per workload: namespace,
// replicas, autoscaling, images, requests/limits, PodDisruptionBudget,
// topology spread, Ingress hosts, and env vars / ConfigMap keys that
// differ.
func CompareEnvironments(ctx context.Context, dir string, wf *model.Workflow) model.EnvCompare {
	envs := []string{""}
	for _, e := range wf.Environments {
		envs = append(envs, e.Name)
	}
	out := model.EnvCompare{Groups: []model.EnvCompareGroup{}}
	for _, env := range envs {
		label := env
		if env == "" {
			label = "Base"
		}
		out.Columns = append(out.Columns, model.EnvCompareColumn{Name: env, Label: label})
	}
	type rowAcc struct {
		label  string
		detail bool
		values []string
	}
	type groupAcc struct {
		label, kind string
		rows        map[string]*rowAcc
		order       []string
	}
	groups := map[string]*groupAcc{}
	multi := multiSource(wf)
	for ci, env := range envs {
		p := Open(ctx, dir, wf, Options{Environment: env, NoSourceMap: true})
		var errs []string
		for _, r := range p.Sources {
			if r.Err != nil {
				errs = append(errs, r.Source.Path+": "+r.Err.Error())
			}
		}
		if len(errs) > 0 {
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[env] = strings.Join(errs, "; ")
		}
		for _, o := range p.Objects {
			if !isWorkload(o) {
				continue
			}
			id := envKey(o, multi)
			g := groups[id]
			if g == nil {
				g = &groupAcc{label: baseName(o), kind: o.Kind, rows: map[string]*rowAcc{}}
				groups[id] = g
			}
			for _, r := range p.workloadFacts(o) {
				acc := g.rows[r.key]
				if acc == nil {
					acc = &rowAcc{label: r.label, detail: r.detail, values: make([]string, len(envs))}
					g.rows[r.key] = acc
					g.order = append(g.order, r.key)
				}
				acc.values[ci] = r.value
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		g := groups[id]
		grp := model.EnvCompareGroup{ID: id, Label: g.label, Kind: g.kind, Rows: []model.EnvCompareRow{}}
		for _, key := range g.order {
			r := g.rows[key]
			differs := slices.ContainsFunc(r.values, func(v string) bool { return v != r.values[0] })
			if r.detail && !differs {
				continue
			}
			grp.Rows = append(grp.Rows, model.EnvCompareRow{Key: key, Label: r.label, Values: r.values, Differs: differs})
		}
		out.Groups = append(out.Groups, grp)
	}
	return out
}

// podTemplate is the pod template mapping of a workload (holding
// metadata and spec).
func podTemplate(o *Object) map[string]any {
	_, keys := podSpec(o)
	if len(keys) == 0 {
		return nil
	}
	m, _ := dig(o.Obj, keys[:len(keys)-1]...).(map[string]any)
	return m
}

// workloadFacts are the compared facts of workload o in p.
func (p *Project) workloadFacts(o *Object) []compareRow {
	spec, _ := podSpec(o)
	podLabels := strMap(dig(podTemplate(o), "metadata", "labels"))
	rows := []compareRow{{key: "namespace", label: "Namespace", value: o.Namespace}}
	if dig(o.Obj, "spec", "replicas") != nil || o.Kind == "Deployment" || o.Kind == "StatefulSet" || o.Kind == "ReplicaSet" {
		v := ""
		if n, ok := toInt(dig(o.Obj, "spec", "replicas")); ok {
			v = strconv.Itoa(n)
		}
		rows = append(rows, compareRow{key: "replicas", label: "Replicas", value: v})
	}
	scaling := ""
	if h := p.scalerOf(o); h != nil {
		scaling = hpaSummary(h)
	}
	rows = append(rows, compareRow{key: "autoscaling", label: "Autoscaling", value: scaling})
	for _, c := range list(spec["containers"]) {
		name := str(c, "name")
		rows = append(rows,
			compareRow{key: "image:" + name, label: name + " image", value: str(c, "image")},
			compareRow{key: "cpu:" + name, label: name + " CPU req / limit", value: reqLimit(c, "cpu")},
			compareRow{key: "memory:" + name, label: name + " memory req / limit", value: reqLimit(c, "memory")},
		)
	}
	pdb := ""
	for _, d := range p.Objects {
		if d.Kind == "PodDisruptionBudget" && d.Namespace == o.Namespace && matches(strMap(dig(d.Obj, "spec", "selector", "matchLabels")), podLabels) {
			if v := dig(d.Obj, "spec", "minAvailable"); v != nil {
				pdb = fmt.Sprint("minAvailable ", v)
			} else if v := dig(d.Obj, "spec", "maxUnavailable"); v != nil {
				pdb = fmt.Sprint("maxUnavailable ", v)
			}
		}
	}
	rows = append(rows, compareRow{key: "pdb", label: "PodDisruptionBudget", value: pdb})
	var spread []string
	for _, t := range list(spec["topologySpreadConstraints"]) {
		spread = append(spread, fmt.Sprintf("%s (%s, skew %v)", str(t, "topologyKey"), str(t, "whenUnsatisfiable"), dig(t, "maxSkew")))
	}
	rows = append(rows, compareRow{key: "spread", label: "Topology spread", value: strings.Join(spread, ", ")})
	rows = append(rows, compareRow{key: "hosts", label: "Ingress hosts", value: strings.Join(p.ingressHosts(o, podLabels), ", ")})
	// Env vars and the ConfigMaps the workload reads: only differences.
	configMaps := map[string]bool{}
	for _, c := range containers(spec["containers"]) {
		for _, e := range c.Env {
			v := ""
			switch {
			case e.From != nil:
				v = "from " + e.From.Kind + " " + p.refName(o.Namespace, e.From.Kind, e.From.Name)
				if e.From.Key != "" {
					v += " " + e.From.Key
				}
				if e.From.Kind == "ConfigMap" {
					configMaps[e.From.Name] = true
				}
			case e.Secret:
				v = "(secret)"
			case e.Value != nil:
				v = *e.Value
			}
			rows = append(rows, compareRow{key: "env:" + c.Name + ":" + e.Name, label: c.Name + " env " + e.Name, value: v, detail: true})
		}
		for _, ef := range c.EnvFrom {
			if ef.Kind == "ConfigMap" {
				configMaps[ef.Name] = true
			}
		}
	}
	for _, v := range list(spec["volumes"]) {
		if n := str(v, "configMap", "name"); n != "" {
			configMaps[n] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(configMaps)) {
		cm := p.objectNamed("ConfigMap", o.Namespace, name)
		if cm == nil {
			continue
		}
		data, _ := dig(cm.Obj, "data").(map[string]any)
		for _, k := range slices.Sorted(maps.Keys(data)) {
			rows = append(rows, compareRow{key: "cm:" + baseName(cm) + ":" + k, label: baseName(cm) + " · " + k, value: shortValue(fmt.Sprint(data[k])), detail: true})
		}
	}
	return rows
}

// scalerOf is the HorizontalPodAutoscaler scaling o.
func (p *Project) scalerOf(o *Object) *Object {
	for _, h := range p.Objects {
		if h.Kind == "HorizontalPodAutoscaler" && h.Namespace == o.Namespace &&
			str(h.Obj, "spec", "scaleTargetRef", "kind") == o.Kind && str(h.Obj, "spec", "scaleTargetRef", "name") == o.Name {
			return h
		}
	}
	return nil
}

// cpuMetric is the index of an HPA's CPU utilization metric (-1: none).
func cpuMetric(h *Object) int {
	for i, m := range list(dig(h.Obj, "spec", "metrics")) {
		if str(m, "type") == "Resource" && str(m, "resource", "name") == "cpu" {
			return i
		}
	}
	return -1
}

func hpaSummary(h *Object) string {
	minR := 1
	if n, ok := toInt(dig(h.Obj, "spec", "minReplicas")); ok {
		minR = n
	}
	s := fmt.Sprintf("%d–%v", minR, dig(h.Obj, "spec", "maxReplicas"))
	if i := cpuMetric(h); i >= 0 {
		if n, ok := toInt(dig(list(dig(h.Obj, "spec", "metrics"))[i], "resource", "target", "averageUtilization")); ok {
			s += fmt.Sprintf(" @ %d%% CPU", n)
		}
	}
	return s
}

func reqLimit(c any, res string) string {
	req, lim := str(c, "resources", "requests", res), str(c, "resources", "limits", res)
	if req == "" && lim == "" {
		return ""
	}
	if req == "" {
		req = "—"
	}
	if lim == "" {
		lim = "—"
	}
	return req + " / " + lim
}

// ingressHosts are the hosts of the Ingresses routing to Services that
// select the workload's pods.
func (p *Project) ingressHosts(o *Object, podLabels map[string]string) []string {
	services := map[string]bool{}
	for _, s := range p.Objects {
		if s.Kind == "Service" && s.Namespace == o.Namespace && matches(strMap(dig(s.Obj, "spec", "selector")), podLabels) {
			services[s.Name] = true
		}
	}
	var hosts []string
	for _, ing := range p.Objects {
		if ing.Kind != "Ingress" || ing.Namespace != o.Namespace {
			continue
		}
		for _, r := range ingressRoutes(ing) {
			if !services[r.Service] {
				continue
			}
			h := r.Host
			if h == "" {
				h = "*"
			}
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	slices.Sort(hosts)
	return hosts
}

// objectNamed finds an object by kind, namespace and rendered name.
func (p *Project) objectNamed(kind, ns, name string) *Object {
	for _, o := range p.Objects {
		if o.Kind == kind && o.Name == name && (o.Namespace == ns || o.Namespace == "") {
			return o
		}
	}
	return nil
}

// refName is a referenced ConfigMap/Secret's name before Kustomize hashes
// and prefixes (so a regenerated hash is not a difference).
func (p *Project) refName(ns, kind, name string) string {
	if o := p.objectNamed(kind, ns, name); o != nil {
		return baseName(o)
	}
	return name
}

// shortValue is a ConfigMap value for a matrix cell: its first line, cut
// at 40 characters, with a fingerprint of the whole value when cut.
func shortValue(v string) string {
	first, _, multi := strings.Cut(v, "\n")
	if !multi && len(first) <= 40 {
		return first
	}
	if len(first) > 40 {
		first = first[:40]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(v))
	return fmt.Sprintf("%s… (%d bytes, #%04x)", strings.TrimSpace(first), len(v), h.Sum32()&0xffff)
}
