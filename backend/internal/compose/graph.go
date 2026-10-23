package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// secretKeyRe matches environment keys holding credentials (values are
// redacted in the graph and flagged when written literally).
var secretKeyRe = regexp.MustCompile(`(?i)(passw(or)?d|pwd|secret|token|api_?key|private_?key|credential|access_?key|auth_?key|_pass$|^pass$)`)

// IsSecretKey reports whether an environment key looks like a credential
// (KEY_FILE variants point to a file and are not).
func IsSecretKey(k string) bool {
	return secretKeyRe.MatchString(k) && !strings.HasSuffix(strings.ToUpper(k), "_FILE")
}

// list returns the names in the project directory rel ("" on errors).
func (p *Project) list(rel string) []string {
	if !inside(rel) && rel != "." {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// ParseImage splits an image reference.
func ParseImage(ref string) *model.ImageRef {
	if ref == "" {
		return nil
	}
	r := &model.ImageRef{Ref: ref}
	rest := ref
	if i := strings.Index(rest, "@"); i >= 0 {
		r.Digest, rest = rest[i+1:], rest[:i]
	}
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		r.Tag, rest = rest[i+1:], rest[:i]
	}
	r.Repo = rest
	return r
}

// Graph derives the canvas of the project. prev are the nodes of the last
// derivation: positions are kept by node ID.
func (p *Project) Graph(prev []model.ComposeNode) model.ComposeGraph {
	g := model.ComposeGraph{Nodes: []model.ComposeNode{}, Edges: []model.ComposeEdge{}}
	if p.Model == nil {
		return g
	}
	pos := map[string]*model.Position{}
	for _, n := range prev {
		if n.Position != nil {
			pos[n.ID] = n.Position
		}
	}
	add := func(n model.ComposeNode) {
		n.Position = pos[n.ID]
		g.Nodes = append(g.Nodes, n)
	}
	edgeIDs := map[string]bool{}
	edge := func(e model.ComposeEdge) {
		e.ID = e.Kind + ":" + e.Source + "->" + e.Target
		if e.Kind == model.ComposeEdgeVolume || e.Kind == model.ComposeEdgeInferred {
			e.ID += ":" + e.Path + e.EnvKey
		}
		if !edgeIDs[e.ID] {
			edgeIDs[e.ID] = true
			g.Edges = append(g.Edges, e)
		}
	}

	// Top-level elements.
	for _, name := range sortedKeys(p.Model.Networks) {
		nw := p.Model.Networks[name]
		if name == "default" && !declared(p, "networks", "default") {
			continue
		}
		file, line := p.declLine("networks", name)
		n := model.ComposeNode{ID: "network:" + name, Kind: model.ComposeNetwork, Name: name, File: file, Line: line,
			Network: &model.NetworkFacts{Driver: nw.Driver, Internal: nw.Internal, Name: nw.Name}}
		if bool(nw.External) {
			n.Kind, n.External, n.ExternalKind = model.ComposeExternal, true, "network"
		}
		if n.Network.Name == name || strings.HasPrefix(n.Network.Name, "nimbo_") {
			n.Network.Name = ""
		}
		add(n)
	}
	for _, name := range sortedKeys(p.Model.Volumes) {
		v := p.Model.Volumes[name]
		file, line := p.declLine("volumes", name)
		n := model.ComposeNode{ID: "volume:" + name, Kind: model.ComposeVolume, Name: name, File: file, Line: line,
			Volume: &model.VolumeFacts{Driver: v.Driver, Name: v.Name}}
		if bool(v.External) {
			n.Kind, n.External, n.ExternalKind = model.ComposeExternal, true, "volume"
		}
		if n.Volume.Name == name || strings.HasPrefix(n.Volume.Name, "nimbo_") {
			n.Volume.Name = ""
		}
		add(n)
	}
	for _, name := range sortedKeys(p.Model.Configs) {
		c := p.Model.Configs[name]
		add(fileObjectNode("config", model.ComposeConfig, name, types.FileObjectConfig(c), p))
	}
	for _, name := range sortedKeys(p.Model.Secrets) {
		s := p.Model.Secrets[name]
		add(fileObjectNode("secret", model.ComposeSecret, name, types.FileObjectConfig(s), p))
	}

	hosts := map[string]string{} // host name -> service
	for _, name := range p.SortedServiceNames() {
		s := p.Model.Services[name]
		hosts[name] = name
		if s.ContainerName != "" {
			hosts[s.ContainerName] = name
		}
		if s.Hostname != "" {
			hosts[s.Hostname] = name
		}
		for _, nc := range s.Networks {
			if nc != nil {
				for _, a := range nc.Aliases {
					hosts[a] = name
				}
			}
		}
	}

	for _, name := range p.SortedServiceNames() {
		s := p.Model.Services[name]
		id := ServiceID(name)
		file, line := p.ServiceLine(name)
		facts := p.serviceFacts(s)
		n := model.ComposeNode{ID: id, Kind: model.ComposeService, Name: name, File: file, Line: line, Inactive: p.Inactive[name], Service: facts}
		add(n)

		for _, dep := range facts.DependsOn {
			edge(model.ComposeEdge{Source: ServiceID(dep.Service), Target: id, Kind: model.ComposeEdgeDependsOn, Label: strings.TrimPrefix(dep.Condition, "service_"), Condition: dep.Condition})
		}
		for _, l := range s.Links {
			target, _, _ := strings.Cut(l, ":")
			edge(model.ComposeEdge{Source: ServiceID(target), Target: id, Kind: model.ComposeEdgeLinks, Label: l})
		}
		for _, nw := range facts.Networks {
			edge(model.ComposeEdge{Source: "network:" + nw, Target: id, Kind: model.ComposeEdgeNetwork})
		}
		for _, m := range s.Volumes {
			if m.Type == types.VolumeTypeVolume && m.Source != "" {
				label := m.Target
				if m.ReadOnly {
					label += " (ro)"
				}
				edge(model.ComposeEdge{Source: "volume:" + m.Source, Target: id, Kind: model.ComposeEdgeVolume, Label: label, Path: m.Target, ReadOnly: m.ReadOnly})
			}
		}
		for _, c := range s.Configs {
			edge(model.ComposeEdge{Source: "config:" + c.Source, Target: id, Kind: model.ComposeEdgeConfig, Path: c.Target, Label: c.Target})
		}
		for _, sc := range s.Secrets {
			edge(model.ComposeEdge{Source: "secret:" + sc.Source, Target: id, Kind: model.ComposeEdgeSecret, Path: sc.Target, Label: sc.Target})
		}
		if ext := p.extendsOf(name); ext != nil {
			src := ServiceID(ext.service)
			if ext.file != "" {
				src = "extends:" + ext.file + "#" + ext.service
				if !slices.ContainsFunc(g.Nodes, func(x model.ComposeNode) bool { return x.ID == src }) {
					add(model.ComposeNode{ID: src, Kind: model.ComposeExternal, ExternalKind: "service", Name: ext.service, File: ext.file, Line: p.docLine(ext.file, "services", ext.service), External: true})
				}
			}
			edge(model.ComposeEdge{Source: src, Target: id, Kind: model.ComposeEdgeExtends})
		}

		// Inferred: environment values naming another service's host.
		explicit := map[string]bool{}
		for _, d := range facts.DependsOn {
			explicit[d.Service] = true
		}
		for _, l := range s.Links {
			t, _, _ := strings.Cut(l, ":")
			explicit[t] = true
		}
		for _, k := range sortedKeys(s.Environment) {
			v := s.Environment[k]
			if v == nil {
				continue
			}
			for _, host := range hostsIn(*v) {
				other, ok := hosts[host]
				if !ok || other == name || explicit[other] {
					continue
				}
				edge(model.ComposeEdge{Source: ServiceID(other), Target: id, Kind: model.ComposeEdgeInferred, Label: k, EnvKey: k})
			}
		}

		// Build stages.
		p.stageNodes(name, s, add, edge)
	}
	return g
}

func declared(p *Project, section, name string) bool {
	for _, rel := range p.Order {
		if p.Docs[rel].Line(section, name) > 0 {
			return true
		}
	}
	return false
}

func (p *Project) docLine(file string, path ...string) int {
	if d := p.Docs[file]; d != nil {
		return d.Line(path...)
	}
	return 0
}

func fileObjectNode(section, kind, name string, c types.FileObjectConfig, p *Project) model.ComposeNode {
	file, line := p.declLine(section+"s", name)
	n := model.ComposeNode{ID: section + ":" + name, Kind: kind, Name: name, File: file, Line: line,
		Resource: &model.ResourceFacts{File: p.rel(c.File), Environment: c.Environment, Content: c.Content != ""}}
	if bool(c.External) {
		n.Kind, n.External, n.ExternalKind = model.ComposeExternal, true, section
	}
	return n
}

type extendsRef struct{ service, file string }

// extendsOf reads a service's extends from the files (compose-go resolves
// and drops it).
func (p *Project) extendsOf(name string) *extendsRef {
	for i := len(p.Order) - 1; i >= 0; i-- {
		rel := p.Order[i]
		_, sv := p.Docs[rel].Get("services", name)
		if sv == nil {
			continue
		}
		ext := Resolve(mergedGet(sv, "extends"))
		if ext == nil {
			continue
		}
		switch ext.Kind {
		case yaml.ScalarNode:
			return &extendsRef{service: ext.Value}
		case yaml.MappingNode:
			r := &extendsRef{}
			if _, v := mapGet(ext, "service"); v != nil {
				r.service = v.Value
			}
			if _, v := mapGet(ext, "file"); v != nil {
				r.file = cleanJoin(pathDir(rel), p.interpolate(v.Value))
			}
			return r
		}
	}
	return nil
}

var hostRe = regexp.MustCompile(`(?:^|[/@=,;\s(])([A-Za-z0-9][A-Za-z0-9_.\-]*)(?::\d+|/|$|[,;\s)?])`)

// hostsIn lists the host-like words of an environment value: URL hosts,
// host:port pairs and bare names.
func hostsIn(v string) []string {
	var out []string
	for _, m := range hostRe.FindAllStringSubmatchIndex(v, -1) {
		h := v[m[2]:m[3]]
		// A scheme ("postgres" in postgres://db) is followed by "://".
		if strings.HasPrefix(v[m[3]:], "://") {
			continue
		}
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// serviceFacts are the inspector facts of one effective service.
func (p *Project) serviceFacts(s types.ServiceConfig) *model.ServiceFacts {
	f := &model.ServiceFacts{
		Image: ParseImage(s.Image), Ports: []model.ServicePort{}, Expose: []string{}, Environment: []model.ServiceEnv{},
		Volumes: []model.ServiceMount{}, Networks: []string{}, DependsOn: []model.ServiceDependency{}, BaseImages: []string{},
		Restart: s.Restart, Profiles: s.Profiles, Links: s.Links, ContainerName: s.ContainerName, Hostname: s.Hostname,
		User: s.User, WorkingDir: s.WorkingDir, Platform: s.Platform,
	}
	if s.Build != nil {
		f.Image = nil
		if s.Image != "" {
			f.Image = ParseImage(s.Image)
		}
		// The context as the compose files write it (relative to the project
		// directory, so the inspector can write it back); the Dockerfile is the
		// workflow file it builds.
		b := &model.ServiceBuild{Context: p.contextPath(s), Dockerfile: p.dockerfilePath(s), Target: s.Build.Target}
		if b.Context == "" {
			b.Context = s.Build.Context
		} else {
			b.Context = p.composeRel(b.Context)
		}
		if len(s.Build.Args) > 0 {
			b.Args = map[string]string{}
			for k, v := range s.Build.Args {
				if v != nil {
					b.Args[k] = *v
				}
			}
		}
		f.Build = b
		if ctx := p.contextPath(s); ctx != "" {
			f.Stack = Stack(ctx, p.read, p.list)
		}
	}
	for _, port := range s.Ports {
		sp := model.ServicePort{HostIP: port.HostIP, Host: port.Published, Container: strconv.Itoa(int(port.Target)), Protocol: port.Protocol, Published: port.Published != ""}
		if sp.Protocol == "tcp" {
			sp.Protocol = ""
		}
		f.Ports = append(f.Ports, sp)
	}
	f.Expose = append(f.Expose, s.Expose...)
	if len(s.Command) > 0 {
		f.Command = strings.Join(s.Command, " ")
	}
	if len(s.Entrypoint) > 0 {
		f.Entrypoint = strings.Join(s.Entrypoint, " ")
	}
	if s.HealthCheck != nil {
		f.Healthcheck, f.HealthcheckFrom = healthFacts(s.HealthCheck), "compose"
	}
	if s.Deploy != nil {
		if s.Deploy.Replicas != nil {
			r := *s.Deploy.Replicas
			f.Replicas = &r
		}
		if l := s.Deploy.Resources.Limits; l != nil && (l.NanoCPUs != 0 || l.MemoryBytes != 0) {
			f.Resources = &model.ServiceResources{}
			if l.NanoCPUs != 0 {
				f.Resources.CPUs = strconv.FormatFloat(float64(l.NanoCPUs), 'f', -1, 32)
			}
			if l.MemoryBytes != 0 {
				f.Resources.Memory = formatBytes(int64(l.MemoryBytes))
			}
		}
	}
	if f.Replicas == nil && s.Scale != nil {
		r := *s.Scale
		f.Replicas = &r
	}
	// Environment: env_file values first, `environment` wins.
	fromFile := map[string]string{}
	values := map[string]*string{}
	for _, ef := range s.EnvFiles {
		rel := p.rel(ef.Path)
		f.EnvFiles = append(f.EnvFiles, rel)
		data, err := p.read(rel)
		if err != nil {
			continue
		}
		vars, err := parseDotenv(data, p.Env)
		if err != nil {
			continue
		}
		for k, v := range vars {
			v := v
			values[k], fromFile[k] = &v, rel
		}
	}
	for k, v := range s.Environment {
		values[k] = v
		delete(fromFile, k)
	}
	raw := p.RawEnv(s.Name)
	for _, k := range sortedKeys(values) {
		e := model.ServiceEnv{Key: k, FromFile: fromFile[k]}
		if r, ok := raw[k]; ok && varRefRe.MatchString(r) {
			for _, m := range varRefRe.FindAllStringSubmatch(r, -1) {
				if slices.Contains(p.Unresolved, m[1]+m[3]) {
					e.Unresolved = true
				}
			}
		}
		if IsSecretKey(k) {
			e.Secret = true
		} else if v := values[k]; v != nil {
			val := *v
			e.Value = &val
			if r, ok := raw[k]; ok && r != "" && r != val { // "" = a bare KEY passed from the host
				e.Raw = &r
			}
		}
		f.Environment = append(f.Environment, e)
	}
	for _, m := range s.Volumes {
		mt := model.ServiceMount{Type: m.Type, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly}
		if m.Type == types.VolumeTypeBind {
			if r := p.rel(m.Source); !strings.HasPrefix(r, "..") {
				mt.Source = p.composeRel(r)
			}
		}
		f.Volumes = append(f.Volumes, mt)
	}
	for _, nw := range sortedKeys(s.Networks) {
		if nw == "default" && !declared(p, "networks", "default") {
			continue
		}
		f.Networks = append(f.Networks, nw)
	}
	for _, dep := range sortedKeys(s.DependsOn) {
		d := s.DependsOn[dep]
		sd := model.ServiceDependency{Service: dep, Condition: d.Condition, Restart: d.Restart}
		if sd.Condition == "" {
			sd.Condition = types.ServiceConditionStarted
		}
		if !d.Required {
			no := false
			sd.Required = &no
		}
		f.DependsOn = append(f.DependsOn, sd)
	}
	if ext := p.extendsOf(s.Name); ext != nil {
		f.Extends = ext.service
		if ext.file != "" {
			f.Extends = ext.file + "#" + ext.service
		}
	}
	// Dockerfile facts of the final stage.
	if final := p.finalStage(s); final != nil {
		for _, e := range final.Expose {
			if !slices.Contains(f.Expose, e) {
				f.Expose = append(f.Expose, e)
			}
		}
		if f.Command == "" {
			f.Command = final.Cmd
		}
		if f.Entrypoint == "" {
			f.Entrypoint = final.Entrypoint
		}
		if f.Healthcheck == nil && final.Health != nil {
			f.Healthcheck, f.HealthcheckFrom = final.Health, "dockerfile"
		}
		if f.User == "" {
			f.User = final.User
		}
		if f.WorkingDir == "" {
			f.WorkingDir = final.Workdir
		}
		f.BaseImages = p.baseImages(s)
	}
	return f
}

func healthFacts(h *types.HealthCheckConfig) *model.ServiceHealthcheck {
	out := &model.ServiceHealthcheck{Disabled: h.Disable}
	test := []string(h.Test)
	switch {
	case len(test) > 0 && test[0] == "NONE":
		out.Disabled = true
	case len(test) > 0 && (test[0] == "CMD" || test[0] == "CMD-SHELL"):
		out.Test = strings.Join(test[1:], " ")
	default:
		out.Test = strings.Join(test, " ")
	}
	dur := func(d *types.Duration) string {
		if d == nil {
			return ""
		}
		return d.String()
	}
	out.Interval, out.Timeout, out.StartPeriod, out.StartInterval = dur(h.Interval), dur(h.Timeout), dur(h.StartPeriod), dur(h.StartInterval)
	if h.Retries != nil {
		r := int(*h.Retries)
		out.Retries = &r
	}
	return out
}

func formatBytes(b int64) string {
	switch {
	case b%(1<<30) == 0:
		return fmt.Sprintf("%dG", b>>30)
	case b%(1<<20) == 0:
		return fmt.Sprintf("%dM", b>>20)
	case b%(1<<10) == 0:
		return fmt.Sprintf("%dK", b>>10)
	}
	return strconv.FormatInt(b, 10)
}

// RawEnv is a service's environment as written (before interpolation),
// later files winning.
func (p *Project) RawEnv(svc string) map[string]string {
	out := map[string]string{}
	for _, rel := range p.Order {
		_, sv := p.Docs[rel].Get("services", svc)
		if sv == nil {
			continue
		}
		env := Resolve(mergedGet(sv, "environment"))
		for k, v := range envEntries(env) {
			out[k] = v.value
		}
	}
	return out
}

type envEntry struct {
	value string
	node  *yaml.Node // the value node (mapping form) or the item (list form)
	index int        // list form: item index
	key   *yaml.Node // mapping form: the key node
	set   bool       // a value is written (not a bare KEY)
}

// envEntries reads an environment mapping or KEY=value list.
func envEntries(env *yaml.Node) map[string]envEntry {
	out := map[string]envEntry{}
	if env == nil {
		return out
	}
	switch env.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(env.Content); i += 2 {
			k, v := env.Content[i], Resolve(env.Content[i+1])
			if k.Value == "<<" {
				for kk, vv := range envEntries(v) {
					if _, own := out[kk]; !own {
						out[kk] = vv
					}
				}
				continue
			}
			out[k.Value] = envEntry{value: v.Value, node: v, key: k, set: v.Tag != "!!null"}
		}
	case yaml.SequenceNode:
		for i, it := range env.Content {
			k, v, set := strings.Cut(it.Value, "=")
			out[k] = envEntry{value: v, node: it, index: i, set: set}
		}
	}
	return out
}

// finalStage is the resolved stage a service runs (build target, else the
// last one).
func (p *Project) finalStage(s types.ServiceConfig) *StageInfo {
	stages, target := p.stages(s)
	if target < 0 {
		return nil
	}
	return &stages[target]
}

// stages resolves the Dockerfile stages of a service and the index of the
// target (-1 without a parsed Dockerfile).
func (p *Project) stages(s types.ServiceConfig) ([]StageInfo, int) {
	rel := p.dockerfilePath(s)
	df := p.Dockerfiles[rel]
	if df == nil || len(df.Stages) == 0 {
		return nil, -1
	}
	args := map[string]string{}
	for k, v := range s.Build.Args {
		if v != nil {
			args[k] = *v
		}
	}
	stages := df.Resolve(args)
	target := len(stages) - 1
	if t := strings.ToLower(s.Build.Target); t != "" {
		for i, st := range stages {
			if st.Name == t {
				target = i
			}
		}
	}
	return stages, target
}

// reachable lists the stages the target depends on (itself included), in
// Dockerfile order.
func reachable(stages []StageInfo, target int) []int {
	byRef := map[string]int{}
	for i, st := range stages {
		byRef[stageRef(st.Stage)] = i
		byRef[strconv.Itoa(i)] = i
	}
	seen := map[int]bool{}
	var visit func(i int)
	visit = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		if st := stages[i]; st.FromStage != "" {
			visit(byRef[st.FromStage])
		}
		for _, c := range stages[i].CopyFrom {
			if j, ok := byRef[strings.ToLower(c)]; ok && j < i {
				visit(j)
			}
		}
	}
	visit(target)
	var out []int
	for i := range stages {
		if seen[i] {
			out = append(out, i)
		}
	}
	return out
}

// baseImages are the external base images of the stages the target
// builds, the final stage's root last.
func (p *Project) baseImages(s types.ServiceConfig) []string {
	stages, target := p.stages(s)
	if target < 0 {
		return []string{}
	}
	root := target
	byRef := map[string]int{}
	for i, st := range stages {
		byRef[stageRef(st.Stage)] = i
	}
	for stages[root].FromStage != "" {
		root = byRef[stages[root].FromStage]
	}
	out := []string{}
	for _, i := range reachable(stages, target) {
		if stages[i].FromStage == "" && i != root && !slices.Contains(out, stages[i].From) {
			out = append(out, stages[i].From)
		}
		for _, c := range stages[i].CopyFrom {
			if _, isStage := byRef[strings.ToLower(c)]; !isStage {
				if _, err := strconv.Atoi(c); err != nil && !slices.Contains(out, c) {
					out = append(out, c)
				}
			}
		}
	}
	out = slices.DeleteFunc(out, func(x string) bool { return x == stages[root].From })
	return append(out, stages[root].From)
}

// stageNodes adds the build stages of a service (only those its target
// builds) with their edges.
func (p *Project) stageNodes(svc string, s types.ServiceConfig, add func(model.ComposeNode), edge func(model.ComposeEdge)) {
	stages, target := p.stages(s)
	if target < 0 || len(stages) < 2 {
		// A single-stage build is the service itself: no stage node.
		return
	}
	rel := p.dockerfilePath(s)
	sid := ServiceID(svc)
	id := func(ref string) string { return "stage:" + svc + ":" + ref }
	byRef := map[string]int{}
	for i, st := range stages {
		byRef[stageRef(st.Stage)] = i
		byRef[strconv.Itoa(i)] = i
	}
	for _, i := range reachable(stages, target) {
		st := stages[i]
		ref := stageRef(st.Stage)
		add(model.ComposeNode{
			ID: id(ref), Kind: model.ComposeBuildStage, Name: ref, Parent: sid, File: rel, Line: st.Line,
			Stage: &model.StageFacts{Index: st.Index, Name: st.Name, From: st.From, FromStage: st.FromStage, Platform: st.Platform,
				Dockerfile: rel, Line: st.Line, Final: i == target, Expose: st.Expose, User: st.User, Workdir: st.Workdir, Cmd: st.Cmd, Entrypoint: st.Entrypoint},
		})
		if st.FromStage != "" {
			edge(model.ComposeEdge{Source: id(stageRef(stages[byRef[st.FromStage]].Stage)), Target: id(ref), Kind: model.ComposeEdgeBuild, Label: "FROM"})
		}
		for _, c := range st.CopyFrom {
			if j, ok := byRef[strings.ToLower(c)]; ok && j < i {
				edge(model.ComposeEdge{Source: id(stageRef(stages[j].Stage)), Target: id(ref), Kind: model.ComposeEdgeCopyFrom, Label: "COPY --from"})
			}
		}
		if i == target {
			edge(model.ComposeEdge{Source: id(ref), Target: sid, Kind: model.ComposeEdgeBuild})
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pathDir(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "."
}

func cleanJoin(dir, rel string) string {
	return filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(rel))))
}
