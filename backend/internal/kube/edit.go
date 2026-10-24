package kube

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
)

// EditError is an edit the files can't take; NotFound and Conflict pick
// the HTTP status.
type EditError struct {
	Msg      string
	NotFound bool
	Conflict bool
}

func (e *EditError) Error() string { return e.Msg }

func editErrorf(format string, args ...any) error {
	return &EditError{Msg: fmt.Sprintf(format, args...)}
}

// Editor collects in-place edits of a project's files. Every edit splices
// the source lines it changes (yaml node positions) and keeps the rest of
// the bytes, so comments, order, quoting and blank lines survive.
type Editor struct {
	p       *Project
	yaml    map[string]*File  // multi-document YAML files being edited
	text    map[string][]byte // other files (templates) being edited
	changed map[string]bool
	removed map[string]bool
}

// Edit starts an edit of the project's files.
func (p *Project) Edit() *Editor {
	return &Editor{p: p, yaml: map[string]*File{}, text: map[string][]byte{}, changed: map[string]bool{}, removed: map[string]bool{}}
}

// Files returns the files the edits changed (nil content: deleted).
func (e *Editor) Files() map[string][]byte {
	out := map[string][]byte{}
	for f := range e.changed {
		if e.removed[f] {
			out[f] = nil
			continue
		}
		if y, ok := e.yaml[f]; ok {
			out[f] = y.Bytes()
		} else {
			out[f] = e.text[f]
		}
	}
	return out
}

// file returns the parsed YAML file rel (edits so far applied).
func (e *Editor) file(rel string) (*File, error) {
	if f, ok := e.yaml[rel]; ok {
		return f, nil
	}
	data, err := e.p.fs.read(rel)
	if err != nil {
		return nil, &EditError{Msg: fmt.Sprintf("%s can't be read", rel), NotFound: true}
	}
	f := ParseFile(rel, data)
	if errs := f.Errors(); len(errs) > 0 {
		return nil, &EditError{Msg: fmt.Sprintf("%s:%d: %s: fix it in the Code tab first", rel, errs[0].Line, errs[0].Message), Conflict: true}
	}
	e.yaml[rel] = f
	return f, nil
}

func (e *Editor) touch(rel string) { e.changed[rel] = true }

// segment returns document doc of rel.
func (e *Editor) segment(rel string, doc int) (*File, *Segment, error) {
	f, err := e.file(rel)
	if err != nil {
		return nil, nil, err
	}
	docs := f.Docs()
	if doc < 0 || doc >= len(docs) {
		return nil, nil, &EditError{Msg: fmt.Sprintf("document %d of %s not found", doc, rel), NotFound: true}
	}
	return f, docs[doc], nil
}

// Set writes value at fieldPath of o where the field comes from (see
// Fields). A missing field is created in the object's manifest document.
func (e *Editor) Set(o *Object, fieldPath string, value any) error {
	segs, err := ParsePath(fieldPath)
	if err != nil {
		return editErrorf("%v", err)
	}
	src := e.p.fieldSources(o, [][]seg{segs})[0]
	if !src.Editable {
		if keys, missing, _ := concrete(o.Obj, segs); missing || keys == nil {
			if o.Doc >= 0 && !o.generated {
				return e.createIn(o.File, o.Doc, segs, value)
			}
		}
		if src.Reason == "" {
			src.Reason = "not editable"
		}
		return editErrorf("%s of %s: %s", fieldPath, o.ID, src.Reason)
	}
	return e.write(o, src, value)
}

// write applies a value to a mapped field.
func (e *Editor) write(o *Object, src *fieldSource, value any) error {
	if isScalar(value) && isScalar(src.Value) && src.Value != nil && scalarString(value) == scalarString(src.Value) {
		return nil // unchanged: nothing to write
	}
	switch src.Via {
	case ViaManifest, ViaPatch:
		f, s, err := e.segment(src.File, src.doc)
		if err != nil {
			return err
		}
		if err := setKeys(s.Doc, src.docKeys, toNode(value, s.Doc.Node(src.docKeys...))); err != nil {
			return editErrorf("%v", err)
		}
		f.Sync(s)
		e.touch(src.File)
	case ViaKustomization:
		f, s, err := e.segment(src.File, 0)
		if err != nil {
			return err
		}
		item := []string{src.transformer, strconv.Itoa(src.item)}
		switch src.transformer {
		case "replicas":
			n, ok := toInt(value)
			if !ok {
				return editErrorf("replicas must be a number")
			}
			if err := s.Doc.Set(append(item, "count"), compose.Int(n)); err != nil {
				return editErrorf("%v", err)
			}
		case "images":
			if err := setImageTransformer(s.Doc, item, fmt.Sprint(value)); err != nil {
				return err
			}
		}
		f.Sync(s)
		e.touch(src.File)
	case ViaValues:
		text := scalarString(value)
		parts := []string{text}
		if len(src.pattern.keys) > 1 || src.pattern.parts[0] != "" || src.pattern.parts[1] != "" {
			var ok bool
			if parts, ok = src.pattern.split(text, src.cur); !ok {
				return editErrorf("%q does not fit the template's format %s: only the <values> parts can change", text, src.pattern.describe())
			}
		}
		for i, k := range src.pattern.keys {
			if i < len(src.cur) && parts[i] == src.cur[i] {
				continue
			}
			v := any(parts[i])
			if len(parts) == 1 && len(src.pattern.keys) == 1 && src.pattern.parts[0] == "" && src.pattern.parts[1] == "" {
				v = value
			}
			// Back to what the chart (or an earlier values file) gives:
			// drop the override instead of repeating the value.
			if lower, ok := e.p.lowerValue(src.helm, src.valuesFiles[i], k); ok && lower == parts[i] && e.definesValuesKey(src.valuesFiles[i], k) {
				if err := e.unsetValuesKey(src.valuesFiles[i], k); err != nil {
					return err
				}
				continue
			}
			if err := e.setValuesKey(src.valuesFiles[i], k, v); err != nil {
				return err
			}
		}
	case ViaTemplate:
		data, err := e.textFile(src.File)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		if src.Line < 1 || src.Line > len(lines) {
			return editErrorf("%s:%d not found", src.File, src.Line)
		}
		l := lines[src.Line-1]
		i := strings.LastIndex(l, src.literal)
		if i < 0 {
			return editErrorf("%s:%d changed; reload", src.File, src.Line)
		}
		repl := scalarString(value)
		if strings.HasPrefix(src.literal, `"`) || strings.HasPrefix(src.literal, `'`) {
			repl = src.literal[:1] + repl + src.literal[:1]
		} else if needsQuoting(repl) {
			repl = strconv.Quote(repl)
		}
		lines[src.Line-1] = l[:i] + repl + l[i+len(src.literal):]
		e.text[src.File] = []byte(strings.Join(lines, "\n"))
		e.touch(src.File)
	default:
		return editErrorf("%s is not editable", src.Path)
	}
	return nil
}

func (e *Editor) textFile(rel string) ([]byte, error) {
	if data, ok := e.text[rel]; ok {
		return data, nil
	}
	data, err := e.p.fs.read(rel)
	if err != nil {
		return nil, &EditError{Msg: rel + " can't be read", NotFound: true}
	}
	return data, nil
}

// Unset removes fieldPath from where it is written (manifests and
// patches; Helm values keys and transformers are not removed).
func (e *Editor) Unset(o *Object, fieldPath string) error {
	segs, err := ParsePath(fieldPath)
	if err != nil {
		return editErrorf("%v", err)
	}
	src := e.p.fieldSources(o, [][]seg{segs})[0]
	if keys, missing, _ := concrete(o.Obj, segs); missing || keys == nil {
		return nil // nothing to remove
	}
	if !src.Editable || (src.Via != ViaManifest && src.Via != ViaPatch) {
		reason := src.Reason
		if src.Via == ViaValues {
			reason = "it comes from the values key " + strings.Join(src.ValuesKeys, ", ") + ": set the value instead"
		}
		return editErrorf("%s of %s can't be removed: %s", fieldPath, o.ID, reason)
	}
	f, s, err := e.segment(src.File, src.doc)
	if err != nil {
		return err
	}
	keys := src.docKeys
	last := segs[len(segs)-1]
	if last.isKey() {
		err = s.Doc.Delete(keys, true)
	} else {
		i, _ := strconv.Atoi(keys[len(keys)-1])
		err = s.Doc.DeleteItem(keys[:len(keys)-1], i, true)
	}
	if err != nil {
		return editErrorf("%v", err)
	}
	f.Sync(s)
	e.touch(src.File)
	return nil
}

// createIn writes a field that does not exist yet into document doc of
// file: missing maps are created, missing named list items appended.
func (e *Editor) createIn(file string, doc int, segs []seg, value any) error {
	f, s, err := e.segment(file, doc)
	if err != nil {
		return err
	}
	if err := setSegs(s.Doc, segs, value); err != nil {
		return editErrorf("%v", err)
	}
	f.Sync(s)
	e.touch(file)
	return nil
}

// setSegs sets a path given by segments in a document, creating what is
// missing.
func setSegs(d *compose.Doc, segs []seg, value any) error {
	var root map[string]any
	if d.Root != nil {
		if err := d.Root.Decode(&root); err != nil {
			return err
		}
	}
	var cur any = normalize(root)
	var keys []string
	for i, s := range segs {
		last := i == len(segs)-1
		switch {
		case s.isKey():
			m, _ := cur.(map[string]any)
			next, has := m[s.Key]
			keys = append(keys, s.Key)
			if !has || last {
				return setKeys(d, keys, build(segs[i+1:], value, d.Node(keys...)))
			}
			cur = next
		default:
			l, _ := cur.([]any)
			idx := s.Index
			if s.Name != "" {
				idx = -1
				for j, it := range l {
					if str(it, "name") == s.Name {
						idx = j
					}
				}
				if idx < 0 {
					item := build(segs[i+1:], value, nil)
					if item.Kind != yaml.MappingNode {
						item = compose.Map("name", compose.Str(s.Name))
					} else {
						item.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Value: "name"}, compose.Str(s.Name)}, item.Content...)
					}
					return d.AppendItem(keys, item)
				}
			}
			if idx < 0 || idx >= len(l) {
				return fmt.Errorf("no item %d in %s", s.Index, strings.Join(keys, "."))
			}
			if last {
				return d.SetItem(keys, idx, toNode(value, nil))
			}
			keys = append(keys, strconv.Itoa(idx))
			cur = l[idx]
		}
	}
	return nil
}

// build makes the node of value nested under segs (keys and named items).
func build(segs []seg, value any, old *yaml.Node) *yaml.Node {
	if len(segs) == 0 {
		return toNode(value, old)
	}
	s := segs[0]
	inner := build(segs[1:], value, nil)
	switch {
	case s.isKey():
		return compose.Map(s.Key, inner)
	case s.Name != "":
		item := compose.Map("name", compose.Str(s.Name))
		if inner.Kind == yaml.MappingNode {
			item.Content = append(item.Content, inner.Content...)
		}
		return compose.Seq(item)
	}
	return compose.Seq(inner)
}

// setKeys sets concrete keys (the last one a key or an item index).
func setKeys(d *compose.Doc, keys []string, n *yaml.Node) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty path")
	}
	if old := d.Node(keys...); old != nil && old.Kind == yaml.ScalarNode && n.Kind == yaml.ScalarNode && old.Value == n.Value &&
		(old.Tag == n.Tag || n.Tag == "!!str" && old.Style != 0) {
		return nil // same value: keep the bytes
	}
	if i, err := strconv.Atoi(keys[len(keys)-1]); err == nil && d.Node(keys...) != nil {
		if p := d.Node(keys[:len(keys)-1]...); p != nil && p.Kind == yaml.SequenceNode {
			return d.SetItem(keys[:len(keys)-1], i, n)
		}
	}
	return d.Set(keys, n)
}

// toNode converts a JSON value to a yaml node, keeping the scalar type of
// old (a quoted "8080" stays a string).
func toNode(v any, old *yaml.Node) *yaml.Node {
	switch t := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case *yaml.Node:
		return t
	case string:
		if old != nil && old.Kind == yaml.ScalarNode && old.Style == 0 {
			switch old.Tag {
			case "!!int":
				if _, err := strconv.Atoi(t); err == nil {
					return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: t}
				}
			case "!!bool":
				if t == "true" || t == "false" {
					return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: t}
				}
			}
		}
		return compose.Str(t)
	case bool:
		return compose.Bool(t)
	case int:
		return numberNode(float64(t), old)
	case int64:
		return numberNode(float64(t), old)
	case float64:
		return numberNode(t, old)
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return compose.Str(fmt.Sprint(v))
	}
	n.Style = 0
	// A flow list of scalars ([get, list]) stays a flow list.
	if old != nil && old.Kind == yaml.SequenceNode && old.Style&yaml.FlowStyle != 0 && len(old.Content) > 0 && n.Kind == yaml.SequenceNode &&
		!slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind != yaml.ScalarNode }) {
		n.Style = yaml.FlowStyle
	}
	return &n
}

func numberNode(f float64, old *yaml.Node) *yaml.Node {
	text := strconv.FormatFloat(f, 'f', -1, 64)
	if old != nil && old.Kind == yaml.ScalarNode && old.Tag == "!!str" {
		return compose.Str(text) // a quoted number stays a string
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(int64(f), 10)}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: text}
}

func scalarString(v any) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func needsQuoting(s string) bool {
	return s == "" || strings.ContainsAny(s[:1], "!&*{}[]|>'\"%@`#,?:-") || strings.Contains(s, ": ") || strings.Contains(s, " #")
}

// setImageTransformer rewrites a kustomization `images:` entry so the
// image renders as image.
func setImageTransformer(d *compose.Doc, item []string, image string) error {
	ref := compose.ParseImage(image)
	if ref == nil {
		return editErrorf("invalid image %q", image)
	}
	var entry map[string]any
	if n := d.Node(item...); n != nil {
		_ = n.Decode(&entry)
	}
	name := str(entry, "name")
	set := func(key, v string) error {
		path := append(append([]string{}, item...), key)
		if v == "" {
			return d.Delete(path, false)
		}
		return d.Set(path, compose.Str(v))
	}
	newName := ref.Repo
	if newName == name {
		newName = ""
	}
	if err := set("newName", newName); err != nil {
		return editErrorf("%v", err)
	}
	if err := set("newTag", ref.Tag); err != nil {
		return editErrorf("%v", err)
	}
	if err := set("digest", ref.Digest); err != nil {
		return editErrorf("%v", err)
	}
	return nil
}

// valuesPath splits a values key ("a.b[0].c", `labels["x.y"]`) into
// document keys.
func valuesPath(key string) ([]string, error) {
	segs, err := ParsePath(key)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(segs))
	for i, s := range segs {
		keys[i] = s.Key
		if !s.isKey() {
			keys[i] = strconv.Itoa(s.Index)
		}
	}
	return keys, nil
}

// definesValuesKey reports whether a values file sets key.
func (e *Editor) definesValuesKey(file, key string) bool {
	f, err := e.file(file)
	if err != nil || len(f.Docs()) == 0 {
		return false
	}
	keys, err := valuesPath(key)
	if err != nil {
		return false
	}
	return f.Docs()[0].Doc.Node(keys...) != nil
}

// unsetValuesKey removes key from a values file (and the maps it leaves
// empty).
func (e *Editor) unsetValuesKey(file, key string) error {
	keys, err := valuesPath(key)
	if err != nil {
		return editErrorf("%v", err)
	}
	f, err := e.file(file)
	if err != nil {
		return err
	}
	s := f.Docs()[0]
	last := keys[len(keys)-1]
	if i, err := strconv.Atoi(last); err == nil {
		err = s.Doc.DeleteItem(keys[:len(keys)-1], i, true)
		if err != nil {
			return editErrorf("%s: %v", file, err)
		}
	} else if err := s.Doc.Delete(keys, true); err != nil {
		return editErrorf("%s: %v", file, err)
	}
	f.Sync(s)
	e.touch(file)
	return nil
}

// lowerValue is the value key would have without file's override: the
// chart's defaults (subcharts' included) under the values files before
// file.
func (p *Project) lowerValue(h *helmRender, file, key string) (string, bool) {
	if h == nil {
		return "", false
	}
	idx := slices.Index(h.valuesFiles, file)
	if idx < 0 {
		return "", false
	}
	c, err := loader.LoadFiles(h.files)
	if err != nil {
		return "", false
	}
	chartValues := path.Join(h.chartDir, "values.yaml")
	if h.chartDir == "." {
		chartValues = "values.yaml"
	}
	if file == chartValues {
		c.Values = map[string]any{}
	}
	var below []string
	for _, f := range h.valuesFiles[:idx] {
		if f != chartValues {
			below = append(below, f)
		}
	}
	vals, err := p.userValues(below)
	if err != nil {
		return "", false
	}
	full, err := util.CoalesceValues(c, vals)
	if err != nil {
		return "", false
	}
	keys, err := valuesPath(key)
	if err != nil {
		return "", false
	}
	v, ok := at(map[string]any(full), keys)
	if !ok || !isScalar(v) || v == nil {
		return "", false
	}
	if n, isInt := toInt(v); isInt {
		return strconv.Itoa(n), true
	}
	return fmt.Sprint(v), true
}

// setValuesKey writes a values key into a values file (created when
// missing), keeping the key's scalar type.
func (e *Editor) setValuesKey(file, key string, v any) error {
	keys, err := valuesPath(key)
	if err != nil {
		return editErrorf("%v", err)
	}
	f, err := e.file(file)
	if err != nil {
		if ee, ok := err.(*EditError); ok && ee.NotFound {
			f = ParseFile(file, nil)
			e.yaml[file] = f
		} else {
			return err
		}
	}
	segs := f.Segments
	var s *Segment
	if docs := f.Docs(); len(docs) > 0 {
		s = docs[0]
	} else {
		s = segs[len(segs)-1]
	}
	old := s.Doc.Node(keys...)
	if str, ok := v.(string); ok && old != nil && old.Kind == yaml.ScalarNode && old.Style == 0 && old.Tag != "!!str" {
		if n, err := strconv.Atoi(str); err == nil && old.Tag == "!!int" {
			v = n
		}
	}
	if err := setKeys(s.Doc, keys, toNode(v, old)); err != nil {
		return editErrorf("%s: %v", file, err)
	}
	f.Sync(s)
	e.touch(file)
	return nil
}

// describe renders a pattern for messages ("<image.repository>:<image.tag>").
func (vp *valuesPattern) describe() string {
	var b strings.Builder
	for i, k := range vp.keys {
		b.WriteString(vp.parts[i] + "<" + k + ">")
	}
	b.WriteString(vp.parts[len(vp.parts)-1])
	return b.String()
}

// ---------------------------------------------------------------------------
// Objects

// sourceDir is where new manifests of a source go.
func (p *Project) sourceDir(src model.KubeSource) string {
	pth := cleanRel(src.Path)
	if src.Kind == model.KubeManifests && p.fs.exists(pth) && !p.fs.isDir(pth) {
		return dirOf(pth)
	}
	return pth
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// AddObjects adds YAML documents (one or more objects) to source si:
// appended to file when given (a manifest file of the source), else
// written to a new file named after the first object; a Kustomize
// source's new file is listed in its kustomization's resources, a Helm
// source's goes to templates/. It returns the file written.
func (e *Editor) AddObjects(si int, yamlText, file string) (string, []*Object, error) {
	r := e.p.sourceAt(si)
	if r == nil {
		return "", nil, &EditError{Msg: fmt.Sprintf("source %d not found", si), NotFound: true}
	}
	pf := ParseFile("new.yaml", []byte(yamlText))
	if errs := pf.Errors(); len(errs) > 0 {
		return "", nil, editErrorf("line %d: %s", errs[0].Line, errs[0].Message)
	}
	objs, notObj := objectsOf(pf)
	if len(objs) == 0 || len(notObj) > 0 {
		return "", nil, editErrorf("every document needs apiVersion, kind and metadata.name")
	}
	for _, o := range objs {
		if o.Name == "" {
			return "", nil, editErrorf("%s has no metadata.name", o.Kind)
		}
	}
	dir := e.p.sourceDir(r.Source)
	switch r.Source.Kind {
	case model.KubeHelm:
		dir = joinRel(dir, "templates")
	case model.KubeKustomize:
		// The base gets the resource; an environment's overlay is
		// edited only when it is the source's own path.
	}
	if file == "" {
		file = joinRel(dir, strings.ToLower(objs[0].Kind)+"-"+safeName(objs[0].Name)+".yaml")
		for n := 2; e.p.fs.exists(file) || e.changed[file]; n++ {
			file = joinRel(dir, fmt.Sprintf("%s-%s-%d.yaml", strings.ToLower(objs[0].Kind), safeName(objs[0].Name), n))
		}
	} else {
		file = cleanRel(file)
		if !inside(file) {
			return "", nil, editErrorf("invalid file %q", file)
		}
	}
	f, err := e.file(file)
	if err != nil {
		if ee, ok := err.(*EditError); ok && ee.NotFound {
			f = ParseFile(file, nil)
			e.yaml[file] = f
		} else {
			return "", nil, err
		}
	}
	isNew := len(f.Docs()) == 0
	f.Append(yamlText)
	e.touch(file)
	if r.Source.Kind == model.KubeKustomize && isNew {
		if err := e.addKustResource(cleanRel(r.Source.Path), file); err != nil {
			return "", nil, err
		}
	}
	if r.Source.Kind == model.KubeManifests && !e.p.fs.isDir(cleanRel(r.Source.Path)) && file != cleanRel(r.Source.Path) {
		return "", nil, editErrorf("this manifests source is the single file %s: add the objects to it (file) or point the source at its folder", r.Source.Path)
	}
	return file, objs, nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return '-'
	}, s)
}

func (p *Project) sourceAt(i int) *Rendered {
	for _, r := range p.Sources {
		if r.Index == i {
			return r
		}
	}
	return nil
}

// addKustResource lists file in the resources of dir's kustomization.
func (e *Editor) addKustResource(dir, file string) error {
	kf := e.p.kustomizationFile(dir)
	if kf == "" {
		return &EditError{Msg: dir + " has no kustomization.yaml", NotFound: true}
	}
	f, err := e.file(kf)
	if err != nil {
		return err
	}
	docs := f.Docs()
	if len(docs) == 0 {
		return editErrorf("%s is empty", kf)
	}
	s := docs[0]
	rel := relDir(dir, file)
	if dir != "." && !strings.HasPrefix(file, dir+"/") {
		rel = relPath(dir, file)
	}
	if err := s.Doc.AppendItem([]string{"resources"}, compose.Str(rel)); err != nil {
		return editErrorf("%v", err)
	}
	f.Sync(s)
	e.touch(kf)
	return nil
}

// relPath is target relative to dir with ".." steps.
func relPath(dir, target string) string {
	up := ""
	for dir != "." && !strings.HasPrefix(target, dir+"/") {
		dir = dirOf(dir)
		up += "../"
	}
	return up + relDir(dir, target)
}

// RemoveObject deletes o's document from its file; an emptied file is
// deleted and dropped from its kustomization's resources. A Helm object
// can only go with its template file, when that file renders nothing else.
func (e *Editor) RemoveObject(o *Object) error {
	r := e.p.Source(o)
	if r == nil {
		return &EditError{Msg: o.ID + " not found", NotFound: true}
	}
	if o.generated {
		return editErrorf("%s is generated by %s: remove its generator entry there", o.ID, o.File)
	}
	if r.Source.Kind == model.KubeHelm {
		others := 0
		for _, x := range r.Objects {
			if x != o && x.File == o.File {
				others++
			}
		}
		if others > 0 || o.Template == "" || !strings.HasPrefix(o.Template, "templates/") {
			return editErrorf("%s is rendered by %s together with other objects (or from a subchart): remove it there in code, or turn it off with its values", o.ID, o.File)
		}
		e.removed[o.File], e.changed[o.File] = true, true
		return nil
	}
	if o.Doc < 0 {
		return editErrorf("%s can't be located in %s", o.ID, o.File)
	}
	f, s, err := e.segment(o.File, o.Doc)
	if err != nil {
		return err
	}
	f.Remove(s)
	e.touch(o.File)
	if len(f.Docs()) > 0 || strings.TrimSpace(stripComments(string(f.Bytes()))) != "" {
		return nil
	}
	e.removed[o.File] = true
	if r.kust != nil {
		for _, n := range r.kust.Nodes {
			for i, it := range anyStrings(n.Data["resources"]) {
				if cleanRel(n.Dir+"/"+it) == o.File || n.Dir == "." && cleanRel(it) == o.File {
					kf, ks, err := e.segment(n.File, 0)
					if err != nil {
						return err
					}
					if err := ks.Doc.DeleteItem([]string{"resources"}, i, false); err != nil {
						return editErrorf("%v", err)
					}
					kf.Sync(ks)
					e.touch(n.File)
				}
			}
		}
	}
	return nil
}

func stripComments(s string) string {
	var b strings.Builder
	for l := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}
