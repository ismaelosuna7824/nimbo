package api

import (
	"fmt"
	"net/http"
	"unicode/utf8"

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
	res, err := kubeconvert.Convert(s.openCompose(wf, nil), kubeconvert.Options{Format: req.Format, Name: req.Name, Namespace: req.Namespace})
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
