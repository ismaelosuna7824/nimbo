package cicd

import (
	"fmt"
	"path"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func (g *gen) kubernetes() error {
	k := g.wf.Kubernetes
	if k == nil || len(k.Sources) == 0 {
		return fmt.Errorf("the Kubernetes workflow has no manifests, Kustomize or Helm source")
	}
	g.what = "Kubernetes renders, validation and deploys"
	var names []string
	for _, e := range g.wf.Environments {
		names = append(names, e.Name)
	}
	if len(names) == 0 {
		names = []string{"default"}
	}
	ts := targets(names)
	envOf := func(t target) string {
		if len(g.wf.Environments) == 0 {
			return ""
		}
		return t.name
	}
	tools := []tool{toolKubectl}
	helm := false
	for _, s := range k.Sources {
		if s.Kind == model.KubeHelm {
			helm = true
		}
	}
	if helm {
		tools = append(tools, toolHelm)
	}

	var render strings.Builder
	render.WriteString("mkdir -p rendered\n")
	var manifests []string
	for _, s := range k.Sources {
		render.WriteString(helmDeps(s))
		if s.Kind == model.KubeManifests {
			manifests = append(manifests, shQuote(s.Path))
		}
	}
	for _, t := range ts {
		out := shQuote("rendered/" + t.id + ".yaml")
		render.WriteString("echo " + shQuote("== "+t.name) + "\n{\n")
		for _, s := range k.Sources {
			if cmd := renderCmd(s, envOf(t)); cmd != "" {
				render.WriteString("  " + cmd + "\n  echo ---\n")
			}
		}
		render.WriteString("} > " + out + "\n")
		render.WriteString("kubeconform -ignore-missing-schemas -summary " + out)
		if len(manifests) > 0 {
			render.WriteString(" " + strings.Join(manifests, " "))
		}
		render.WriteString("\n")
	}
	g.add(job{id: "validate", name: "Render and validate", stage: stageCheck, on: everyRun,
		tools: append(append([]tool(nil), tools...), toolKubeconform),
		steps: []step{{"Render and kubeconform", strings.TrimSuffix(render.String(), "\n")}}, artifacts: []string{"rendered/"}, artifactName: "rendered-manifests"})

	g.use(setting{name: "KUBECONFIG", secret: true, file: true, githubEnv: "KUBECONFIG_DATA", required: true,
		about: g.ifGitHub("Kubeconfig of the environment's cluster (file contents; an environment secret per environment)",
			"Kubeconfig of the environment's cluster (a File variable scoped to each environment)")})
	prev := ""
	for _, t := range ts {
		var b strings.Builder
		if g.github {
			b.WriteString("if [ -z \"${KUBECONFIG_DATA:-}\" ]; then " + g.fail("Set the KUBECONFIG secret of the "+t.name+" environment") + "; fi\n")
			b.WriteString("export KUBECONFIG=\"$RUNNER_TEMP/kubeconfig\"\nprintf '%s\\n' \"$KUBECONFIG_DATA\" > \"$KUBECONFIG\"\nchmod 600 \"$KUBECONFIG\"\n")
		} else {
			b.WriteString("if [ -z \"${KUBECONFIG:-}\" ]; then " + g.fail("Set the KUBECONFIG File variable of the "+t.name+" environment") + "; fi\n")
		}
		for _, s := range k.Sources {
			b.WriteString(applyCmd(s, envOf(t)) + "\n")
		}
		needs := []string{"validate"}
		if prev != "" {
			needs = append(needs, prev)
		}
		id := "deploy-" + t.id
		g.add(job{
			id: id, name: "Deploy " + t.name, stage: stageDeploy, on: release, needs: needs,
			env: t.name, deploy: true, gated: model.ProdLike(t.name), lock: "deploy-" + t.id,
			tools: tools, settings: []string{"KUBECONFIG"}, smokeURL: g.opts.Smoke,
			steps: []step{{"Apply", strings.TrimSuffix(b.String(), "\n")}},
		})
		if g.opts.Smoke {
			g.smokeJob(t, id)
		}
		prev = id
	}

	envs := make([]string, len(ts))
	for i, t := range ts {
		envs[i] = "`" + t.name + "`"
	}
	g.overview = append(g.overview,
		"- **Render and validate** (every push and pull request): renders every source for every environment ("+strings.Join(envs, ", ")+": `kubectl kustomize` of the overlay, `helm template` with the values files, the manifests as they are) and checks the objects with `kubeconform` (CRDs without a schema are skipped); the rendered YAML is attached to the run.",
		"- **Deploy** (pushes to the default branch, one environment after the other, production-like ones last): `kubectl apply -k <overlay>`, `helm upgrade --install` and `kubectl apply -f`, in the workflow's source order, against the cluster of the environment's `KUBECONFIG`.")
	g.smokeNotes()
	if len(k.Builds) > 0 {
		images := make([]string, len(k.Builds))
		for i, b := range k.Builds {
			images[i] = b.Image
		}
		g.notes = append(g.notes, "Images: "+strings.Join(images, ", ")+" are built by Nimbo for its local cluster only. Push them to a registry your cluster pulls from and reference them there before deploying from CI.")
	}
	return nil
}

// helmRelease is the release name of a Helm source.
func helmRelease(s model.KubeSource) string {
	switch {
	case s.Release != "":
		return s.Release
	case s.Chart != nil && s.Chart.Name != "":
		return s.Chart.Name
	}
	return path.Base(s.Path)
}

// helmArgs are the chart, namespace and values file arguments of a Helm
// source for env ("" = the base).
func helmArgs(s model.KubeSource, env string) string {
	ns := s.Namespace
	if ns == "" {
		ns = "default"
	}
	args := shQuote(helmRelease(s)) + " " + shQuote(s.Path) + " --namespace " + shQuote(ns)
	for _, v := range s.ValuesFiles {
		args += " -f " + shQuote(v)
	}
	if p := s.EnvPaths[env]; env != "" && p != "" {
		args += " -f " + shQuote(p)
	}
	return args
}

// helmDeps builds the chart's dependencies when some are not vendored.
func helmDeps(s model.KubeSource) string {
	if s.Chart == nil {
		return ""
	}
	for _, d := range s.Chart.Dependencies {
		if !d.Vendored && d.Repository != "" {
			return "helm dependency build " + shQuote(s.Path) + "\n"
		}
	}
	return ""
}

// kustomizeDir is the directory a Kustomize source renders for env.
func kustomizeDir(s model.KubeSource, env string) string {
	if p := s.EnvPaths[env]; env != "" && p != "" {
		return p
	}
	return s.Path
}

// renderCmd prints the objects of s for env ("" for manifests: kubeconform
// reads them as they are). Helm dependencies are built beforehand
// (helmDeps): their output would mix with the objects.
func renderCmd(s model.KubeSource, env string) string {
	switch s.Kind {
	case model.KubeKustomize:
		return "kubectl kustomize " + shQuote(kustomizeDir(s, env))
	case model.KubeHelm:
		return "helm template " + helmArgs(s, env)
	}
	return ""
}

func applyCmd(s model.KubeSource, env string) string {
	ns := ""
	if s.Namespace != "" && s.Namespace != "default" {
		ns = " -n " + shQuote(s.Namespace)
	}
	switch s.Kind {
	case model.KubeKustomize:
		return "kubectl apply" + ns + " -k " + shQuote(kustomizeDir(s, env))
	case model.KubeHelm:
		return helmDeps(s) + "helm upgrade --install " + helmArgs(s, env) + " --create-namespace --wait --timeout 10m"
	}
	create := ""
	if ns != "" {
		create = "kubectl create namespace " + shQuote(s.Namespace) + " --dry-run=client -o yaml | kubectl apply -f -\n"
	}
	return create + "kubectl apply" + ns + " -f " + shQuote(s.Path)
}
