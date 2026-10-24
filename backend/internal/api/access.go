package api

import (
	"errors"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Access editors of Terraform nodes (package access): the structured view
// of a node's permissions, writes from the inspector and MCP, and the
// pinned reference data the editors pick from. Grants (drag an identity
// onto a resource) are connect recipes (GET/POST /connections, group
// "access"). Kubernetes RBAC has its own endpoints (kube_access.go).

// accessError maps access errors to HTTP errors.
func accessError(err error) error {
	var ee *access.EditError
	switch {
	case errors.Is(err, access.ErrUnsupported):
		return errorf(http.StatusNotFound, "%v", err)
	case errors.As(err, &ee):
		return errorf(http.StatusBadRequest, "%v", err)
	}
	return err
}

// getAccess is GET /workflows/{id}/access?node=<id|address>.
func (s *Server) getAccess(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	n, err := findNode(wf, r.URL.Query().Get("node"))
	if err != nil {
		return err
	}
	v, err := access.View(wf, n)
	if err != nil {
		return accessError(err)
	}
	return ok(w, v)
}

// putAccess is PUT /workflows/{id}/access (model.AccessEdit) → Workflow.
func (s *Server) putAccess(w http.ResponseWriter, r *http.Request) error {
	var edit model.AccessEdit
	if err := decode(w, r, maxBody, &edit); err != nil {
		return err
	}
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		n, err := findNode(wf, edit.NodeID)
		if err != nil {
			return nil, err
		}
		edit.NodeID = n.ID
		return nil, accessError(access.Apply(wf, n, edit))
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// accessCatalog is GET /access/catalog/{provider}?service=&resourceType=&q=.
func (s *Server) accessCatalog(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	c, err := access.Catalog(r.PathValue("provider"), access.CatalogQuery{
		Service:      q.Get("service"),
		ResourceType: q.Get("resourceType"),
		Search:       q.Get("q"),
	})
	if err != nil {
		return accessError(err)
	}
	return ok(w, c)
}
