package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Kubernetes workflows: manifests, Kustomize trees and Helm charts stored
// as the workflow's files. The graph is derived from the rendered objects
// (kube.Derive) on every save; graph edits rewrite the files in place
// where each field comes from (kube.Editor).

// blankKubeDir and blankKubeManifest make the source of a new empty
// Kubernetes workflow.
const (
	blankKubeDir      = "k8s"
	blankKubeManifest = "# Add objects from the palette, or write Kubernetes manifests here.\n" +
		"# Every *.yaml file in k8s/ is applied (like kubectl apply -f k8s/).\n"
)

// deriveKube refreshes a Kubernetes workflow's graph and diagnostics from
// its files (render problems plus the k8s_* rules under the settings).
func (s *Server) deriveKube(wf *model.Workflow, extra []model.Diagnostic) {
	p, diags := kube.Derive(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf)
	diags = append(diags, kuberules.Check(p, wf, s.bestPracticeSettings())...)
	wf.Diagnostics = dedupe(append(diags, extra...))
	ensureSlices(wf)
}

// openKube renders a Kubernetes workflow's files for the environment its
// graph shows (content overrides unsaved files).
func (s *Server) openKube(wf *model.Workflow, content map[string][]byte) *kube.Project {
	env := ""
	if wf.Kubernetes != nil {
		env = wf.Kubernetes.Environment
	}
	return kube.Open(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf, kube.Options{Environment: env, Content: content})
}

// requireKube rejects Kubernetes endpoints for other workflows.
func requireKube(wf *model.Workflow) error {
	if !wf.IsKubernetes() {
		return errorf(http.StatusBadRequest, "this is a %s workflow: kubernetes endpoints need a Kubernetes workflow", model.KindLabel(wf.Kind))
	}
	return nil
}

// kubeEditErr maps kube edit errors to HTTP errors.
func kubeEditErr(err error) error {
	var ee *kube.EditError
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

// editKube runs fn on the Kubernetes workflow id under the edit lock: fn
// may change wf.Kubernetes, edit files through e and add files (a nil
// value deletes one); the files are written and the workflow saved
// (derived, checked, announced).
func (s *Server) editKube(id string, fn func(wf *model.Workflow, p *kube.Project, e *kube.Editor, files map[string][]byte) error) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		p := s.openKube(wf, nil)
		e := p.Edit()
		files := map[string][]byte{}
		if err := fn(wf, p, e, files); err != nil {
			return nil, kubeEditErr(err)
		}
		changed := e.Files()
		maps.Copy(changed, files)
		return nil, s.writeKubeFiles(wf.ID, changed)
	})
}

// writeKubeFiles writes files (nil content deletes the file).
func (s *Server) writeKubeFiles(id string, files map[string][]byte) error {
	write := map[string][]byte{}
	for rel, data := range files {
		if _, err := cleanFilePath(rel); err != nil {
			return err
		}
		if data == nil {
			if err := s.removeAsset(id, rel); err != nil {
				return err
			}
			continue
		}
		write[rel] = data
	}
	return s.writeAssets(id, write)
}

// removeAsset deletes a file of the workflow's assets (absent is fine).
func (s *Server) removeAsset(id, rel string) error {
	root, err := os.OpenRoot(s.cfg.Store.AssetsDir(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.FromSlash(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// kubeObject resolves the node ID of a request (query "node").
func kubeObject(p *kube.Project, id string) (*kube.Object, error) {
	if id == "" {
		return nil, errorf(http.StatusBadRequest, "node is required (a node ID such as Deployment/default/web)")
	}
	o := p.Object(id)
	if o == nil {
		return nil, errorf(http.StatusNotFound, "object %q not found in the rendered workflow", id)
	}
	return o, nil
}

// ---------------------------------------------------------------------------
// Create

// newKubeWorkflow writes the files of a new Kubernetes workflow (a
// template, or a k8s/ manifests folder) into its assets.
func (s *Server) newKubeWorkflow(wf *model.Workflow, templateID string) error {
	files := map[string][]byte{path.Join(blankKubeDir, "app.yaml"): []byte(blankKubeManifest)}
	k := &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: blankKubeDir}}}
	if templateID != "" {
		var ok bool
		if files, k, ok = kubecatalog.TemplateFiles(templateID); !ok {
			return errorf(http.StatusBadRequest, "unknown template %q", templateID)
		}
	}
	wf.Kind = model.WorkflowKubernetes
	wf.Kubernetes = k
	return s.writeAssets(wf.ID, files)
}

// createKubeWorkflow creates and saves a Kubernetes workflow holding files
// deployed as k describes (conversions, agents). The workflow is removed
// again when it can't be saved.
func (s *Server) createKubeWorkflow(name, description string, files map[string][]byte, k *model.KubernetesModel) (*model.Workflow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errorf(http.StatusBadRequest, "name is required")
	}
	if k == nil || len(k.Sources) == 0 {
		return nil, errorf(http.StatusBadRequest, "a Kubernetes workflow needs at least one source")
	}
	if err := validKubeModel(k); err != nil {
		return nil, err
	}
	for rel := range files {
		if _, err := cleanFilePath(rel); err != nil {
			return nil, err
		}
	}
	wf := &model.Workflow{ID: store.NewID(name), Name: name, Description: description, Kind: model.WorkflowKubernetes,
		Kubernetes: &model.KubernetesModel{Sources: slices.Clone(k.Sources), Builds: slices.Clone(k.Builds)}}
	wf.Icon = s.randomIcon()
	if err := s.writeAssets(wf.ID, files); err != nil {
		_ = s.cfg.Store.Delete(wf.ID)
		return nil, err
	}
	if err := s.save(wf, nil); err != nil {
		_ = s.cfg.Store.Delete(wf.ID)
		return nil, err
	}
	return wf, nil
}

// putKube applies PUT /workflows/{id} to a Kubernetes workflow: name,
// description, icon, the active environment and the sources and builds;
// the graph is derived from the files (positions change through PATCH
// /kubernetes/layout).
func putKube(cur *model.Workflow, in model.Workflow) error {
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
	cur.ActiveEnvironment = in.ActiveEnvironment
	if in.Kubernetes != nil {
		if err := validKubeModel(in.Kubernetes); err != nil {
			return err
		}
		if cur.Kubernetes == nil {
			cur.Kubernetes = &model.KubernetesModel{}
		}
		cur.Kubernetes.Sources = authoredSources(in.Kubernetes.Sources)
		cur.Kubernetes.Builds = slices.Clone(in.Kubernetes.Builds)
	}
	return nil
}

// authoredSources keeps the fields clients set (derived ones are
// recomputed).
func authoredSources(in []model.KubeSource) []model.KubeSource {
	out := make([]model.KubeSource, len(in))
	for i, s := range in {
		out[i] = model.KubeSource{Kind: s.Kind, Path: s.Path, Release: s.Release, Namespace: s.Namespace,
			ValuesFiles: slices.Clone(s.ValuesFiles), Environments: maps.Clone(s.Environments)}
	}
	return out
}

// validKubeModel checks sources and builds.
func validKubeModel(k *model.KubernetesModel) error {
	for i, src := range k.Sources {
		switch src.Kind {
		case model.KubeManifests, model.KubeKustomize, model.KubeHelm:
		default:
			return errorf(http.StatusBadRequest, "sources[%d]: invalid kind %q: use manifests, kustomize or helm", i, src.Kind)
		}
		if src.Path != "." {
			if _, err := cleanFilePath(src.Path); err != nil {
				return errorf(http.StatusBadRequest, "sources[%d].path: %v", i, err)
			}
		}
		for _, f := range src.ValuesFiles {
			if _, err := cleanFilePath(f); err != nil {
				return errorf(http.StatusBadRequest, "sources[%d].valuesFiles: %v", i, err)
			}
		}
		for env, p := range src.Environments {
			if env == "" || strings.ContainsAny(env, " /") {
				return errorf(http.StatusBadRequest, "sources[%d].environments: invalid environment name %q", i, env)
			}
			if _, err := cleanFilePath(p); err != nil {
				return errorf(http.StatusBadRequest, "sources[%d].environments[%s]: %v", i, env, err)
			}
		}
		if src.Namespace != "" && !dnsLabel(src.Namespace) {
			return errorf(http.StatusBadRequest, "sources[%d].namespace %q is not a valid namespace name", i, src.Namespace)
		}
		if src.Release != "" && !dnsLabel(src.Release) {
			return errorf(http.StatusBadRequest, "sources[%d].release %q is not a valid release name", i, src.Release)
		}
	}
	for i, b := range k.Builds {
		if strings.TrimSpace(b.Image) == "" {
			return errorf(http.StatusBadRequest, "builds[%d].image is required", i)
		}
		if b.Context != "." {
			if _, err := cleanFilePath(b.Context); err != nil {
				return errorf(http.StatusBadRequest, "builds[%d].context: %v", i, err)
			}
		}
	}
	return nil
}

// dnsLabel reports an RFC 1123 label (namespaces, release names).
func dnsLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func kubeTemplate(id string) (model.Template, bool) {
	for _, t := range kubecatalog.Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return model.Template{}, false
}

// ---------------------------------------------------------------------------
// Handlers

// kubeLayout saves canvas positions (PATCH /kubernetes/layout) of objects
// and of collapsed charts (KubeChartGroup IDs).
func (s *Server) kubeLayout(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Positions map[string]*model.Position `json:"positions"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireKube(wf); err != nil {
			return nil, err
		}
		for id, pos := range req.Positions {
			if i := slices.IndexFunc(wf.Kubernetes.Graph.Nodes, func(n model.KubeNode) bool { return n.ID == id }); i >= 0 {
				wf.Kubernetes.Graph.Nodes[i].Position = pos
				continue
			}
			i := slices.IndexFunc(wf.Kubernetes.Graph.Charts, func(c model.KubeChartGroup) bool { return c.ID == id })
			if i < 0 {
				return nil, errorf(http.StatusBadRequest, "node %q not found", id)
			}
			wf.Kubernetes.Graph.Charts[i].Position = pos
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// kubeRendered is GET /kubernetes/render.
type kubeRendered struct {
	Environment string             `json:"environment"`
	Sources     []kubeRenderedSrc  `json:"sources"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
}

type kubeRenderedSrc struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	YAML  string `json:"yaml"`
	Error string `json:"error,omitempty"`
}

// kubeRender renders the sources for an environment (query environment;
// default: the one the graph shows).
func (s *Server) kubeRender(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := requireKube(wf); err != nil {
		return err
	}
	env := wf.Kubernetes.Environment
	if q := r.URL.Query(); q.Has("environment") {
		env = q.Get("environment")
	}
	p := kube.Open(r.Context(), s.cfg.Store.AssetsDir(wf.ID), wf, kube.Options{Environment: env})
	out := kubeRendered{Environment: env, Sources: []kubeRenderedSrc{}, Diagnostics: nonNil(p.Diagnostics)}
	for _, src := range p.Sources {
		rs := kubeRenderedSrc{Index: src.Index, Kind: src.Source.Kind, Path: src.Source.Path, YAML: string(src.YAML)}
		if src.Err != nil {
			rs.Error = src.Err.Error()
		}
		out.Sources = append(out.Sources, rs)
	}
	return ok(w, out)
}

// kubeFields says where every field of an object comes from
// (GET /kubernetes/fields?node=<id>).
func (s *Server) kubeFields(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := requireKube(wf); err != nil {
		return err
	}
	p := s.openKube(wf, nil)
	o, err := kubeObject(p, r.URL.Query().Get("node"))
	if err != nil {
		return err
	}
	return ok(w, model.KubeFields{NodeID: o.ID, Fields: nonNil(p.Fields(o))})
}

// patchKubeObject sets/unsets fields of an object where they are written
// (PATCH /kubernetes/objects).
func (s *Server) patchKubeObject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Node  string         `json:"node"`
		Set   map[string]any `json:"set"`
		Unset []string       `json:"unset"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if len(req.Set) == 0 && len(req.Unset) == 0 {
		return errorf(http.StatusBadRequest, "set or unset at least one field")
	}
	wf, err := s.editKube(r.PathValue("id"), func(_ *model.Workflow, p *kube.Project, e *kube.Editor, _ map[string][]byte) error {
		o, err := kubeObject(p, req.Node)
		if err != nil {
			return err
		}
		for _, f := range slices.Sorted(maps.Keys(req.Set)) {
			if err := e.Set(o, f, req.Set[f]); err != nil {
				return err
			}
		}
		for _, f := range req.Unset {
			if err := e.Unset(o, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// addKubeObjects adds YAML documents to source (-1: the first source) of
// the Kubernetes workflow id, in file when given (see kube.Editor
// AddObjects), and returns the saved workflow and the added objects' node
// IDs.
func (s *Server) addKubeObjects(id string, yamlText []byte, source int, file string) (*model.Workflow, []string, error) {
	var ids []string
	wf, err := s.editKube(id, func(wf *model.Workflow, p *kube.Project, e *kube.Editor, _ map[string][]byte) error {
		if len(wf.Kubernetes.Sources) == 0 {
			return errorf(http.StatusConflict, "the workflow has no source to add objects to: add one (PUT the workflow's kubernetes.sources)")
		}
		if source < 0 {
			source = 0
		}
		if source >= len(wf.Kubernetes.Sources) {
			return errorf(http.StatusBadRequest, "source %d not found (the workflow has %d)", source, len(wf.Kubernetes.Sources))
		}
		_, objs, err := e.AddObjects(source, string(yamlText), file)
		if err != nil {
			return err
		}
		ns := wf.Kubernetes.Sources[source].Namespace
		if ns == "" {
			ns = "default"
		}
		for _, o := range objs {
			ids = append(ids, kube.ObjectID(o, ns))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return wf, ids, nil
}

// addKubeObject is POST /kubernetes/objects {yaml, source?, file?}.
func (s *Server) addKubeObject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		YAML   string `json:"yaml"`
		Source *int   `json:"source"`
		File   string `json:"file"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.YAML) == "" {
		return errorf(http.StatusBadRequest, "yaml is required")
	}
	src := -1
	if req.Source != nil {
		src = *req.Source
	}
	wf, _, err := s.addKubeObjects(r.PathValue("id"), []byte(req.YAML), src, req.File)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, wf)
}

// deleteKubeObject removes an object's document (DELETE
// /kubernetes/objects?node=<id>).
func (s *Server) deleteKubeObject(w http.ResponseWriter, r *http.Request) error {
	node := r.URL.Query().Get("node")
	wf, err := s.editKube(r.PathValue("id"), func(_ *model.Workflow, p *kube.Project, e *kube.Editor, _ map[string][]byte) error {
		o, err := kubeObject(p, node)
		if err != nil {
			return err
		}
		return e.RemoveObject(o)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) kubeConnectRecipes(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := requireKube(wf); err != nil {
		return err
	}
	p := s.openKube(wf, nil)
	out, err := p.Recipes(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		return kubeEditErr(err)
	}
	return ok(w, nonNil(out))
}

func (s *Server) kubeConnect(w http.ResponseWriter, r *http.Request) error {
	var req kube.ConnectRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.editKube(r.PathValue("id"), func(_ *model.Workflow, _ *kube.Project, e *kube.Editor, _ map[string][]byte) error {
		_, err := e.Connect(req)
		return err
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// ---------------------------------------------------------------------------
// Code tab

// checkKubeCode is POST /terraform/check for a Kubernetes workflow: the
// problems of a manifest, kustomization, values file or template being
// edited (rendered with the unsaved content).
func (s *Server) checkKubeCode(wf *model.Workflow, req model.CodeCheckRequest, rel string) ([]model.CodeProblem, error) {
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
		problems = append(problems, model.CodeProblem{Severity: d.Severity, Code: d.Code, Message: d.Message, Detail: d.Detail,
			Attribute: d.Attribute, Fix: d.Fix, Line: line, Column: 1, EndLine: line, EndColumn: 1000})
	}
	if isYAMLName(rel) && !helmTemplatePath(wf, rel) {
		f := kube.ParseFile(rel, content[rel])
		if errs := f.Errors(); len(errs) > 0 {
			for _, se := range errs {
				add(model.Diagnostic{Severity: model.SeverityError, Code: "k8s_yaml", Message: se.Message, File: rel, Line: se.Line})
			}
			return problems, nil
		}
	}
	p := s.openKube(wf, content)
	diags := append(slices.Clone(p.Diagnostics), kuberules.Check(p, wf, s.bestPracticeSettings())...)
	for _, d := range diags {
		if d.File == rel {
			add(d)
		}
	}
	return problems, nil
}

func isYAMLName(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".yaml" || ext == ".yml" || path.Base(p) == "Kustomization"
}

// helmTemplatePath reports a file of a Helm source's templates (not YAML
// until rendered).
func helmTemplatePath(wf *model.Workflow, p string) bool {
	return wf.Kubernetes != nil && kube.FileKind(wf, p) == model.FileHelmTemplate
}

// kubeSyntax checks the YAML files a code request changes (manifests,
// kustomizations, values files; Helm templates are checked by rendering):
// syntax errors refuse the save, like HCL syntax errors for Terraform.
func kubeSyntax(wf *model.Workflow, entries map[string]*fileEntry) []model.Diagnostic {
	var out []model.Diagnostic
	for _, p := range slices.Sorted(maps.Keys(entries)) {
		e := entries[p]
		if !e.changed || e.data == nil || !isYAMLName(p) || helmTemplatePath(wf, p) {
			continue
		}
		for _, se := range kube.ParseFile(p, e.data).Errors() {
			out = append(out, model.Diagnostic{Severity: model.SeverityError, Code: "k8s_yaml", Message: fmt.Sprintf("%s:%d: %s", p, se.Line, se.Message), File: p, Line: se.Line})
		}
	}
	return out
}

// followKubeSources keeps the sources' paths (and values files, overlays
// and build contexts) pointing at their files after the Code tab moved
// them.
func followKubeSources(wf *model.Workflow, entries map[string]*fileEntry) {
	if wf.Kubernetes == nil {
		return
	}
	moved := map[string]string{}
	for p, e := range entries {
		if e.origin != "" && e.origin != p {
			moved[e.origin] = p
		}
	}
	if len(moved) == 0 {
		return
	}
	follow := func(p string) string {
		if p == "" || p == "." {
			return p
		}
		if to, ok := moved[p]; ok {
			return to
		}
		// A folder follows its files when they all moved together.
		var to string
		for from, dst := range moved {
			rest, ok := strings.CutPrefix(from, p+"/")
			if !ok {
				continue
			}
			base, ok := strings.CutSuffix(dst, "/"+rest)
			if !ok || to != "" && to != base {
				return p
			}
			to = base
		}
		for q, e := range entries {
			if strings.HasPrefix(q, p+"/") && (e.origin == "" || e.origin == q) {
				return p // files stayed: the folder did not move
			}
		}
		if to == "" {
			return p
		}
		return to
	}
	k := wf.Kubernetes
	for i := range k.Sources {
		src := &k.Sources[i]
		src.Path = follow(src.Path)
		for j, f := range src.ValuesFiles {
			src.ValuesFiles[j] = follow(f)
		}
		for env, p := range src.Environments {
			src.Environments[env] = follow(p)
		}
	}
	for i := range k.Builds {
		k.Builds[i].Context = follow(k.Builds[i].Context)
	}
}
