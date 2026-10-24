package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// runRequest is the body of POST /workflows/{id}/runs: Terraform actions
// use Environment, docker compose actions Service/RemoveVolumes/RemapPorts,
// Kubernetes actions Environment (up), Workload (restart), RemoveVolumes
// and DeleteCluster (down); TimeoutSec bounds compose and Kubernetes waits.
type runRequest struct {
	Action      model.RunAction `json:"action"`
	Environment string          `json:"environment"`
	// Compose runs (sandbox.ComposeRunRequest).
	Service       string `json:"service"`
	RemoveVolumes bool   `json:"removeVolumes"`
	RemapPorts    bool   `json:"remapPorts"`
	TimeoutSec    int    `json:"timeoutSec"`
	// Kubernetes runs (sandbox.KubeRunRequest).
	Workload      string `json:"workload"`
	DeleteCluster bool   `json:"deleteCluster"`
}

// refuseDocker rejects Terraform/Floci runtime endpoints (what) for docker
// and Kubernetes workflows.
func refuseDocker(wf *model.Workflow, what string) error {
	switch {
	case wf.IsDocker():
		return errorf(http.StatusBadRequest, "%s is not available for docker workflows: they run with docker compose", what)
	case wf.IsKubernetes():
		return errorf(http.StatusBadRequest, "%s is not available for Kubernetes workflows: they run on a local Kubernetes cluster", what)
	}
	return nil
}

// requireDocker rejects compose endpoints for Terraform workflows.
func requireDocker(wf *model.Workflow) error {
	if !wf.IsDocker() {
		return errorf(http.StatusBadRequest, "this is a Terraform workflow: compose endpoints need a docker workflow")
	}
	return nil
}

// composeFiles returns the -f list, --env-file list and profiles of a
// docker workflow in its active environment (see compose.RunFiles).
func (s *Server) composeFiles(wf *model.Workflow) (files, envFiles, profiles []string) {
	if wf.Compose == nil {
		return nil, nil, nil
	}
	dir := s.cfg.Store.AssetsDir(wf.ID)
	files, envFiles = compose.RunFiles(wf.Compose, func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil
	})
	return files, envFiles, wf.Compose.Profiles
}

// composeServices lists the active services of the workflow's graph.
func composeServices(wf *model.Workflow) []sandbox.ComposeService {
	var out []sandbox.ComposeService
	if wf.Compose == nil {
		return out
	}
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind != model.ComposeService || n.Inactive {
			continue
		}
		svc := sandbox.ComposeService{Name: n.Name}
		if n.Service != nil && n.Service.Image != nil {
			svc.Image = n.Service.Image.Ref
		}
		out = append(out, svc)
	}
	return out
}

func (s *Server) composeRuntime(ctx context.Context, wf *model.Workflow) (sandbox.ComposeRuntime, error) {
	return s.cfg.Sandbox.ComposeInspect(ctx, wf.ID, composeServices(wf))
}

// composeError maps compose runtime failures to HTTP errors.
func composeError(err error) error {
	var conflict *sandbox.PortConflictError
	var invalid *sandbox.InvalidComposeError
	switch {
	case errors.As(err, &conflict):
		return &httpError{status: http.StatusConflict, msg: err.Error(), code: "port_conflict", conflicts: conflict.Conflicts}
	case errors.As(err, &invalid):
		return errorf(http.StatusBadRequest, "%s", invalid.Msg)
	case errors.Is(err, sandbox.ErrComposeMissing):
		return &httpError{status: http.StatusServiceUnavailable, msg: err.Error(), code: "compose_missing"}
	}
	return err
}

func (s *Server) startComposeRun(w http.ResponseWriter, r *http.Request, wf *model.Workflow, req runRequest) error {
	if !model.ComposeAction(req.Action) {
		return errorf(http.StatusBadRequest, "action must be up, down, build, pull or restart for a docker workflow")
	}
	if req.Environment != "" && req.Environment != wf.Compose.Environment {
		return errorf(http.StatusBadRequest, "docker workflows run their active environment (%q): switch environments first", cmp.Or(wf.Compose.Environment, "base"))
	}
	if req.TimeoutSec < 0 || req.TimeoutSec > 3600 {
		return errorf(http.StatusBadRequest, "timeoutSec must be between 0 and 3600")
	}
	// Bringing an archived project down is allowed: it frees its resources.
	if req.Action != model.ActionDown {
		if err := refuseArchived(wf); err != nil {
			return err
		}
	}
	run, err := s.cfg.Sandbox.ComposeRun(context.WithoutCancel(r.Context()), s.composeRunRequest(wf, req))
	if err != nil {
		return composeError(err)
	}
	return writeJSON(w, http.StatusCreated, run)
}

// composeRunRequest is the sandbox request of a compose action on wf in
// its active environment.
func (s *Server) composeRunRequest(wf *model.Workflow, req runRequest) sandbox.ComposeRunRequest {
	files, envFiles, profiles := s.composeFiles(wf)
	var hints map[string]sandbox.BuildHint
	if req.Action == model.ActionUp || req.Action == model.ActionBuild {
		hints = s.buildHints(wf)
	}
	return sandbox.ComposeRunRequest{
		Workflow:      wf,
		Action:        req.Action,
		SourceDir:     s.cfg.Store.AssetsDir(wf.ID),
		Files:         files,
		EnvFiles:      envFiles,
		Profiles:      profiles,
		Service:       req.Service,
		RemoveVolumes: req.RemoveVolumes,
		RemapPorts:    req.RemapPorts,
		Timeout:       time.Duration(req.TimeoutSec) * time.Second,
		BuildHints:    hints,
	}
}

func (s *Server) composeWorkflow(r *http.Request) (*model.Workflow, error) {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return wf, requireDocker(wf)
}

func (s *Server) composeStatus(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.composeWorkflow(r)
	if err != nil {
		return err
	}
	rt, err := s.composeRuntime(r.Context(), wf)
	if err != nil {
		return err
	}
	return ok(w, rt.Status)
}

func (s *Server) composePorts(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.composeWorkflow(r)
	if err != nil {
		return err
	}
	files, envFiles, profiles := s.composeFiles(wf)
	ports, err := s.cfg.Sandbox.ComposePorts(r.Context(), wf.ID, s.cfg.Store.AssetsDir(wf.ID), files, envFiles, profiles)
	if err != nil {
		return composeError(err)
	}
	return ok(w, ports)
}

// buildHints describe each built service's Dockerfile for the build
// metrics: the final stage's base image, how many history entries the
// Dockerfile adds on top of it, and the build context's stack hints.
func (s *Server) buildHints(wf *model.Workflow) map[string]sandbox.BuildHint {
	hints := map[string]sandbox.BuildHint{}
	if wf.Compose == nil {
		return hints
	}
	for name, b := range s.openCompose(wf, nil).ImageBases() {
		hints[name] = sandbox.BuildHint{BaseImage: b.Image, OwnLayers: b.OwnLayers}
	}
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind != model.ComposeService || n.Service == nil || n.Service.Build == nil {
			continue
		}
		h := hints[n.Name]
		h.Stack = n.Service.Stack
		hints[n.Name] = h
	}
	return hints
}

// composeStats returns the live resource usage of the project's services
// (GET /workflows/{id}/compose/stats).
func (s *Server) composeStats(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.composeWorkflow(r)
	if err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.ComposeStats(r.Context(), wf.ID, composeServices(wf))
	if err != nil {
		return err
	}
	return ok(w, st)
}

// composeBuilds returns the recorded build metrics, newest first
// (GET /workflows/{id}/compose/builds?limit=).
func (s *Server) composeBuilds(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.composeWorkflow(r)
	if err != nil {
		return err
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errorf(http.StatusBadRequest, "limit must be a non-negative integer")
		}
		limit = n
	}
	builds, err := s.cfg.Sandbox.ComposeBuilds(wf.ID, limit)
	if err != nil {
		return err
	}
	return ok(w, builds)
}

// composeLogs returns the last lines of a service's containers
// (?service=&tail=), or streams them as server-sent events with
// follow=true: "compose.log" per line, "end" when the containers stop.
func (s *Server) composeLogs(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.composeWorkflow(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	service := q.Get("service")
	tail := 200
	if v := q.Get("tail"); v != "" {
		if tail, err = strconv.Atoi(v); err != nil || tail < 0 {
			return errorf(http.StatusBadRequest, "tail must be a non-negative number")
		}
	}
	tail = min(tail, sandbox.MaxComposeLogTail)
	if q.Get("follow") != "true" {
		lines := []model.ComposeLogLine{}
		err := s.cfg.Sandbox.ComposeLogs(r.Context(), wf.ID, service, tail, false, func(l model.ComposeLogLine) { lines = append(lines, l) })
		if err != nil {
			return composeError(err)
		}
		return ok(w, map[string]any{"lines": lines})
	}

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 2000\n: connected\n\n"); err != nil {
		return nil
	}
	if rc.Flush() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	lines := make(chan model.ComposeLogLine, 256)
	done := make(chan error, 1)
	go func() {
		done <- s.cfg.Sandbox.ComposeLogs(ctx, wf.ID, service, tail, true, func(l model.ComposeLogLine) {
			select {
			case lines <- l:
			case <-ctx.Done():
			}
		})
	}()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		var werr error
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			_, werr = fmt.Fprint(w, ": ping\n\n")
		case l := <-lines:
			data, _ := json.Marshal(l)
			_, werr = fmt.Fprintf(w, "event: compose.log\ndata: %s\n\n", data)
		case err := <-done:
			// Drain what was emitted before the followers ended.
			for {
				select {
				case l := <-lines:
					data, _ := json.Marshal(l)
					if _, err := fmt.Fprintf(w, "event: compose.log\ndata: %s\n\n", data); err != nil {
						return nil
					}
					continue
				default:
				}
				break
			}
			msg := ""
			if err != nil && ctx.Err() == nil {
				msg = err.Error()
			}
			data, _ := json.Marshal(map[string]string{"error": msg})
			_, _ = fmt.Fprintf(w, "event: end\ndata: %s\n\n", data)
			_ = rc.Flush()
			return nil
		}
		if werr != nil || rc.Flush() != nil {
			return nil
		}
	}
}
