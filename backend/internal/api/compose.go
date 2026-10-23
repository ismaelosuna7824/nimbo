package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/schema"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker workflows: a docker-compose project stored as the workflow's
// files. The graph is derived from them (compose.Derive) on every save;
// graph edits rewrite the YAML/Dockerfiles in place (compose.Editor).

// blankCompose is the compose file of a new empty docker workflow.
const blankCompose = "# Add services from the palette, or write them here.\nservices: {}\n"

func (s *Server) bestPracticeSettings() model.BestPracticeSettings {
	if s.cfg.Settings != nil {
		return s.cfg.Settings.Get().BestPractices
	}
	return model.BestPracticeSettings{}
}

// deriveDocker refreshes a docker workflow's graph and diagnostics from its
// files.
func (s *Server) deriveDocker(wf *model.Workflow, extra []model.Diagnostic) {
	diags := compose.Derive(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf, s.bestPracticeSettings())
	wf.Diagnostics = dedupe(append(diags, extra...))
	ensureSlices(wf)
}

// openCompose reads a docker workflow's project in its active environment
// (content overrides unsaved files): service edits go to the file that
// sets the attribute last, so the override file's attributes are edited
// there and everything else in the base files.
func (s *Server) openCompose(wf *model.Workflow, content map[string][]byte) *compose.Project {
	return compose.Open(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf.Compose.Files, wf.Compose.Profiles, compose.Options{Content: content, Environment: wf.Compose.Environment})
}

// openComposeEnv reads a docker workflow's project in environment env ("" =
// the base files).
func (s *Server) openComposeEnv(wf *model.Workflow, env string) *compose.Project {
	return compose.Open(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf.Compose.Files, wf.Compose.Profiles, compose.Options{Environment: env})
}

// writeAssets writes files (slash paths) into the workflow's assets
// directory, keeping the mode of files that exist.
func (s *Server) writeAssets(id string, files map[string][]byte) error {
	if len(files) == 0 {
		return nil
	}
	dir := s.cfg.Store.AssetsDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		name := filepath.FromSlash(rel)
		mode := fs.FileMode(0o644)
		if fi, err := root.Lstat(name); err == nil {
			if !fi.Mode().IsRegular() {
				return errorf(http.StatusConflict, "%s is not a regular file", rel)
			}
			mode = fi.Mode().Perm()
		}
		if d := filepath.Dir(name); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		if err := root.WriteFile(name, files[rel], mode); err != nil {
			return err
		}
	}
	return nil
}

// editErr maps compose edit errors to HTTP errors.
func editErr(err error) error {
	var ee *compose.EditError
	if errors.As(err, &ee) {
		switch {
		case ee.NotFound:
			return errorf(http.StatusNotFound, "%s", ee.Msg)
		case ee.Conflict:
			return errorf(http.StatusConflict, "%s", ee.Msg)
		}
		return errorf(http.StatusBadRequest, "%s", ee.Msg)
	}
	return err
}

// editCompose runs fn on the docker workflow id's project under the edit
// lock, writes the files it changed (plus extra files fn adds) and saves.
func (s *Server) editCompose(id string, fn func(wf *model.Workflow, p *compose.Project, e *compose.Editor, extra map[string][]byte) error) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireDocker(wf); err != nil {
			return nil, err
		}
		p := s.openCompose(wf, nil)
		e, err := p.Edit()
		if err != nil {
			return nil, editErr(err)
		}
		extra := map[string][]byte{}
		if err := fn(wf, p, e, extra); err != nil {
			return nil, editErr(err)
		}
		files := e.Files()
		maps.Copy(files, extra)
		return nil, s.writeAssets(wf.ID, files)
	})
}

// composeRef resolves a path parameter naming a service, network or volume
// by name or node ID ("service:web").
func composeRef(r *http.Request, prefix string) string {
	return strings.TrimPrefix(r.PathValue("name"), prefix+":")
}

// ---------------------------------------------------------------------------
// Create

// newDockerWorkflow writes the files of a new docker workflow (a template
// or a blank compose.yaml) into its assets.
func (s *Server) newDockerWorkflow(wf *model.Workflow, templateID string) error {
	files := map[string][]byte{compose.DefaultFile: []byte(blankCompose)}
	if templateID != "" {
		var ok bool
		if files, ok = compose.TemplateFiles(templateID); !ok {
			return errorf(http.StatusBadRequest, "unknown template %q", templateID)
		}
	}
	wf.Kind = model.WorkflowDocker
	wf.Compose = &model.ComposeModel{Files: []string{compose.DefaultFile}}
	return s.writeAssets(wf.ID, files)
}

// putDocker applies PUT /workflows/{id} to a docker workflow: name,
// description, icon, the compose files/profiles and the active
// environment (one of the override files, "" = the base); the graph is
// derived from the files (positions change through PATCH /compose/layout).
func putDocker(cur *model.Workflow, in model.Workflow) error {
	if strings.TrimSpace(in.Name) == "" {
		return errorf(http.StatusBadRequest, "name is required")
	}
	if in.Icon != nil {
		if err := in.Icon.Validate(); err != nil {
			return errorf(http.StatusBadRequest, "%v", err)
		}
		cur.Icon = in.Icon
	}
	cur.Name, cur.Description = in.Name, in.Description
	if in.ActiveEnvironment != "" && compose.FindEnvironment(cur.Compose, in.ActiveEnvironment) == nil {
		return errorf(http.StatusBadRequest, "unknown environment %q: create it in the Environments tab first", in.ActiveEnvironment)
	}
	cur.ActiveEnvironment = in.ActiveEnvironment
	if in.Compose != nil {
		if len(in.Compose.Files) == 0 {
			return errorf(http.StatusBadRequest, "compose.files needs at least one compose file")
		}
		for _, f := range in.Compose.Files {
			if _, err := cleanFilePath(f); err != nil {
				return err
			}
		}
		for _, p := range in.Compose.Profiles {
			if p == "" || strings.ContainsAny(p, " ,") {
				return errorf(http.StatusBadRequest, "invalid profile %q", p)
			}
		}
		cur.Compose.Files, cur.Compose.Profiles = slices.Clone(in.Compose.Files), slices.Clone(in.Compose.Profiles)
	}
	return nil
}

func dockerTemplate(id string) (model.Template, bool) {
	for _, t := range compose.Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return model.Template{}, false
}

// ---------------------------------------------------------------------------
// Handlers

func (s *Server) dockerImages(w http.ResponseWriter, r *http.Request) error {
	return ok(w, compose.Images())
}

func (s *Server) composeSchema(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/schema+json")
	_, err := w.Write([]byte(schema.Schema))
	return err
}

type addServiceRequest struct {
	Name      string             `json:"name"`
	Image     string             `json:"image"`
	CatalogID string             `json:"catalogId"`
	Build     *compose.BuildSpec `json:"build"`
	Position  *model.Position    `json:"position"`
}

var envNameRe = regexp.MustCompile(`[^A-Z0-9]+`)

func (s *Server) addComposeService(w http.ResponseWriter, r *http.Request) error {
	var req addServiceRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	var name string
	wf, err := s.editCompose(r.PathValue("id"), func(wf *model.Workflow, p *compose.Project, e *compose.Editor, extra map[string][]byte) error {
		spec := compose.ServiceSpec{Name: strings.TrimSpace(req.Name), Image: strings.TrimSpace(req.Image)}
		base := ""
		published := publishedPorts(p)
		hostPort := func(port int) string {
			for published[port] {
				port++
			}
			published[port] = true
			return strconv.Itoa(port)
		}
		switch {
		case req.CatalogID != "":
			im, found := compose.Image(req.CatalogID)
			if !found {
				return errorf(http.StatusBadRequest, "unknown catalog image %q", req.CatalogID)
			}
			base = im.ID
			if spec.Image == "" {
				spec.Image = im.Image + ":" + im.Tag
			}
			spec.Healthcheck = im.Healthcheck
			for _, port := range im.Ports {
				spec.Ports = append(spec.Ports, model.ServicePort{HostIP: compose.PublishIP(im.Image), Host: hostPort(port), Container: strconv.Itoa(port), Published: true})
			}
			spec.Name = uniqueService(p, orName(spec.Name, base))
			envPrefix := envNameRe.ReplaceAllString(strings.ToUpper(spec.Name), "_")
			var dotenv []string
			for _, ev := range im.Env {
				v := ev.Value
				if ev.Secret {
					// POSTGRES_PASSWORD of a service named postgres stays POSTGRES_PASSWORD (not POSTGRES_POSTGRES_PASSWORD).
					key := envNameRe.ReplaceAllString(strings.ToUpper(ev.Key), "_")
					if !strings.HasPrefix(key, envPrefix+"_") {
						key = envPrefix + "_" + key
					}
					if _, set := p.Env[key]; !set {
						dotenv = append(dotenv, key+"="+v)
					}
					v = "${" + key + "}"
				}
				spec.Environment = append(spec.Environment, compose.EnvSpec{Key: ev.Key, Value: &v})
			}
			if len(dotenv) > 0 {
				envFile := path.Join(p.ProjectDir, ".env")
				cur, err := os.ReadFile(filepath.Join(s.cfg.Store.AssetsDir(wf.ID), filepath.FromSlash(envFile)))
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				if len(cur) > 0 && cur[len(cur)-1] != '\n' {
					cur = append(cur, '\n')
				}
				cur = append(cur, []byte(fmt.Sprintf("# %s (added with the service; keep real credentials out of git)\n%s\n", spec.Name, strings.Join(dotenv, "\n")))...)
				extra[envFile] = cur
			}
			for i, v := range im.Volumes {
				vol := spec.Name + "-data"
				if i > 0 {
					vol = spec.Name + "-" + orName(v.Name, strconv.Itoa(i+1))
				}
				if err := e.AddTop("volume", compose.TopSpec{Name: vol}); err != nil {
					return err
				}
				spec.Volumes = append(spec.Volumes, model.ServiceMount{Type: "volume", Source: vol, Target: v.Target})
			}
			spec.Command = strings.ReplaceAll(im.Command, "{{host}}", spec.Name)
			spec.Entrypoint = im.Entrypoint
		case req.Build != nil:
			b := *req.Build
			// The context is relative to the project directory (the first
			// compose file's folder), like in the compose file; dir is the
			// workflow folder it names.
			b.Context = path.Clean(strings.TrimPrefix(strings.TrimSpace(b.Context), "./"))
			dir := path.Join(p.ProjectDir, b.Context)
			if !fs.ValidPath(dir) {
				return errorf(http.StatusBadRequest, "build.context must be a folder inside the workflow files")
			}
			if base = path.Base(dir); base == "." {
				base = "app"
			}
			spec.Name = uniqueService(p, orName(spec.Name, base))
			df := path.Join(dir, orName(b.Dockerfile, "Dockerfile"))
			if _, err := os.Stat(filepath.Join(s.cfg.Store.AssetsDir(wf.ID), filepath.FromSlash(df))); errors.Is(err, fs.ErrNotExist) {
				starter := orName(b.Starter, "alpine")
				files, port, ok := compose.StarterFiles(starter)
				if !ok {
					return errorf(http.StatusBadRequest, "unknown starter %q: use node, python, go, static or alpine", starter)
				}
				for rel, data := range files {
					dst := path.Join(dir, rel)
					if rel == "Dockerfile" {
						dst = df
					}
					if _, err := os.Stat(filepath.Join(s.cfg.Store.AssetsDir(wf.ID), filepath.FromSlash(dst))); errors.Is(err, fs.ErrNotExist) {
						extra[dst] = data
					}
				}
				if port > 0 {
					spec.Ports = append(spec.Ports, model.ServicePort{Host: hostPort(port), Container: strconv.Itoa(port), Published: true})
				}
				// node and python ignore SIGTERM as PID 1: run them under Docker's init.
				spec.Init = starter == "node" || starter == "python"
			}
			b.Starter = ""
			if b.Context != "." && !strings.HasPrefix(b.Context, "../") {
				b.Context = "./" + b.Context
			}
			spec.Build = &b
		case spec.Image != "":
			ref := compose.ParseImage(spec.Image)
			base = path.Base(ref.Repo)
			spec.Name = uniqueService(p, orName(spec.Name, base))
			if im, found := compose.ImageFor(spec.Image); found {
				spec.Healthcheck = im.Healthcheck
				for _, port := range im.Ports {
					spec.Ports = append(spec.Ports, model.ServicePort{HostIP: compose.PublishIP(spec.Image), Host: hostPort(port), Container: strconv.Itoa(port), Published: true})
				}
			}
		default:
			return errorf(http.StatusBadRequest, "pass image, catalogId or build")
		}
		if req.Name != "" && spec.Name != strings.TrimSpace(req.Name) {
			return errorf(http.StatusConflict, "service %q already exists", req.Name)
		}
		name = spec.Name
		// Long-running services come back after a crash or a Docker restart.
		spec.Restart = "unless-stopped"
		if req.Position != nil {
			wf.Compose.Graph.Nodes = append(wf.Compose.Graph.Nodes, model.ComposeNode{ID: compose.ServiceID(name), Position: req.Position})
		}
		return e.AddService(spec)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "nodeId": compose.ServiceID(name)})
}

func orName(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

var serviceNameClean = regexp.MustCompile(`[^a-z0-9._-]+`)

// uniqueService turns base into a free service name (-2, -3, ...).
func uniqueService(p *compose.Project, base string) string {
	base = strings.Trim(serviceNameClean.ReplaceAllString(strings.ToLower(base), "-"), "-._")
	if base == "" {
		base = "service"
	}
	name := base
	for i := 2; ; i++ {
		if _, taken := p.Model.Services[name]; !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// publishedPorts are the host ports the project's services publish.
func publishedPorts(p *compose.Project) map[int]bool {
	out := map[int]bool{}
	for _, svc := range p.Model.Services {
		for _, port := range svc.Ports {
			if n, err := strconv.Atoi(port.Published); err == nil {
				out[n] = true
			}
		}
	}
	return out
}

func (s *Server) patchComposeService(w http.ResponseWriter, r *http.Request) error {
	var patch map[string]json.RawMessage
	if err := decode(w, r, maxBody, &patch); err != nil {
		return err
	}
	name := composeRef(r, "service")
	wf, err := s.editCompose(r.PathValue("id"), func(wf *model.Workflow, _ *compose.Project, e *compose.Editor, _ map[string][]byte) error {
		if raw, ok := patch["name"]; ok {
			var n string
			if json.Unmarshal(raw, &n) == nil && n != name {
				movePosition(wf, compose.ServiceID(name), compose.ServiceID(n))
			}
		}
		return e.PatchService(name, patch)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// movePosition carries a node's canvas position over a rename.
func movePosition(wf *model.Workflow, from, to string) {
	for i, n := range wf.Compose.Graph.Nodes {
		if n.ID == from {
			wf.Compose.Graph.Nodes[i].ID = to
		}
		if strings.HasPrefix(n.ID, "stage:"+strings.TrimPrefix(from, "service:")+":") && strings.HasPrefix(from, "service:") {
			wf.Compose.Graph.Nodes[i].ID = "stage:" + strings.TrimPrefix(to, "service:") + ":" + strings.TrimPrefix(n.ID, "stage:"+strings.TrimPrefix(from, "service:")+":")
		}
	}
}

func (s *Server) deleteComposeService(w http.ResponseWriter, r *http.Request) error {
	name := composeRef(r, "service")
	wf, err := s.editCompose(r.PathValue("id"), func(_ *model.Workflow, _ *compose.Project, e *compose.Editor, _ map[string][]byte) error {
		return e.RemoveService(name)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) addComposeTop(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req compose.TopSpec
		if err := decode(w, r, maxBody, &req); err != nil {
			return err
		}
		req.Name = strings.TrimSpace(req.Name)
		wf, err := s.editCompose(r.PathValue("id"), func(_ *model.Workflow, _ *compose.Project, e *compose.Editor, _ map[string][]byte) error {
			return e.AddTop(kind, req)
		})
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "nodeId": kind + ":" + req.Name})
	}
}

func (s *Server) patchComposeTop(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var patch map[string]json.RawMessage
		if err := decode(w, r, maxBody, &patch); err != nil {
			return err
		}
		name := composeRef(r, kind)
		wf, err := s.editCompose(r.PathValue("id"), func(wf *model.Workflow, _ *compose.Project, e *compose.Editor, _ map[string][]byte) error {
			if raw, ok := patch["name"]; ok {
				var n string
				if json.Unmarshal(raw, &n) == nil && n != name {
					movePosition(wf, kind+":"+name, kind+":"+n)
				}
			}
			return e.PatchTop(kind, name, patch)
		})
		if err != nil {
			return err
		}
		return ok(w, wf)
	}
}

func (s *Server) deleteComposeTop(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		name := composeRef(r, kind)
		wf, err := s.editCompose(r.PathValue("id"), func(_ *model.Workflow, _ *compose.Project, e *compose.Editor, _ map[string][]byte) error {
			return e.RemoveTop(kind, name)
		})
		if err != nil {
			return err
		}
		return ok(w, wf)
	}
}

// ConnectRecipe is one way to connect two compose nodes
// (GET /workflows/{id}/compose/connect).
type connectRecipe struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"` // depends_on | network | volume | env
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Condition   string            `json:"condition,omitempty"`
	Network     string            `json:"network,omitempty"`
	Target      string            `json:"target,omitempty"`
	Env         []compose.EnvSpec `json:"env,omitempty"`
}

// connectRecipes lists the ways service from can use node to.
func connectRecipes(p *compose.Project, from, to string) ([]connectRecipe, error) {
	from = strings.TrimPrefix(from, "service:")
	src, ok := p.Model.Services[from]
	if !ok {
		return nil, errorf(http.StatusNotFound, "service %q not found", from)
	}
	var out []connectRecipe
	switch {
	case strings.HasPrefix(to, "volume:"):
		vol := strings.TrimPrefix(to, "volume:")
		rc := connectRecipe{ID: "volume", Kind: "volume", Label: "Mount volume " + vol, Description: fmt.Sprintf("Mount the named volume %s into %s (data survives container re-creation).", vol, from)}
		if im, found := compose.ImageFor(src.Image); found && len(im.Volumes) > 0 {
			rc.Target = im.Volumes[0].Target
		}
		out = append(out, rc)
	case strings.HasPrefix(to, "network:"):
		nw := strings.TrimPrefix(to, "network:")
		out = append(out, connectRecipe{ID: "network", Kind: "network", Network: nw, Label: "Join network " + nw, Description: fmt.Sprintf("Attach %s to the network %s.", from, nw)})
	default:
		to = strings.TrimPrefix(to, "service:")
		dst, ok := p.Model.Services[to]
		if !ok {
			return nil, errorf(http.StatusNotFound, "service %q not found", to)
		}
		if to == from {
			return nil, errorf(http.StatusBadRequest, "a service can't connect to itself")
		}
		out = append(out, connectRecipe{ID: "depends_on", Kind: "depends_on", Condition: "service_started", Label: "Start after " + to, Description: fmt.Sprintf("%s starts once %s has started (depends_on).", from, to)})
		healthy := dst.HealthCheck != nil
		desc := fmt.Sprintf("%s starts once %s reports healthy (depends_on condition: service_healthy).", from, to)
		if !healthy {
			desc += " " + to + " has no healthcheck yet: add one, or it never becomes healthy."
		}
		out = append(out, connectRecipe{ID: "depends_on_healthy", Kind: "depends_on", Condition: "service_healthy", Label: "Wait until " + to + " is healthy", Description: desc})
		if im, found := compose.ImageFor(dst.Image); found && len(im.Connect) > 0 {
			// Values as written: a password read from .env stays ${VAR}.
			env := map[string]string{}
			for k, v := range dst.Environment {
				if v != nil {
					env[k] = *v
				}
			}
			maps.Copy(env, p.RawEnv(to))
			port := 0
			if len(im.Ports) > 0 {
				port = im.Ports[0]
			}
			rc := connectRecipe{ID: "env", Kind: "env", Label: "Set connection settings", Description: fmt.Sprintf("Write the variables %s needs to reach %s (%s).", from, to, im.Label)}
			for _, c := range im.Connect {
				v := compose.RenderConnect(c.Template, to, port, env)
				rc.Env = append(rc.Env, compose.EnvSpec{Key: c.Key, Value: &v})
			}
			out = append(out, rc)
		}
		// A shared network when they are on different explicit networks.
		srcNets, dstNets := slices.Sorted(maps.Keys(src.Networks)), slices.Sorted(maps.Keys(dst.Networks))
		shared := slices.ContainsFunc(srcNets, func(n string) bool { return slices.Contains(dstNets, n) })
		if !shared {
			nw := "backend"
			if len(dstNets) > 0 && dstNets[0] != "default" {
				nw = dstNets[0]
			}
			out = append(out, connectRecipe{ID: "network", Kind: "network", Network: nw, Label: "Share network " + nw, Description: fmt.Sprintf("Put %s and %s on the network %s so they can reach each other by name.", from, to, nw)})
		}
	}
	return out, nil
}

func (s *Server) composeConnectRecipes(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := requireDocker(wf); err != nil {
		return err
	}
	p := s.openCompose(wf, nil)
	if p.Model == nil {
		return errorf(http.StatusConflict, "the compose files can't be loaded; fix them in the Code tab first")
	}
	out, err := connectRecipes(p, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		return err
	}
	return ok(w, nonNil(out))
}

func (s *Server) composeConnect(w http.ResponseWriter, r *http.Request) error {
	var req compose.ConnectRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.editCompose(r.PathValue("id"), func(_ *model.Workflow, p *compose.Project, e *compose.Editor, _ map[string][]byte) error {
		to := req.To
		if req.RecipeID != "" {
			recipes, err := connectRecipes(p, req.From, req.To)
			if err != nil {
				return err
			}
			i := slices.IndexFunc(recipes, func(rc connectRecipe) bool { return rc.ID == req.RecipeID })
			if i < 0 {
				return errorf(http.StatusBadRequest, "unknown recipe %q", req.RecipeID)
			}
			rc := recipes[i]
			req.Kind = rc.Kind
			req.Condition = orName(req.Condition, rc.Condition)
			req.Network = orName(req.Network, rc.Network)
			req.Target = orName(req.Target, rc.Target)
			if rc.Kind == "env" {
				req.Env = map[string]string{}
				for _, ev := range rc.Env {
					req.Env[ev.Key] = *ev.Value
				}
			}
		}
		if req.Kind == "" {
			switch {
			case strings.HasPrefix(to, "volume:"):
				req.Kind = "volume"
			case strings.HasPrefix(to, "network:"):
				req.Kind, req.Network = "network", orName(req.Network, strings.TrimPrefix(to, "network:"))
			default:
				req.Kind = "depends_on"
			}
		}
		req.From = strings.TrimPrefix(req.From, "service:")
		if req.Kind == "network" && strings.HasPrefix(to, "network:") {
			req.Network, req.To = orName(req.Network, strings.TrimPrefix(to, "network:")), ""
		} else {
			req.To = strings.TrimPrefix(strings.TrimPrefix(to, "service:"), "volume:")
		}
		if req.Kind == "volume" && req.Target == "" {
			if im, found := compose.ImageFor(p.Model.Services[req.From].Image); found && len(im.Volumes) > 0 {
				req.Target = im.Volumes[0].Target
			}
		}
		return e.Connect(req)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// composeLayout saves canvas positions (PATCH /compose/layout).
func (s *Server) composeLayout(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Positions map[string]*model.Position `json:"positions"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireDocker(wf); err != nil {
			return nil, err
		}
		for id, pos := range req.Positions {
			i := slices.IndexFunc(wf.Compose.Graph.Nodes, func(n model.ComposeNode) bool { return n.ID == id })
			if i < 0 {
				return nil, errorf(http.StatusBadRequest, "node %q not found", id)
			}
			wf.Compose.Graph.Nodes[i].Position = pos
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// patchComposeStage changes the base image of a build stage (its FROM).
func (s *Server) patchComposeStage(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		From string `json:"from"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	nodeID := r.PathValue("nodeId")
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireDocker(wf); err != nil {
			return nil, err
		}
		var st *model.StageFacts
		for _, n := range wf.Compose.Graph.Nodes {
			if n.ID == nodeID && n.Stage != nil {
				st = n.Stage
			}
			if n.ID == nodeID && n.Kind == model.ComposeService && n.Service != nil && n.Service.Build != nil && st == nil {
				// A single-stage build: the service's Dockerfile, stage 0.
				st = &model.StageFacts{Index: 0, Dockerfile: n.Service.Build.Dockerfile}
			}
		}
		if st == nil {
			return nil, errorf(http.StatusNotFound, "build stage %q not found", nodeID)
		}
		file := filepath.Join(s.cfg.Store.AssetsDir(wf.ID), filepath.FromSlash(st.Dockerfile))
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, errorf(http.StatusNotFound, "%s can't be read", st.Dockerfile)
		}
		out, err := compose.SetStageBase(src, st.Index, strings.TrimSpace(req.From))
		if err != nil {
			return nil, errorf(http.StatusBadRequest, "%v", err)
		}
		return nil, s.writeAssets(wf.ID, map[string][]byte{st.Dockerfile: out})
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// checkComposeCode is POST /terraform/check for a docker workflow: the
// problems of a compose file, Dockerfile or .env being edited.
func (s *Server) checkComposeCode(wf *model.Workflow, req model.CodeCheckRequest, rel string) ([]model.CodeProblem, error) {
	content := map[string][]byte{}
	for p, c := range req.Files {
		cp, err := cleanFilePath(p)
		if err != nil {
			return nil, err
		}
		content[cp] = []byte(c)
	}
	if _, found := content[rel]; !found {
		return nil, errorf(http.StatusBadRequest, "files has no content for %s", rel)
	}
	problems := []model.CodeProblem{}
	add := func(d model.Diagnostic) {
		line := max(d.Line, 1)
		problems = append(problems, model.CodeProblem{Severity: d.Severity, Code: d.Code, Message: d.Message, Detail: d.Detail, Line: line, Column: 1, EndLine: line, EndColumn: 1000})
	}
	if compose.IsDockerfileName(path.Base(rel)) && !slices.Contains(wf.Compose.Files, rel) {
		_, probs := compose.ParseDockerfile(content[rel])
		for _, pr := range probs {
			add(model.Diagnostic{Severity: model.SeverityError, Code: "dk_dockerfile_syntax", Message: pr.Message, Line: pr.Line})
		}
		if len(probs) > 0 {
			return problems, nil
		}
	}
	p := s.openCompose(wf, content)
	for _, d := range compose.FilterSettings(p.Diagnostics, s.bestPracticeSettings()) {
		if d.File == rel {
			add(d)
		}
	}
	return problems, nil
}

// terraformOnly wraps an endpoint of Terraform workflows: a docker
// workflow gets 400 naming what to use instead (hint).
func (s *Server) terraformOnly(what, hint string, fn func(http.ResponseWriter, *http.Request) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		wf, err := s.cfg.Store.Get(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !wf.IsTerraform() {
			msg := fmt.Sprintf("%s: %s is a %s workflow", what, wf.ID, model.KindLabel(wf.Kind))
			if hint != "" {
				msg += "; " + hint
			}
			return errorf(http.StatusBadRequest, "%s", msg+" (only Terraform workflows have this)")
		}
		return fn(w, r)
	}
}

// dockerSyntax checks the compose files and Dockerfiles a code request
// changes: YAML or Dockerfile syntax errors refuse the save (like HCL
// syntax errors for Terraform); problems of meaning are saved and shown.
func dockerSyntax(wf *model.Workflow, entries map[string]*fileEntry) []model.Diagnostic {
	var out []model.Diagnostic
	for _, p := range slices.Sorted(maps.Keys(entries)) {
		e := entries[p]
		if !e.changed || e.data == nil {
			continue
		}
		base := path.Base(p)
		isCompose := compose.IsComposeName(base) || wf.Compose != nil && (slices.Contains(wf.Compose.Files, p) || slices.Contains(wf.Compose.Included, p))
		switch {
		case isCompose:
			if _, err := compose.ParseDoc(p, e.data); err != nil {
				var se *compose.SyntaxError
				if errors.As(err, &se) {
					out = append(out, model.Diagnostic{Severity: model.SeverityError, Code: "dk_yaml", Message: fmt.Sprintf("%s: %s", p, se.Message), File: p, Line: se.Line})
				}
			}
		case compose.IsDockerfileName(base):
			_, problems := compose.ParseDockerfile(e.data)
			for _, pr := range problems {
				out = append(out, model.Diagnostic{Severity: model.SeverityError, Code: "dk_dockerfile_syntax", Message: fmt.Sprintf("%s:%d: %s", p, pr.Line, pr.Message), File: p, Line: pr.Line})
			}
		}
	}
	return out
}

// followComposeFiles keeps Compose.Files pointing at the compose files
// after the Code tab moved or deleted them.
func followComposeFiles(wf *model.Workflow, entries map[string]*fileEntry) {
	moved := map[string]string{}
	for p, e := range entries {
		if e.origin != "" {
			moved[e.origin] = p
		}
	}
	var files []string
	for _, f := range wf.Compose.Files {
		if to, ok := moved[f]; ok {
			files = append(files, to)
		}
	}
	if len(files) == 0 {
		files = defaultComposeFiles(func(n string) bool { _, ok := entries[n]; return ok })
	}
	wf.Compose.Files = nonNil(files)
}

// defaultComposeFiles picks the compose files of a project whose list is
// empty: the default names docker compose looks for.
func defaultComposeFiles(has func(string) bool) []string {
	return nonNil(compose.DefaultFiles(has))
}
