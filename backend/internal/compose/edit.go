package compose

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Editor changes a project's compose files in place (see Doc). Every
// operation validates its input first; Files returns what changed.
type Editor struct {
	p       *Project
	changed map[string]bool
	// created are other project files the edits write (a secret's file).
	created map[string][]byte
	// scoped: service edits go to the active environment's override file
	// only (PatchService "scope": "environment").
	scoped bool
}

// EditError is a request the project can't apply (bad input, unknown
// names, conflicts); Conflict marks name clashes.
type EditError struct {
	Msg      string
	Conflict bool
	NotFound bool
}

func (e *EditError) Error() string { return e.Msg }

func badf(format string, a ...any) error { return &EditError{Msg: fmt.Sprintf(format, a...)} }

// Edit starts editing p. The project must have loaded (p.Model != nil).
func (p *Project) Edit() (*Editor, error) {
	if p.Model == nil {
		msg := "the compose files can't be loaded"
		for _, d := range p.Diagnostics {
			if d.Severity == model.SeverityError {
				msg += ": " + d.Message
				break
			}
		}
		return nil, &EditError{Msg: msg + "; fix them in the Code tab first", Conflict: true}
	}
	return &Editor{p: p, changed: map[string]bool{}}, nil
}

// Files returns the content of every file the edits changed.
func (e *Editor) Files() map[string][]byte {
	out := maps.Clone(e.created)
	if out == nil {
		out = map[string][]byte{}
	}
	for rel := range e.changed {
		out[rel] = e.p.Docs[rel].Bytes()
	}
	return out
}

func (e *Editor) mark(rel string) { e.changed[rel] = true }

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidName checks a service, network or volume name.
func ValidName(kind, name string) error {
	if !nameRe.MatchString(name) {
		return badf("invalid %s name %q: use letters, digits, '.', '_' and '-' (starting with a letter or digit)", kind, name)
	}
	return nil
}

// primary is the file new top-level elements go to.
func (e *Editor) primary() *Doc { return e.p.Docs[e.p.Files[0]] }

// serviceDocs are the files declaring service name, in load order.
func (e *Editor) serviceDocs(name string) []*Doc {
	var out []*Doc
	for _, rel := range e.p.Order {
		if _, v := e.p.Docs[rel].Get("services", name); v != nil {
			out = append(out, e.p.Docs[rel])
		}
	}
	return out
}

func (e *Editor) requireService(name string) error {
	if len(e.serviceDocs(name)) == 0 {
		return &EditError{Msg: fmt.Sprintf("service %q not found", name), NotFound: true}
	}
	return nil
}

// fieldDoc is the file a service attribute is edited in: the last file
// writing it (overrides win), else the first file declaring the service.
func (e *Editor) fieldDoc(svc, field string) *Doc {
	if e.scoped {
		return e.p.Docs[e.p.Override]
	}
	docs := e.serviceDocs(svc)
	for i := len(docs) - 1; i >= 0; i-- {
		_, sv := docs[i].Get("services", svc)
		if mergedGet(sv, field) != nil {
			return docs[i]
		}
	}
	return docs[0]
}

// own makes the attribute an own key of the service in d when it is only
// inherited through a merge key or written as an alias, so an edit never
// changes the anchor other services share. Mappings keep the anchor as a
// merge (<<: *x); lists are copied.
func (e *Editor) own(d *Doc, svc, field string) error {
	path := []string{"services", svc, field}
	k, v := d.Get(path...)
	if k != nil && v.Kind != yaml.AliasNode {
		return nil
	}
	var src *yaml.Node
	if k != nil {
		src = Resolve(v)
	} else {
		_, sv := d.Get("services", svc)
		src = Resolve(mergedGet(sv, field))
	}
	if src == nil {
		return nil
	}
	var next *yaml.Node
	if src.Kind == yaml.MappingNode && src.Anchor != "" {
		next = Map("<<", Raw("*"+src.Anchor))
	} else {
		next = cloneNode(src)
	}
	e.mark(d.Path)
	return d.Set(path, next)
}

func cloneNode(n *yaml.Node) *yaml.Node {
	n = Resolve(n)
	c := *n
	c.Anchor = ""
	c.Style &^= yaml.FlowStyle
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = cloneNode(ch)
	}
	if c.Kind == yaml.MappingNode || c.Kind == yaml.SequenceNode {
		if len(c.Content) == 0 {
			c.Style |= yaml.FlowStyle
		}
	}
	return &c
}

// setField writes a service attribute.
func (e *Editor) setField(svc, field string, v *yaml.Node) error {
	d := e.fieldDoc(svc, field)
	e.mark(d.Path)
	return d.Set([]string{"services", svc, field}, v)
}

// deleteDocs are the files a deletion of a service attribute edits: the
// active environment's override file alone when it sets the attribute
// (sets reports that), so the base value applies again there and the
// base files, shared by every environment, keep theirs; else every file
// declaring the service.
func (e *Editor) deleteDocs(svc string, sets func(d *Doc) bool) []*Doc {
	docs := e.serviceDocs(svc)
	if o := e.p.Docs[e.p.Override]; o != nil && slices.Contains(docs, o) && sets(o) {
		return []*Doc{o}
	}
	return docs
}

// deleteField removes a service attribute from every file (only from the
// active environment's override when it sets it, see deleteDocs); an
// attribute only inherited through a merge key is reset (!reset).
func (e *Editor) deleteField(svc string, field ...string) error {
	if e.scoped {
		return e.resetInOverride(svc, append([]string{"services", svc}, field...))
	}
	path := append([]string{"services", svc}, field...)
	sets := func(d *Doc) bool {
		if k, _ := d.Get(path...); k != nil {
			return true
		}
		_, sv := d.Get("services", svc)
		return len(field) == 1 && sv != nil && mergedGet(sv, field[0]) != nil
	}
	for _, d := range e.deleteDocs(svc, sets) {
		if k, _ := d.Get(path...); k != nil {
			e.mark(d.Path)
			if err := deleteUnder(d, path); err != nil {
				return err
			}
			continue
		}
		if len(field) == 1 {
			_, sv := d.Get("services", svc)
			if sv != nil && mergedGet(sv, field[0]) != nil {
				e.mark(d.Path)
				if err := d.Set(path, Raw("!reset null")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ResetOverrides makes the active environment's override file stop
// setting attributes of service svc (named like ServiceFacts.Overrides:
// "image", "ports", "deploy.replicas"…), so the base files' values apply
// again. The base files are never touched.
func (e *Editor) ResetOverrides(svc string, attrs []string) error {
	if err := e.requireService(svc); err != nil {
		return err
	}
	d := e.p.Docs[e.p.Override]
	if d == nil {
		return badf("no environment is active: there is no override to reset")
	}
	for _, a := range attrs {
		parts := strings.Split(a, ".")
		if a == "" || len(parts) > 2 || len(parts) == 2 && parts[0] != "deploy" {
			return badf("invalid attribute %q: use a service key (image, ports…) or deploy.<key>", a)
		}
		path := append([]string{"services", svc}, parts...)
		if k, _ := d.Get(path...); k == nil {
			continue
		}
		e.mark(d.Path)
		if err := d.Delete(path, true); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Services

// ServiceSpec is a new service.
type ServiceSpec struct {
	Name        string
	Image       string
	Build       *BuildSpec
	Ports       []model.ServicePort
	Environment []EnvSpec
	Volumes     []model.ServiceMount
	Healthcheck *model.ServiceHealthcheck
	Command     string
	Entrypoint  string
	DependsOn   []model.ServiceDependency
	Restart     string
	// Init runs the command under Docker's init (tini): signals reach
	// node/python and zombies are reaped.
	Init bool
	// Labels are the service's labels (written sorted).
	Labels map[string]string
}

// BuildSpec is a service's build section.
type BuildSpec struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile,omitempty"`
	Target     string `json:"target,omitempty"`
	Starter    string `json:"starter,omitempty"`
}

// EnvSpec is one environment entry of a patch: Value nil writes a bare key
// (passed from the host), unless Secret, which keeps the stored value.
type EnvSpec struct {
	Key    string  `json:"key"`
	Value  *string `json:"value,omitempty"`
	Secret bool    `json:"secret,omitempty"`
}

// AddService declares a service in the first compose file.
func (e *Editor) AddService(spec ServiceSpec) error {
	if err := ValidName("service", spec.Name); err != nil {
		return err
	}
	if _, ok := e.p.Model.Services[spec.Name]; ok || len(e.serviceDocs(spec.Name)) > 0 {
		return &EditError{Msg: fmt.Sprintf("service %q already exists", spec.Name), Conflict: true}
	}
	if spec.Image == "" && spec.Build == nil {
		return badf("a service needs an image or a build")
	}
	svc := &yaml.Node{Kind: yaml.MappingNode}
	addKV := func(k string, v *yaml.Node) { svc.Content = append(svc.Content, Str(k), v) }
	if spec.Image != "" {
		addKV("image", Str(spec.Image))
	}
	if spec.Build != nil {
		addKV("build", buildNode(*spec.Build))
	}
	if spec.Entrypoint != "" {
		addKV("entrypoint", Str(spec.Entrypoint))
	}
	if spec.Command != "" {
		addKV("command", Str(spec.Command))
	}
	if len(spec.Ports) > 0 {
		s := Seq()
		for _, p := range spec.Ports {
			s.Content = append(s.Content, portNode(p))
		}
		addKV("ports", s)
	}
	if len(spec.Environment) > 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, ev := range spec.Environment {
			v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
			if ev.Value != nil {
				v = Str(*ev.Value)
			}
			m.Content = append(m.Content, Str(ev.Key), v)
		}
		addKV("environment", m)
	}
	if len(spec.Volumes) > 0 {
		s := Seq()
		for _, m := range spec.Volumes {
			s.Content = append(s.Content, mountNode(m))
		}
		addKV("volumes", s)
	}
	if len(spec.DependsOn) > 0 {
		addKV("depends_on", dependsNode(spec.DependsOn))
	}
	if spec.Healthcheck != nil {
		addKV("healthcheck", healthNode(*spec.Healthcheck))
	}
	if spec.Init {
		addKV("init", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
	if len(spec.Labels) > 0 {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range slices.Sorted(maps.Keys(spec.Labels)) {
			v := Str(spec.Labels[k])
			v.Style = yaml.DoubleQuotedStyle
			m.Content = append(m.Content, Str(k), v)
		}
		addKV("labels", m)
	}
	if spec.Restart != "" {
		addKV("restart", Str(spec.Restart))
	}
	d := e.primary()
	e.mark(d.Path)
	return d.Set([]string{"services", spec.Name}, svc)
}

func buildNode(b BuildSpec) *yaml.Node {
	ctx := b.Context
	if ctx == "" {
		ctx = "."
	}
	if b.Dockerfile == "" && b.Target == "" {
		return Str(ctx)
	}
	m := Map("context", Str(ctx))
	if b.Dockerfile != "" {
		m.Content = append(m.Content, Str("dockerfile"), Str(b.Dockerfile))
	}
	if b.Target != "" {
		m.Content = append(m.Content, Str("target"), Str(b.Target))
	}
	return m
}

func portNode(p model.ServicePort) *yaml.Node {
	n := Str(portText(p))
	n.Style = yaml.DoubleQuotedStyle
	return n
}

func portText(p model.ServicePort) string {
	s := p.Container
	if p.Host != "" {
		s = p.Host + ":" + s
		if p.HostIP != "" {
			s = p.HostIP + ":" + s
		}
	} else if p.HostIP != "" {
		s = p.HostIP + "::" + s
	}
	if p.Protocol != "" && p.Protocol != "tcp" {
		s += "/" + p.Protocol
	}
	return s
}

func mountNode(m model.ServiceMount) *yaml.Node {
	if m.Type == "tmpfs" || m.Source == "" {
		n := Map("type", Str(orDefault(m.Type, "volume")), "target", Str(m.Target))
		if m.ReadOnly {
			n.Content = append(n.Content, Str("read_only"), Bool(true))
		}
		return n
	}
	return Str(mountText(m))
}

// mountText is the short syntax; relative bind sources start with ./ (a
// bare name is a named volume).
func mountText(m model.ServiceMount) string {
	src := m.Source
	if m.Type == "bind" && !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "~") && !strings.HasPrefix(src, "$") {
		src = "./" + src
	}
	s := src + ":" + m.Target
	if m.ReadOnly {
		s += ":ro"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func dependsNode(deps []model.ServiceDependency) *yaml.Node {
	simple := true
	for _, d := range deps {
		if d.Condition != "" && d.Condition != "service_started" || d.Required != nil || d.Restart {
			simple = false
		}
	}
	if simple {
		s := Seq()
		for _, d := range deps {
			s.Content = append(s.Content, Str(d.Service))
		}
		return s
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, d := range deps {
		m.Content = append(m.Content, Str(d.Service), dependencyNode(d))
	}
	return m
}

func dependencyNode(d model.ServiceDependency) *yaml.Node {
	n := Map("condition", Str(orDefault(d.Condition, "service_started")))
	if d.Restart {
		n.Content = append(n.Content, Str("restart"), Bool(true))
	}
	if d.Required != nil && !*d.Required {
		n.Content = append(n.Content, Str("required"), Bool(false))
	}
	return n
}

func healthNode(h model.ServiceHealthcheck) *yaml.Node {
	if h.Disabled {
		return Map("disable", Bool(true))
	}
	cmd := Str(h.Test)
	cmd.Style = yaml.DoubleQuotedStyle
	kind := Str("CMD-SHELL")
	kind.Style = yaml.DoubleQuotedStyle
	test := Seq(kind, cmd)
	test.Style = yaml.FlowStyle
	n := Map("test", test)
	for _, kv := range [][2]string{{"interval", h.Interval}, {"timeout", h.Timeout}} {
		if kv[1] != "" {
			n.Content = append(n.Content, Str(kv[0]), Str(kv[1]))
		}
	}
	if h.Retries != nil {
		n.Content = append(n.Content, Str("retries"), Int(*h.Retries))
	}
	for _, kv := range [][2]string{{"start_period", h.StartPeriod}, {"start_interval", h.StartInterval}} {
		if kv[1] != "" {
			n.Content = append(n.Content, Str(kv[0]), Str(kv[1]))
		}
	}
	return n
}

// unchangedHealthTest is the service's healthcheck test node as written
// when the patch carries the same test (the graph shows it interpolated:
// "$${VAR}" reads "${VAR}", which written back would be interpolated by
// compose instead of the container's shell), else nil.
func (e *Editor) unchangedHealthTest(svc string, h model.ServiceHealthcheck) *yaml.Node {
	s, ok := e.p.Model.Services[svc]
	if h.Disabled || !ok || s.HealthCheck == nil || healthFacts(s.HealthCheck).Test != h.Test {
		return nil
	}
	for _, d := range slices.Backward(e.serviceDocs(svc)) {
		_, sv := d.Get("services", svc)
		if t := mergedGet(mergedGet(sv, "healthcheck"), "test"); t != nil {
			return t
		}
	}
	return nil
}

// RemoveService deletes a service and the references other services make
// to it (depends_on, links, volumes_from).
func (e *Editor) RemoveService(name string) error {
	if err := e.requireService(name); err != nil {
		return err
	}
	for _, d := range e.serviceDocs(name) {
		e.mark(d.Path)
		if err := d.Delete([]string{"services", name}, false); err != nil {
			return err
		}
	}
	return e.rewriteServiceRefs(name, "")
}

// RenameService renames a service and every reference to it.
func (e *Editor) RenameService(old, name string) error {
	if err := e.requireService(old); err != nil {
		return err
	}
	if err := ValidName("service", name); err != nil {
		return err
	}
	if len(e.serviceDocs(name)) > 0 {
		return &EditError{Msg: fmt.Sprintf("service %q already exists", name), Conflict: true}
	}
	for _, d := range e.serviceDocs(old) {
		e.mark(d.Path)
		if err := d.RenameKey([]string{"services", old}, name); err != nil {
			return err
		}
	}
	return e.rewriteServiceRefs(old, name)
}

// rewriteServiceRefs renames (name != "") or removes (name == "")
// references to service old in every service of every file.
func (e *Editor) rewriteServiceRefs(old, name string) error {
	for _, rel := range e.p.Order {
		d := e.p.Docs[rel]
		for _, svc := range mapKeys(d, "services") {
			base := []string{"services", svc}
			// depends_on: list or mapping.
			if _, v := d.Get(append(base, "depends_on")...); v != nil {
				switch v.Kind {
				case yaml.SequenceNode:
					if err := e.rewriteItems(d, append(base, "depends_on"), func(s string) (string, bool) { return name, s == old }, name); err != nil {
						return err
					}
				case yaml.MappingNode:
					if k, _ := d.Get(append(base, "depends_on", old)...); k != nil {
						e.mark(rel)
						var err error
						if name == "" {
							err = d.Delete(append(base, "depends_on", old), true)
						} else {
							err = d.RenameKey(append(base, "depends_on", old), name)
						}
						if err != nil {
							return err
						}
					}
				}
			}
			if err := e.rewriteItems(d, append(base, "links"), func(s string) (string, bool) {
				t, alias, ok := strings.Cut(s, ":")
				if t != old {
					return "", false
				}
				if ok && name != "" {
					return name + ":" + alias, true
				}
				return name, true
			}, name); err != nil {
				return err
			}
			if err := e.rewriteItems(d, append(base, "volumes_from"), func(s string) (string, bool) {
				t, mode, ok := strings.Cut(s, ":")
				if t != old {
					return "", false
				}
				if ok && name != "" {
					return name + ":" + mode, true
				}
				return name, true
			}, name); err != nil {
				return err
			}
			if name != "" {
				if _, v := d.Get(append(base, "network_mode")...); v != nil && v.Value == "service:"+old {
					e.mark(rel)
					if err := d.Set(append(base, "network_mode"), Str("service:"+name)); err != nil {
						return err
					}
				}
				if _, v := d.Get(append(base, "extends")...); v != nil {
					switch {
					case v.Kind == yaml.ScalarNode && v.Value == old:
						e.mark(rel)
						if err := d.Set(append(base, "extends"), Str(name)); err != nil {
							return err
						}
					case v.Kind == yaml.MappingNode:
						_, f := mapGet(v, "file")
						if _, sv := mapGet(v, "service"); sv != nil && sv.Value == old && f == nil {
							e.mark(rel)
							if err := d.Set(append(base, "extends", "service"), Str(name)); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// rewriteItems rewrites (or, with name "", deletes) the scalar items of
// the list at path that match.
func (e *Editor) rewriteItems(d *Doc, path []string, match func(string) (string, bool), name string) error {
	for {
		_, v := d.Get(path...)
		if v == nil || v.Kind != yaml.SequenceNode {
			return nil
		}
		done := true
		for i, it := range v.Content {
			repl, ok := match(it.Value)
			if !ok || it.Kind != yaml.ScalarNode {
				continue
			}
			e.mark(d.Path)
			if name == "" {
				if err := d.DeleteItem(path, i, true); err != nil {
					return err
				}
				done = false // indexes moved: scan again
				break
			}
			if repl != it.Value {
				if err := d.SetItem(path, i, Str(repl)); err != nil {
					return err
				}
			}
		}
		if done {
			return nil
		}
	}
}

func mapKeys(d *Doc, path ...string) []string {
	_, v := d.Get(path...)
	v = Resolve(v)
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(v.Content); i += 2 {
		out = append(out, v.Content[i].Value)
	}
	return out
}

// PatchService applies a partial update: every key present is set (null
// deletes it); lists replace the whole value, keeping the source text
// (quoting, ${VAR}) of the items that did not change. "resetOverrides"
// (a list of attributes, see ResetOverrides) goes first. "scope":
// "environment" writes every key to the active environment's override file
// only (a removed key is reset there), leaving the base files as they are.
func (e *Editor) PatchService(name string, patch map[string]json.RawMessage) error {
	if err := e.requireService(name); err != nil {
		return err
	}
	if raw, ok := patch["scope"]; ok {
		var scope string
		if err := json.Unmarshal(raw, &scope); err != nil || scope != "" && scope != "environment" {
			return badf(`scope must be "environment" (or "" for the usual files)`)
		}
		if scope == "environment" {
			if e.p.Docs[e.p.Override] == nil {
				return badf("no environment with an override file is active: switch to one first")
			}
			for k := range patch {
				if k != "scope" && !slices.Contains(scopedKeys, k) {
					return badf("%s can't be changed for one environment only: edit it in the base files", k)
				}
			}
			e.scoped = true
			defer func() { e.scoped = false }()
		}
	}
	if raw, ok := patch["resetOverrides"]; ok {
		var attrs []string
		if err := json.Unmarshal(raw, &attrs); err != nil {
			return badf("resetOverrides must be a list of attributes")
		}
		if err := e.ResetOverrides(name, attrs); err != nil {
			return err
		}
	}
	cur, _ := e.p.Model.Services[name]
	// The rename goes last so the other fields find the service.
	keys := make([]string, 0, len(patch))
	for k := range patch {
		if k != "name" && k != "resetOverrides" && k != "scope" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		raw := patch[k]
		null := string(raw) == "null"
		var err error
		switch k {
		case "image", "restart", "user", "containerName", "hostname", "workingDir", "platform":
			field := map[string]string{"containerName": "container_name", "workingDir": "working_dir"}[k]
			if field == "" {
				field = k
			}
			var s string
			if !null {
				if err := json.Unmarshal(raw, &s); err != nil {
					return badf("%s must be a string", k)
				}
			}
			if null || s == "" {
				err = e.deleteField(name, field)
			} else {
				err = e.setField(name, field, Str(s))
			}
		case "command", "entrypoint":
			if null {
				err = e.deleteField(name, k)
				break
			}
			var s string
			var list []string
			switch {
			case json.Unmarshal(raw, &s) == nil:
				err = e.setField(name, k, Str(s))
			case json.Unmarshal(raw, &list) == nil:
				n := StrSeq(list)
				n.Style = yaml.FlowStyle
				for _, it := range n.Content {
					it.Style = yaml.DoubleQuotedStyle
				}
				err = e.setField(name, k, n)
			default:
				return badf("%s must be a string (shell form) or a list of strings (exec form)", k)
			}
		case "build":
			if null {
				err = e.deleteField(name, "build")
				break
			}
			var b BuildSpec
			if err := json.Unmarshal(raw, &b); err != nil {
				return badf("build must be {context, dockerfile?, target?}")
			}
			err = e.setField(name, "build", buildNode(b))
		case "ports":
			var ports []model.ServicePort
			if !null {
				if err := json.Unmarshal(raw, &ports); err != nil {
					return badf("ports must be a list of {hostIp?, host?, container, protocol?}")
				}
			}
			for _, p := range ports {
				if p.Container == "" {
					return badf("every port needs a container port")
				}
			}
			items := make([]listItem, len(ports))
			for i, p := range ports {
				items[i] = listItem{canon: canonPort(portText(p)), node: portNode(p)}
			}
			err = e.setList(name, "ports", items, canonPortNode)
		case "volumes":
			var mounts []model.ServiceMount
			if !null {
				if err := json.Unmarshal(raw, &mounts); err != nil {
					return badf("volumes must be a list of {type?, source?, target, readOnly?}")
				}
			}
			items := make([]listItem, len(mounts))
			for i, m := range mounts {
				if m.Target == "" {
					return badf("every volume needs a target path")
				}
				if m.Type == "" {
					m.Type = "volume"
					if strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, "~") {
						m.Type = "bind"
					}
				}
				items[i] = listItem{canon: canonMount(m), node: mountNode(m)}
			}
			err = e.setList(name, "volumes", items, canonMountNode)
		case "expose", "profiles", "envFiles", "links":
			field := map[string]string{"envFiles": "env_file"}[k]
			if field == "" {
				field = k
			}
			var list []string
			if !null {
				if err := json.Unmarshal(raw, &list); err != nil {
					return badf("%s must be a list of strings", k)
				}
			}
			items := make([]listItem, len(list))
			for i, s := range list {
				items[i] = listItem{canon: s, node: Str(s)}
			}
			err = e.setList(name, field, items, func(n *yaml.Node, p *Project) string { return p.interpolate(n.Value) })
		case "networks":
			var list []string
			if !null {
				if err := json.Unmarshal(raw, &list); err != nil {
					return badf("networks must be a list of network names")
				}
			}
			err = e.setNetworks(name, list)
		case "dependsOn":
			var deps []model.ServiceDependency
			if !null {
				if err := json.Unmarshal(raw, &deps); err != nil {
					return badf("dependsOn must be a list of {service, condition?}")
				}
			}
			err = e.setDependsOn(name, deps)
		case "environment":
			var env []EnvSpec
			if !null {
				if err := json.Unmarshal(raw, &env); err != nil {
					return badf("environment must be a list of {key, value?}")
				}
			}
			err = e.setEnvironment(name, env)
		case "healthcheck":
			if null {
				err = e.deleteField(name, "healthcheck")
				break
			}
			var h model.ServiceHealthcheck
			if err := json.Unmarshal(raw, &h); err != nil || (!h.Disabled && h.Test == "") {
				return badf("healthcheck must be {test, interval?, timeout?, retries?, startPeriod?} or {disabled: true}")
			}
			n := healthNode(h)
			if src := e.unchangedHealthTest(name, h); src != nil {
				n.Content[1] = src
			}
			err = e.setField(name, "healthcheck", n)
		case "labels":
			var labels map[string]string
			if !null {
				if err := json.Unmarshal(raw, &labels); err != nil {
					return badf("labels must be an object of strings")
				}
			}
			for k := range labels {
				if k == "" || strings.ContainsAny(k, " \t\n") {
					return badf("invalid label %q: no spaces", k)
				}
			}
			err = e.setLabels(name, labels)
		case "replicas":
			if null {
				err = e.deleteDeploy(name, "replicas")
				break
			}
			var n int
			if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
				return badf("replicas must be a number ≥ 0")
			}
			err = e.setDeploy(name, []string{"replicas"}, Int(n))
		case "resources":
			if null {
				err = e.deleteDeploy(name, "resources", "limits")
				break
			}
			var r model.ServiceResources
			if err := json.Unmarshal(raw, &r); err != nil || r.Pids < 0 {
				return badf("resources must be {cpus?, memory?, pids?}")
			}
			for _, kv := range [][2]string{{"cpus", r.CPUs}, {"memory", r.Memory}} {
				if kv[1] == "" {
					err = e.deleteDeploy(name, "resources", "limits", kv[0])
				} else {
					err = e.setDeploy(name, []string{"resources", "limits", kv[0]}, Str(kv[1]))
				}
				if err != nil {
					break
				}
			}
			if err == nil && r.Pids == 0 {
				err = e.deleteDeploy(name, "resources", "limits", "pids")
			} else if err == nil {
				err = e.setDeploy(name, []string{"resources", "limits", "pids"}, Int(r.Pids))
			}
		case "logging":
			if null {
				err = e.deleteField(name, "logging")
				break
			}
			var l model.ServiceLogging
			if err := json.Unmarshal(raw, &l); err != nil {
				return badf("logging must be {driver?, maxSize?, maxFile?}")
			}
			err = e.setLogging(name, l)
		case "readOnly", "privileged", "init":
			// true sets the key; false (or null) removes it: compose's default.
			field := map[string]string{"readOnly": "read_only"}[k]
			if field == "" {
				field = k
			}
			var on bool
			if !null {
				if err := json.Unmarshal(raw, &on); err != nil {
					return badf("%s must be a boolean", k)
				}
			}
			if on {
				err = e.setField(name, field, Bool(true))
			} else {
				err = e.deleteField(name, field)
			}
		case "capDrop", "capAdd", "securityOpt", "tmpfs":
			field := map[string]string{"capDrop": "cap_drop", "capAdd": "cap_add", "securityOpt": "security_opt", "tmpfs": "tmpfs"}[k]
			var list []string
			if !null {
				if err := json.Unmarshal(raw, &list); err != nil {
					return badf("%s must be a list of strings", k)
				}
			}
			items := make([]listItem, 0, len(list))
			for _, s := range list {
				if s = strings.TrimSpace(s); s == "" {
					return badf("%s: empty item", k)
				}
				if k == "tmpfs" && !strings.HasPrefix(s, "/") {
					return badf("tmpfs: %q is not an absolute path in the container", s)
				}
				items = append(items, listItem{canon: s, node: Str(s)})
			}
			err = e.setList(name, field, items, func(n *yaml.Node, p *Project) string { return p.interpolate(n.Value) })
		case "secrets":
			var list []string
			if !null {
				if err := json.Unmarshal(raw, &list); err != nil {
					return badf("secrets must be a list of secret names")
				}
			}
			err = e.setSecrets(name, list)
		case "secretFromEnv":
			if _, ok := patch["environment"]; ok {
				return badf("secretFromEnv changes the environment: send it alone")
			}
			if _, ok := patch["secrets"]; ok {
				return badf("secretFromEnv changes secrets: send it alone")
			}
			var req SecretFromEnv
			if err := json.Unmarshal(raw, &req); err != nil {
				return badf("secretFromEnv must be {key, secret?, fileEnv?}")
			}
			err = e.secretFromEnv(name, req)
		default:
			return badf("unknown service attribute %q", k)
		}
		if err != nil {
			return err
		}
	}
	_ = cur
	if raw, ok := patch["name"]; ok {
		var n string
		if err := json.Unmarshal(raw, &n); err != nil || n == "" {
			return badf("name must be a non-empty string")
		}
		if n != name {
			return e.RenameService(name, n)
		}
	}
	return nil
}

func (e *Editor) setDeploy(svc string, path []string, v *yaml.Node) error {
	d := e.fieldDoc(svc, "deploy")
	e.mark(d.Path)
	return d.Set(append([]string{"services", svc, "deploy"}, path...), v)
}

func (e *Editor) deleteDeploy(svc string, path ...string) error {
	full := append([]string{"services", svc, "deploy"}, path...)
	if e.scoped {
		return e.resetInOverride(svc, full)
	}
	sets := func(d *Doc) bool { k, _ := d.Get(full...); return k != nil }
	for _, d := range e.deleteDocs(svc, sets) {
		if sets(d) {
			e.mark(d.Path)
			if err := deleteUnder(d, full); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteUnder removes the service attribute at path (services.<svc>.…)
// with the parents it leaves empty, up to the attribute itself: a service
// left without keys stays ({}) so the rest of an edit still finds it.
func deleteUnder(d *Doc, path []string) error {
	top := path
	for len(top) > 3 {
		parent := Resolve(d.Node(top[:len(top)-1]...))
		if parent == nil || parent.Kind != yaml.MappingNode || len(parent.Content) != 2 {
			break
		}
		top = top[:len(top)-1]
	}
	return d.Delete(top, false)
}

// setLogging writes a service's logging driver and rotation options: an
// empty value removes its key, and a logging: left empty is removed. Other
// keys and options are kept.
func (e *Editor) setLogging(svc string, l model.ServiceLogging) error {
	d := e.fieldDoc(svc, "logging")
	if err := e.own(d, svc, "logging"); err != nil {
		return err
	}
	e.mark(d.Path)
	logging := []string{"services", svc, "logging"}
	for _, kv := range [][2]string{{"driver", l.Driver}, {"options.max-size", l.MaxSize}, {"options.max-file", l.MaxFile}} {
		path := append(slices.Clone(logging), strings.Split(kv[0], ".")...)
		if kv[1] == "" {
			if err := d.Delete(path, false); err != nil {
				return err
			}
			continue
		}
		v := Str(kv[1])
		if kv[0] != "driver" {
			// Option values are strings ("3", not 3).
			v.Style = yaml.DoubleQuotedStyle
		}
		if err := d.Set(path, v); err != nil {
			return err
		}
	}
	options := append(slices.Clone(logging), "options")
	if _, v := d.Get(options...); v != nil && v.Kind == yaml.MappingNode && len(v.Content) == 0 {
		if err := d.Delete(options, false); err != nil {
			return err
		}
	}
	if _, v := d.Get(logging...); v != nil && v.Kind == yaml.MappingNode && len(v.Content) == 0 {
		return e.deleteField(svc, "logging")
	}
	return nil
}

// setLabels makes the service's labels exactly labels (none removes
// labels:): kept labels keep their source, removed ones are deleted, new
// and changed ones are written quoted. A list form (- key=value) is
// rewritten as a mapping.
func (e *Editor) setLabels(svc string, labels map[string]string) error {
	if len(labels) == 0 {
		return e.deleteField(svc, "labels")
	}
	d := e.fieldDoc(svc, "labels")
	if err := e.own(d, svc, "labels"); err != nil {
		return err
	}
	e.mark(d.Path)
	path := []string{"services", svc, "labels"}
	quoted := func(s string) *yaml.Node {
		v := Str(s)
		v.Style = yaml.DoubleQuotedStyle
		return v
	}
	if _, cur := d.Get(path...); cur == nil || Resolve(cur).Kind != yaml.MappingNode {
		m := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range slices.Sorted(maps.Keys(labels)) {
			m.Content = append(m.Content, Str(k), quoted(labels[k]))
		}
		return d.Set(path, m)
	}
	for _, k := range mapKeys(d, path...) {
		if _, keep := labels[k]; !keep && k != "<<" {
			if err := d.Delete(append(slices.Clone(path), k), false); err != nil {
				return err
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		if _, old := d.Get(append(slices.Clone(path), k)...); old != nil && Resolve(old).Value == labels[k] {
			continue
		}
		if err := d.Set(append(slices.Clone(path), k), quoted(labels[k])); err != nil {
			return err
		}
	}
	return nil
}

// scopedKeys are the ServicePatch keys that can be written to the active
// environment's override file alone ("scope": "environment").
var scopedKeys = []string{
	"image", "restart", "user", "containerName", "hostname", "workingDir", "platform", "command", "entrypoint",
	"ports", "volumes", "expose", "profiles", "healthcheck", "replicas", "resources", "logging",
	"readOnly", "privileged", "init", "capDrop", "capAdd", "securityOpt", "tmpfs",
}

// resetInOverride removes path from the active environment's override
// file and, when an earlier file sets it, resets it there (!reset), so
// the environment runs without it and the base files keep theirs.
func (e *Editor) resetInOverride(svc string, path []string) error {
	o := e.p.Docs[e.p.Override]
	e.mark(o.Path)
	if err := o.Delete(path, false); err != nil {
		return err
	}
	for _, d := range e.serviceDocs(svc) {
		if d == o {
			continue
		}
		k, _ := d.Get(path...)
		if k == nil && len(path) == 3 {
			_, sv := d.Get(path[:2]...)
			if mergedGet(sv, path[2]) == nil {
				continue
			}
		} else if k == nil {
			continue
		}
		return o.Set(path, Raw("!reset null"))
	}
	return nil
}

// listItem is one wanted list item: canon is its normalized value, node
// what is written when it is new.
type listItem struct {
	canon string
	node  *yaml.Node
}

// appends reports whether the service's field written in d is appended to
// what earlier files write (compose appends ports, volumes, expose…
// lists of an override file to the base's): d must then tag its list
// !override so the service gets exactly the items wanted, not the base's
// plus them.
func (e *Editor) appends(d *Doc, svc, field string) bool {
	for _, o := range e.serviceDocs(svc) {
		if o == d {
			return false
		}
		_, sv := o.Get("services", svc)
		if mergedGet(sv, field) != nil {
			return true
		}
	}
	return false
}

// setList makes the service's list field hold items: kept items keep
// their source; removed ones are deleted, new ones appended; a reordered
// list is rewritten. canonOf normalizes an existing item. In a file whose
// list compose appends to an earlier file's (an environment override over
// the base), the list is written whole and tagged !override.
func (e *Editor) setList(svc, field string, items []listItem, canonOf func(*yaml.Node, *Project) string) error {
	d := e.fieldDoc(svc, field)
	path := []string{"services", svc, field}
	merged := e.appends(d, svc, field)
	tag := ""
	if merged {
		tag = "!override"
	}
	if len(items) == 0 {
		if merged {
			e.mark(d.Path)
			return d.Set(path, Raw("!override []"))
		}
		return e.deleteField(svc, field)
	}
	if err := e.own(d, svc, field); err != nil {
		return err
	}
	_, v := d.Get(path...)
	if v == nil || v.Kind != yaml.SequenceNode {
		s := Seq()
		s.Tag = tag
		for _, it := range items {
			s.Content = append(s.Content, it.node)
		}
		e.mark(d.Path)
		return d.Set(path, s)
	}
	old := make([]string, len(v.Content))
	for i, n := range v.Content {
		old[i] = canonOf(Resolve(n), e.p)
	}
	// Match wanted items to existing ones in order.
	used := make([]bool, len(old))
	match := make([]int, len(items))
	for i, it := range items {
		match[i] = -1
		for j, o := range old {
			if !used[j] && o == it.canon {
				used[j], match[i] = true, j
				break
			}
		}
	}
	rewrite := func() error {
		s := Seq()
		s.Tag = tag
		for i, it := range items {
			if match[i] >= 0 {
				s.Content = append(s.Content, cloneNode(v.Content[match[i]]))
			} else {
				s.Content = append(s.Content, it.node)
			}
		}
		e.mark(d.Path)
		return d.Set(path, s)
	}
	if v.Tag == "!override" {
		tag = v.Tag
	} else if merged {
		// Items added or removed in place would merge with the earlier
		// files' list: write it whole, tagged.
		return rewrite()
	}
	if !slices.ContainsFunc(match, func(m int) bool { return m >= 0 }) {
		// Nothing kept: write the new list (block style).
		return rewrite()
	}
	last := -1
	ordered := true
	for _, m := range match {
		if m >= 0 {
			if m < last {
				ordered = false
			}
			last = m
		}
	}
	// New items must come after the kept ones to be appended.
	seenNew := false
	for _, m := range match {
		if m < 0 {
			seenNew = true
		} else if seenNew {
			ordered = false
		}
	}
	if !ordered {
		return rewrite()
	}
	for j := len(old) - 1; j >= 0; j-- {
		if !used[j] {
			e.mark(d.Path)
			if err := d.DeleteItem(path, j, false); err != nil {
				return err
			}
		}
	}
	for i, it := range items {
		if match[i] < 0 {
			e.mark(d.Path)
			if err := d.AppendItem(path, it.node); err != nil {
				return err
			}
		}
	}
	return nil
}

var portRe = regexp.MustCompile(`^(?:(\[[^\]]+\]|[0-9.]+):)?(?:([0-9-]*):)?([0-9-]+)(?:/(\w+))?$`)

// canonPort normalizes short port syntax: "[ip:][host:]container[/proto]".
func canonPort(s string) string {
	m := portRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return s
	}
	ip, host, ctr, proto := m[1], m[2], m[3], m[4]
	if m[2] == "" && m[1] != "" && !strings.Contains(m[1], ".") && !strings.HasPrefix(m[1], "[") {
		host, ip = m[1], "" // "8080:80": the first part is the host port
	}
	if proto == "tcp" {
		proto = ""
	}
	return portText(model.ServicePort{HostIP: ip, Host: host, Container: ctr, Protocol: proto})
}

func canonPortNode(n *yaml.Node, p *Project) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return canonPort(p.interpolate(n.Value))
	case yaml.MappingNode:
		get := func(k string) string {
			if _, v := mapGet(n, k); v != nil {
				return p.interpolate(v.Value)
			}
			return ""
		}
		proto := get("protocol")
		if proto == "tcp" {
			proto = ""
		}
		return portText(model.ServicePort{HostIP: get("host_ip"), Host: get("published"), Container: get("target"), Protocol: proto})
	}
	return ""
}

func canonMount(m model.ServiceMount) string {
	src := m.Source
	if m.Type == "bind" {
		if rest, ok := strings.CutPrefix(src, "~"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				src = home + rest
			}
		}
		if src != "" {
			src = path.Clean(src)
		}
	}
	return orDefault(m.Type, "volume") + "|" + src + "|" + path.Clean(m.Target) + "|" + strconv.FormatBool(m.ReadOnly)
}

func canonMountNode(n *yaml.Node, p *Project) string {
	switch n.Kind {
	case yaml.ScalarNode:
		parts := strings.Split(p.interpolate(n.Value), ":")
		m := model.ServiceMount{Type: "volume"}
		switch len(parts) {
		case 1:
			m.Target = parts[0]
			m.Source = ""
		default:
			m.Source, m.Target = parts[0], parts[1]
			if len(parts) > 2 {
				m.ReadOnly = strings.Contains(parts[2], "ro")
			}
		}
		if strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, "~") {
			m.Type = "bind"
		}
		return canonMount(m)
	case yaml.MappingNode:
		get := func(k string) string {
			if _, v := mapGet(n, k); v != nil {
				return p.interpolate(v.Value)
			}
			return ""
		}
		return canonMount(model.ServiceMount{Type: orDefault(get("type"), "volume"), Source: get("source"), Target: get("target"), ReadOnly: get("read_only") == "true"})
	}
	return ""
}

// setNetworks sets a service's networks (list or mapping form).
func (e *Editor) setNetworks(svc string, list []string) error {
	for _, n := range list {
		if _, ok := e.p.Model.Networks[n]; !ok && n != "default" {
			return badf("network %q is not declared; add it first", n)
		}
	}
	d := e.fieldDoc(svc, "networks")
	if err := e.own(d, svc, "networks"); err != nil {
		return err
	}
	path := []string{"services", svc, "networks"}
	_, v := d.Get(path...)
	if v != nil && v.Kind == yaml.MappingNode {
		if len(list) == 0 {
			return e.deleteField(svc, "networks")
		}
		for _, k := range mapKeys(d, path...) {
			if !slices.Contains(list, k) {
				e.mark(d.Path)
				if err := d.Delete(append(path, k), false); err != nil {
					return err
				}
			}
		}
		for _, n := range list {
			if k, _ := d.Get(append(path, n)...); k == nil {
				e.mark(d.Path)
				if err := d.Set(append(path, n), &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	items := make([]listItem, len(list))
	for i, n := range list {
		items[i] = listItem{canon: n, node: Str(n)}
	}
	return e.setList(svc, "networks", items, func(n *yaml.Node, p *Project) string { return p.interpolate(n.Value) })
}

// setDependsOn sets depends_on, in list form while every condition is
// service_started and the file does not use the long form.
func (e *Editor) setDependsOn(svc string, deps []model.ServiceDependency) error {
	for _, dep := range deps {
		if _, ok := e.p.Model.Services[dep.Service]; !ok {
			return badf("service %q not found", dep.Service)
		}
		if dep.Service == svc {
			return badf("a service can't depend on itself")
		}
		switch dep.Condition {
		case "", "service_started", "service_healthy", "service_completed_successfully":
		default:
			return badf("invalid condition %q: use service_started, service_healthy or service_completed_successfully", dep.Condition)
		}
	}
	if len(deps) == 0 {
		return e.deleteField(svc, "depends_on")
	}
	d := e.fieldDoc(svc, "depends_on")
	if err := e.own(d, svc, "depends_on"); err != nil {
		return err
	}
	path := []string{"services", svc, "depends_on"}
	_, v := d.Get(path...)
	want := dependsNode(deps)
	if v != nil && v.Kind == yaml.MappingNode || want.Kind == yaml.MappingNode {
		if v == nil || v.Kind != yaml.MappingNode {
			e.mark(d.Path)
			if want.Kind != yaml.MappingNode {
				want = &yaml.Node{Kind: yaml.MappingNode}
				for _, dep := range deps {
					want.Content = append(want.Content, Str(dep.Service), dependencyNode(dep))
				}
			}
			return d.Set(path, want)
		}
		// Per entry, keeping entries that did not change.
		for _, k := range mapKeys(d, path...) {
			if !slices.ContainsFunc(deps, func(x model.ServiceDependency) bool { return x.Service == k }) {
				e.mark(d.Path)
				if err := d.Delete(append(path, k), false); err != nil {
					return err
				}
			}
		}
		for _, dep := range deps {
			cur := e.p.Model.Services[svc].DependsOn[dep.Service]
			if _, ok := e.p.Model.Services[svc].DependsOn[dep.Service]; ok && orDefault(dep.Condition, "service_started") == orDefault(cur.Condition, "service_started") {
				if k, _ := d.Get(append(path, dep.Service)...); k != nil {
					continue
				}
			}
			e.mark(d.Path)
			if err := d.Set(append(path, dep.Service), dependencyNode(dep)); err != nil {
				return err
			}
		}
		return nil
	}
	items := make([]listItem, len(deps))
	for i, dep := range deps {
		items[i] = listItem{canon: dep.Service, node: Str(dep.Service)}
	}
	return e.setList(svc, "depends_on", items, func(n *yaml.Node, p *Project) string { return n.Value })
}

// setEnvironment makes the service's environment hold env, per key:
// unchanged values keep their source (${VAR}), keys from env_file stay in
// their file unless the value changes, removed keys are deleted.
func (e *Editor) setEnvironment(svc string, env []EnvSpec) error {
	s := e.p.Model.Services[svc]
	effective := map[string]*string{}
	fromFile := map[string]bool{}
	for _, ef := range s.EnvFiles {
		if data, err := e.p.read(e.p.rel(ef.Path)); err == nil {
			if vars, err := parseDotenv(data, e.p.Env); err == nil {
				for k, v := range vars {
					v := v
					effective[k], fromFile[k] = &v, true
				}
			}
		}
	}
	inCompose := map[string]bool{}
	for k, v := range s.Environment {
		effective[k], inCompose[k] = v, true
		delete(fromFile, k)
	}
	want := map[string]bool{}
	for _, ev := range env {
		if ev.Key == "" || strings.ContainsAny(ev.Key, "= \t\n") {
			return badf("invalid environment key %q", ev.Key)
		}
		want[ev.Key] = true
	}
	// Deletions.
	for k := range inCompose {
		if !want[k] {
			if err := e.envDelete(svc, k); err != nil {
				return err
			}
		}
	}
	raw := e.p.RawEnv(svc)
	for _, ev := range env {
		cur, exists := effective[ev.Key]
		if ev.Secret && ev.Value == nil {
			continue // redacted in the graph: keep what is stored
		}
		same := exists && (cur == nil && ev.Value == nil || cur != nil && ev.Value != nil && *cur == *ev.Value)
		// The value as written (${VAR}, $$) is unchanged too.
		if r, ok := raw[ev.Key]; ok && inCompose[ev.Key] && ev.Value != nil && r != "" && *ev.Value == r {
			same = true
		}
		if same {
			continue
		}
		if err := e.envSet(svc, ev.Key, ev.Value); err != nil {
			return err
		}
	}
	return nil
}

// envDoc is the file holding key in the service's environment (the last
// one), else the file environment is edited in.
func (e *Editor) envDoc(svc, key string) (*Doc, bool) {
	docs := e.serviceDocs(svc)
	for i := len(docs) - 1; i >= 0; i-- {
		_, sv := docs[i].Get("services", svc)
		if _, ok := envEntries(Resolve(mergedGet(sv, "environment")))[key]; ok {
			return docs[i], true
		}
	}
	return e.fieldDoc(svc, "environment"), false
}

func (e *Editor) envSet(svc, key string, value *string) error {
	d, _ := e.envDoc(svc, key)
	if err := e.own(d, svc, "environment"); err != nil {
		return err
	}
	path := []string{"services", svc, "environment"}
	_, v := d.Get(path...)
	e.mark(d.Path)
	if v != nil && v.Kind == yaml.SequenceNode {
		item := key
		if value != nil {
			item += "=" + *value
		}
		if en, ok := envEntries(v)[key]; ok {
			return d.SetItem(path, en.index, Str(item))
		}
		return d.AppendItem(path, Str(item))
	}
	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	if value != nil {
		val = Str(*value)
	}
	return d.Set(append(path, key), val)
}

// envDelete removes key from the service's environment in every file
// (only from the active environment's override when it sets it, see
// deleteDocs).
func (e *Editor) envDelete(svc, key string) error {
	sets := func(d *Doc) bool {
		_, sv := d.Get("services", svc)
		_, ok := envEntries(Resolve(mergedGet(sv, "environment")))[key]
		return ok
	}
	for _, d := range e.deleteDocs(svc, sets) {
		path := []string{"services", svc, "environment"}
		k, v := d.Get(path...)
		if k == nil {
			continue
		}
		if v.Kind == yaml.AliasNode {
			if _, ok := envEntries(Resolve(v))[key]; !ok {
				continue
			}
			if err := e.own(d, svc, "environment"); err != nil {
				return err
			}
			_, v = d.Get(path...)
		}
		en, ok := envEntries(v)[key]
		if !ok {
			continue
		}
		e.mark(d.Path)
		var err error
		switch {
		case v.Kind == yaml.SequenceNode:
			err = d.DeleteItem(path, en.index, true)
		case en.key != nil && slices.Contains(v.Content, en.key):
			err = d.Delete(append(path, key), true)
		default: // inherited through <<: *x
			err = d.Set(append(path, key), Raw("!reset null"))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Secrets

// SecretFromEnv moves an environment variable of a service into a secret
// file: the value is written to ./secrets/<Secret>.txt (unless the secret
// is already declared), declared as a top-level secret, granted to the
// service and removed from its environment. FileEnv also sets
// <Key>_FILE=/run/secrets/<Secret>, for images that read it.
type SecretFromEnv struct {
	Key     string `json:"key"`
	Secret  string `json:"secret,omitempty"` // default: the key in lower case
	FileEnv bool   `json:"fileEnv,omitempty"`
}

// secretSource is the secret a service's secrets item grants (short
// syntax "x", long syntax {source: x}).
func secretSource(n *yaml.Node, _ *Project) string {
	if n.Kind == yaml.MappingNode {
		if src := mergedGet(n, "source"); src != nil {
			return src.Value
		}
	}
	return n.Value
}

// setSecrets makes the service's secrets list grant the named top-level
// secrets; long-syntax items that stay keep their target, uid and mode.
func (e *Editor) setSecrets(svc string, names []string) error {
	items := make([]listItem, 0, len(names))
	for _, n := range names {
		if _, ok := e.p.Model.Secrets[n]; !ok {
			return badf("secret %q is not declared: add it under the top-level secrets: first", n)
		}
		items = append(items, listItem{canon: n, node: Str(n)})
	}
	return e.setList(svc, "secrets", items, secretSource)
}

func (e *Editor) secretFromEnv(svc string, req SecretFromEnv) error {
	s := e.p.Model.Services[svc]
	value, inEnv := s.Environment[req.Key]
	if _, written := e.p.RawEnv(svc)[req.Key]; !inEnv || !written {
		if _, fromFile := e.p.serviceEnv(s); fromFile[req.Key] != "" {
			return badf("%s comes from %s: move it to a secret there", req.Key, fromFile[req.Key])
		}
		return badf("service %s has no environment variable %s", svc, req.Key)
	}
	if value == nil {
		return badf("%s has no value in the compose files (it is passed from the shell)", req.Key)
	}
	name := req.Secret
	if name == "" {
		name = strings.ToLower(req.Key)
	}
	if err := ValidName("secret", name); err != nil {
		return err
	}
	if _, declared := e.p.Model.Secrets[name]; !declared {
		file := "secrets/" + name + ".txt"
		rel := path.Join(e.p.ProjectDir, file)
		if _, err := e.p.read(rel); err == nil {
			return &EditError{Msg: fmt.Sprintf("%s already exists: choose another secret name", rel), Conflict: true}
		}
		if e.created == nil {
			e.created = map[string][]byte{}
		}
		e.created[rel] = []byte(*value)
		d := e.primary()
		e.mark(d.Path)
		if err := d.Set([]string{"secrets", name}, Map("file", Str("./"+file))); err != nil {
			return err
		}
	}
	if err := e.envDelete(svc, req.Key); err != nil {
		return err
	}
	// Where the service sees the secret: /run/secrets/<name>, or the
	// target of a long-syntax grant it already has.
	mounted := "/run/secrets/" + name
	granted := make([]listItem, 0, len(s.Secrets)+1)
	already := false
	for _, sec := range s.Secrets {
		if sec.Source == name {
			already = true
			if sec.Target != "" {
				mounted = sec.Target
				if !strings.HasPrefix(mounted, "/") {
					mounted = "/run/secrets/" + mounted
				}
			}
		}
		granted = append(granted, listItem{canon: sec.Source, node: Str(sec.Source)})
	}
	if req.FileEnv {
		if err := e.envSet(svc, req.Key+"_FILE", &mounted); err != nil {
			return err
		}
	}
	if already {
		return nil
	}
	return e.setList(svc, "secrets", append(granted, listItem{canon: name, node: Str(name)}), secretSource)
}

// ---------------------------------------------------------------------------
// Networks and volumes

// TopSpec is a network or volume.
type TopSpec struct {
	Name     string `json:"name"`
	Driver   string `json:"driver,omitempty"`
	External bool   `json:"external,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}

func topSection(kind string) string { return kind + "s" }

func (e *Editor) topDocs(kind, name string) []*Doc {
	var out []*Doc
	for _, rel := range e.p.Order {
		if k, _ := e.p.Docs[rel].Get(topSection(kind), name); k != nil {
			out = append(out, e.p.Docs[rel])
		}
	}
	return out
}

// AddTop declares a network or volume (kind "network" | "volume").
func (e *Editor) AddTop(kind string, spec TopSpec) error {
	if err := ValidName(kind, spec.Name); err != nil {
		return err
	}
	if len(e.topDocs(kind, spec.Name)) > 0 {
		return &EditError{Msg: fmt.Sprintf("%s %q already exists", kind, spec.Name), Conflict: true}
	}
	if kind == "volume" && spec.Internal {
		return badf("volumes have no internal flag")
	}
	d := e.primary()
	e.mark(d.Path)
	return d.Set([]string{topSection(kind), spec.Name}, topNode(spec))
}

func topNode(spec TopSpec) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	if spec.Driver != "" {
		n.Content = append(n.Content, Str("driver"), Str(spec.Driver))
	}
	if spec.External {
		n.Content = append(n.Content, Str("external"), Bool(true))
	}
	if spec.Internal {
		n.Content = append(n.Content, Str("internal"), Bool(true))
	}
	if len(n.Content) == 0 {
		n.Style = yaml.FlowStyle
	}
	return n
}

// PatchTop updates a network or volume: driver/external/internal and a
// rename (services follow).
func (e *Editor) PatchTop(kind, name string, patch map[string]json.RawMessage) error {
	docs := e.topDocs(kind, name)
	if len(docs) == 0 {
		return &EditError{Msg: fmt.Sprintf("%s %q not found", kind, name), NotFound: true}
	}
	d := docs[len(docs)-1]
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range patch {
			if !yield(k) {
				return
			}
		}
	}) {
		raw := patch[k]
		null := string(raw) == "null"
		path := []string{topSection(kind), name, k}
		switch k {
		case "name":
		case "driver":
			var s string
			if !null && json.Unmarshal(raw, &s) != nil {
				return badf("driver must be a string")
			}
			e.mark(d.Path)
			var err error
			if null || s == "" {
				err = d.Delete(path, false)
			} else {
				err = e.setTopAttr(d, kind, name, k, Str(s))
			}
			if err != nil {
				return err
			}
		case "external", "internal":
			if k == "internal" && kind == "volume" {
				return badf("volumes have no internal flag")
			}
			var b bool
			if !null && json.Unmarshal(raw, &b) != nil {
				return badf("%s must be a boolean", k)
			}
			e.mark(d.Path)
			var err error
			if !b {
				err = d.Delete(path, false)
			} else {
				err = e.setTopAttr(d, kind, name, k, Bool(true))
			}
			if err != nil {
				return err
			}
		default:
			return badf("unknown %s attribute %q", kind, k)
		}
	}
	if raw, ok := patch["name"]; ok {
		var n string
		if err := json.Unmarshal(raw, &n); err != nil || n == "" {
			return badf("name must be a non-empty string")
		}
		if n != name {
			return e.RenameTop(kind, name, n)
		}
	}
	return nil
}

// setTopAttr sets an attribute of a top-level element whose value may be
// null ("networks: {front: }").
func (e *Editor) setTopAttr(d *Doc, kind, name, attr string, v *yaml.Node) error {
	return d.Set([]string{topSection(kind), name, attr}, v)
}

// RemoveTop deletes a network or volume and the services' uses of it.
func (e *Editor) RemoveTop(kind, name string) error {
	docs := e.topDocs(kind, name)
	if len(docs) == 0 {
		return &EditError{Msg: fmt.Sprintf("%s %q not found", kind, name), NotFound: true}
	}
	for _, d := range docs {
		e.mark(d.Path)
		if err := d.Delete([]string{topSection(kind), name}, false); err != nil {
			return err
		}
	}
	return e.rewriteTopRefs(kind, name, "")
}

// RenameTop renames a network or volume and the services' uses of it.
func (e *Editor) RenameTop(kind, old, name string) error {
	if err := ValidName(kind, name); err != nil {
		return err
	}
	if len(e.topDocs(kind, name)) > 0 {
		return &EditError{Msg: fmt.Sprintf("%s %q already exists", kind, name), Conflict: true}
	}
	for _, d := range e.topDocs(kind, old) {
		e.mark(d.Path)
		if err := d.RenameKey([]string{topSection(kind), old}, name); err != nil {
			return err
		}
	}
	return e.rewriteTopRefs(kind, old, name)
}

func (e *Editor) rewriteTopRefs(kind, old, name string) error {
	for _, rel := range e.p.Order {
		d := e.p.Docs[rel]
		for _, svc := range mapKeys(d, "services") {
			if kind == "network" {
				path := []string{"services", svc, "networks"}
				_, v := d.Get(path...)
				switch {
				case v == nil:
				case v.Kind == yaml.MappingNode:
					if k, _ := d.Get(append(path, old)...); k != nil {
						e.mark(rel)
						var err error
						if name == "" {
							err = d.Delete(append(path, old), true)
						} else {
							err = d.RenameKey(append(path, old), name)
						}
						if err != nil {
							return err
						}
					}
				case v.Kind == yaml.SequenceNode:
					if err := e.rewriteItems(d, path, func(s string) (string, bool) { return name, s == old }, name); err != nil {
						return err
					}
				}
				continue
			}
			path := []string{"services", svc, "volumes"}
			for {
				_, v := d.Get(path...)
				if v == nil || v.Kind != yaml.SequenceNode {
					break
				}
				changed := false
				for i, it := range v.Content {
					switch it.Kind {
					case yaml.ScalarNode:
						src, rest, ok := strings.Cut(it.Value, ":")
						if !ok || src != old {
							continue
						}
						e.mark(rel)
						var err error
						if name == "" {
							err = d.DeleteItem(path, i, true)
						} else {
							err = d.SetItem(path, i, Str(name+":"+rest))
						}
						if err != nil {
							return err
						}
						changed = name == ""
					case yaml.MappingNode:
						_, sv := mapGet(it, "source")
						_, tv := mapGet(it, "type")
						if sv == nil || sv.Value != old || tv != nil && tv.Value != "volume" {
							continue
						}
						e.mark(rel)
						if name == "" {
							if err := d.DeleteItem(path, i, true); err != nil {
								return err
							}
							changed = true
						} else {
							// Rewrite the item with the new source.
							n := cloneNode(it)
							for j := 0; j+1 < len(n.Content); j += 2 {
								if n.Content[j].Value == "source" {
									n.Content[j+1] = Str(name)
								}
							}
							if err := d.SetItem(path, i, n); err != nil {
								return err
							}
						}
					}
					if changed {
						break
					}
				}
				if !changed {
					break
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Connections

// ConnectRequest connects service From to To.
type ConnectRequest struct {
	From      string            `json:"from"`
	To        string            `json:"to"`
	Kind      string            `json:"kind"` // depends_on | network | volume | env
	Condition string            `json:"condition,omitempty"`
	Network   string            `json:"network,omitempty"`
	Target    string            `json:"target,omitempty"`
	RecipeID  string            `json:"recipeId,omitempty"`
	Env       map[string]string `json:"-"`
}

// Connect applies a connection.
func (e *Editor) Connect(req ConnectRequest) error {
	if err := e.requireService(req.From); err != nil {
		return err
	}
	s := e.p.Model.Services[req.From]
	switch req.Kind {
	case "depends_on":
		if err := e.requireService(req.To); err != nil {
			return err
		}
		var deps []model.ServiceDependency
		for _, dn := range sortedKeys(s.DependsOn) {
			d := s.DependsOn[dn]
			sd := model.ServiceDependency{Service: dn, Condition: d.Condition, Restart: d.Restart}
			if !d.Required {
				no := false
				sd.Required = &no
			}
			if dn == req.To {
				sd.Condition = orDefault(req.Condition, sd.Condition)
			}
			deps = append(deps, sd)
		}
		if _, ok := s.DependsOn[req.To]; !ok {
			deps = append(deps, model.ServiceDependency{Service: req.To, Condition: req.Condition})
		}
		// Keep the file's order: existing entries first.
		return e.setDependsOn(req.From, e.orderDeps(req.From, deps))
	case "network":
		nw := req.Network
		if nw == "" {
			return badf("network is required")
		}
		if _, ok := e.p.Model.Networks[nw]; !ok {
			if err := e.AddTop("network", TopSpec{Name: nw}); err != nil {
				return err
			}
			e.p.Model.Networks[nw] = e.p.Model.Networks["default"]
		}
		for _, svc := range []string{req.From, req.To} {
			if svc == "" {
				continue
			}
			if err := e.requireService(svc); err != nil {
				return err
			}
			// A service on the implicit default network keeps it: joining a
			// first explicit network would otherwise cut it off from the
			// services left there.
			cur := sortedKeys(e.p.Model.Services[svc].Networks)
			if slices.Contains(cur, nw) {
				continue
			}
			if err := e.setNetworks(svc, e.orderNetworks(svc, append(cur, nw))); err != nil {
				return err
			}
		}
		return nil
	case "volume":
		if req.Target == "" || !strings.HasPrefix(req.Target, "/") {
			return badf("target (an absolute path in the container) is required")
		}
		vol := strings.TrimPrefix(req.To, "volume:")
		if _, ok := e.p.Model.Volumes[vol]; !ok {
			if err := e.AddTop("volume", TopSpec{Name: vol}); err != nil {
				return err
			}
		}
		d := e.fieldDoc(req.From, "volumes")
		if err := e.own(d, req.From, "volumes"); err != nil {
			return err
		}
		for _, m := range s.Volumes {
			if m.Target == req.Target {
				return &EditError{Msg: fmt.Sprintf("%s already mounts something at %s", req.From, req.Target), Conflict: true}
			}
		}
		e.mark(d.Path)
		return d.AppendItem([]string{"services", req.From, "volumes"}, Str(vol+":"+req.Target))
	case "env":
		for _, k := range sortedKeys(req.Env) {
			v := req.Env[k]
			if err := e.envSet(req.From, k, &v); err != nil {
				return err
			}
		}
		return nil
	}
	return badf("unknown connection kind %q: use depends_on, network, volume or env", req.Kind)
}

// orderDeps orders dependencies as the file lists them, new ones last.
func (e *Editor) orderDeps(svc string, deps []model.ServiceDependency) []model.ServiceDependency {
	d := e.fieldDoc(svc, "depends_on")
	_, v := d.Get("services", svc, "depends_on")
	v = Resolve(v)
	var order []string
	if v != nil {
		switch v.Kind {
		case yaml.SequenceNode:
			for _, it := range v.Content {
				order = append(order, it.Value)
			}
		case yaml.MappingNode:
			for i := 0; i < len(v.Content); i += 2 {
				order = append(order, v.Content[i].Value)
			}
		}
	}
	idx := func(s string) int {
		if i := slices.Index(order, s); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(deps, func(a, b model.ServiceDependency) int { return idx(a.Service) - idx(b.Service) })
	return deps
}

func (e *Editor) orderNetworks(svc string, nws []string) []string {
	d := e.fieldDoc(svc, "networks")
	order := mapKeys(d, "services", svc, "networks")
	if _, v := d.Get("services", svc, "networks"); v != nil && v.Kind == yaml.SequenceNode {
		order = nil
		for _, it := range v.Content {
			order = append(order, it.Value)
		}
	}
	idx := func(s string) int {
		if i := slices.Index(order, s); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(nws, func(a, b string) int { return idx(a) - idx(b) })
	return nws
}
