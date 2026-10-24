package api

import (
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

// scenarios lists the incident scenarios a load test of the workflow can
// inject (GET /workflows/{id}/scenarios): model.ScenarioCatalog for the
// active environment, Kind "" for Terraform workflows.
func (s *Server) scenarios(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, scenario.Catalog(wf))
}
