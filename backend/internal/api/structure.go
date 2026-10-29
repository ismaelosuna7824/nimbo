package api

import (
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
)

// structure returns how the workflow's Terraform is organized: effective
// export layout, states, resources per state, module tree and backend, plus
// how its provider versions fare (the sandbox's lock file, else the
// imported one).
func (s *Server) structure(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	sum := lint.Structure(wf, s.localModules(wf))
	sum.Providers = s.cfg.Catalog.ProviderVersions(wf, s.cfg.Sandbox.LockedVersions(wf.ID, s.cfg.Store.AssetsDir(wf.ID)))
	return ok(w, sum)
}
