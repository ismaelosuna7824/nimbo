package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/capacity"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubeready"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/readiness"
)

// workflowReadiness is GET /workflows/{id}/readiness: the production
// readiness of every environment of a docker or Kubernetes workflow
// (query environment: just that one; "" is the base) with the evidence of
// its load tests, incident scenarios and capacity plans.
func (s *Server) workflowReadiness(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return errorf(http.StatusBadRequest, "production readiness is for docker and Kubernetes workflows; %s is a %s workflow", wf.Name, model.KindLabel(wf.Kind))
	}
	var only *string
	if q := r.URL.Query(); q.Has("environment") {
		env := q.Get("environment")
		if !readinessEnvironment(wf, env) {
			return errorf(http.StatusNotFound, "environment %q not found", env)
		}
		only = &env
	}
	return ok(w, s.readinessReport(r.Context(), wf, only))
}

// readinessReport is the readiness of a docker or Kubernetes workflow's
// environments (only: just that one) with the evidence of its load tests,
// incident scenarios and capacity plans.
func (s *Server) readinessReport(ctx context.Context, wf *model.Workflow, only *string) model.WorkflowReadiness {
	src := readiness.Sources{Runs: []model.LoadTestRun{}, Plan: func(env string) *model.CapacityPlan {
		p, _ := capacity.LastPlan(s.cfg.Home, wf.ID, env)
		return p
	}}
	if s.cfg.LoadTest != nil {
		src.Runs = s.cfg.LoadTest.List(wf.ID)
	}
	dir := s.cfg.Store.AssetsDir(wf.ID)
	if wf.IsDocker() {
		return readiness.Docker(ctx, dir, wf, s.bestPracticeSettings(), src, only)
	}
	return kubeready.Report(ctx, dir, wf, s.bestPracticeSettings(), src, only)
}

// readinessEnvironment reports whether the readiness report has an entry
// for env: docker workflows check the base and every environment;
// Kubernetes ones their environments, or the base when they have none.
func readinessEnvironment(wf *model.Workflow, env string) bool {
	if wf.IsDocker() {
		return env == "" || slices.ContainsFunc(wf.Compose.Environments, func(e model.ComposeEnvironment) bool { return e.Name == env })
	}
	return len(wf.Environments) == 0 && env == "" || slices.ContainsFunc(wf.Environments, func(e model.Environment) bool { return e.Name == env })
}
