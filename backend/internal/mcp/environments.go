package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Environments of docker and Kubernetes workflows: the same tools for both
// kinds (the daemon dispatches by kind). Terraform environments keep
// set_environment.

const groupEnvironments = "Environments"

func registerEnvironmentTools(r *registry, t *tools) {
	r.group = groupEnvironments
	addTool(r, "list_environments", "List environments", "List the environments of a docker or Kubernetes workflow: name, active, production-like, the files each renders on top of the base (Kubernetes: Kustomize overlays/<env>, Helm values-<env>.yaml; docker: compose.<env>.yaml and/or an env file .env.<env> or <env>.env, either alone being an environment; baseFiles are the compose files and .env every environment shares) and which sources take environments. Switch the active one with update_workflow environment.", t.listEnvironments)
	addTool(r, "create_environment", "Create environment", "Create an environment in a docker or Kubernetes workflow: from (an existing environment) copies its files with its name rewritten in namespaces, hosts and labels (prod -> staging: hello-prod -> hello-staging); without from it starts from the base (Kustomize: overlays/<name>/kustomization.yaml referencing the base, with the overlays' namespace convention; Helm: values-<name>.yaml). Kubernetes: created for every Kustomize and Helm source (plain manifests are the same everywhere). name: lowercase DNS label, not \"base\".", t.createEnvironment)
	addTool(r, "rename_environment", "Rename environment", "Rename an environment: its overlay directory / values file / env files move and their contents follow (hello-staging -> hello-qa); the active environment follows.", t.renameEnvironment)
	addTool(r, "delete_environment", "Delete environment", "Delete an environment and its files (overlay directory, values file, compose override and env files). The base is never deleted; deleting the active environment makes the base active.", t.deleteEnvironment)
	addTool(r, "compare_environments", "Compare environments", "Render the base and every environment on its own (the active one is not changed) and compare them per workload/service: namespace, replicas, autoscaling (min-max @ CPU target), images, CPU/memory requests and limits, PodDisruptionBudget, topology spread, Ingress hosts and the env vars / ConfigMap keys that differ. differs=true rows change between environments.", t.compareEnvironments)
	addTool(r, "get_environment_settings", "Get environment settings", "The common knobs of one workload (Kubernetes: replicas, autoscaler min/max/CPU target, per container image tag and CPU/memory requests/limits) or service (docker) in one environment: value there, value in the base, whether the environment overrides it, whether it is editable (Helm: only values the chart exposes) and the file edits go to. target: a workload id from the result's targets (e.g. \"Deployment/web\"); omit for the first.", t.getEnvironmentSettings)
	addTool(r, "set_environment_values", "Set environment values", "Set knobs of one workload/service in ONE environment only (keys from get_environment_settings; null drops the environment's override so the base value applies). Kubernetes writes the environment's own files: the overlay's replicas/images transformers, its patches (a new strategic-merge patch when needed) or the Helm values key the chart renders the field from in values-<env>.yaml. The graph, rules, readiness and deploy pick it up. Returns the updated settings.", t.setEnvironmentValues)
	addTool(r, "set_environment_variable", "Set environment variable", "Docker workflows: set (or with unset=true remove) a variable in one environment's env file (its .env.<env> or <env>.env as named; the first variable creates .env.<env>); secret=true keeps its value out of views. Kubernetes environments vary through set_environment_values or ConfigMaps in their overlays/values files.", t.setEnvironmentVariable)
}

func envPath(wf *model.Workflow, rest string) string {
	return "/workflows/" + pathEscape(wf.ID) + "/environments" + rest
}

// containerWorkflow resolves a docker or Kubernetes workflow.
func (t *tools) containerWorkflow(ctx context.Context, ref string) (*model.Workflow, error) {
	wf, err := t.c.workflow(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return nil, errors.New("environment tools are for docker and Kubernetes workflows; Terraform environments are set with set_environment")
	}
	return wf, nil
}

func (t *tools) environmentsOf(ctx context.Context, wf *model.Workflow) (any, error) {
	var view model.EnvironmentsView
	if err := t.c.get(ctx, envPath(wf, ""), &view); err != nil {
		return nil, err
	}
	return map[string]any{"workflow": wf.ID, "kind": view.Kind, "active": view.Active, "environments": view.Environments, "sources": view.Sources, "note": view.Note}, nil
}

func (t *tools) listEnvironments(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	return t.environmentsOf(ctx, wf)
}

type createEnvironmentIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name     string `json:"name" jsonschema:"the new environment (lowercase DNS label, e.g. staging)"`
	From     string `json:"from,omitempty" jsonschema:"environment to copy (e.g. prod); omit to start from the base"`
}

func (t *tools) createEnvironment(ctx context.Context, in createEnvironmentIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if err := t.c.do(ctx, http.MethodPost, envPath(wf, ""), map[string]string{"name": in.Name, "from": in.From}, nil); err != nil {
		return nil, err
	}
	return t.environmentsOf(ctx, wf)
}

type renameEnvironmentIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name     string `json:"name" jsonschema:"the environment to rename"`
	NewName  string `json:"new_name" jsonschema:"its new name (lowercase DNS label)"`
}

func (t *tools) renameEnvironment(ctx context.Context, in renameEnvironmentIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if err := t.c.do(ctx, http.MethodPatch, envPath(wf, "/"+pathEscape(in.Name)), map[string]string{"name": in.NewName}, nil); err != nil {
		return nil, err
	}
	return t.environmentsOf(ctx, wf)
}

type environmentIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name     string `json:"name" jsonschema:"the environment"`
}

func (t *tools) deleteEnvironment(ctx context.Context, in environmentIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if err := t.c.do(ctx, http.MethodDelete, envPath(wf, "/"+pathEscape(in.Name)), nil, nil); err != nil {
		return nil, err
	}
	return t.environmentsOf(ctx, wf)
}

func (t *tools) compareEnvironments(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.EnvCompare
	if err := t.c.get(ctx, envPath(wf, "/compare"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

type environmentSettingsIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment string `json:"environment" jsonschema:"the environment (not the base)"`
	Target      string `json:"target,omitempty" jsonschema:"workload id (e.g. Deployment/web) or docker service; omit for the first"`
}

func (t *tools) getEnvironmentSettings(ctx context.Context, in environmentSettingsIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.EnvSettings
	q := ""
	if in.Target != "" {
		q = "?target=" + url.QueryEscape(in.Target)
	}
	if err := t.c.get(ctx, envPath(wf, "/"+pathEscape(in.Environment)+"/settings"+q), &out); err != nil {
		return nil, err
	}
	return out, nil
}

type setEnvironmentValuesIn struct {
	Workflow    string             `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment string             `json:"environment" jsonschema:"the environment to change (only its files are written)"`
	Target      string             `json:"target,omitempty" jsonschema:"workload id (e.g. Deployment/web) or docker service from get_environment_settings; omit for the first"`
	Values      map[string]*string `json:"values" jsonschema:"setting key -> new value as text (\"3\", \"250m\", \"512Mi\", \"1.29-alpine\"); null drops the environment's override"`
}

func (t *tools) setEnvironmentValues(ctx context.Context, in setEnvironmentValuesIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if len(in.Values) == 0 {
		return nil, errors.New("values is required (keys from get_environment_settings)")
	}
	if err := t.c.do(ctx, http.MethodPatch, envPath(wf, "/"+pathEscape(in.Environment)+"/settings"), map[string]any{"target": in.Target, "values": in.Values}, nil); err != nil {
		return nil, err
	}
	return t.getEnvironmentSettings(ctx, environmentSettingsIn{Workflow: wf.ID, Environment: in.Environment, Target: in.Target})
}

type setEnvironmentVariableIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment string `json:"environment" jsonschema:"the environment"`
	Key         string `json:"key" jsonschema:"variable name"`
	Value       string `json:"value,omitempty" jsonschema:"its value"`
	Secret      bool   `json:"secret,omitempty" jsonschema:"keep the value out of views"`
	Unset       bool   `json:"unset,omitempty" jsonschema:"remove the variable from the environment's file"`
}

func (t *tools) setEnvironmentVariable(ctx context.Context, in setEnvironmentVariableIn) (any, error) {
	wf, err := t.containerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	p := envPath(wf, "/"+pathEscape(in.Environment)+"/variables/"+pathEscape(in.Key))
	if in.Unset {
		err = t.c.do(ctx, http.MethodDelete, p, nil, nil)
	} else {
		err = t.c.do(ctx, http.MethodPut, p, map[string]any{"value": in.Value, "secret": in.Secret}, nil)
	}
	if err != nil {
		return nil, err
	}
	return t.environmentsOf(ctx, wf)
}
