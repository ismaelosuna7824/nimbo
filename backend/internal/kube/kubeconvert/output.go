package kubeconvert

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// File layout of the converted workflow.
const (
	manifestsDir = "k8s"
	kustomizeDir = "k8s"
	chartDir     = "chart"
)

func (c *converter) write() (map[string][]byte, error) {
	switch c.opts.Format {
	case model.ConvertKustomize:
		return c.writeKustomize(), nil
	case model.ConvertHelm:
		return c.writeHelm(), nil
	}
	return c.writeManifests(), nil
}

func (c *converter) obj(kind, name, file, svc string) {
	c.report.Objects = append(c.report.Objects, model.KubeConvertObj{Kind: kind, Name: name, File: file, Service: svc})
}

func (c *converter) ingressUnits() []*unit {
	var out []*unit
	for _, u := range c.units {
		if u.ingress != nil {
			out = append(out, u)
		}
	}
	return out
}

// unitDocs are the objects of one service except ConfigMaps/Secrets.
func (b *builder) unitDocs(u *unit) []*yaml.Node {
	var docs []*yaml.Node
	for _, cl := range u.pvcs {
		docs = append(docs, b.pvc(u, cl))
	}
	docs = append(docs, b.workload(u))
	if len(u.ports) > 0 {
		docs = append(docs, b.service(u))
	}
	if u.kind == kindStatefulSet {
		docs = append(docs, b.headless(u))
	}
	return docs
}

func docKind(d *yaml.Node) (string, string) {
	return get(d, "kind").Value, get(get(d, "metadata"), "name").Value
}

// ---------------------------------------------------------------------------
// Plain manifests

func (c *converter) writeManifests() map[string][]byte {
	b := &builder{c: c, v: literal{}, format: "manifests"}
	files := map[string][]byte{}
	nsFile := manifestsDir + "/namespace.yaml"
	files[nsFile] = encode(b.namespace())
	c.obj("Namespace", c.opts.Namespace, nsFile, "")
	written := map[string]bool{}
	for _, u := range c.units {
		file := manifestsDir + "/" + u.k8s + ".yaml"
		var docs []*yaml.Node
		for _, cfg := range u.cfgs {
			if written[cfg.kind+"/"+cfg.name] {
				continue
			}
			written[cfg.kind+"/"+cfg.name] = true
			docs = append(docs, b.configObject(cfg, u))
		}
		docs = append(docs, b.unitDocs(u)...)
		for _, d := range docs {
			k, n := docKind(d)
			c.obj(k, n, file, u.name)
		}
		files[file] = encode(docs...)
	}
	if in := c.ingressUnits(); len(in) > 0 {
		file := manifestsDir + "/ingress.yaml"
		files[file] = encode(b.ingress(in))
		c.obj("Ingress", c.opts.Name, file, "")
	}
	c.report.Sources = append(c.report.Sources, model.KubeSource{Kind: model.KubeManifests, Path: manifestsDir, Namespace: c.opts.Namespace})
	return files
}

// ---------------------------------------------------------------------------
// Kustomize: base + overlays/dev + overlays/prod

func (c *converter) writeKustomize() map[string][]byte {
	b := &builder{c: c, v: literal{}, format: "kustomize"}
	base := kustomizeDir + "/base"
	files := map[string][]byte{}
	resources := []string{"namespace.yaml"}
	files[base+"/namespace.yaml"] = encode(b.namespace())
	c.obj("Namespace", c.opts.Namespace, base+"/namespace.yaml", "")
	var cmGen, secGen []*yaml.Node
	written := map[string]bool{}
	for _, u := range c.units {
		name := u.k8s + ".yaml"
		docs := b.unitDocs(u)
		for _, d := range docs {
			k, n := docKind(d)
			c.obj(k, n, base+"/"+name, u.name)
		}
		files[base+"/"+name] = encode(docs...)
		resources = append(resources, name)
		for _, cfg := range u.cfgs {
			if written[cfg.kind+"/"+cfg.name] {
				continue
			}
			written[cfg.kind+"/"+cfg.name] = true
			gen := c.generator(cfg, base, files)
			if cfg.kind == "Secret" {
				secGen = append(secGen, gen)
			} else {
				cmGen = append(cmGen, gen)
			}
			c.obj(cfg.kind, cfg.name, base+"/kustomization.yaml", u.name)
		}
	}
	if in := c.ingressUnits(); len(in) > 0 {
		files[base+"/ingress.yaml"] = encode(b.ingress(in))
		resources = append(resources, "ingress.yaml")
		c.obj("Ingress", c.opts.Name, base+"/ingress.yaml", "")
	}
	k := mapping(
		"apiVersion", "kustomize.config.k8s.io/v1beta1",
		"kind", "Kustomization",
		"namespace", c.opts.Namespace,
		"labels", seq(mapping("pairs", mapping("app.kubernetes.io/managed-by", "kustomize"))),
		"resources", strList(resources),
	)
	if len(cmGen) > 0 {
		set(k, "configMapGenerator", seq(cmGen...))
	}
	if len(secGen) > 0 {
		set(k, "secretGenerator", seq(secGen...))
	}
	overlays := "overlays/dev and overlays/prod"
	if len(c.envs) > 0 {
		names := make([]string, len(c.envs))
		for i, e := range c.envs {
			names[i] = "overlays/" + e.name
		}
		overlays = strings.Join(names, ", ") + " (the docker workflow's environments)"
	}
	files[base+"/kustomization.yaml"] = encode(comment(k,
		"Base of "+c.opts.Name+", converted from its compose project. Generated ConfigMaps/Secrets get a",
		"content hash suffix, so pods roll when their environment or files change.",
		"Environments: "+overlays+"."))
	c.report.Sources = append(c.report.Sources, model.KubeSource{Kind: model.KubeKustomize, Path: base, Namespace: c.opts.Namespace})
	if len(c.envs) > 0 {
		c.writeKustomizeEnvironments(files)
		return files
	}

	// dev: the base as converted.
	dev := mapping(
		"apiVersion", "kustomize.config.k8s.io/v1beta1",
		"kind", "Kustomization",
		"namespace", c.opts.Namespace,
		"resources", strList([]string{"../../base"}),
		"labels", seq(mapping("pairs", mapping("app.kubernetes.io/environment", "dev"))),
	)
	files[kustomizeDir+"/overlays/dev/kustomization.yaml"] = encode(comment(dev, "Development: one replica of everything, like docker compose."))

	// prod: stateless services scale out behind a PDB, autoscaled by an
	// HPA (which then owns spec.replicas) unless they are data services;
	// the Ingress uses the "nginx" class.
	pb := &builder{c: c, v: literal{}, format: "kustomize"}
	prodRes := []string{"../../base"}
	var pdbs, hpas []*yaml.Node
	replicas := seq()
	patches := seq()
	for _, u := range c.units {
		if !u.stateless() {
			continue
		}
		if u.autoscale {
			hpas = append(hpas, pb.hpa(u))
			c.obj("HorizontalPodAutoscaler", u.k8s, kustomizeDir+"/overlays/prod/hpa.yaml", u.name)
			patches.Content = append(patches.Content, mapping(
				"target", mapping("kind", "Deployment", "name", u.k8s),
				"patch", textNode("- op: remove\n  path: /spec/replicas\n"),
			))
		} else {
			replicas.Content = append(replicas.Content, mapping("name", u.k8s, "count", u.prodReplicas()))
		}
		pdbs = append(pdbs, pb.pdb(u))
		c.obj("PodDisruptionBudget", u.k8s, kustomizeDir+"/overlays/prod/pdb.yaml", u.name)
	}
	if len(pdbs) > 0 {
		files[kustomizeDir+"/overlays/prod/pdb.yaml"] = encode(pdbs...)
		prodRes = append(prodRes, "pdb.yaml")
	}
	if len(hpas) > 0 {
		files[kustomizeDir+"/overlays/prod/hpa.yaml"] = encode(hpas...)
		prodRes = append(prodRes, "hpa.yaml")
	}
	prod := mapping(
		"apiVersion", "kustomize.config.k8s.io/v1beta1",
		"kind", "Kustomization",
		"namespace", c.opts.Namespace,
		"resources", strList(prodRes),
		"labels", seq(mapping("pairs", mapping("app.kubernetes.io/environment", "prod", "app.kubernetes.io/managed-by", "kustomize"))),
	)
	if len(replicas.Content) > 0 {
		set(prod, "replicas", replicas)
	}
	if len(c.ingressUnits()) > 0 {
		patches.Content = append(patches.Content, mapping(
			"target", mapping("kind", "Ingress", "name", c.opts.Name),
			"patch", textNode("- op: replace\n  path: /spec/ingressClassName\n  value: "+prodClass+"\n"),
		))
	}
	if len(patches.Content) > 0 {
		set(prod, "patches", patches)
	}
	files[kustomizeDir+"/overlays/prod/kustomization.yaml"] = encode(comment(prod,
		"Production: stateless services run 2+ replicas with a PodDisruptionBudget; those that aren't data",
		"services are autoscaled by a HorizontalPodAutoscaler (hpa.yaml), so their Deployments drop",
		"spec.replicas (the HPA owns it). Data services keep one replica (a second one would be a separate,",
		"empty database). Replace the dev credentials in base/env/*.secret.env (or use an external secret",
		"store) and the .localhost hosts."))

	c.report.Environments = []string{"dev", "prod"}
	return files
}

// generator writes a ConfigMap/Secret generator and its files.
func (c *converter) generator(cfg *config, base string, files map[string][]byte) *yaml.Node {
	gen := mapping("name", cfg.name)
	if cfg.env {
		var lines []string
		var literals []string
		for _, e := range cfg.entries {
			v := string(e.value)
			if strings.ContainsAny(v, "\n\r") {
				literals = append(literals, e.key+"="+v)
				continue
			}
			lines = append(lines, e.key+"="+v)
		}
		ext := ".env"
		if cfg.kind == "Secret" {
			ext = ".secret.env"
		}
		if len(lines) > 0 {
			rel := "env/" + strings.TrimSuffix(strings.TrimSuffix(cfg.name, "-env"), "-secret") + ext
			files[base+"/"+rel] = []byte(strings.Join(lines, "\n") + "\n")
			set(gen, "envs", strList([]string{rel}))
		}
		if len(literals) > 0 {
			set(gen, "literals", strList(literals))
		}
	} else {
		var list []string
		for _, e := range cfg.entries {
			rel := "files/" + e.path
			files[base+"/"+rel] = e.value
			list = append(list, e.key+"="+rel)
		}
		set(gen, "files", strList(list))
	}
	if cfg.kind == "Secret" {
		set(gen, "type", "Opaque")
	}
	return gen
}

// ---------------------------------------------------------------------------
// Helm chart with values-dev.yaml / values-prod.yaml

func (c *converter) writeHelm() map[string][]byte {
	h := &helmValuer{values: mapping()}
	b := &builder{c: c, v: h, format: "helm"}
	files := map[string][]byte{}
	tpl := func(file string, d *yaml.Node, cond string) {
		k, n := docKind(d)
		path := chartDir + "/templates/" + file
		out := h.expand(encodeOne(d))
		if cond != "" {
			out = append(append([]byte("{{- if "+cond+" }}\n"), out...), []byte("{{- end }}\n")...)
		}
		files[path] = out
		c.obj(k, n, path, "")
	}
	written := map[string]bool{}
	for _, u := range c.units {
		start := len(c.report.Objects)
		for _, cfg := range u.cfgs {
			if written[cfg.kind+"/"+cfg.name] {
				continue
			}
			written[cfg.kind+"/"+cfg.name] = true
			d := b.configObject(cfg, u)
			tpl(helmFile(cfg.kind, cfg.name), d, "")
			if !cfg.env {
				for _, e := range cfg.entries {
					files[chartDir+"/files/"+e.path] = e.value
				}
			}
		}
		for _, d := range b.unitDocs(u) {
			k, n := docKind(d)
			tpl(helmFile(k, n), d, "")
		}
		if u.stateless() {
			h.put(u.key+".pdb.enabled", boolean(false))
			tpl(helmFile("PodDisruptionBudget", u.k8s), b.pdb(u), ".Values."+u.key+".pdb.enabled")
		}
		if u.autoscale {
			h.put(u.key+".autoscaling.enabled", boolean(false))
			tpl(helmFile("HorizontalPodAutoscaler", u.k8s), b.hpa(u), ".Values."+u.key+".autoscaling.enabled")
		}
		for i := start; i < len(c.report.Objects); i++ {
			c.report.Objects[i].Service = u.name
		}
	}
	if in := c.ingressUnits(); len(in) > 0 {
		h.put("ingress.enabled", boolean(true))
		tpl("ingress.yaml", b.ingress(in), ".Values.ingress.enabled")
	}
	// Comments on the values of each service, keys in reading order.
	for i := 0; i+1 < len(h.values.Content); i += 2 {
		k := h.values.Content[i]
		order(h.values.Content[i+1], "enabled", "className", "hosts", "replicaCount", "image", "resources", "env", "secretEnv", "persistence", "pdb", "autoscaling")
		for _, u := range c.units {
			if u.key == k.Value {
				what := "image " + u.image
				if u.build != nil {
					what = "built from " + u.build.Context + " (" + u.image + ")"
				}
				k.HeadComment = "Compose service " + u.name + ": " + what + "."
			}
		}
		if k.Value == "ingress" {
			k.HeadComment = "Services users reach (*.localhost resolves to this machine)."
		}
	}
	layered := "values-dev.yaml and values-prod.yaml are layered on top per environment."
	if len(c.envs) > 0 {
		names := make([]string, len(c.envs))
		for i, e := range c.envs {
			names[i] = "values-" + e.name + ".yaml"
		}
		layered = strings.Join(names, ", ") + " (the docker workflow's environments) are layered on top."
	}
	vals := encodeOne(comment(h.values, "Default values of "+c.opts.Name+", converted from its compose project.", layered))
	files[chartDir+"/values.yaml"] = vals

	if len(c.envs) > 0 {
		c.writeHelmEnvironments(files)
	} else {
		c.writeHelmDevProd(files)
		c.report.Environments = []string{"dev", "prod"}
	}

	chart := mapping(
		"apiVersion", "v2",
		"name", c.opts.Name,
		"description", fmt.Sprintf("%s, converted from its docker compose project by Nimbo", c.opts.Name),
		"type", "application",
		"version", "0.1.0",
		"appVersion", "1.0.0",
	)
	files[chartDir+"/Chart.yaml"] = encodeOne(chart)
	files[chartDir+"/.helmignore"] = []byte("# Patterns ignored when packaging the chart.\n.DS_Store\n.git/\n*.swp\n*.bak\n*.tmp\n")
	files[chartDir+"/templates/NOTES.txt"] = []byte(c.notes())
	c.report.Sources = append(c.report.Sources, model.KubeSource{Kind: model.KubeHelm, Path: chartDir, Release: c.opts.Name, Namespace: c.opts.Namespace})
	slices.SortStableFunc(c.report.Objects, func(a, b model.KubeConvertObj) int { return strings.Compare(a.Service, b.Service) })
	return files
}

// writeHelmDevProd writes values-dev.yaml and values-prod.yaml (no docker
// environments).
func (c *converter) writeHelmDevProd(files map[string][]byte) {
	dev, prod := mapping(), mapping()
	for _, u := range c.units {
		if u.kind == kindJob {
			continue
		}
		dv := mapping("replicaCount", u.replicas)
		if u.autoscale {
			set(dv, "autoscaling", mapping("enabled", false))
		}
		set(dev, u.key, dv)
		if u.stateless() {
			pv := mapping("replicaCount", u.prodReplicas(), "pdb", mapping("enabled", true))
			if u.autoscale {
				set(pv, "autoscaling", mapping("enabled", true))
			}
			set(prod, u.key, pv)
		}
	}
	if len(c.ingressUnits()) > 0 {
		set(dev, "ingress", mapping("className", ingressClass))
		set(prod, "ingress", mapping("className", prodClass))
	}
	files[chartDir+"/values-dev.yaml"] = encodeOne(comment(dev, "Development: one replica of everything, like docker compose."))
	files[chartDir+"/values-prod.yaml"] = encodeOne(comment(prod,
		"Production: stateless services run 2+ replicas with a PodDisruptionBudget; those that aren't data",
		"services are autoscaled (autoscaling.enabled: a HorizontalPodAutoscaler owns the replica count,",
		"so the Deployment drops spec.replicas). Data services keep one replica (a second one would be a",
		"separate, empty database). Override the dev credentials (secretEnv) with --set or an external",
		"secret store, and the .localhost hosts."))
}

func (c *converter) notes() string {
	var b strings.Builder
	b.WriteString("{{ .Chart.Name }} is installed as release {{ .Release.Name }} in namespace {{ .Release.Namespace }}.\n")
	if in := c.ingressUnits(); len(in) > 0 {
		b.WriteString("{{- if .Values.ingress.enabled }}\n\nOpen:\n")
		for _, u := range in {
			fmt.Fprintf(&b, "  http://{{ .Values.ingress.hosts.%s }}\n", u.key)
		}
		b.WriteString("{{- end }}\n")
	}
	b.WriteString("\nServices inside the cluster:\n")
	for _, u := range c.units {
		if len(u.ports) > 0 {
			fmt.Fprintf(&b, "  %s:%d\n", u.k8s, u.ports[0].number)
		}
	}
	return b.String()
}

// order sorts the keys of mapping m: listed keys first, in that order.
func order(m *yaml.Node, keys ...string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	rank := func(k string) int {
		if i := slices.Index(keys, k); i >= 0 {
			return i
		}
		return len(keys)
	}
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		pairs = append(pairs, pair{m.Content[i], m.Content[i+1]})
	}
	slices.SortStableFunc(pairs, func(a, b pair) int { return rank(a.k.Value) - rank(b.k.Value) })
	m.Content = m.Content[:0]
	for _, p := range pairs {
		m.Content = append(m.Content, p.k, p.v)
	}
}
