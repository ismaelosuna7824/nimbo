package kube

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"go.yaml.in/yaml/v4"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// kustomizationNames are the file names kustomize looks for, in order.
var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

func isKustomizationName(base string) bool { return slices.Contains(kustomizationNames, base) }

// kustomizationFile returns the kustomization file of dir ("" when none).
func (p *Project) kustomizationFile(dir string) string {
	for _, n := range kustomizationNames {
		if f := path.Join(dir, n); p.fs.exists(f) {
			return f
		}
	}
	return ""
}

// kustTree is the chain of kustomizations a root builds on, root first.
type kustTree struct {
	Root  string // the directory built
	Nodes []*kustNode
}

// kustNode is one kustomization file.
type kustNode struct {
	Dir  string
	File string
	Doc  *compose.Doc
	Data map[string]any
}

// remoteRef matches remote kustomize targets (git/http URLs).
var remoteRef = regexp.MustCompile(`^(https?://|git@|ssh://|github\.com/|gitlab\.com/|bitbucket\.org/)|\?ref=|\.git(//|$)`)

// loadKustTree reads the kustomizations reachable from dir (resources,
// bases and components that are directories), refusing remote targets.
func (p *Project) loadKustTree(dir string) (*kustTree, error) {
	t := &kustTree{Root: dir}
	seen := map[string]bool{}
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if seen[dir] || depth > 30 {
			return nil
		}
		seen[dir] = true
		file := p.kustomizationFile(dir)
		if file == "" {
			return fmt.Errorf("%s has no kustomization.yaml", dir)
		}
		data, err := p.fs.read(file)
		if err != nil {
			return err
		}
		doc, err := compose.ParseDoc(file, data)
		if err != nil {
			if se, ok := err.(*compose.SyntaxError); ok {
				return &locatedError{file: file, line: se.Line, err: fmt.Errorf("%s", se.Message)}
			}
			return err
		}
		var m map[string]any
		if doc.Root != nil {
			if err := doc.Root.Decode(&m); err != nil {
				return &locatedError{file: file, err: err}
			}
		}
		m, _ = normalize(m).(map[string]any)
		t.Nodes = append(t.Nodes, &kustNode{Dir: dir, File: file, Doc: doc, Data: m})
		if _, ok := m["helmCharts"]; ok {
			return &locatedError{file: file, line: doc.Line("helmCharts"), err: fmt.Errorf("helmCharts needs kustomize --enable-helm: add the chart as a helm source of the workflow instead")}
		}
		for _, key := range []string{"resources", "bases", "components"} {
			for i, it := range list(m[key]) {
				ref, _ := it.(string)
				if ref == "" {
					continue
				}
				if remoteRef.MatchString(ref) {
					line := doc.LocatePath([]string{key, strconv.Itoa(i)})
					return &locatedError{file: file, line: line, err: fmt.Errorf("remote %s %q is not supported: copy it into the workflow and reference the local folder", strings.TrimSuffix(key, "s"), ref)}
				}
				target := cleanRel(path.Join(dir, ref))
				if p.kustomizationFile(target) != "" {
					if err := walk(target, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(dir, 0); err != nil {
		return t, err
	}
	return t, nil
}

// files are the files of the tree: kustomizations, resource files,
// patches and generator inputs that exist in the workflow.
func (t *kustTree) files(p *Project) []string {
	set := map[string]bool{}
	for _, n := range t.Nodes {
		set[n.File] = true
		add := func(ref string) {
			if ref == "" || remoteRef.MatchString(ref) {
				return
			}
			f := cleanRel(path.Join(n.Dir, ref))
			if p.fs.exists(f) {
				set[f] = true
			}
		}
		for _, key := range []string{"resources", "bases", "components", "patchesStrategicMerge", "crds"} {
			for _, it := range list(n.Data[key]) {
				s, _ := it.(string)
				add(s)
			}
		}
		for _, key := range []string{"patches", "patchesJson6902"} {
			for _, it := range list(n.Data[key]) {
				add(str(it, "path"))
			}
		}
		for _, key := range []string{"configMapGenerator", "secretGenerator"} {
			for _, g := range list(n.Data[key]) {
				for _, f := range list(dig(g, "files")) {
					s, _ := f.(string)
					if _, after, ok := strings.Cut(s, "="); ok {
						s = after
					}
					add(s)
				}
				for _, f := range list(dig(g, "envs")) {
					s, _ := f.(string)
					add(s)
				}
				add(str(g, "env"))
			}
		}
	}
	return slices.Sorted(func(yield func(string) bool) {
		for f := range set {
			if !yield(f) {
				return
			}
		}
	})
}

// maxKustFile bounds the files copied into the in-memory file system.
const maxKustFile = 1 << 20

// renderKustomize builds the source's root (or the environment's overlay)
// with kustomize in memory: the workflow's files are the only files it
// can read, and remote bases are refused.
func (p *Project) renderKustomize(r *Rendered, env string) {
	root := cleanRel(r.Source.Path)
	if env != "" {
		if e := p.envPaths(r.Source)[env]; e != "" {
			root, r.Env = cleanRel(e), e
		}
	}
	tree, err := p.loadKustTree(root)
	r.kust = tree
	if tree != nil {
		r.Files = tree.files(p)
	}
	if err != nil {
		r.Err = err
		return
	}
	mem := filesys.MakeFsInMemory()
	const base = "/w"
	for _, rel := range p.fs.list() {
		data, err := p.fs.read(rel)
		if err != nil || len(data) > maxKustFile {
			continue
		}
		_ = mem.MkdirAll(path.Dir(base + "/" + rel))
		_ = mem.WriteFile(base+"/"+rel, data)
	}
	// Ask for origin annotations in the copy of the root kustomization:
	// they say which file each object comes from.
	top := tree.Nodes[0]
	keepOrigin := slices.Contains(anyStrings(top.Data["buildMetadata"]), "originAnnotations")
	if !keepOrigin {
		m := map[string]any{}
		for k, v := range top.Data {
			m[k] = v
		}
		m["buildMetadata"] = append(anyStrings(top.Data["buildMetadata"]), "originAnnotations")
		out, err := yaml.Marshal(m)
		if err != nil {
			r.Err = err
			return
		}
		_ = mem.WriteFile(base+"/"+top.File, out)
	}
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	rm, err := k.Run(mem, base+"/"+root)
	if err != nil {
		r.Err = kustomizeError(err, base)
		return
	}
	origins := make([]kustOrigin, 0, rm.Size())
	for _, res := range rm.Resources() {
		o, _ := res.GetOrigin()
		origins = append(origins, originOf(o, root))
	}
	if !keepOrigin {
		if err := rm.RemoveOriginAnnotations(); err != nil {
			r.Err = err
			return
		}
	}
	var out []byte
	fileCache := map[string]*File{}
	for i, res := range rm.Resources() {
		data, err := res.AsYAML()
		if err != nil {
			r.Err = err
			return
		}
		out = appendDocs(out, data)
		f := ParseFile("", data)
		objs, _ := objectsOf(f)
		if len(objs) == 0 {
			continue
		}
		o := objs[0]
		o.Source, o.Doc, o.Line = r.Index, -1, 1
		org := res.OrgId()
		o.orgKind, o.orgName, o.orgNamespace = org.Kind, org.Name, org.Namespace
		if i < len(origins) {
			o.File, o.generated = origins[i].file, origins[i].gen
		}
		p.locateKust(o, tree, fileCache)
		r.Objects = append(r.Objects, o)
	}
	r.YAML = out
}

// kustOrigin is the file an object comes from (gen: a generator's
// kustomization).
type kustOrigin struct {
	file string
	gen  bool
}

// originOf turns a kustomize origin into a workflow path.
func originOf(o *resource.Origin, root string) (res kustOrigin) {
	if o == nil {
		return res
	}
	switch {
	case o.ConfiguredIn != "":
		res.file, res.gen = cleanRel(path.Join(root, o.ConfiguredIn)), true
	case o.Repo == "" && o.Path != "":
		res.file = cleanRel(path.Join(root, o.Path))
	}
	return res
}

// locateKust finds the document (resource) or generator entry (generated
// objects) declaring o.
func (p *Project) locateKust(o *Object, tree *kustTree, cache map[string]*File) {
	if o.File == "" {
		if len(tree.Nodes) > 0 {
			o.File, o.Line = tree.Nodes[0].File, 1
		}
		return
	}
	if o.generated {
		for _, n := range tree.Nodes {
			if n.File != o.File {
				continue
			}
			for _, key := range []string{"configMapGenerator", "secretGenerator"} {
				for i, g := range list(n.Data[key]) {
					if str(g, "name") == o.orgName {
						o.Line = n.Doc.LocatePath([]string{key, strconv.Itoa(i)})
						return
					}
				}
			}
		}
		o.Line = 1
		return
	}
	f := cache[o.File]
	if f == nil {
		data, err := p.fs.read(o.File)
		if err != nil {
			return
		}
		f = ParseFile(o.File, data)
		cache[o.File] = f
	}
	for di, s := range f.Docs() {
		var m map[string]any
		if s.Doc.Root.Decode(&m) != nil {
			continue
		}
		if str(m, "kind") == o.orgKind && str(m, "metadata", "name") == o.orgName {
			o.Doc, o.Line = di, firstContentLine(f, s)
			return
		}
	}
}

var kustPathRe = regexp.MustCompile(`/w/([^\s:'"]+)`)

// kustomizeError rewrites the in-memory paths of a kustomize error and
// locates it in the file it names.
func kustomizeError(err error, base string) error {
	msg := strings.ReplaceAll(err.Error(), base+"/", "")
	if m := kustPathRe.FindStringSubmatch(err.Error()); m != nil {
		return &locatedError{file: strings.TrimRight(m[1], ".,;)"), err: fmt.Errorf("%s", msg)}
	}
	return fmt.Errorf("%s", msg)
}

func anyStrings(v any) []string {
	var out []string
	for _, it := range list(v) {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// kustOverlays finds the conventional overlays of a kustomize source:
// <path>/overlays/<env>, <parent>/overlays/<env> and, for a "base"
// directory, its sibling kustomizations.
func (p *Project) kustOverlays(src string) map[string]string {
	src = cleanRel(src)
	out := map[string]string{}
	add := func(dir string) {
		for _, f := range p.fs.under(dir) {
			rest := strings.TrimPrefix(f, dir+"/")
			env, file, ok := strings.Cut(rest, "/")
			if ok && isKustomizationName(file) && env != "" {
				if _, dup := out[env]; !dup {
					out[env] = dir + "/" + env
				}
			}
		}
	}
	add(path.Join(src, "overlays"))
	parent := path.Dir(src)
	if parent != src {
		add(cleanRel(path.Join(parent, "overlays")))
	}
	if path.Base(src) == "base" {
		for _, f := range p.fs.under(parent) {
			rest := strings.TrimPrefix(f, strings.TrimPrefix(parent+"/", "./"))
			if parent == "." {
				rest = f
			}
			env, file, ok := strings.Cut(rest, "/")
			if ok && isKustomizationName(file) && env != "base" && env != "overlays" {
				if _, dup := out[env]; !dup {
					out[env] = cleanRel(path.Join(parent, env))
				}
			}
		}
	}
	return out
}
