package kube

import (
	"bytes"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Environments of a Kubernetes workflow are files: a Kustomize source's
// overlay directory (overlays/<env>/kustomization.yaml on top of the base)
// and a Helm source's values file (values-<env>.yaml layered after the
// source's values files). Creating, renaming and deleting an environment
// writes, moves and removes those files for every source that takes
// environments; plain manifests deploy the same in every environment.

// EnvPlan is what an environment operation changes: Files to write (nil
// content deletes the file), the sources with their explicit Environments
// maps updated, and Dirs left empty by deletions (to remove).
type EnvPlan struct {
	Files   map[string][]byte
	Sources []model.KubeSource
	Dirs    []string
}

// TakesEnvironments reports whether a source kind renders environments of
// its own (Kustomize overlays, Helm values files).
func TakesEnvironments(kind string) bool {
	return kind == model.KubeKustomize || kind == model.KubeHelm
}

// filesOnly is a project that reads dir's files without rendering.
func filesOnly(dir string) *Project { return &Project{Dir: dir, fs: newFiles(dir, nil)} }

func newPlan(wf *model.Workflow) *EnvPlan {
	plan := &EnvPlan{Files: map[string][]byte{}}
	if wf.Kubernetes != nil {
		for _, s := range wf.Kubernetes.Sources {
			s.Environments = maps.Clone(s.Environments)
			s.ValuesFiles = slices.Clone(s.ValuesFiles)
			plan.Sources = append(plan.Sources, s)
		}
	}
	return plan
}

// CreateEnvironment adds environment name to every Kustomize and Helm
// source of wf (files in dir): a copy of environment from (its overlay
// directory or values file, with the environment's name rewritten in
// namespaces, hosts and labels) or, when from is "" or the source lacks
// it, a new overlay/values file on top of the base.
func CreateEnvironment(dir string, wf *model.Workflow, name, from string) (*EnvPlan, error) {
	p := filesOnly(dir)
	plan := newPlan(wf)
	supported := 0
	for i := range plan.Sources {
		src := &plan.Sources[i]
		if !TakesEnvironments(src.Kind) {
			continue
		}
		supported++
		paths := p.envPaths(*src)
		if _, dup := paths[name]; dup {
			return nil, editErrorf("%s %s already has an environment %q (%s)", src.Kind, src.Path, name, paths[name])
		}
		var target string
		var err error
		if src.Kind == model.KubeKustomize {
			target, err = p.createOverlay(*src, paths, name, from, plan.Files)
		} else {
			target, err = p.createValuesFile(*src, paths, name, from, plan.Files)
		}
		if err != nil {
			return nil, err
		}
		if len(src.Environments) > 0 {
			src.Environments[name] = target
		}
	}
	if supported == 0 {
		return nil, editErrorf("environments need a Kustomize or Helm source: plain manifests deploy the same in every environment (move them into base/ with a kustomization.yaml to add overlays)")
	}
	return plan, nil
}

// overlayDirFor is where a new overlay of src goes: next to its existing
// overlays, else overlays/ beside a "base" directory, else
// <source>/overlays.
func overlayDirFor(src model.KubeSource, paths map[string]string, name string) string {
	parents := map[string]int{}
	for _, d := range paths {
		parents[path.Dir(d)]++
	}
	best, n := "", 0
	for _, d := range slices.Sorted(maps.Keys(parents)) {
		if parents[d] > n {
			best, n = d, parents[d]
		}
	}
	if best != "" {
		return joinRel(best, name)
	}
	base := cleanRel(src.Path)
	if path.Base(base) == "base" {
		return joinRel(path.Dir(base), "overlays/"+name)
	}
	return joinRel(base, "overlays/"+name)
}

func (p *Project) createOverlay(src model.KubeSource, paths map[string]string, name, from string, out map[string][]byte) (string, error) {
	base := cleanRel(src.Path)
	if fromDir := paths[from]; from != "" && fromDir != "" && fromDir != base {
		dir := joinRel(path.Dir(fromDir), name)
		if err := p.ensureFree(dir); err != nil {
			return "", err
		}
		for _, f := range p.fs.under(fromDir) {
			data, err := p.fs.read(f)
			if err != nil {
				return "", editErrorf("%s can't be read: %v", f, err)
			}
			out[dir+"/"+strings.TrimPrefix(f, fromDir+"/")] = renameEnvText(data, from, name)
		}
		return dir, nil
	}
	dir := overlayDirFor(src, paths, name)
	if err := p.ensureFree(dir); err != nil {
		return "", err
	}
	ns, withObject := p.overlayNamespace(paths, name)
	var b strings.Builder
	fmt.Fprintf(&b, "# The %s environment: %s/ plus what changes here (replicas, images,\n", name, base)
	b.WriteString("# patches), edited in the Environments tab or in this file.\n")
	b.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n")
	if ns != "" {
		b.WriteString("namespace: " + ns + "\n")
	}
	b.WriteString("resources:\n  - " + relPath(dir, base) + "\n")
	if withObject {
		b.WriteString("  - namespace.yaml\n")
		out[dir+"/namespace.yaml"] = []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + ns + "\n")
	}
	out[dir+"/kustomization.yaml"] = []byte(b.String())
	return dir, nil
}

// overlayNamespace follows the existing overlays' namespace convention
// (hello-dev, hello-prod -> hello-<name>) and whether they declare their
// Namespace object.
func (p *Project) overlayNamespace(paths map[string]string, name string) (ns string, withObject bool) {
	for _, env := range slices.Sorted(maps.Keys(paths)) {
		dir := paths[env]
		kf := p.kustomizationFile(dir)
		if kf == "" {
			continue
		}
		data, err := p.fs.read(kf)
		if err != nil {
			continue
		}
		var k map[string]any
		docs := ParseFile(kf, data).Docs()
		if len(docs) == 0 || docs[0].Doc.Root.Decode(&k) != nil {
			continue
		}
		cur := str(k, "namespace")
		prefix, ok := strings.CutSuffix(cur, "-"+env)
		if !ok || prefix == "" {
			continue
		}
		for _, f := range p.fs.under(dir) {
			if !isManifestFile(f) {
				continue
			}
			if b, err := p.fs.read(f); err == nil {
				objs, _ := objectsOf(ParseFile(f, b))
				for _, o := range objs {
					if o.Kind == "Namespace" && o.Name == cur {
						withObject = true
					}
				}
			}
		}
		return prefix + "-" + name, withObject
	}
	return "", false
}

// ensureFree refuses to write into a directory or file that exists.
func (p *Project) ensureFree(rel string) error {
	if p.fs.exists(rel) || len(p.fs.under(rel)) > 0 {
		return &EditError{Msg: rel + " already exists: rename or remove it first", Conflict: true}
	}
	return nil
}

// envValuesName is file renamed from environment from to name
// (chart/values-prod.yaml -> chart/values-staging.yaml, values/prod.yaml ->
// values/staging.yaml); other names become values-<name>.yaml beside it.
func envValuesName(file, from, name string) string {
	dir, base := path.Dir(file), path.Base(file)
	if m := valuesEnvRe.FindStringSubmatchIndex(base); m != nil && base[m[2]:m[3]] == from {
		return joinRel(dir, base[:m[2]]+name+base[m[3]:])
	}
	if path.Base(dir) == "values" && strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml") == from {
		return joinRel(dir, name+path.Ext(base))
	}
	return joinRel(dir, "values-"+name+".yaml")
}

func (p *Project) createValuesFile(src model.KubeSource, paths map[string]string, name, from string, out map[string][]byte) (string, error) {
	chart := cleanRel(src.Path)
	if fromFile := paths[from]; from != "" && fromFile != "" {
		file := envValuesName(fromFile, from, name)
		if err := p.ensureFree(file); err != nil {
			return "", err
		}
		data, err := p.fs.read(fromFile)
		if err != nil {
			return "", editErrorf("%s can't be read: %v", fromFile, err)
		}
		out[file] = renameEnvText(data, from, name)
		return file, nil
	}
	file := joinRel(chart, "values-"+name+".yaml")
	if len(paths) > 0 {
		env := slices.Sorted(maps.Keys(paths))[0]
		file = envValuesName(paths[env], env, name)
	}
	if err := p.ensureFree(file); err != nil {
		return "", err
	}
	out[file] = fmt.Appendf(nil, "# %s: values layered on %s/values.yaml for this environment\n# (set in the Environments tab or here).\n", name, chart)
	return file, nil
}

// RenameEnvironment renames environment from to to: overlay directories
// and values files named after it move (their contents follow: namespace
// hello-staging -> hello-qa), explicit Environments entries and values
// files lists are updated.
func RenameEnvironment(dir string, wf *model.Workflow, from, to string) (*EnvPlan, error) {
	p := filesOnly(dir)
	plan := newPlan(wf)
	found := false
	for i := range plan.Sources {
		src := &plan.Sources[i]
		paths := p.envPaths(*src)
		cur := paths[from]
		if cur == "" {
			continue
		}
		found = true
		if _, dup := paths[to]; dup {
			return nil, editErrorf("%s %s already has an environment %q", src.Kind, src.Path, to)
		}
		next := cur
		switch {
		case src.Kind == model.KubeKustomize && path.Base(cur) == from && cur != cleanRel(src.Path):
			next = joinRel(path.Dir(cur), to)
			if err := p.ensureFree(next); err != nil {
				return nil, err
			}
			for _, f := range p.fs.under(cur) {
				data, err := p.fs.read(f)
				if err != nil {
					return nil, editErrorf("%s can't be read: %v", f, err)
				}
				plan.Files[f] = nil
				plan.Files[next+"/"+strings.TrimPrefix(f, cur+"/")] = renameEnvText(data, from, to)
			}
			plan.Dirs = append(plan.Dirs, cur)
		case src.Kind == model.KubeHelm:
			next = envValuesName(cur, from, to)
			if next == cur {
				break
			}
			if err := p.ensureFree(next); err != nil {
				return nil, err
			}
			data, err := p.fs.read(cur)
			if err != nil {
				return nil, editErrorf("%s can't be read: %v", cur, err)
			}
			plan.Files[cur] = nil
			plan.Files[next] = renameEnvText(data, from, to)
		}
		if _, ok := src.Environments[from]; ok {
			delete(src.Environments, from)
			src.Environments[to] = next
		}
		for j, f := range src.ValuesFiles {
			if cleanRel(f) == cur {
				src.ValuesFiles[j] = next
			}
		}
	}
	if !found {
		return nil, &EditError{Msg: fmt.Sprintf("environment %q not found", from), NotFound: true}
	}
	return plan, nil
}

// DeleteEnvironment removes environment name: its overlay directories and
// values files (never the base, a source's own path, values files every
// environment uses or a directory holding another environment).
func DeleteEnvironment(dir string, wf *model.Workflow, name string) (*EnvPlan, error) {
	p := filesOnly(dir)
	plan := newPlan(wf)
	found := false
	for i := range plan.Sources {
		src := &plan.Sources[i]
		paths := p.envPaths(*src)
		cur := paths[name]
		if cur == "" {
			continue
		}
		found = true
		own := cleanRel(src.Path)
		if cur == own || cur == "." || strings.HasPrefix(own, cur+"/") {
			return nil, editErrorf("%s is the %s source itself: change the source instead of deleting it", cur, src.Kind)
		}
		for env, other := range paths {
			if env != name && (other == cur || strings.HasPrefix(other, cur+"/")) {
				return nil, editErrorf("%s also holds environment %q: delete its files in the Code tab", cur, env)
			}
		}
		if slices.ContainsFunc(src.ValuesFiles, func(f string) bool { return cleanRel(f) == cur }) || cur == joinRel(own, "values.yaml") {
			return nil, editErrorf("%s is used by every environment: it can't be deleted with one", cur)
		}
		if p.fs.isDir(cur) && !p.fs.exists(cur) {
			for _, f := range p.fs.under(cur) {
				plan.Files[f] = nil
			}
			plan.Dirs = append(plan.Dirs, cur)
		} else {
			plan.Files[cur] = nil
		}
		delete(src.Environments, name)
	}
	if !found {
		return nil, &EditError{Msg: fmt.Sprintf("environment %q not found", name), NotFound: true}
	}
	return plan, nil
}

// imageKeyLine matches YAML lines naming images: an environment's name in
// an image tag is a version, not something to rename.
var imageKeyLine = regexp.MustCompile(`^\s*(-\s+)?(image|newTag|tag|newName|repository|digest)\s*:`)

// renameEnvText rewrites environment name from to to as a whole word
// (hello-prod -> hello-staging, "(prod)" -> "(staging)", not "product")
// in a text file; image lines and binary files are kept as they are.
func renameEnvText(data []byte, from, to string) []byte {
	if from == "" || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return slices.Clone(data)
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, l := range lines {
		if !imageKeyLine.MatchString(l) {
			lines[i] = replaceWord(l, from, to)
		}
	}
	return []byte(strings.Join(lines, ""))
}

func wordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func replaceWord(s, from, to string) string {
	var b strings.Builder
	start := 0
	for start <= len(s) {
		i := strings.Index(s[start:], from)
		if i < 0 {
			break
		}
		i += start
		end := i + len(from)
		if (i == 0 || !wordByte(s[i-1])) && (end == len(s) || !wordByte(s[end])) {
			b.WriteString(s[start:i] + to)
		} else {
			b.WriteString(s[start:end])
		}
		start = end
	}
	b.WriteString(s[start:])
	return b.String()
}
