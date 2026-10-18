package api

import (
	"context"
	"net/http"
	"os"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// syncLive refines catalog Floci support with the services a running
// emulator reports.
func (s *Server) syncLive(st model.SandboxStatus) {
	for _, e := range st.Emulators {
		if e.State == model.SandboxRunning && len(e.Services) > 0 {
			s.cfg.Catalog.SetLiveServices(e.Cloud, e.Services)
		}
	}
}

func (s *Server) sandboxStatus(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.cfg.Store.Get(id); err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.Status(r.Context(), id)
	if err != nil {
		return err
	}
	s.syncLive(st)
	return ok(w, st)
}

func sandboxClouds(wf *model.Workflow) []model.Cloud {
	if len(wf.Clouds) == 0 {
		return []model.Cloud{model.CloudAWS}
	}
	return wf.Clouds
}

func (s *Server) sandboxAction(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return err
	}
	// Lifecycle operations must not be torn down halfway when the client
	// disconnects.
	ctx := context.WithoutCancel(r.Context())
	var st model.SandboxStatus
	switch action := r.PathValue("action"); action {
	case "start":
		if err := refuseArchived(wf); err != nil {
			return err
		}
		st, err = s.cfg.Sandbox.Start(ctx, id, sandboxClouds(wf))
	case "stop":
		if err = s.cfg.Sandbox.Stop(ctx, id); err == nil {
			st, err = s.cfg.Sandbox.Status(ctx, id)
		}
	case "reset":
		if err = s.cfg.Sandbox.Reset(ctx, id); err == nil {
			st, err = s.cfg.Sandbox.Status(ctx, id)
		}
	default:
		return errorf(http.StatusNotFound, "unknown sandbox action %q", action)
	}
	if err != nil {
		return err
	}
	s.syncLive(st)
	return ok(w, st)
}

func environment(wf *model.Workflow, name string) (*model.Environment, error) {
	explicit := name != ""
	if !explicit {
		name = wf.ActiveEnvironment
	}
	if name == "" {
		return nil, nil
	}
	for i := range wf.Environments {
		if wf.Environments[i].Name == name {
			return &wf.Environments[i], nil
		}
	}
	if explicit {
		return nil, errorf(http.StatusNotFound, "environment %q not found", name)
	}
	return nil, nil
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Action      model.RunAction `json:"action"`
		Environment string          `json:"environment"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	switch req.Action {
	case model.ActionValidate, model.ActionPlan, model.ActionApply, model.ActionDestroy:
	default:
		return errorf(http.StatusBadRequest, "action must be validate, plan, apply or destroy")
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := refuseArchived(wf); err != nil {
		return err
	}
	env, err := environment(wf, req.Environment)
	if err != nil {
		return err
	}
	if req.Action == model.ActionPlan || req.Action == model.ActionApply {
		var errs []model.Diagnostic
		for _, d := range wf.Diagnostics {
			if d.Severity == model.SeverityError {
				errs = append(errs, d)
			}
		}
		if len(errs) > 0 {
			return &httpError{status: http.StatusBadRequest, msg: "the workflow has errors; fix them before running " + string(req.Action), diagnostics: errs}
		}
	}
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		return err
	}
	assets := s.cfg.Store.AssetsDir(wf.ID)
	if _, err := os.Stat(assets); err != nil {
		assets = ""
	}
	run, err := s.cfg.Sandbox.Run(context.WithoutCancel(r.Context()), sandbox.RunRequest{
		Workflow:    wf,
		Action:      req.Action,
		Files:       files,
		AssetsDir:   assets,
		Environment: env,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) error {
	return ok(w, nonNil(s.cfg.Sandbox.Runs(r.PathValue("id"))))
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) error {
	run, found := s.cfg.Sandbox.GetRun(r.PathValue("runId"))
	if !found {
		return errorf(http.StatusNotFound, "run %q not found", r.PathValue("runId"))
	}
	return ok(w, run)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("runId")
	if _, found := s.cfg.Sandbox.GetRun(id); !found {
		return errorf(http.StatusNotFound, "run %q not found", id)
	}
	if err := s.cfg.Sandbox.Cancel(id); err != nil {
		return errorf(http.StatusConflict, "%v", err)
	}
	return noContent(w)
}

func (s *Server) deployedState(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.State(r.Context(), wf)
	if err != nil {
		return err
	}
	return ok(w, st)
}

func (s *Server) loadTargets(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.State(r.Context(), wf)
	if err != nil {
		return err
	}
	return ok(w, nonNil(loadtest.Suggest(wf, st, s.cfg.Sandbox)))
}

func (s *Server) startLoadTest(w http.ResponseWriter, r *http.Request) error {
	var spec model.LoadTestSpec
	if err := decode(w, r, maxBody, &spec); err != nil {
		return err
	}
	id := r.PathValue("id")
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return err
	}
	if err := refuseArchived(wf); err != nil {
		return err
	}
	run, err := s.cfg.LoadTest.Start(context.WithoutCancel(r.Context()), id, spec)
	if err != nil {
		return loadTestError(err)
	}
	return writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listLoadTests(w http.ResponseWriter, r *http.Request) error {
	return ok(w, nonNil(s.cfg.LoadTest.List(r.PathValue("id"))))
}

func (s *Server) getLoadTest(w http.ResponseWriter, r *http.Request) error {
	run, found := s.cfg.LoadTest.Get(r.PathValue("runId"))
	if !found {
		return errorf(http.StatusNotFound, "load test %q not found", r.PathValue("runId"))
	}
	return ok(w, run)
}

func (s *Server) stopLoadTest(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("runId")
	if _, found := s.cfg.LoadTest.Get(id); !found {
		return errorf(http.StatusNotFound, "load test %q not found", id)
	}
	if err := s.cfg.LoadTest.Stop(id); err != nil {
		return errorf(http.StatusConflict, "%v", err)
	}
	return noContent(w)
}
