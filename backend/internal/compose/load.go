// Package compose reads, checks and edits the docker-compose projects of
// docker workflows: compose files (with overrides, include, extends,
// profiles, anchors and .env interpolation) and the Dockerfiles they build.
// compose-go (the reference loader docker compose uses) computes the
// effective model; yaml node trees locate every declaration (file:line) and
// drive in-place edits that keep comments, order and quoting.
package compose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/interpolation"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/schema"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/sirupsen/logrus"
	"go.yaml.in/yaml/v4"
)

// compose-go reports unset variables and obsolete attributes through
// logrus on stderr; Nimbo turns them into diagnostics instead.
func init() { logrus.SetLevel(logrus.ErrorLevel) }

// DefaultFile is the compose file of a new docker workflow.
const DefaultFile = "compose.yaml"

// composeNames are the default compose file names, in docker compose's
// lookup order, and their override files.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// DefaultFiles picks the compose files docker compose would read in a
// directory holding names: the first default name and its override file.
func DefaultFiles(has func(name string) bool) []string {
	for _, n := range composeNames {
		if has(n) {
			files := []string{n}
			ext := path.Ext(n)
			if o := strings.TrimSuffix(n, ext) + ".override" + ext; has(o) {
				files = append(files, o)
			}
			return files
		}
	}
	return nil
}

// IsComposeName reports whether base is a compose file name (default
// names, their overrides, and compose.<x>.yaml / docker-compose.<x>.yml).
func IsComposeName(base string) bool {
	b := strings.ToLower(base)
	if !strings.HasSuffix(b, ".yaml") && !strings.HasSuffix(b, ".yml") {
		return false
	}
	return strings.HasPrefix(b, "compose.") || strings.HasPrefix(b, "docker-compose.") || b == "compose.yaml" || b == "compose.yml"
}

// IsDockerfileName reports whether base names a Dockerfile.
func IsDockerfileName(base string) bool {
	b := strings.ToLower(base)
	return b == "dockerfile" || strings.HasPrefix(b, "dockerfile.") || strings.HasSuffix(b, ".dockerfile") || b == "containerfile"
}

// Project is a compose project read from a directory.
type Project struct {
	Dir   string
	Files []string
	// ProjectDir is docker compose's project directory, a slash path
	// relative to Dir: the folder of the first compose file. Relative paths
	// of the compose files and the .env file resolve against it, so a
	// compose file at docker/compose.yaml may build ../services/api.
	ProjectDir string
	Profiles   []string
	// Docs are the compose files read, by slash path relative to Dir:
	// Files, then the files they include or extend from, in Order.
	Docs  map[string]*Doc
	Order []string
	// Env is the project's .env.
	Env map[string]string
	// Model is the effective project docker compose runs (nil when the
	// files can't be loaded; Diagnostics say why). Every service is in
	// Model.Services; services of inactive profiles are listed in Inactive.
	Model    *types.Project
	Inactive map[string]bool
	// Dockerfiles parsed, by slash path relative to Dir.
	Dockerfiles map[string]*Dockerfile
	Diagnostics []model.Diagnostic
	Unresolved  []string
	Name        string
	read        func(rel string) ([]byte, error)
}

// Options tune Open.
type Options struct {
	// Content overrides files (slash paths relative to the dir) with unsaved
	// content; a nil value marks a deleted file.
	Content map[string][]byte
}

// Open reads the compose project of dir. It never fails: problems are
// diagnostics (file:line), and the model is nil when the files can't be
// loaded at all.
func Open(ctx context.Context, dir string, files, profiles []string, opts Options) *Project {
	p := &Project{
		Dir: dir, Files: files, ProjectDir: ProjectDir(files), Profiles: profiles,
		Docs: map[string]*Doc{}, Env: map[string]string{}, Inactive: map[string]bool{},
		Dockerfiles: map[string]*Dockerfile{},
	}
	p.read = func(rel string) ([]byte, error) {
		if data, ok := opts.Content[rel]; ok {
			if data == nil {
				return nil, fs.ErrNotExist
			}
			return data, nil
		}
		if !inside(rel) {
			return nil, fmt.Errorf("%s is outside the project", rel)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		return root.ReadFile(filepath.FromSlash(rel))
	}
	if len(files) == 0 {
		p.diag(model.SeverityError, "dk_load", "", 0, "", "The workflow has no compose file", "Add a compose.yaml (Code tab) or create the workflow again from a template.")
		return p
	}
	envFile := path.Join(p.ProjectDir, ".env")
	if data, err := p.read(envFile); err == nil {
		env, err := dotenv.UnmarshalBytesWithLookup(data, nil)
		if err != nil {
			p.diag(model.SeverityError, "dk_env_syntax", envFile, envErrLine(err), "", fmt.Sprintf("%s: %v", envFile, err), "Write one KEY=value per line.")
		} else {
			p.Env = env
		}
	}
	ok := true
	for _, f := range files {
		if !p.readDoc(f, true) {
			ok = false
		}
	}
	if !ok {
		return p
	}
	// Order grows while included files are read: index loop on purpose.
	for i := 0; i < len(p.Order); i++ {
		p.followIncludes(p.Order[i])
	}
	p.Unresolved = p.unresolved()
	p.validateSchema()
	p.load(ctx)
	if p.Model != nil {
		p.readDockerfiles()
		p.check()
	}
	return p
}

// ProjectDir is the project directory of a compose file list: the folder
// of the first file ("." for none or a file at the root), as docker compose
// picks it.
func ProjectDir(files []string) string {
	if len(files) == 0 {
		return "."
	}
	return path.Dir(path.Clean(files[0]))
}

// inside reports whether the slash path rel stays inside the project.
func inside(rel string) bool {
	return fs.ValidPath(rel) && rel != "."
}

// composeRel turns a slash path relative to Dir into the path the compose
// files would write: relative to the project directory.
func (p *Project) composeRel(rel string) string {
	if p.ProjectDir == "." {
		return rel
	}
	r, err := filepath.Rel(filepath.FromSlash(p.ProjectDir), filepath.FromSlash(rel))
	if err != nil {
		return rel
	}
	return filepath.ToSlash(r)
}

func (p *Project) diag(sev model.Severity, code, file string, line int, node, msg, fix string) {
	d := model.Diagnostic{Severity: sev, Code: code, Message: msg, File: file, Line: line, NodeID: node, Fix: fix}
	if r, ok := ruleByCode[code]; ok {
		d.Detail, d.Docs = r.Why, r.Docs
	}
	p.Diagnostics = append(p.Diagnostics, d)
}

// readDoc parses a compose file; listed: one of Files (a missing one is an
// error), else included.
func (p *Project) readDoc(rel string, listed bool) bool {
	if _, done := p.Docs[rel]; done {
		return true
	}
	data, err := p.read(rel)
	if err != nil {
		p.diag(model.SeverityError, "dk_load", rel, 0, "", fmt.Sprintf("%s can't be read: %v", rel, trimPathErr(err)), "Create the file or remove it from the compose files list.")
		return false
	}
	d, err := ParseDoc(rel, data)
	if err != nil {
		var se *SyntaxError
		if errors.As(err, &se) {
			p.diag(model.SeverityError, "dk_yaml", rel, se.Line, "", fmt.Sprintf("%s: %s", rel, se.Message), "Fix the YAML syntax (indentation, a missing ':' or quote).")
		}
		return false
	}
	p.Docs[rel] = d
	p.Order = append(p.Order, rel)
	if p.Name == "" && d.Root != nil {
		if _, v := d.Get("name"); v != nil && v.Kind == yaml.ScalarNode {
			p.Name = v.Value
		}
	}
	return true
}

func trimPathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// followIncludes reads the files rel includes and the files its services
// extend from.
func (p *Project) followIncludes(rel string) {
	d := p.Docs[rel]
	base := path.Dir(rel)
	join := func(f string) string { return path.Clean(path.Join(base, f)) }
	if _, inc := d.Get("include"); inc != nil && inc.Kind == yaml.SequenceNode {
		for _, it := range inc.Content {
			var paths []*yaml.Node
			switch it.Kind {
			case yaml.ScalarNode:
				paths = append(paths, it)
			case yaml.MappingNode:
				if _, pv := mapGet(it, "path"); pv != nil {
					if pv.Kind == yaml.SequenceNode {
						paths = append(paths, pv.Content...)
					} else {
						paths = append(paths, pv)
					}
				}
			}
			for _, pn := range paths {
				f := join(p.interpolate(pn.Value))
				if !inside(f) {
					p.diag(model.SeverityError, "dk_outside_project", rel, pn.Line, "", fmt.Sprintf("include %s points outside the workflow files", pn.Value), "Copy the included file into the workflow and include it with a relative path.")
					continue
				}
				p.readDoc(f, false)
			}
		}
	}
	if _, svcs := d.Get("services"); svcs != nil && svcs.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(svcs.Content); i += 2 {
			ext := mergedGet(svcs.Content[i+1], "extends")
			if ext == nil || ext.Kind != yaml.MappingNode {
				continue
			}
			if _, fv := mapGet(ext, "file"); fv != nil {
				f := join(p.interpolate(fv.Value))
				if !inside(f) {
					p.diag(model.SeverityError, "dk_outside_project", rel, fv.Line, "", fmt.Sprintf("extends file %s points outside the workflow files", fv.Value), "Copy the file into the workflow and reference it with a relative path.")
					continue
				}
				p.readDoc(f, false)
			}
		}
	}
}

// lookup resolves interpolation variables from .env.
func (p *Project) lookup(name string) (string, bool) {
	v, ok := p.Env[name]
	return v, ok
}

func (p *Project) interpolate(s string) string {
	out, err := template.Substitute(s, p.lookup)
	if err != nil {
		return s
	}
	return out
}

// varRefRe finds ${NAME...} and $NAME references ($$ escapes).
var varRefRe = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)([:]?[-?+])?[^}]*\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// unresolved lists the variables the compose files use without a value
// and without a default, and reports each first use.
func (p *Project) unresolved() []string {
	var names []string
	for _, rel := range p.Order {
		d := p.Docs[rel]
		var walk func(n *yaml.Node, path []string)
		walk = func(n *yaml.Node, path []string) {
			switch n.Kind {
			case yaml.ScalarNode:
				for _, m := range varRefRe.FindAllStringSubmatch(n.Value, -1) {
					name := m[1] + m[3]
					if name == "" {
						continue
					}
					op := m[2]
					if _, set := p.Env[name]; set || op == ":-" || op == "-" || op == ":+" || op == "+" {
						continue
					}
					if !slices.Contains(names, name) {
						names = append(names, name)
						sev, fix := model.SeverityWarning, fmt.Sprintf("Add %s=<value> to .env, or give it a default: ${%s:-value}.", name, name)
						msg := fmt.Sprintf("${%s} has no value: .env does not set it and it has no default (it is empty when Nimbo runs the project)", name)
						if op == "?" || op == ":?" {
							sev = model.SeverityError
							msg = fmt.Sprintf("${%s} is required but has no value in .env", name)
						}
						p.diag(sev, "dk_unresolved_var", rel, n.Line, nodeForPath(path), msg, fix)
					}
				}
			case yaml.MappingNode:
				for i := 0; i+1 < len(n.Content); i += 2 {
					walk(n.Content[i+1], append(path, n.Content[i].Value))
				}
			case yaml.SequenceNode:
				for _, c := range n.Content {
					walk(c, path)
				}
			}
		}
		if d.Root != nil {
			walk(d.Root, nil)
		}
	}
	return names
}

// nodeForPath maps a YAML path to the graph node it belongs to.
func nodeForPath(path []string) string {
	if len(path) < 2 {
		return ""
	}
	switch path[0] {
	case "services":
		return ServiceID(path[1])
	case "networks":
		return "network:" + path[1]
	case "volumes":
		return "volume:" + path[1]
	case "configs":
		return "config:" + path[1]
	case "secrets":
		return "secret:" + path[1]
	}
	return ""
}

// ServiceID is the graph node ID of a service.
func ServiceID(name string) string { return model.ComposeServiceID(name) }

var schemaPathRe = regexp.MustCompile(`^(?:validating [^:]+: )?([A-Za-z0-9_.\-]+) `)

// validateSchema checks every compose file on its own against the compose
// specification schema (after interpolation), reporting the most specific
// error of each file at its line.
func (p *Project) validateSchema() {
	for _, rel := range p.Order {
		d := p.Docs[rel]
		if d.Root == nil {
			continue
		}
		var raw map[string]any
		if err := yaml.Unmarshal(d.Bytes(), &raw); err != nil || raw == nil {
			continue
		}
		cfg, err := interpolation.Interpolate(raw, interpolation.Options{LookupValue: p.lookup})
		if err != nil {
			p.diag(model.SeverityError, "dk_schema", rel, 0, "", fmt.Sprintf("%s: %v", rel, err), "")
			continue
		}
		if err := schema.Validate(cfg); err != nil {
			msg := err.Error()
			line := 1
			node := ""
			if m := schemaPathRe.FindStringSubmatch(msg); m != nil {
				parts := strings.Split(m[1], ".")
				line = d.LocatePath(parts)
				node = nodeForPath(parts)
			}
			if path, ok := strings.CutSuffix(msg, " false schema"); ok {
				// additionalProperties: false
				msg = path + ": unknown attribute"
			}
			p.diag(model.SeverityError, "dk_schema", rel, line, node, fmt.Sprintf("%s: %s", rel, msg), "Check the attribute against the Compose specification (https://docs.docker.com/reference/compose-file/).")
		}
	}
}

// load computes the effective model with compose-go. Consistency checks
// are ours (they report every problem with its line instead of the first).
func (p *Project) load(ctx context.Context) {
	details := types.ConfigDetails{WorkingDir: filepath.Join(p.Dir, filepath.FromSlash(p.ProjectDir)), Environment: types.Mapping(p.Env)}
	for _, f := range p.Files {
		details.ConfigFiles = append(details.ConfigFiles, types.ConfigFile{
			Filename: filepath.Join(p.Dir, filepath.FromSlash(f)),
			Content:  p.Docs[f].Bytes(),
		})
	}
	escapes := slices.ContainsFunc(p.Diagnostics, func(d model.Diagnostic) bool { return d.Code == "dk_outside_project" })
	proj, err := loader.LoadWithContext(ctx, details, func(o *loader.Options) {
		o.SetProjectName("nimbo", true)
		o.SkipValidation = true
		o.SkipConsistencyCheck = true
		o.SkipResolveEnvironment = true
		o.SkipResolveLabels = true
		o.SkipInclude = escapes
		o.SkipExtends = escapes
		o.Interpolate = &interpolation.Options{LookupValue: p.lookup, TypeCastMapping: nil, Substitute: template.Substitute}
		o.Profiles = []string{"*"}
	})
	if err != nil {
		if slices.ContainsFunc(p.Diagnostics, func(d model.Diagnostic) bool { return d.Severity == model.SeverityError }) {
			return // the schema or variable error found already explains it
		}
		msg := strings.ReplaceAll(err.Error(), p.Dir+string(filepath.Separator), "")
		file, line := p.locateError(msg)
		p.diag(model.SeverityError, "dk_load", file, line, "", msg, "docker compose would refuse this project: fix the attribute the message names.")
		return
	}
	for _, s := range proj.Services {
		if len(s.Profiles) > 0 && !s.HasProfile(p.Profiles) {
			p.Inactive[s.Name] = true
		}
	}
	p.Model = proj
}

var quotedNameRe = regexp.MustCompile(`services\.([A-Za-z0-9_.\-]+)|service "([^"]+)"|service ([A-Za-z0-9_.\-]+)`)

// locateError finds the file and line a loader message is about.
func (p *Project) locateError(msg string) (string, int) {
	if m := quotedNameRe.FindStringSubmatch(msg); m != nil {
		name := m[1] + m[2] + m[3]
		name, _, _ = strings.Cut(name, ".")
		if file, line := p.ServiceLine(name); file != "" {
			return file, line
		}
	}
	if m := yamlLineRe.FindStringSubmatch(msg); m != nil {
		var line int
		fmt.Sscan(m[1], &line)
		return p.Files[0], line
	}
	return p.Files[0], 0
}

// ServiceLine is where a service is first declared.
func (p *Project) ServiceLine(name string) (string, int) {
	for _, rel := range p.Order {
		if l := p.Docs[rel].Line("services", name); l > 0 {
			return rel, l
		}
	}
	return "", 0
}

// declLine is where a top-level element (networks.x, volumes.x) is
// declared.
func (p *Project) declLine(section, name string) (string, int) {
	for _, rel := range p.Order {
		if l := p.Docs[rel].Line(section, name); l > 0 {
			return rel, l
		}
	}
	return "", 0
}

// fieldLine is where a service attribute is set (the last file setting it
// wins, like the merge), else the service's line.
func (p *Project) fieldLine(svc string, field ...string) (string, int) {
	for i := len(p.Order) - 1; i >= 0; i-- {
		rel := p.Order[i]
		_, sv := p.Docs[rel].Get("services", svc)
		if sv == nil {
			continue
		}
		if len(field) == 0 {
			continue
		}
		if v := mergedGet(sv, field[0]); v != nil {
			n := v
			for _, f := range field[1:] {
				if m := mergedGet(n, f); m != nil {
					n = m
				}
			}
			return rel, n.Line
		}
	}
	return p.ServiceLine(svc)
}

// FieldLine is where a service attribute is set (file, 1-based line), else
// where the service is declared.
func (p *Project) FieldLine(svc string, field ...string) (string, int) {
	return p.fieldLine(svc, field...)
}

// refLine is where a service attribute names target: the depends_on /
// networks key, the list item ("db", "db:alias", "data:/var/lib/x") or the
// long-syntax item whose source is target; else the attribute's line.
func (p *Project) refLine(svc, field, target string) (string, int) {
	for i := len(p.Order) - 1; i >= 0; i-- {
		rel := p.Order[i]
		_, sv := p.Docs[rel].Get("services", svc)
		v := Resolve(mergedGet(sv, field))
		if v == nil {
			continue
		}
		switch v.Kind {
		case yaml.MappingNode:
			for j := 0; j+1 < len(v.Content); j += 2 {
				if v.Content[j].Value == target {
					return rel, v.Content[j].Line
				}
			}
		case yaml.SequenceNode:
			for _, it := range v.Content {
				it = Resolve(it)
				switch it.Kind {
				case yaml.ScalarNode:
					name, _, _ := strings.Cut(it.Value, ":")
					if name == target || it.Value == target {
						return rel, it.Line
					}
				case yaml.MappingNode:
					if src := mergedGet(it, "source"); src != nil && src.Value == target {
						return rel, it.Line
					}
				}
			}
		case yaml.ScalarNode:
			return rel, v.Line
		}
	}
	return p.fieldLine(svc, field)
}

// rel turns a loader path (absolute after path resolution; a relative one
// is relative to the project directory) into a slash path relative to Dir.
func (p *Project) rel(abs string) string {
	if abs == "" {
		return ""
	}
	if !filepath.IsAbs(abs) {
		return path.Join(p.ProjectDir, filepath.ToSlash(abs))
	}
	r, err := filepath.Rel(p.Dir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

// readDockerfiles parses the Dockerfile of every service that builds one.
func (p *Project) readDockerfiles() {
	for _, s := range p.Model.Services {
		if s.Build == nil || s.Build.DockerfileInline != "" {
			continue
		}
		rel := p.dockerfilePath(s)
		if rel == "" {
			continue
		}
		if _, done := p.Dockerfiles[rel]; done {
			continue
		}
		data, err := p.read(rel)
		if err != nil {
			continue // reported by the checks
		}
		df, problems := ParseDockerfile(data)
		p.Dockerfiles[rel] = df
		for _, pr := range problems {
			p.diag(model.SeverityError, "dk_dockerfile_syntax", rel, pr.Line, ServiceID(s.Name), fmt.Sprintf("%s:%d: %s", rel, pr.Line, pr.Message), "Fix the instruction (see https://docs.docker.com/reference/dockerfile/).")
		}
	}
}

// contextPath is a service's build context relative to Dir
// ("" for remote contexts: git URLs, http).
func (p *Project) contextPath(s types.ServiceConfig) string {
	if s.Build == nil {
		return ""
	}
	c := s.Build.Context
	if c == "" {
		c = "."
	}
	if strings.Contains(c, "://") || strings.HasPrefix(c, "git@") {
		return ""
	}
	return p.rel(c)
}

// dockerfilePath is the Dockerfile a service builds, relative to Dir ("" when
// remote or inline).
func (p *Project) dockerfilePath(s types.ServiceConfig) string {
	ctx := p.contextPath(s)
	if s.Build == nil || ctx == "" || s.Build.DockerfileInline != "" {
		return ""
	}
	df := s.Build.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	if filepath.IsAbs(df) {
		return p.rel(df)
	}
	return path.Clean(path.Join(ctx, filepath.ToSlash(df)))
}

// SortedServiceNames lists the effective services (inactive included).
func (p *Project) SortedServiceNames() []string {
	if p.Model == nil {
		return nil
	}
	names := make([]string, 0, len(p.Model.Services))
	for n := range p.Model.Services {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

var envErrLineRe = regexp.MustCompile(`line (\d+)`)

func envErrLine(err error) int {
	if m := envErrLineRe.FindStringSubmatch(err.Error()); m != nil {
		var n int
		fmt.Sscan(m[1], &n)
		return n
	}
	return 0
}
