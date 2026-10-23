package model

import "strings"

// ProdLike reports whether an environment name is production-like (prod,
// production, prd, live, staging, stage, stg and their -/_ variants): the
// production-only Kubernetes rules run there.
func ProdLike(env string) bool {
	e := strings.ToLower(env)
	for _, p := range []string{"prod", "production", "prd", "live", "staging", "stage", "stg"} {
		if e == p || strings.HasPrefix(e, p+"-") || strings.HasSuffix(e, "-"+p) || strings.HasPrefix(e, p+"_") {
			return true
		}
	}
	return false
}

// Environments of docker and Kubernetes workflows
// (GET/POST/PATCH/DELETE /workflows/{id}/environments...). They live in the
// workflow's files (Kustomize overlays/<env>, Helm values-<env>.yaml, compose
// override files and .env.<env>); Workflow.Environments lists their names and
// Workflow.ActiveEnvironment selects the one the graph, rules and deploy
// render. Terraform environments are edited with PUT /workflows/{id}.

// EnvironmentsView answers GET /workflows/{id}/environments.
type EnvironmentsView struct {
	Kind         string              `json:"kind"`   // workflow kind
	Active       string              `json:"active"` // "" = the base
	Environments []EnvironmentInfo   `json:"environments"`
	Sources      []EnvironmentSource `json:"sources"`
	// BaseFiles are what every environment builds on (docker: the compose
	// files and .env when it exists).
	BaseFiles []string `json:"baseFiles,omitempty"`
	// Note explains how environments map to files for this workflow.
	Note string `json:"note,omitempty"`
}

// EnvironmentInfo is one environment.
type EnvironmentInfo struct {
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	ProdLike bool   `json:"prodLike"`
	// Files are what it renders on top of the base (overlays/prod,
	// chart/values-prod.yaml, compose.prod.yaml, .env.prod).
	Files     []string              `json:"files"`
	Variables []EnvironmentVariable `json:"variables,omitempty"`
	Changes   []EnvironmentChange   `json:"changes,omitempty"`
}

// EnvironmentSource is a deployable unit and whether it takes
// environments (Kustomize and Helm sources do; plain manifests don't).
type EnvironmentSource struct {
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Supported bool   `json:"supported"`
	Note      string `json:"note,omitempty"`
}

// EnvironmentVariable is a variable of an environment's env file (docker).
type EnvironmentVariable struct {
	Key        string  `json:"key"`
	Value      *string `json:"value,omitempty"` // nil when secret
	Secret     bool    `json:"secret"`
	Base       *string `json:"base,omitempty"` // inherited base value (nil: none or secret)
	InBase     bool    `json:"inBase"`         // the base .env sets it (secrets too)
	Overridden bool    `json:"overridden"`     // set in this environment's file
	File       string  `json:"file"`
}

// EnvironmentChange is one field an environment changes from the base.
type EnvironmentChange struct {
	Target string `json:"target"` // service / workload
	Field  string `json:"field"`
	Base   string `json:"base"`
	Value  string `json:"value"`
}

// EnvCompare answers GET /workflows/{id}/environments/compare: each
// environment rendered on its own (the active one is not changed).
type EnvCompare struct {
	Columns []EnvCompareColumn `json:"columns"` // the base ("") first
	Groups  []EnvCompareGroup  `json:"groups"`
	// Errors are the render errors by environment ("" = base).
	Errors map[string]string `json:"errors,omitempty"`
}

// EnvCompareColumn is one environment of the matrix (Name "" = base).
type EnvCompareColumn struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// EnvCompareGroup is the rows of one workload or service.
type EnvCompareGroup struct {
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Kind  string          `json:"kind"`
	Rows  []EnvCompareRow `json:"rows"`
}

// EnvCompareRow is one setting across environments.
type EnvCompareRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Values are aligned with Columns ("" = not set / not rendered).
	Values  []string `json:"values"`
	Differs bool     `json:"differs"`
}

// EnvSettings answers GET /workflows/{id}/environments/{name}/settings:
// the common knobs of one workload/service in one environment.
type EnvSettings struct {
	Environment string       `json:"environment"`
	Target      string       `json:"target"`
	Targets     []EnvTarget  `json:"targets"`
	File        string       `json:"file,omitempty"` // where this environment's edits go
	Settings    []EnvSetting `json:"settings"`
	Note        string       `json:"note,omitempty"`
}

// EnvTarget is a workload (node ID) or compose service.
type EnvTarget struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// EnvSetting types (EnvSetting.Type).
const (
	EnvSettingInt    = "int"
	EnvSettingString = "string"
	EnvSettingCPU    = "cpu"
	EnvSettingMemory = "memory"
)

// EnvSetting is one knob. Key is opaque: clients send it back to PATCH
// /settings.
type EnvSetting struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
	Type  string `json:"type"`
	Value string `json:"value"`          // rendered in this environment
	Base  string `json:"base,omitempty"` // rendered in the base
	// Overridden: this environment's own file sets it.
	Overridden bool   `json:"overridden"`
	Editable   bool   `json:"editable"`
	Reason     string `json:"reason,omitempty"`
}
