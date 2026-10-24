package api

import (
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes RBAC editors (kube/rbac.go): GET/PUT
// /workflows/{id}/kubernetes/access. Grants are the "access:*" recipes of
// /kubernetes/connect.

func init() {
	// ?service= is an API group ("core" for the core group).
	access.RegisterCatalog("kubernetes", func(q access.CatalogQuery) (*model.AccessCatalog, error) {
		return kube.RBACCatalog(q.Service, q.Search), nil
	})
}

// kubeAccess is GET /kubernetes/access?node=<id>.
func (s *Server) kubeAccess(w http.ResponseWriter, r *http.Request) error {
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
	v, err := p.RBACView(o)
	if err != nil {
		return kubeEditErr(err)
	}
	return ok(w, v)
}

// putKubeAccess is PUT /kubernetes/access: writes a model.KubeAccessEdit
// and returns the saved workflow.
func (s *Server) putKubeAccess(w http.ResponseWriter, r *http.Request) error {
	var edit model.KubeAccessEdit
	if err := decode(w, r, maxBody, &edit); err != nil {
		return err
	}
	wf, err := s.editKube(r.PathValue("id"), func(_ *model.Workflow, p *kube.Project, e *kube.Editor, _ map[string][]byte) error {
		o, err := kubeObject(p, edit.NodeID)
		if err != nil {
			return err
		}
		return e.ApplyRBAC(o, edit)
	})
	if err != nil {
		return err
	}
	return ok(w, wf)
}
