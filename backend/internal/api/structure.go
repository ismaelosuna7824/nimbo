package api

import (
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
)

// structure returns how the workflow's Terraform is organized: effective
// export layout, states, resources per state, module tree and backend.
func (s *Server) structure(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, lint.Structure(wf, s.localModules(wf)))
}
