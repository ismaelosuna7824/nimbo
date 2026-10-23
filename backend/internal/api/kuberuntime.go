package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// Kubernetes runtime endpoints: runs (POST /workflows/{id}/runs for a
// Kubernetes workflow), GET /workflows/{id}/kubernetes/status, /logs and
// /stats. Endpoints, Try, Postman and load test targets dispatch here from
// endpoints.go and runtime.go.

// kubeRuntimeWorkflow loads a workflow for the Kubernetes runtime
// endpoints, refusing other kinds.
func (s *Server) kubeRuntimeWorkflow(r *http.Request) (*model.Workflow, error) {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if !wf.IsKubernetes() {
		return nil, errorf(http.StatusBadRequest, "this is a %s workflow: Kubernetes runtime endpoints need a Kubernetes workflow", model.KindLabel(wf.Kind))
	}
	return wf, nil
}

// kubeError maps Kubernetes runtime failures to HTTP errors.
func kubeError(err error) error {
	var invalid *sandbox.InvalidKubeError
	switch {
	case errors.As(err, &invalid):
		return errorf(http.StatusBadRequest, "%s", invalid.Msg)
	case errors.Is(err, sandbox.ErrDockerUnavailable):
		return &httpError{status: http.StatusServiceUnavailable, msg: err.Error(), code: "docker_missing"}
	case errors.Is(err, sandbox.ErrRunActive):
		return errorf(http.StatusConflict, "a run is in progress for this workflow; wait for it or cancel it")
	}
	return err
}

func (s *Server) startKubeRun(w http.ResponseWriter, r *http.Request, wf *model.Workflow, req runRequest) error {
	if !model.KubeAction(req.Action) {
		return errorf(http.StatusBadRequest, "action must be up, down, restart, cluster-stop, cluster-start or cluster-delete for a Kubernetes workflow")
	}
	if req.TimeoutSec < 0 || req.TimeoutSec > 3600 {
		return errorf(http.StatusBadRequest, "timeoutSec must be between 0 and 3600")
	}
	if req.Service != "" || req.RemapPorts {
		return errorf(http.StatusBadRequest, "service and remapPorts are docker compose options; use workload (restart) for Kubernetes")
	}
	if req.Environment != "" && req.Action != model.ActionUp {
		return errorf(http.StatusBadRequest, "environment applies to up only")
	}
	// Stopping or deleting the cluster of an archived workflow frees its
	// resources; everything else needs an active workflow.
	switch req.Action {
	case model.ActionDown, model.ActionClusterStop, model.ActionClusterDelete:
	default:
		if err := refuseArchived(wf); err != nil {
			return err
		}
	}
	kreq := sandbox.KubeRunRequest{
		Workflow:      wf,
		Action:        req.Action,
		SourceDir:     s.cfg.Store.AssetsDir(wf.ID),
		Workload:      req.Workload,
		RemoveVolumes: req.RemoveVolumes,
		DeleteCluster: req.DeleteCluster,
		Timeout:       time.Duration(req.TimeoutSec) * time.Second,
	}
	if req.Action == model.ActionUp {
		if err := s.prepareKubeUp(r.Context(), wf, req.Environment, &kreq); err != nil {
			return err
		}
	}
	run, err := s.cfg.Sandbox.KubeRun(context.WithoutCancel(r.Context()), kreq)
	if err != nil {
		return kubeError(err)
	}
	return writeJSON(w, http.StatusCreated, run)
}

// prepareKubeUp renders the workflow for the environment (explicit, else
// the active one) and fills the sources and builds of an up. Render
// errors refuse the run with their diagnostics.
func (s *Server) prepareKubeUp(ctx context.Context, wf *model.Workflow, env string, kreq *sandbox.KubeRunRequest) error {
	if env != "" {
		if _, err := environment(wf, env); err != nil {
			return err
		}
	} else {
		env = wf.ActiveEnvironment
	}
	if wf.Kubernetes == nil || len(wf.Kubernetes.Sources) == 0 {
		return errorf(http.StatusBadRequest, "the workflow has no Kubernetes sources to deploy")
	}
	dir := kreq.SourceDir
	p := kube.Open(ctx, dir, wf, kube.Options{Environment: env, NoSourceMap: true})
	var errs []model.Diagnostic
	for _, d := range p.Diagnostics {
		if d.Severity == model.SeverityError {
			errs = append(errs, d)
		}
	}
	for _, rs := range p.Sources {
		if rs.Err != nil && len(errs) == 0 {
			errs = append(errs, model.Diagnostic{Severity: model.SeverityError, Message: fmt.Sprintf("source %s: %v", rs.Source.Path, rs.Err)})
		}
	}
	if len(errs) > 0 {
		return &httpError{status: http.StatusBadRequest, msg: "the workflow does not render; fix it before running up", diagnostics: errs}
	}
	var images []string
	for _, rs := range p.Sources {
		src := sandbox.KubeApplySource{Index: rs.Index, Kind: rs.Source.Kind, Path: rs.Source.Path, Namespace: rs.Source.Namespace}
		for _, o := range rs.Objects {
			images = append(images, sandbox.KubeObjectImages(o.Obj)...)
		}
		if rs.Source.Kind == model.KubeHelm {
			ch, vals, err := kube.HelmChart(dir, rs.Source, env)
			if err != nil {
				return errorf(http.StatusBadRequest, "helm source %s: %v", rs.Source.Path, err)
			}
			src.Helm = &sandbox.KubeHelmRelease{Release: kube.ReleaseName(rs.Source, ch.Name()), Chart: ch, Values: vals}
		} else {
			for _, o := range rs.Objects {
				src.Objects = append(src.Objects, o.Obj)
			}
		}
		kreq.Sources = append(kreq.Sources, src)
	}
	kreq.Builds = sandbox.KubeBuildPlan(dir, wf.Kubernetes.Builds, images)
	return nil
}

func (s *Server) kubeStatus(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.kubeRuntimeWorkflow(r)
	if err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.KubeStatus(r.Context(), wf.ID)
	if err != nil {
		return err
	}
	return ok(w, st)
}

func (s *Server) kubeStats(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.kubeRuntimeWorkflow(r)
	if err != nil {
		return err
	}
	st, err := s.cfg.Sandbox.KubeStats(r.Context(), wf.ID)
	if err != nil {
		return err
	}
	return ok(w, st)
}

// kubeLogs returns the last lines of a workload's (or a pod's) containers
// (?workload=<node id>|namespace=&pod=, container=, tail=), or streams
// them as server-sent events with follow=true: "kube.log" per line, "end"
// when the containers stop.
func (s *Server) kubeLogs(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.kubeRuntimeWorkflow(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	sel := sandbox.KubeLogSelector{Workload: q.Get("workload"), Namespace: q.Get("namespace"), Pod: q.Get("pod"), Container: q.Get("container")}
	tail := 200
	if v := q.Get("tail"); v != "" {
		if tail, err = strconv.Atoi(v); err != nil || tail < 0 {
			return errorf(http.StatusBadRequest, "tail must be a non-negative number")
		}
	}
	tail = min(tail, sandbox.MaxKubeLogTail)
	if q.Get("follow") != "true" {
		lines := []model.KubeLogLine{}
		err := s.cfg.Sandbox.KubeLogs(r.Context(), wf.ID, sel, tail, false, func(l model.KubeLogLine) { lines = append(lines, l) })
		if err != nil {
			return kubeError(err)
		}
		return ok(w, map[string]any{"lines": lines})
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 2000\n: connected\n\n"); err != nil || rc.Flush() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	lines := make(chan model.KubeLogLine, 256)
	done := make(chan error, 1)
	go func() {
		done <- s.cfg.Sandbox.KubeLogs(ctx, wf.ID, sel, tail, true, func(l model.KubeLogLine) {
			select {
			case lines <- l:
			case <-ctx.Done():
			}
		})
	}()
	write := func(l model.KubeLogLine) error {
		data, _ := json.Marshal(l)
		_, err := fmt.Fprintf(w, "event: kube.log\ndata: %s\n\n", data)
		return err
	}
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
			werr = write(l)
		case err := <-done:
			for drained := false; !drained; {
				select {
				case l := <-lines:
					if write(l) != nil {
						return nil
					}
				default:
					drained = true
				}
			}
			msg := ""
			if err != nil && ctx.Err() == nil {
				msg = kubeError(err).Error()
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
