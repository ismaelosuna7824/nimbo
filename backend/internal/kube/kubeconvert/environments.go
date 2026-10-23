package kubeconvert

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Environments of the docker workflow (compose.<env>.yaml + .env.<env>)
// become Kustomize overlays (overlays/<env>) or Helm values files
// (values-<env>.yaml) carrying what each one changes from the base: replica
// counts, images, resources and environment values (ConfigMap values;
// credentials through Secret generators / secretEnv values, never in a
// manifest). Prod-like environments (prod, production, staging, ...; see
// model.ProdLike) also get today's prod treatment: stateless services run
// 2+ replicas behind a PodDisruptionBudget and are autoscaled by a
// HorizontalPodAutoscaler. Without environments the converter writes the
// dev and prod environments.

// envUnits is one environment, analyzed like the base.
type envUnits struct {
	name  string
	prod  bool
	units map[string]*unit // by compose service name
}

// unit is the environment's unit of base unit bu (bu when absent).
func (e *envUnits) unit(bu *unit) *unit {
	if u := e.units[bu.name]; u != nil {
		return u
	}
	return bu
}

// analyzeEnvironments converts every environment's project like the base
// and records the units; services only an environment declares are
// reported (the base decides the objects).
func (c *converter) analyzeEnvironments() error {
	if len(c.opts.Environments) == 0 {
		return nil
	}
	if c.opts.Format == model.ConvertManifests {
		names := make([]string, len(c.opts.Environments))
		for i, e := range c.opts.Environments {
			names[i] = e.Name
		}
		c.issue(nil, "info", "environments", fmt.Sprintf("Plain manifests have one environment, the base: %s not converted. Convert to kustomize or helm to get one overlay per environment.", strings.Join(names, ", ")))
		return nil
	}
	anyProd := false
	c.report.Autoscaling = []model.KubeConvertHPA{}
	for _, env := range c.opts.Environments {
		if !ValidName(env.Name) {
			return fmt.Errorf("environment %q can't name an overlay: rename it to lower-case letters, digits and '-' (starting with a letter)", env.Name)
		}
		if env.Project == nil || env.Project.Model == nil {
			msg := "its compose files don't load"
			if env.Project != nil {
				for _, d := range env.Project.Diagnostics {
					if d.Severity == model.SeverityError {
						msg = d.Message
						break
					}
				}
			}
			return fmt.Errorf("can't convert environment %s: %s", env.Name, msg)
		}
		ec := newConverter(env.Project, c.opts)
		ec.measured = c.measured
		if err := ec.analyze(); err != nil {
			return fmt.Errorf("environment %s: %w", env.Name, err)
		}
		e := &envUnits{name: env.Name, prod: model.ProdLike(env.Name), units: map[string]*unit{}}
		for _, u := range ec.units {
			if c.byName[u.name] == nil {
				c.issue(nil, "warning", "environments", fmt.Sprintf("Service %s exists only in environment %s and was not converted: declare it in the base compose file.", u.name, env.Name))
				continue
			}
			e.units[u.name] = u
		}
		if e.prod {
			anyProd = true
			for _, bu := range c.units {
				if bu.autoscale {
					u := e.unit(bu)
					c.report.Autoscaling = append(c.report.Autoscaling, model.KubeConvertHPA{Service: bu.name, Environment: e.name, MinReplicas: u.prodReplicas(), MaxReplicas: c.hpaMax(u), TargetCPU: c.opts.Autoscaling.TargetCPU})
				}
			}
		}
		for _, bu := range c.units {
			if d := c.differences(bu, e.unit(bu)); len(d) > 0 {
				c.mapped(bu.name, overrideName(envSource(env.Project)), envDir(c.opts.Format, e.name)+": "+strings.Join(d, ", "))
			}
		}
		c.envs = append(c.envs, e)
		c.report.Environments = append(c.report.Environments, e.name)
	}
	if !anyProd && !c.opts.Autoscaling.Disabled {
		c.issue(nil, "info", "environments", "No production-like environment (prod, production, staging, ...): no HorizontalPodAutoscaler or PodDisruptionBudget is written. Name an environment prod to get them.")
	}
	return nil
}

func overrideName(file string) string {
	if file == "" {
		return "environment"
	}
	return file
}

// envDir is where an environment's differences go.
func envDir(format, env string) string {
	if format == model.ConvertHelm {
		return chartDir + "/values-" + env + ".yaml"
	}
	return kustomizeDir + "/overlays/" + env
}

// differences describes what u changes from bu (empty: nothing).
func (c *converter) differences(bu, u *unit) []string {
	var out []string
	if u.replicas != bu.replicas && bu.kind != kindJob {
		out = append(out, fmt.Sprintf("replicas %d", u.replicas))
	}
	if u.image != bu.image {
		out = append(out, "image "+u.image)
	}
	if u.requests != bu.requests || u.limits != bu.limits {
		out = append(out, fmt.Sprintf("resources requests %s/%s limits %s/%s", u.requests.cpu, u.requests.memory, u.limits.cpu, u.limits.memory))
	}
	for _, d := range envConfigDiffs(bu, u) {
		keys := make([]string, len(d.entries))
		for i, e := range d.entries {
			keys[i] = e.key
		}
		what := "ConfigMap " + d.cfg.name
		if d.cfg.kind == "Secret" {
			what = "Secret " + d.cfg.name
		}
		out = append(out, what+" "+strings.Join(keys, ", "))
	}
	return out
}

// configDiff is an environment ConfigMap/Secret of u whose entries differ
// from the base's: entries are the changed and added ones; created when
// the base has no such object.
type configDiff struct {
	cfg     *config
	entries []entry
	created bool
}

func envConfigDiffs(bu, u *unit) []configDiff {
	var out []configDiff
	for _, cfg := range u.cfgs {
		if !cfg.env {
			continue
		}
		i := slices.IndexFunc(bu.cfgs, func(b *config) bool { return b.env && b.kind == cfg.kind && b.name == cfg.name })
		if i < 0 {
			out = append(out, configDiff{cfg: cfg, entries: cfg.entries, created: true})
			continue
		}
		base := map[string]string{}
		for _, e := range bu.cfgs[i].entries {
			base[e.key] = string(e.value)
		}
		var changed []entry
		for _, e := range cfg.entries {
			if v, ok := base[e.key]; !ok || v != string(e.value) {
				changed = append(changed, e)
			}
		}
		if len(changed) > 0 {
			out = append(out, configDiff{cfg: cfg, entries: changed})
		}
	}
	return out
}

func apiVersionOf(kind string) string {
	if kind == kindJob {
		return "batch/v1"
	}
	return "apps/v1"
}

// ---------------------------------------------------------------------------
// Kustomize: one overlay per environment

func (c *converter) writeKustomizeEnvironments(files map[string][]byte) {
	for _, e := range c.envs {
		dir := kustomizeDir + "/overlays/" + e.name
		b := &builder{c: c, v: literal{}, format: "kustomize"}
		res := []string{"../../base"}
		var pdbs, hpas, cmGen, secGen []*yaml.Node
		replicas, patches := seq(), seq()
		var changed []string
		for _, bu := range c.units {
			u := e.unit(bu)
			if e.prod && bu.stateless() {
				if bu.autoscale {
					hpas = append(hpas, b.hpa(u))
					c.obj("HorizontalPodAutoscaler", u.k8s, dir+"/hpa.yaml", u.name)
					patches.Content = append(patches.Content, mapping(
						"target", mapping("kind", "Deployment", "name", u.k8s),
						"patch", textNode("- op: remove\n  path: /spec/replicas\n"),
					))
				} else {
					replicas.Content = append(replicas.Content, mapping("name", u.k8s, "count", u.prodReplicas()))
				}
				pdbs = append(pdbs, b.pdb(u))
				c.obj("PodDisruptionBudget", u.k8s, dir+"/pdb.yaml", u.name)
			} else if u.replicas != bu.replicas && bu.kind != kindJob {
				replicas.Content = append(replicas.Content, mapping("name", u.k8s, "count", u.replicas))
			}
			container := mapping("name", bu.k8s)
			if u.image != bu.image {
				set(container, "image", u.image)
			}
			if u.requests != bu.requests || u.limits != bu.limits {
				set(container, "resources", mapping(
					"requests", mapping("cpu", u.requests.cpu, "memory", u.requests.memory),
					"limits", mapping("cpu", u.limits.cpu, "memory", u.limits.memory),
				))
			}
			envFrom := false
			for _, d := range envConfigDiffs(bu, u) {
				gen := c.generator(&config{kind: d.cfg.kind, name: d.cfg.name, env: true, entries: d.entries}, dir, files)
				if d.created {
					envFrom = true
				} else {
					set(gen, "behavior", "merge")
				}
				if d.cfg.kind == "Secret" {
					secGen = append(secGen, gen)
				} else {
					cmGen = append(cmGen, gen)
				}
				c.obj(d.cfg.kind, d.cfg.name, dir+"/kustomization.yaml", u.name)
			}
			if envFrom {
				// envFrom has no merge key: the patch lists every source.
				list := seq()
				for _, cfg := range u.cfgs {
					if !cfg.env {
						continue
					}
					ref := "configMapRef"
					if cfg.kind == "Secret" {
						ref = "secretRef"
					}
					list.Content = append(list.Content, mapping(ref, mapping("name", cfg.name)))
				}
				set(container, "envFrom", list)
			}
			if len(container.Content) > 2 {
				patch := mapping(
					"apiVersion", apiVersionOf(bu.kind), "kind", bu.kind, "metadata", mapping("name", bu.k8s),
					"spec", mapping("template", mapping("spec", mapping("containers", seq(container)))),
				)
				patches.Content = append(patches.Content, mapping(
					"target", mapping("kind", bu.kind, "name", bu.k8s),
					"patch", textNode(string(encodeOne(patch))),
				))
				changed = append(changed, bu.name)
			}
		}
		if len(pdbs) > 0 {
			files[dir+"/pdb.yaml"] = encode(pdbs...)
			res = append(res, "pdb.yaml")
		}
		if len(hpas) > 0 {
			files[dir+"/hpa.yaml"] = encode(hpas...)
			res = append(res, "hpa.yaml")
		}
		labels := mapping("app.kubernetes.io/environment", e.name)
		if e.prod {
			set(labels, "app.kubernetes.io/managed-by", "kustomize")
		}
		k := mapping(
			"apiVersion", "kustomize.config.k8s.io/v1beta1",
			"kind", "Kustomization",
			"namespace", c.opts.Namespace,
			"resources", strList(res),
			"labels", seq(mapping("pairs", labels)),
		)
		if len(replicas.Content) > 0 {
			set(k, "replicas", replicas)
		}
		if e.prod && len(c.ingressUnits()) > 0 {
			patches.Content = append(patches.Content, mapping(
				"target", mapping("kind", "Ingress", "name", c.opts.Name),
				"patch", textNode("- op: replace\n  path: /spec/ingressClassName\n  value: "+prodClass+"\n"),
			))
		}
		if len(patches.Content) > 0 {
			set(k, "patches", patches)
		}
		if len(cmGen) > 0 {
			set(k, "configMapGenerator", seq(cmGen...))
		}
		if len(secGen) > 0 {
			set(k, "secretGenerator", seq(secGen...))
		}
		head := []string{"Environment " + e.name + ", from the docker workflow's " + overrideName(c.envProject(e.name)) + ": what it changes from the base."}
		if len(changed) > 0 {
			head = append(head, "Patched: "+strings.Join(changed, ", ")+"; generators with behavior: merge only add the values it changes.")
		}
		if e.prod {
			head = append(head,
				"Production-like: stateless services run 2+ replicas with a PodDisruptionBudget; those that aren't data",
				"services are autoscaled by a HorizontalPodAutoscaler (hpa.yaml), so their Deployments drop spec.replicas.",
				"Keep real credentials out of git (env/*.secret.env) or use an external secret store.")
		}
		files[dir+"/kustomization.yaml"] = encode(comment(k, head...))
	}
}

// envProject names the files environment name adds to the base: its
// override file and/or its env file ("" when unknown).
func (c *converter) envProject(name string) string {
	for _, e := range c.opts.Environments {
		if e.Name == name && e.Project != nil {
			return envSource(e.Project)
		}
	}
	return ""
}

// envSource is the override and env file an opened environment reads on
// top of the base (compose.prod.yaml, .env.prod / prod.env).
func envSource(p *compose.Project) string {
	var out []string
	if p.Override != "" {
		out = append(out, p.Override)
	}
	if n := len(p.EnvFiles); n > 0 && p.EnvFiles[n-1] != compose.BaseEnvFile(p.ProjectDir) {
		out = append(out, p.EnvFiles[n-1])
	}
	return strings.Join(out, " + ")
}

// ---------------------------------------------------------------------------
// Helm: one values file per environment

func (c *converter) writeHelmEnvironments(files map[string][]byte) {
	for _, e := range c.envs {
		vals := mapping()
		for _, bu := range c.units {
			u := e.unit(bu)
			v := mapping()
			if e.prod && bu.stateless() {
				set(v, "replicaCount", u.prodReplicas())
				set(v, "pdb", mapping("enabled", true))
				if bu.autoscale {
					as := mapping("enabled", true)
					if u.prodReplicas() != bu.prodReplicas() {
						set(as, "minReplicas", u.prodReplicas())
					}
					if c.hpaMax(u) != c.hpaMax(bu) {
						set(as, "maxReplicas", c.hpaMax(u))
					}
					set(v, "autoscaling", as)
				}
			} else if u.replicas != bu.replicas && bu.kind != kindJob {
				set(v, "replicaCount", u.replicas)
			}
			if u.image != bu.image {
				bref, ref := compose.ParseImage(bu.image), compose.ParseImage(u.image)
				switch {
				case bref == nil || bref.Digest != "" || bref.Tag == "":
					set(v, "image", mapping("repository", u.image))
				case ref != nil && ref.Digest == "" && ref.Tag != "":
					set(v, "image", mapping("repository", ref.Repo, "tag", ref.Tag))
				default:
					c.issue(bu, "warning", "environments", fmt.Sprintf("Environment %s runs %s, which has no tag: values-%s.yaml can't set it (the chart writes repository:tag).", e.name, u.image, e.name), "image")
				}
			}
			if u.requests != bu.requests || u.limits != bu.limits {
				set(v, "resources", mapping(
					"requests", mapping("cpu", u.requests.cpu, "memory", u.requests.memory),
					"limits", mapping("cpu", u.limits.cpu, "memory", u.limits.memory),
				))
			}
			for _, d := range envConfigDiffs(bu, u) {
				if d.created {
					c.issue(bu, "warning", "environments", fmt.Sprintf("Environment %s gives %s variables the base doesn't have (%s): the chart has no %s for them. Add them to the base compose file.", e.name, bu.name, d.cfg.name, d.cfg.kind), "environment")
					continue
				}
				m := mapping()
				for _, en := range d.entries {
					set(m, en.key, str(string(en.value)))
				}
				key := "env"
				if d.cfg.kind == "Secret" {
					key = "secretEnv"
				}
				set(v, key, m)
			}
			if len(v.Content) > 0 {
				order(v, "replicaCount", "image", "resources", "env", "secretEnv", "pdb", "autoscaling")
				set(vals, bu.key, v)
			}
		}
		if e.prod && len(c.ingressUnits()) > 0 {
			set(vals, "ingress", mapping("className", prodClass))
		}
		head := []string{"Environment " + e.name + ", from the docker workflow's " + overrideName(c.envProject(e.name)) + ": what it changes from values.yaml."}
		if e.prod {
			head = append(head,
				"Production-like: stateless services run 2+ replicas with a PodDisruptionBudget; those that aren't data",
				"services are autoscaled (autoscaling.enabled: a HorizontalPodAutoscaler owns the replica count).",
				"Override credentials (secretEnv) with --set or an external secret store.")
		}
		files[chartDir+"/values-"+e.name+".yaml"] = encodeOne(comment(vals, head...))
	}
}
