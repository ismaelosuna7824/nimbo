package api

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubeconvert"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Converting a docker workflow into a new Kubernetes workflow
// (kubeconvert): the docker workflow is left as it is.

type convertKubeRequest struct {
	Format    string `json:"format"` // manifests (default) | kustomize | helm
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// DryRun returns the report and the generated files without creating
	// the workflow (the dialog's preview).
	DryRun bool `json:"dryRun"`
	// SizeFromLoadTest sizes requests/limits from the newest usable load
	// test run of the workflow (default true when there is one);
	// LoadTestRunID picks a specific run.
	SizeFromLoadTest *bool               `json:"sizeFromLoadTest,omitempty"`
	LoadTestRunID    string              `json:"loadTestRunId,omitempty"`
	Autoscaling      *convertAutoscaling `json:"autoscaling,omitempty"`
}

// convertAutoscaling tunes the prod HorizontalPodAutoscalers (defaults:
// enabled, 5 max replicas, 70% CPU).
type convertAutoscaling struct {
	Enabled     *bool `json:"enabled,omitempty"`
	MaxReplicas int   `json:"maxReplicas,omitempty"`
	TargetCPU   int   `json:"targetCpu,omitempty"`
}

type convertKubeResponse struct {
	Workflow *model.Workflow         `json:"workflow,omitempty"`
	Report   model.KubeConvertReport `json:"report"`
	Files    map[string]string       `json:"files,omitempty"` // dry run: generated text files
}

func (s *Server) convertToKubernetes(w http.ResponseWriter, r *http.Request) error {
	var req convertKubeRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if !wf.IsDocker() {
		return errorf(http.StatusBadRequest, "%s is a %s workflow: convert to Kubernetes works on docker workflows", wf.Name, model.KindLabel(wf.Kind))
	}
	if req.Name == "" {
		req.Name = kubeconvert.SuggestName(wf.Name)
	}
	// The base files are the base of the conversion; each environment
	// becomes an overlay / values file of its own.
	p := s.openComposeEnv(wf, "")
	run, err := s.sizingRun(wf.ID, p, req.LoadTestRunID)
	if err != nil {
		return err
	}
	opts := kubeconvert.Options{
		Format: req.Format, Name: req.Name, Namespace: req.Namespace,
		LoadTest: run, IgnoreLoadTest: req.SizeFromLoadTest != nil && !*req.SizeFromLoadTest,
	}
	if a := req.Autoscaling; a != nil {
		opts.Autoscaling = kubeconvert.Autoscaling{Disabled: a.Enabled != nil && !*a.Enabled, MaxReplicas: a.MaxReplicas, TargetCPU: a.TargetCPU}
	}
	for _, e := range wf.Compose.Environments {
		opts.Environments = append(opts.Environments, kubeconvert.Environment{Name: e.Name, Project: s.openComposeEnv(wf, e.Name)})
	}
	res, err := kubeconvert.Convert(p, opts)
	if err != nil {
		return errorf(http.StatusBadRequest, "%v", err)
	}
	if req.DryRun {
		files := map[string]string{}
		for _, rel := range res.Generated {
			if data := res.Files[rel]; utf8.Valid(data) {
				files[rel] = string(data)
			}
		}
		return ok(w, convertKubeResponse{Report: res.Report, Files: files})
	}
	desc := fmt.Sprintf("Converted from the docker workflow %s (%s).", wf.Name, res.Report.Format)
	created, err := s.createKubeWorkflow(wf.Name+" (Kubernetes)", desc, res.Files, res.Model)
	if err != nil {
		return err
	}
	s.announceCreated(r, created)
	return writeJSON(w, http.StatusCreated, convertKubeResponse{Workflow: created, Report: res.Report})
}

// sizingRun is the load test run a conversion sizes from: runID when set
// (an error when it isn't a usable run of the workflow), else the newest
// usable run (nil when there is none).
func (s *Server) sizingRun(wfID string, p *compose.Project, runID string) (*model.LoadTestRun, error) {
	if s.cfg.LoadTest == nil {
		return nil, nil
	}
	if runID != "" {
		run, found := s.cfg.LoadTest.Get(runID)
		if !found || run.WorkflowID != wfID {
			return nil, errorf(http.StatusNotFound, "load test run %s not found in this workflow", runID)
		}
		if err := kubeconvert.UsableRun(p, run); err != nil {
			return nil, errorf(http.StatusBadRequest, "can't size from it: %v", err)
		}
		return run, nil
	}
	// List is newest first, without timelines: load the full run of each
	// candidate until one is usable.
	for _, r := range s.cfg.LoadTest.List(wfID) {
		if r.Resources == nil || r.Status == model.LoadRunning {
			continue
		}
		if run, found := s.cfg.LoadTest.Get(r.ID); found && kubeconvert.UsableRun(p, run) == nil {
			return run, nil
		}
	}
	return nil, nil
}
