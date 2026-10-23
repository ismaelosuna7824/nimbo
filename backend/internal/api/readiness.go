package api

import (
	"net/http"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubeready"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// kubeReadiness is GET /workflows/{id}/kubernetes/readiness: the
// production readiness checklist of every environment (query environment:
// just that one; "" is the base of a workflow without environments).
func (s *Server) kubeReadiness(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := requireKube(wf); err != nil {
		return err
	}
	var only *string
	if q := r.URL.Query(); q.Has("environment") {
		env := q.Get("environment")
		known := len(wf.Environments) == 0 && env == "" || slices.ContainsFunc(wf.Environments, func(e model.Environment) bool { return e.Name == env })
		if !known {
			return errorf(http.StatusNotFound, "environment %q not found", env)
		}
		only = &env
	}
	runs := []model.LoadTestRun{}
	if s.cfg.LoadTest != nil {
		runs = s.cfg.LoadTest.List(wf.ID)
	}
	return ok(w, kubeready.Report(r.Context(), s.cfg.Store.AssetsDir(wf.ID), wf, s.bestPracticeSettings(), runs, only))
}
