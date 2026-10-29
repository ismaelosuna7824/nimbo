package mcp

import (
	"context"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/codeguide"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// code_guidelines: static guidance from internal/codeguide; with a
// workflow, its mode and platform are the defaults (GET /workflows/{id}).

const groupCode = "Code"

func registerCodeGuideTools(r *registry, t *tools) {
	r.group = groupCode
	addTool(r, "code_guidelines", "Code guidelines",
		"Call before writing any application code for a workflow (handlers, services, Dockerfiles, specs) and when choosing where and how it runs. "+
			"Returns, for mode (architecture: smoke handlers that prove the wiring | full: real code), language, style (service | modular-monolith | microservices), platform (aws, azure, gcp, oci, docker, kubernetes) and target (function | container | vm): "+
			"principles (dependency rule, ports only at real seams, composition root, value objects, typed errors, idempotency, outbox, 12-factor config; scaled to the style), layout (services/<svc>/ folders), files (complete starter files to write as-is with apply_terraform_code, then adapt), "+
			"packaging (zip of src, package-stage build into dist, or image, with the exact Terraform snippet), compute (functions vs managed containers vs Kubernetes vs VMs on that platform: when, avoidWhen, runs locally or export only), testing, security (OWASP API Top 10), observability, "+
			"spec (OpenSpec layout, clarification questions, proposal/spec/design/tasks templates, gate rules) and sources. "+
			"Full mode without language returns languages (each with why/when): ask the user, or propose one with its reason, then call again with it. workflow fills mode and platform from the workflow.",
		t.codeGuidelines)
}

type codeGuidelinesIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name: its mode and platform are the defaults; omit for generic guidance"`
	Mode     string `json:"mode,omitempty" jsonschema:"architecture (default) or full"`
	Language string `json:"language,omitempty" jsonschema:"typescript, javascript, python, go, java or csharp; omit in full mode to get the options to propose"`
	Style    string `json:"style,omitempty" jsonschema:"service (default), modular-monolith or microservices"`
	Platform string `json:"platform,omitempty" jsonschema:"aws (default), azure, gcp, oci, docker or kubernetes"`
	Target   string `json:"target,omitempty" jsonschema:"function (default on clouds), container (default on docker/kubernetes) or vm"`
}

type codeGuidelinesView struct {
	codeguide.Guide
	Next string `json:"next"`
}

func (t *tools) codeGuidelines(ctx context.Context, in codeGuidelinesIn) (any, error) {
	o := codeguide.Options{Mode: in.Mode, Language: in.Language, Style: in.Style, Platform: in.Platform, Target: in.Target}
	if strings.TrimSpace(in.Workflow) != "" {
		wf, err := t.c.workflow(ctx, in.Workflow)
		if err != nil {
			return nil, err
		}
		if o.Mode == "" {
			o.Mode = wf.EffectiveMode()
		}
		if o.Platform == "" {
			o.Platform = workflowPlatform(wf)
		}
	}
	g, err := codeguide.Get(o)
	if err != nil {
		return nil, err
	}
	return codeGuidelinesView{Guide: g, Next: codeGuidelinesNext(g)}, nil
}

// workflowPlatform is the platform of a workflow: its kind, or the only
// cloud its resources use ("" when several or none, leaving the default).
func workflowPlatform(wf *model.Workflow) string {
	switch wf.Kind {
	case model.WorkflowDocker, model.WorkflowKubernetes:
		return wf.Kind
	}
	clouds := wf.UsedClouds
	if len(clouds) == 0 {
		clouds = wf.Clouds
	}
	if len(clouds) == 1 {
		return string(clouds[0])
	}
	return ""
}

func codeGuidelinesNext(g codeguide.Guide) string {
	switch {
	case g.Mode == codeguide.ModeFull && g.Language == "":
		return "Ask the user which language (or propose one from languages with its why), confirm compute and style, then call code_guidelines again with language."
	case g.Mode == codeguide.ModeArchitecture:
		return "Write files with apply_terraform_code (one services/<svc>/ folder per compute in the diagram), wire the Terraform per packaging with one env var per connected store, deploy, then call GET /health and GET /smoke (try_endpoint): done when every check is ok."
	}
	return "Clarify first (spec questions, openspec/ files; no code while [NEEDS CLARIFICATION] remains), then write files with apply_terraform_code, adapt the Item example to the spec's domain, wire the Terraform per packaging, deploy and prove each scenario."
}
