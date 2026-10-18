package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// finalize derives edges/clouds, fills value pairs and recomputes
// diagnostics: normalization first, then schema validation, then extra
// (e.g. import) diagnostics, deduplicated.
func (s *Server) finalize(wf *model.Workflow, extra []model.Diagnostic) {
	wf.Diagnostics = nil
	diags := hclcodec.Normalize(wf)
	diags = append(diags, s.cfg.Catalog.Validate(wf)...)
	diags = append(diags, extra...)
	wf.Diagnostics = dedupe(diags)
	ensureSlices(wf)
}

func dedupe(diags []model.Diagnostic) []model.Diagnostic {
	type key struct{ sev, code, msg, node, attr string }
	seen := map[key]bool{}
	out := []model.Diagnostic{}
	for _, d := range diags {
		k := key{string(d.Severity), d.Code, d.Message, d.NodeID, d.Attribute}
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	return out
}

// ensureSlices replaces nil slices so the wire format always has arrays.
func ensureSlices(wf *model.Workflow) {
	wf.Nodes = nonNil(wf.Nodes)
	wf.Variables = nonNil(wf.Variables)
	wf.Outputs = nonNil(wf.Outputs)
	wf.Locals = nonNil(wf.Locals)
	wf.Providers = nonNil(wf.Providers)
	wf.Other = nonNil(wf.Other)
	wf.Environments = nonNil(wf.Environments)
	wf.Edges = nonNil(wf.Edges)
	wf.Clouds = nonNil(wf.Clouds)
	wf.Diagnostics = nonNil(wf.Diagnostics)
	for i := range wf.Nodes {
		ensureBody(&wf.Nodes[i].Body)
	}
	for _, list := range [][]model.NamedBlock{wf.Variables, wf.Outputs, wf.Providers} {
		for i := range list {
			ensureBody(&list[i].Body)
		}
	}
	for i := range wf.Other {
		ensureBody(&wf.Other[i].Body)
	}
	if wf.Terraform != nil {
		ensureBody(wf.Terraform)
	}
	for i := range wf.Environments {
		if wf.Environments[i].Values == nil {
			wf.Environments[i].Values = map[string]model.Value{}
		}
		wf.Environments[i].EnvVars = nonNil(wf.Environments[i].EnvVars)
	}
}

func ensureBody(b *model.Body) {
	b.Attrs = nonNil(b.Attrs)
	b.Blocks = nonNil(b.Blocks)
	for i := range b.Blocks {
		ensureBody(&b.Blocks[i].Body)
	}
}

// save finalizes, persists and announces wf.
func (s *Server) save(wf *model.Workflow, extra []model.Diagnostic) error {
	s.finalize(wf, extra)
	if err := s.cfg.Store.Save(wf); err != nil {
		return err
	}
	s.cfg.Events.Publish(model.Event{Type: model.EventWorkflowUpdated, WorkflowID: wf.ID, Data: wf})
	return nil
}

// recentIconWindow is how many recently updated workflows a new random icon
// avoids repeating.
const recentIconWindow = 5

// randomIcon picks an icon for a new workflow that differs from the most
// recently updated workflows (and from also, when given) where possible.
func (s *Server) randomIcon(also ...*model.WorkflowIcon) *model.WorkflowIcon {
	recent, err := s.cfg.Store.RecentIcons(recentIconWindow)
	if err != nil {
		s.log.Warn("list recent icons", "err", err)
	}
	return model.RandomWorkflowIcon(append(also, recent...)...)
}

// mutate runs fn on the stored workflow under the edit lock and saves the
// result. fn returns extra diagnostics to keep (e.g. from an import).
func (s *Server) mutate(id string, fn func(wf *model.Workflow) ([]model.Diagnostic, error)) (*model.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return nil, err
	}
	extra, err := fn(wf)
	if err != nil {
		return nil, err
	}
	if err := s.save(wf, extra); err != nil {
		return nil, err
	}
	return wf, nil
}

// findNode resolves ref as a node ID or address.
func findNode(wf *model.Workflow, ref string) (*model.Node, error) {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == ref {
			return &wf.Nodes[i], nil
		}
	}
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == ref {
			return &wf.Nodes[i], nil
		}
	}
	return nil, errorf(http.StatusNotFound, "node %q not found", ref)
}

// ---------------------------------------------------------------------------
// Handlers

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) error {
	list, err := s.cfg.Store.List()
	if err != nil {
		return err
	}
	return ok(w, list)
}

type createRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	TemplateID  string `json:"templateId"`
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	var (
		wf    *model.Workflow
		extra []model.Diagnostic
	)
	if req.TemplateID != "" {
		files, found := s.cfg.Catalog.TemplateFiles(req.TemplateID)
		if !found {
			return errorf(http.StatusBadRequest, "unknown template %q", req.TemplateID)
		}
		var tmpl model.Template
		for _, t := range s.cfg.Catalog.Templates() {
			if t.ID == req.TemplateID {
				tmpl = t
			}
		}
		if req.Name == "" {
			req.Name = tmpl.Name
		}
		if req.Description == "" {
			req.Description = tmpl.Description
		}
		var err error
		wf, extra, err = hclcodec.Import(req.Name, files)
		if err != nil {
			return fmt.Errorf("template %s: %w", req.TemplateID, err)
		}
	} else {
		if req.Name == "" {
			return errorf(http.StatusBadRequest, "name is required")
		}
		wf = &model.Workflow{}
	}
	wf.ID = store.NewID(req.Name)
	wf.Name = req.Name
	wf.Description = req.Description
	// Random even for templates (their own icon only decorates the template
	// card) so several workflows from one template stay distinguishable.
	wf.Icon = s.randomIcon()
	if err := s.save(wf, extra); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, wf)
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) putWorkflow(w http.ResponseWriter, r *http.Request) error {
	var in model.Workflow
	if err := decode(w, r, maxImportBody, &in); err != nil {
		return err
	}
	id := r.PathValue("id")
	if in.ID != "" && in.ID != id {
		return errorf(http.StatusBadRequest, "body id %q does not match path id %q", in.ID, id)
	}
	wf, err := s.mutate(id, func(cur *model.Workflow) ([]model.Diagnostic, error) {
		createdAt, archived, icon := cur.CreatedAt, cur.Archived, cur.Icon
		*cur = in
		// Archive state changes only through archive/unarchive.
		cur.ID, cur.CreatedAt, cur.Archived = id, createdAt, archived
		// A body without an icon (older clients, agents) keeps the current one.
		if cur.Icon == nil {
			cur.Icon = icon
		}
		if strings.TrimSpace(cur.Name) == "" {
			return nil, errorf(http.StatusBadRequest, "name is required")
		}
		if err := cur.Icon.Validate(); err != nil {
			return nil, errorf(http.StatusBadRequest, "%v", err)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.cfg.Store.Get(id); err != nil {
		return err
	}
	// Keep the workflow when its sandbox cannot be removed, so the user can
	// retry instead of orphaning containers. Without Docker there is nothing
	// to remove now; deletion proceeds.
	if err := s.cfg.Sandbox.Remove(r.Context(), id); err != nil {
		if s.cfg.Sandbox.DockerAvailable(r.Context()) == nil {
			return fmt.Errorf("remove sandbox: %w", err)
		}
		s.log.Warn("remove sandbox without docker", "workflow", id, "err", err)
	}
	if err := s.cfg.Store.Delete(id); err != nil {
		return err
	}
	s.cfg.Events.Publish(model.Event{Type: model.EventWorkflowDeleted, WorkflowID: id, Data: map[string]string{"id": id}})
	return noContent(w)
}

func (s *Server) duplicateWorkflow(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = src.Name + " (copy)"
	}
	wf := *src
	wf.ID, wf.Name = store.NewID(name), name
	wf.CreatedAt = time.Time{} // Save stamps a fresh creation time
	wf.Archived = false
	wf.Icon = s.randomIcon(src.Icon) // a fresh look tells the copy apart
	if err := s.save(&wf, nil); err != nil {
		return err
	}
	if err := copyTree(s.cfg.Store.AssetsDir(src.ID), s.cfg.Store.AssetsDir(wf.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("copy assets: %w", err)
	}
	return writeJSON(w, http.StatusCreated, &wf)
}

type addNodeRequest struct {
	Kind     model.NodeKind `json:"kind"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Position model.Position `json:"position"`
}

func (s *Server) addNode(w http.ResponseWriter, r *http.Request) error {
	var req addNodeRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if req.Kind == "" {
		req.Kind = model.KindResource
	}
	if req.Type == "" {
		return errorf(http.StatusBadRequest, "type is required")
	}
	var nodeID string
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		before := nodeIDs(wf)
		id, err := s.cfg.Catalog.AddNode(wf, req.Kind, req.Type, req.Name, req.Position)
		if err != nil {
			return nil, &httpError{status: http.StatusBadRequest, msg: err.Error()}
		}
		settleNewNodes(wf, before)
		nodeID = id
		return nil, nil
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "nodeId": nodeID})
}

type nodePatch struct {
	Name       *string         `json:"name"`
	Label      *string         `json:"label"`
	Position   *model.Position `json:"position"`
	Positioned *bool           `json:"positioned"`
	Parent     *string         `json:"parent"`
	Body       *model.Body     `json:"body"`
}

func (s *Server) patchNode(w http.ResponseWriter, r *http.Request) error {
	var p nodePatch
	if err := decode(w, r, maxImportBody, &p); err != nil {
		return err
	}
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		n, err := findNode(wf, r.PathValue("nodeId"))
		if err != nil {
			return nil, err
		}
		if p.Label != nil {
			n.Label = *p.Label
		}
		if p.Position != nil {
			n.Position, n.Positioned = *p.Position, true
		}
		if p.Positioned != nil {
			n.Positioned = *p.Positioned
		}
		if p.Parent != nil {
			if *p.Parent != "" {
				if *p.Parent == n.ID {
					return nil, errorf(http.StatusBadRequest, "a node cannot be its own parent")
				}
				if _, err := findNode(wf, *p.Parent); err != nil {
					return nil, errorf(http.StatusBadRequest, "parent %q not found", *p.Parent)
				}
			}
			n.Parent = *p.Parent
		}
		if p.Body != nil {
			n.Body = *p.Body
		}
		if p.Name != nil && *p.Name != n.Name {
			if err := hclcodec.RenameNode(wf, n.ID, *p.Name); err != nil {
				return nil, errorf(http.StatusConflict, "%v", err)
			}
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		n, err := findNode(wf, r.PathValue("nodeId"))
		if err != nil {
			return nil, err
		}
		id := n.ID
		nodes := wf.Nodes[:0]
		for _, other := range wf.Nodes {
			if other.ID == id {
				continue
			}
			if other.Parent == id {
				other.Parent = ""
			}
			nodes = append(nodes, other)
		}
		wf.Nodes = nodes
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

func (s *Server) connections(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	src, err := findNode(wf, r.URL.Query().Get("source"))
	if err != nil {
		return err
	}
	dst, err := findNode(wf, r.URL.Query().Get("target"))
	if err != nil {
		return err
	}
	return ok(w, nonNil(s.cfg.Catalog.Connections(wf, src.ID, dst.ID)))
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		RecipeID string `json:"recipeId"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		src, err := findNode(wf, req.Source)
		if err != nil {
			return nil, err
		}
		dst, err := findNode(wf, req.Target)
		if err != nil {
			return nil, err
		}
		before := nodeIDs(wf)
		if err := s.cfg.Catalog.Connect(wf, src.ID, dst.ID, req.RecipeID); err != nil {
			return nil, &httpError{status: http.StatusBadRequest, msg: err.Error()}
		}
		settleNewNodes(wf, before)
		return nil, nil
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}
