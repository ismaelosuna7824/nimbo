package kube

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Node is a yaml node (go.yaml.in/yaml/v4).
type Node = yaml.Node

// Segment is one document of a multi-document YAML file.
type Segment struct {
	// Start is the 0-based line of the document's first line in the file
	// (after its "---" separator); End is exclusive.
	Start, End int
	// Doc is the parsed document (nil when it has a syntax error).
	Doc *compose.Doc
	// Err is the document's syntax error (lines are file lines).
	Err *compose.SyntaxError
}

// File is a multi-document YAML file split into documents, edited in
// place document by document (see Bytes).
type File struct {
	Path     string
	lines    []string
	crlf     bool
	Segments []*Segment
}

var docSep = regexp.MustCompile(`^(---|\.\.\.)(\s.*)?$`)

// ParseFile splits a YAML stream into documents at "---" lines (block
// scalars can't hold a column-0 "---") and parses each one. Empty and
// comment-only documents have a nil Doc Root.
func ParseFile(rel string, src []byte) *File {
	text := string(src)
	f := &File{Path: rel, crlf: strings.Contains(text, "\r\n")}
	if f.crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	text = strings.TrimPrefix(text, "\ufeff")
	f.lines = strings.Split(text, "\n")
	start := 0
	for i, l := range f.lines {
		if docSep.MatchString(l) {
			f.add(start, i)
			start = i + 1
		}
	}
	f.add(start, len(f.lines))
	return f
}

func (f *File) add(start, end int) {
	s := &Segment{Start: start, End: end}
	s.parse(f)
	f.Segments = append(f.Segments, s)
}

func (s *Segment) parse(f *File) {
	s.Doc, s.Err = nil, nil
	d, err := compose.ParseDoc(f.Path, []byte(strings.Join(f.lines[s.Start:s.End], "\n")))
	if err != nil {
		var se *compose.SyntaxError
		if !errors.As(err, &se) {
			se = &compose.SyntaxError{File: f.Path, Message: err.Error()}
		}
		cp := *se
		if cp.Line > 0 {
			cp.Line += s.Start
		} else {
			cp.Line = s.Start + 1
		}
		s.Err = &cp
		return
	}
	s.Doc = d
}

// Docs are the documents holding a mapping, in order.
func (f *File) Docs() []*Segment {
	var out []*Segment
	for _, s := range f.Segments {
		if s.Doc != nil && s.Doc.Root != nil {
			out = append(out, s)
		}
	}
	return out
}

// Errors are the syntax errors of the file's documents.
func (f *File) Errors() []*compose.SyntaxError {
	var out []*compose.SyntaxError
	for _, s := range f.Segments {
		if s.Err != nil {
			out = append(out, s.Err)
		}
	}
	return out
}

// Sync writes a segment's edited document back into the file's lines.
func (f *File) Sync(s *Segment) {
	text := strings.ReplaceAll(string(s.Doc.Bytes()), "\r\n", "\n")
	repl := strings.Split(text, "\n")
	delta := len(repl) - (s.End - s.Start)
	lines := make([]string, 0, len(f.lines)+delta)
	lines = append(lines, f.lines[:s.Start]...)
	lines = append(lines, repl...)
	lines = append(lines, f.lines[s.End:]...)
	f.lines = lines
	s.End += delta
	shift := false
	for _, o := range f.Segments {
		if shift {
			o.Start += delta
			o.End += delta
		}
		if o == s {
			shift = true
		}
	}
}

// Remove deletes a document with one separator next to it, and parses the
// file again (Segments are replaced).
func (f *File) Remove(s *Segment) {
	i := -1
	for j, o := range f.Segments {
		if o == s {
			i = j
		}
	}
	if i < 0 {
		return
	}
	from, to := s.Start, s.End // lines [from, to)
	switch {
	case i+1 < len(f.Segments):
		to++ // the separator after it (line s.End)
	case from > 0:
		from-- // the separator before the last document
	}
	f.lines = append(f.lines[:from:from], f.lines[min(to, len(f.lines)):]...)
	if len(f.lines) == 0 {
		f.lines = []string{""}
	}
	f.Segments = ParseFile(f.Path, f.Bytes()).Segments
}

// Append adds documents (YAML text, may hold several) at the end.
func (f *File) Append(text string) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	last := len(f.lines)
	for last > 0 && strings.TrimSpace(f.lines[last-1]) == "" {
		last--
	}
	head := f.lines[:last]
	if last > 0 && !docSep.MatchString(f.lines[last-1]) {
		head = append(head, "---")
	}
	if strings.HasPrefix(text, "---\n") {
		text = text[4:]
	}
	f.lines = append(head, strings.Split(text, "\n")...)
	f.lines = append(f.lines, "")
	re := ParseFile(f.Path, f.Bytes())
	f.Segments = re.Segments
}

// Bytes is the current source.
func (f *File) Bytes() []byte {
	text := strings.Join(f.lines, "\n")
	if f.crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return []byte(text)
}

// Line is the file line (1-based) of a document-relative line.
func (s *Segment) Line(docLine int) int { return s.Start + docLine }

// ---------------------------------------------------------------------------
// Objects from documents

// isManifestFile reports whether a file name can hold manifests.
func isManifestFile(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

// objectsOf returns the Kubernetes objects of a parsed file's documents
// (kind: List expanded). notObject counts mapping documents without
// apiVersion/kind.
func objectsOf(f *File) (objs []*Object, notObject []*Segment) {
	for di, s := range f.Docs() {
		var m map[string]any
		if err := s.Doc.Root.Decode(&m); err != nil {
			notObject = append(notObject, s)
			continue
		}
		m = normalize(m).(map[string]any)
		api, kind := str(m, "apiVersion"), str(m, "kind")
		if api == "" || kind == "" {
			notObject = append(notObject, s)
			continue
		}
		if strings.HasSuffix(kind, "List") && api == "v1" {
			_, items := s.Doc.Get("items")
			for i, it := range list(m["items"]) {
				im, ok := it.(map[string]any)
				if !ok {
					continue
				}
				o := newObject(im, nil)
				if items != nil && i < len(items.Content) {
					o.Node = items.Content[i]
				}
				o.File, o.Line, o.Doc = f.Path, s.Start+1, di
				if o.Node != nil {
					o.Line = s.Line(o.Node.Line)
				}
				objs = append(objs, o)
			}
			continue
		}
		o := newObject(m, s.Doc.Root)
		o.File, o.Line, o.Doc = f.Path, firstContentLine(f, s), di
		objs = append(objs, o)
	}
	return objs, notObject
}

// firstContentLine is the 1-based line of the document's first key.
func firstContentLine(f *File, s *Segment) int {
	if s.Doc != nil && s.Doc.Root != nil && len(s.Doc.Root.Content) > 0 {
		return s.Line(s.Doc.Root.Content[0].Line)
	}
	return s.Start + 1
}

func newObject(m map[string]any, n *Node) *Object {
	return &Object{
		APIVersion: str(m, "apiVersion"), Kind: str(m, "kind"),
		Name: str(m, "metadata", "name"), Namespace: str(m, "metadata", "namespace"),
		Obj: m, Node: n, Doc: -1,
	}
}

// normalize turns yaml-decoded values into JSON-like ones (map[string]any
// keys, ints as int).
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		for i := range t {
			t[i] = normalize(t[i])
		}
		return t
	case int64:
		return int(t)
	case uint64:
		return int(t)
	}
	return v
}

// renderManifests reads a manifests source (or the path its environment
// maps to): one file, or every YAML file below a directory except
// kustomization roots, charts and files holding no Kubernetes object
// (compose files, values files, ...).
func (p *Project) renderManifests(r *Rendered, env string) {
	src := cleanRel(r.Source.Path)
	if env != "" {
		if e := p.envPaths(r.Source)[env]; e != "" {
			src, r.Env = cleanRel(e), e
		}
	}
	var paths []string
	switch {
	case p.fs.exists(src) && src != ".":
		paths = []string{src}
	case p.fs.isDir(src):
		// Like `kubectl apply -f <dir>`: the folder's own files only.
		for _, f := range p.fs.under(src) {
			if src == "." && strings.Contains(f, "/") || src != "." && strings.Contains(strings.TrimPrefix(f, src+"/"), "/") {
				continue
			}
			base := path.Base(f)
			if !isManifestFile(f) || compose.IsComposeName(base) || isKustomizationName(base) || base == "Chart.yaml" {
				continue
			}
			paths = append(paths, f)
		}
	default:
		r.Err = fmt.Errorf("%s not found", src)
		return
	}
	var out []byte
	for _, rel := range paths {
		data, err := p.fs.read(rel)
		if err != nil {
			continue
		}
		f := ParseFile(rel, data)
		objs, notObj := objectsOf(f)
		errs := f.Errors()
		if len(objs) == 0 && len(errs) == 0 {
			continue // not a manifest file
		}
		r.Files = append(r.Files, rel)
		for _, se := range errs {
			p.diag(model.SeverityError, "k8s_yaml", rel, se.Line, fmt.Sprintf("%s:%d: %s", rel, se.Line, se.Message), "")
		}
		for _, s := range notObj {
			p.diag(model.SeverityWarning, "k8s_object", rel, firstContentLine(f, s),
				fmt.Sprintf("%s:%d: this document has no apiVersion/kind: it is not a Kubernetes object and is not applied", rel, firstContentLine(f, s)), "")
		}
		for _, o := range objs {
			o.Source = r.Index
			if o.Name == "" && str(o.Obj, "metadata", "generateName") == "" {
				p.diag(model.SeverityError, "k8s_object", rel, o.Line, fmt.Sprintf("%s:%d: %s has no metadata.name", rel, o.Line, o.Kind), "")
				continue
			}
			r.Objects = append(r.Objects, o)
		}
		out = appendDocs(out, data)
	}
	r.YAML = out
}

func appendDocs(out, data []byte) []byte {
	if len(out) > 0 {
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, "---\n"...)
	}
	return append(out, data...)
}

func underAny(f string, dirs map[string]bool) bool {
	for d := range dirs {
		if strings.HasPrefix(f, d+"/") {
			return true
		}
	}
	return false
}

// errFile and errLine locate a render error.
func errFile(r *Rendered) string {
	var le *locatedError
	if errors.As(r.Err, &le) {
		return le.file
	}
	return ""
}

func errLine(err error) int {
	var le *locatedError
	if errors.As(err, &le) {
		return le.line
	}
	return 0
}

// locatedError is a render error with a file:line.
type locatedError struct {
	file string
	line int
	err  error
}

func (e *locatedError) Error() string {
	if e.line > 0 {
		return e.file + ":" + strconv.Itoa(e.line) + ": " + e.err.Error()
	}
	return e.file + ": " + e.err.Error()
}

func (e *locatedError) Unwrap() error { return e.err }
