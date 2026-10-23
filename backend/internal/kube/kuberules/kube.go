package kuberules

import (
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Check runs every k8s_* rule on the rendered objects of a Kubernetes
// workflow's project (its active environment) and drops the advice turned
// off in Settings (cfg).
func Check(p *kube.Project, wf *model.Workflow, cfg model.BestPracticeSettings) []model.Diagnostic {
	return FilterSettings(run(newInput(p, wf)), cfg)
}

// newInput collects what the rules read from a rendered project.
func newInput(p *kube.Project, wf *model.Workflow) input {
	in := input{env: p.Environment, localImages: map[string]bool{}, namespaces: map[string]bool{}}
	var k model.KubernetesModel
	if wf.Kubernetes != nil {
		k = *wf.Kubernetes
	}
	for _, b := range k.Builds {
		if ref := compose.ParseImage(b.Image); ref != nil {
			in.localImages[ref.Repo] = true
		}
	}
	for _, r := range p.Sources {
		if r.Source.Namespace != "" {
			in.namespaces[r.Source.Namespace] = true
		}
	}
	for _, o := range p.Objects {
		if o.Node == nil {
			continue
		}
		obj := &object{id: o.ID, apiVersion: o.APIVersion, kind: o.Kind, name: o.Name, namespace: o.Namespace, node: o.Node,
			vendored: strings.HasPrefix(o.Template, "charts/")}
		obj.explicitNS = str(o.Node, "metadata", "namespace") != ""
		if o.Source >= 0 && o.Source < len(p.Sources) && p.Sources[o.Source].Source.Namespace != "" {
			obj.explicitNS = true
		}
		o := o
		obj.locate = func(path string) (string, int) { return p.Locate(o, path) }
		in.objects = append(in.objects, obj)
		// Images built from a folder named like their repository.
		if w := newWorkload(obj); w != nil {
			for _, ct := range w.containers() {
				img := str(ct.n, "image")
				if ref := compose.ParseImage(img); ref != nil && kube.ImageRepoDockerfile(k.Dockerfiles, img) != "" {
					in.localImages[ref.Repo] = true
				}
			}
		}
	}
	return in
}
