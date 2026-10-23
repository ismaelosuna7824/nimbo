package kube

import (
	"context"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// envPaths are a source's environments: the conventional overlays/values
// files, overridden by the explicit ones.
func (p *Project) envPaths(src model.KubeSource) map[string]string {
	out := map[string]string{}
	switch src.Kind {
	case model.KubeKustomize:
		maps.Copy(out, p.kustOverlays(src.Path))
	case model.KubeHelm:
		maps.Copy(out, p.helmEnvFiles(cleanRel(src.Path)))
	}
	for env, pth := range src.Environments {
		if env != "" && pth != "" {
			out[env] = cleanRel(pth)
		}
	}
	return out
}

// Derive renders the Kubernetes workflow wf's files in dir for its active
// environment and refreshes wf.Kubernetes (everything but Sources' authored
// fields and Builds) and wf.Environments (one per environment a source
// maps; values kept by name). It returns the project and its render
// diagnostics (rules are the caller's). When nothing renders, the last
// graph is kept so the canvas does not empty while the user fixes a typo.
func Derive(ctx context.Context, dir string, wf *model.Workflow) (*Project, []model.Diagnostic) {
	if wf.Kubernetes == nil {
		wf.Kubernetes = &model.KubernetesModel{}
	}
	k := wf.Kubernetes
	if k.Sources == nil {
		k.Sources = []model.KubeSource{}
	}
	p := Open(ctx, dir, wf, Options{Environment: wf.ActiveEnvironment})
	envs := map[string]bool{}
	for i, r := range p.Sources {
		src := &k.Sources[i]
		src.EnvPaths = p.envPaths(*src)
		for e := range src.EnvPaths {
			envs[e] = true
		}
		src.Objects = len(r.Objects)
		src.Error = ""
		if r.Err != nil {
			src.Error = r.Err.Error()
		}
		src.Chart = p.chartFacts(r)
		if len(src.EnvPaths) == 0 {
			src.EnvPaths = nil
		}
	}
	k.Environment = ""
	if envs[wf.ActiveEnvironment] {
		k.Environment = wf.ActiveEnvironment
	}
	p.Environment = k.Environment
	k.Files = p.Files
	k.Dockerfiles = p.dockerfiles(k)
	rendered := len(p.Objects) > 0
	for _, r := range p.Sources {
		if r.Err == nil {
			rendered = true
		}
	}
	if rendered || len(k.Sources) == 0 {
		k.Graph = p.Graph(k.Graph.Nodes)
	}
	if k.Graph.Nodes == nil {
		k.Graph.Nodes = []model.KubeNode{}
	}
	if k.Graph.Edges == nil {
		k.Graph.Edges = []model.KubeEdge{}
	}
	// Environments follow the overlays/values files.
	byName := map[string]model.Environment{}
	for _, e := range wf.Environments {
		byName[e.Name] = e
	}
	wf.Environments = []model.Environment{}
	for _, name := range slices.Sorted(maps.Keys(envs)) {
		e, ok := byName[name]
		if !ok {
			e = model.Environment{Name: name, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}}
		}
		wf.Environments = append(wf.Environments, e)
	}
	return p, p.Diagnostics
}

// dockerfiles are the Dockerfiles of the builds, and of folders named
// like a rendered image's repository (an image built locally).
func (p *Project) dockerfiles(k *model.KubernetesModel) []string {
	set := map[string]bool{}
	for _, b := range k.Builds {
		f := path.Join(cleanRel(b.Context), firstNonEmpty(b.Dockerfile, "Dockerfile"))
		if p.fs.exists(f) {
			set[f] = true
		}
	}
	repos := map[string]bool{}
	for _, o := range p.Objects {
		spec, _ := podSpec(o)
		for _, key := range []string{"containers", "initContainers"} {
			for _, c := range list(spec[key]) {
				if ref := compose.ParseImage(str(c, "image")); ref != nil {
					repos[path.Base(ref.Repo)] = true
				}
			}
		}
	}
	for _, f := range p.fs.list() {
		if !compose.IsDockerfileName(path.Base(f)) {
			continue
		}
		dir := path.Dir(f)
		if dir != "." && repos[path.Base(dir)] {
			set[f] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// ImageRepoDockerfile returns the Dockerfile of the folder named like an
// image's repository ("api:dev" -> "services/api/Dockerfile"), "" when
// none: images Nimbo builds and loads into the cluster.
func ImageRepoDockerfile(dockerfiles []string, image string) string {
	ref := compose.ParseImage(image)
	if ref == nil {
		return ""
	}
	for _, f := range dockerfiles {
		if path.Base(path.Dir(f)) == path.Base(ref.Repo) {
			return f
		}
	}
	return ""
}

// FileKind classifies a file of a Kubernetes workflow for the Code tab.
func FileKind(wf *model.Workflow, rel string) model.FileKind {
	base := path.Base(rel)
	k := wf.Kubernetes
	switch {
	case compose.IsDockerfileName(base):
		return model.FileDockerfile
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return model.FileEnv
	case isKustomizationName(base):
		return model.FileKustomization
	case base == "Chart.yaml" || base == "Chart.lock":
		return model.FileChart
	}
	if k == nil {
		return model.FileAsset
	}
	for _, s := range k.Sources {
		if s.Kind != model.KubeHelm {
			continue
		}
		dir := cleanRel(s.Path)
		if slices.Contains(s.ValuesFiles, rel) || slices.Contains(slices.Collect(maps.Values(s.EnvPaths)), rel) {
			return model.FileValues
		}
		inner, ok := strings.CutPrefix(rel, dir+"/")
		if dir == "." {
			inner, ok = rel, true
		}
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(inner, "templates/") || strings.Contains(inner, "/templates/") && strings.HasPrefix(inner, "charts/"):
			return model.FileHelmTemplate
		case !strings.Contains(inner, "/") && valuesEnvRe.MatchString(inner) || inner == "values.yaml" || inner == "values.yml" || inner == "values.schema.json":
			return model.FileValues
		}
	}
	if isManifestFile(rel) && slices.Contains(k.Files, rel) {
		return model.FileManifest
	}
	return model.FileAsset
}
