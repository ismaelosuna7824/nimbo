package api

import (
	"net/http"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Environments of docker and Kubernetes workflows
// (/workflows/{id}/environments...): files on top of the base (Kustomize
// overlays and Helm values files; compose override files and .env.<env>).
// The handlers validate names and dispatch by workflow kind to the
// kube* (environments_kube.go) and compose* (environments_compose.go)
// implementations. Terraform environments are edited with PUT
// /workflows/{id} and keep working unchanged.

// terraformEnvironments refuses the endpoints for Terraform workflows.
func terraformEnvironments(wf *model.Workflow) error {
	return errorf(http.StatusBadRequest, "this is a %s workflow: its environments are variable values edited in Variables & Environments (PUT /workflows/{id}, MCP set_environment)", model.KindLabel(wf.Kind))
}

// validEnvironmentName checks a new environment name of wf.
func validEnvironmentName(wf *model.Workflow, name string) error {
	if !dnsLabel(name) {
		return errorf(http.StatusBadRequest, "environment name %q must be a lowercase DNS label (a-z, 0-9 and -, at most 63 characters): it names overlays, values files and namespaces", name)
	}
	if name == "base" {
		return errorf(http.StatusBadRequest, `"base" is what every environment builds on: pick another name`)
	}
	if slices.ContainsFunc(wf.Environments, func(e model.Environment) bool { return e.Name == name }) {
		return errorf(http.StatusConflict, "environment %q already exists", name)
	}
	return nil
}

// existingEnvironment checks that wf has environment name.
func existingEnvironment(wf *model.Workflow, name string) error {
	if !slices.ContainsFunc(wf.Environments, func(e model.Environment) bool { return e.Name == name }) {
		return errorf(http.StatusNotFound, "environment %q not found", name)
	}
	return nil
}

// GET /workflows/{id}/environments
func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var view model.EnvironmentsView
	switch {
	case wf.IsKubernetes():
		view = s.kubeEnvironments(wf)
	case wf.IsDocker():
		if view, err = s.composeEnvironments(wf); err != nil {
			return err
		}
	default:
		return terraformEnvironments(wf)
	}
	return ok(w, view)
}

// POST /workflows/{id}/environments {name, from?}
func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
		From string `json:"from"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := validEnvironmentName(wf, req.Name); err != nil {
		return err
	}
	if req.From != "" {
		if err := existingEnvironment(wf, req.From); err != nil {
			return err
		}
	}
	switch {
	case wf.IsKubernetes():
		wf, err = s.kubeCreateEnvironment(wf.ID, req.Name, req.From)
	case wf.IsDocker():
		wf, err = s.composeCreateEnvironment(wf.ID, req.Name, req.From)
	default:
		return terraformEnvironments(wf)
	}
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// PATCH /workflows/{id}/environments/{name} {name}
func (s *Server) renameEnvironment(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	name := r.PathValue("name")
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := existingEnvironment(wf, name); err != nil {
		return err
	}
	if req.Name == name {
		return ok(w, wf)
	}
	if err := validEnvironmentName(wf, req.Name); err != nil {
		return err
	}
	switch {
	case wf.IsKubernetes():
		wf, err = s.kubeRenameEnvironment(wf.ID, name, req.Name)
	case wf.IsDocker():
		wf, err = s.composeRenameEnvironment(wf.ID, name, req.Name)
	default:
		return terraformEnvironments(wf)
	}
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// DELETE /workflows/{id}/environments/{name}
func (s *Server) deleteEnvironment(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := existingEnvironment(wf, name); err != nil {
		return err
	}
	switch {
	case wf.IsKubernetes():
		wf, err = s.kubeDeleteEnvironment(wf.ID, name)
	case wf.IsDocker():
		wf, err = s.composeDeleteEnvironment(wf.ID, name)
	default:
		return terraformEnvironments(wf)
	}
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// GET /workflows/{id}/environments/compare
func (s *Server) compareEnvironments(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var out model.EnvCompare
	switch {
	case wf.IsKubernetes():
		out = s.kubeCompareEnvironments(r, wf)
	case wf.IsDocker():
		if out, err = s.composeCompareEnvironments(wf); err != nil {
			return err
		}
	default:
		return terraformEnvironments(wf)
	}
	return ok(w, out)
}

// GET /workflows/{id}/environments/{name}/settings?target=
func (s *Server) environmentSettings(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := existingEnvironment(wf, name); err != nil {
		return err
	}
	target := r.URL.Query().Get("target")
	var out model.EnvSettings
	switch {
	case wf.IsKubernetes():
		out, err = s.kubeEnvironmentSettings(r, wf, name, target)
	case wf.IsDocker():
		out, err = s.composeEnvironmentSettings(wf, name, target)
	default:
		return terraformEnvironments(wf)
	}
	if err != nil {
		return err
	}
	return ok(w, out)
}

// PATCH /workflows/{id}/environments/{name}/settings {target, values}
func (s *Server) setEnvironmentSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Target string             `json:"target"`
		Values map[string]*string `json:"values"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if len(req.Values) == 0 {
		return errorf(http.StatusBadRequest, "values is required: {key: value, or null to drop the environment's override}")
	}
	name := r.PathValue("name")
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := existingEnvironment(wf, name); err != nil {
		return err
	}
	switch {
	case wf.IsKubernetes():
		wf, err = s.kubeSetEnvironmentSettings(wf.ID, name, req.Target, req.Values)
	case wf.IsDocker():
		wf, err = s.composeSetEnvironmentSettings(wf.ID, name, req.Target, req.Values)
	default:
		return terraformEnvironments(wf)
	}
	if err != nil {
		return err
	}
	return ok(w, wf)
}

// PUT /workflows/{id}/environments/{name}/variables/{key} {value, secret?}
// and DELETE (docker: a variable of the environment's env file).
func (s *Server) environmentVariable(remove bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req struct {
			Value  string `json:"value"`
			Secret bool   `json:"secret"`
		}
		if !remove {
			if err := decode(w, r, maxBody, &req); err != nil {
				return err
			}
		}
		name := r.PathValue("name")
		wf, err := s.cfg.Store.Get(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !wf.IsDocker() {
			if wf.IsKubernetes() {
				return errorf(http.StatusBadRequest, "Kubernetes environments take their settings from overlays and values files: use PATCH /environments/{name}/settings, or edit ConfigMaps in Code")
			}
			return terraformEnvironments(wf)
		}
		if err := existingEnvironment(wf, name); err != nil {
			return err
		}
		var value *string
		if !remove {
			value = &req.Value
		}
		if wf, err = s.composeSetEnvironmentVariable(wf.ID, name, r.PathValue("key"), value, req.Secret); err != nil {
			return err
		}
		return ok(w, wf)
	}
}
