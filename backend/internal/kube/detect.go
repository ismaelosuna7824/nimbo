package kube

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Detected is a Kubernetes project found in a folder tree.
type Detected struct {
	// Dir is the project's folder (slash, relative to the scanned root):
	// the common ancestor of everything its sources read.
	Dir string
	// Sources are relative to Dir.
	Sources []model.KubeSource
	// Envs are the environments the sources map.
	Envs []string
}

// IsCandidate reports whether a file name can take part in detection
// (manifests, kustomizations, charts).
func IsCandidate(rel string) bool {
	base := path.Base(rel)
	return isManifestFile(rel) || isKustomizationName(base)
}

// Detect finds the Kubernetes projects among files (slash paths relative
// to the scanned root; read reads one): every Helm chart (not the
// subcharts of another), every Kustomize tree (a base with the overlays
// that build on it, or a kustomization nothing else builds on), and every
// folder of plain manifests outside them.
func Detect(files []string, read func(rel string) ([]byte, error)) []Detected {
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	dirOf := func(f string) string { return path.Dir(f) }
	// Charts.
	var charts []string
	for _, f := range files {
		if path.Base(f) == "Chart.yaml" {
			charts = append(charts, dirOf(f))
		}
	}
	slices.Sort(charts)
	inChart := func(f string) bool {
		for _, c := range charts {
			if c == "." || strings.HasPrefix(f, c+"/") {
				return true
			}
		}
		return false
	}
	var out []Detected
	for _, c := range charts {
		if inChart(c) && slices.ContainsFunc(charts, func(o string) bool { return o != c && (o == "." || strings.HasPrefix(c, o+"/")) }) {
			continue // a subchart
		}
		d := Detected{Dir: c, Sources: []model.KubeSource{{Kind: model.KubeHelm, Path: "."}}}
		p := &Project{fs: newFiles("", contentOf(files, c, read))}
		for env := range p.helmEnvFiles(c) {
			d.Envs = append(d.Envs, env)
		}
		slices.Sort(d.Envs)
		out = append(out, d)
	}
	// Kustomizations.
	kdirs := map[string]*kustNode{}
	for _, f := range files {
		if isKustomizationName(path.Base(f)) && !inChart(f) {
			data, err := read(f)
			if err != nil {
				continue
			}
			doc, err := compose.ParseDoc(f, data)
			if err != nil || doc.Root == nil {
				continue
			}
			var m map[string]any
			if doc.Root.Decode(&m) != nil {
				continue
			}
			m, _ = normalize(m).(map[string]any)
			kdirs[dirOf(f)] = &kustNode{Dir: dirOf(f), File: f, Doc: doc, Data: m}
		}
	}
	refs := map[string][]string{}   // kustomization dir -> kustomization dirs it builds on
	usedFiles := map[string]bool{}  // files kustomizations reference
	referenced := map[string]bool{} // kustomization dirs built on
	for dir, n := range kdirs {
		for _, key := range []string{"resources", "bases", "components"} {
			for _, it := range anyStrings(n.Data[key]) {
				if remoteRef.MatchString(it) {
					continue
				}
				t := cleanRel(path.Join(dir, it))
				if kdirs[t] != nil {
					refs[dir] = append(refs[dir], t)
					referenced[t] = true
				} else {
					usedFiles[t] = true
				}
			}
		}
		for _, key := range []string{"patchesStrategicMerge", "crds"} {
			for _, it := range anyStrings(n.Data[key]) {
				usedFiles[cleanRel(path.Join(dir, it))] = true
			}
		}
		for _, key := range []string{"patches", "patchesJson6902"} {
			for _, it := range list(n.Data[key]) {
				if p := str(it, "path"); p != "" {
					usedFiles[cleanRel(path.Join(dir, p))] = true
				}
			}
		}
	}
	closure := func(dir string) []string {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(d string) {
			if seen[d] {
				return
			}
			seen[d] = true
			for _, r := range refs[d] {
				walk(r)
			}
		}
		walk(dir)
		return slices.Sorted(maps.Keys(seen))
	}
	var roots []string
	for dir := range kdirs {
		if !referenced[dir] {
			roots = append(roots, dir)
		}
	}
	slices.Sort(roots)
	groups := map[string][]string{} // base -> overlays
	var standalone []string
	for _, r := range roots {
		if len(refs[r]) == 1 && !slices.ContainsFunc(anyStrings(kdirs[r].Data["components"]), func(c string) bool { return cleanRel(path.Join(r, c)) == refs[r][0] }) {
			groups[refs[r][0]] = append(groups[refs[r][0]], r)
			continue
		}
		standalone = append(standalone, r)
	}
	for _, r := range standalone {
		dirs := closure(r)
		d := Detected{Dir: commonAncestor(dirs)}
		d.Sources = []model.KubeSource{{Kind: model.KubeKustomize, Path: relDir(d.Dir, r)}}
		out = append(out, d)
	}
	for _, base := range slices.Sorted(maps.Keys(groups)) {
		overlays := groups[base]
		dirs := closure(base)
		for _, o := range overlays {
			dirs = append(dirs, closure(o)...)
		}
		d := Detected{Dir: commonAncestor(dirs)}
		src := model.KubeSource{Kind: model.KubeKustomize, Path: relDir(d.Dir, base), Environments: map[string]string{}}
		for _, o := range overlays {
			env := path.Base(o)
			if _, dup := src.Environments[env]; dup || o == "." {
				env = strings.ReplaceAll(relDir(d.Dir, o), "/", "-")
			}
			src.Environments[env] = relDir(d.Dir, o)
			d.Envs = append(d.Envs, env)
		}
		slices.Sort(d.Envs)
		d.Sources = []model.KubeSource{src}
		out = append(out, d)
	}
	// Plain manifests: folders of Kubernetes objects outside charts and
	// kustomizations.
	byDir := map[string]bool{}
	for _, f := range files {
		base := path.Base(f)
		if !isManifestFile(f) || inChart(f) || usedFiles[f] || kdirs[dirOf(f)] != nil || isKustomizationName(base) ||
			compose.IsComposeName(base) || byDir[dirOf(f)] {
			continue
		}
		data, err := read(f)
		if err != nil || strings.Contains(string(data), "{{") {
			continue
		}
		objs, _ := objectsOf(ParseFile(f, data))
		if slices.ContainsFunc(objs, isResource) {
			byDir[dirOf(f)] = true
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(byDir)) {
		out = append(out, Detected{Dir: dir, Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "."}}})
	}
	slices.SortFunc(out, func(a, b Detected) int { return strings.Compare(a.Dir, b.Dir) })
	return out
}

// isResource tells Kubernetes objects from look-alike configuration files
// (kubeconfig, kind/k3d cluster configs, kustomize/skaffold files).
func isResource(o *Object) bool {
	g := apiGroup(o.APIVersion)
	switch {
	case o.Name == "" && str(o.Obj, "metadata", "generateName") == "":
		return false
	case o.Kind == "Config" && o.APIVersion == "v1":
		return false
	case strings.HasSuffix(g, "kind.x-k8s.io"), strings.HasSuffix(g, "k3d.io"), strings.HasPrefix(g, "skaffold"),
		strings.HasPrefix(g, "kustomize.config.k8s.io"), strings.HasPrefix(g, "kubeadm.k8s.io"), strings.HasPrefix(g, "kubelet.config.k8s.io"):
		return false
	}
	return true
}

// contentOf maps the files below dir to lazy content for a Project.
func contentOf(files []string, dir string, read func(string) ([]byte, error)) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range files {
		if dir == "." || strings.HasPrefix(f, dir+"/") {
			if data, err := read(f); err == nil {
				out[f] = data
			}
		}
	}
	return out
}

// commonAncestor is the deepest folder holding every dir.
func commonAncestor(dirs []string) string {
	if len(dirs) == 0 {
		return "."
	}
	anc := dirs[0]
	for _, d := range dirs[1:] {
		for anc != "." && d != anc && !strings.HasPrefix(d, anc+"/") {
			anc = path.Dir(anc)
		}
	}
	return anc
}

// relDir is dir relative to base ("." when equal).
func relDir(base, dir string) string {
	if base == dir {
		return "."
	}
	if base == "." {
		return dir
	}
	return strings.TrimPrefix(dir, base+"/")
}
