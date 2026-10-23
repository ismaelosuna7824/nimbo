package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Best-practice findings (diagnostics with code "bp_*", and the warning/
// info "dk_*" and "k8s_*" advice of docker and Kubernetes workflows) are
// kept out of the "diagnostics" of tool results: review_terraform /
// review_docker / review_kubernetes list them, and every mutating tool returns the ones its change introduced as
// "best_practices" so the agent can fix them right away.

const (
	bpPrefix  = "bp_"
	dkPrefix  = "dk_"
	k8sPrefix = "k8s_"
)

func isBestPractice(d model.Diagnostic) bool {
	return strings.HasPrefix(d.Code, bpPrefix) || (strings.HasPrefix(d.Code, dkPrefix) || strings.HasPrefix(d.Code, k8sPrefix)) && d.Severity != model.SeverityError
}

// bpView is the compact form of a finding in mutation results.
type bpView struct {
	Severity  model.Severity `json:"severity"`
	Code      string         `json:"code"`
	Node      string         `json:"node,omitempty"`
	Attribute string         `json:"attribute,omitempty"`
	At        string         `json:"at,omitempty"` // file:line (docker findings)
	Message   string         `json:"message"`
	Fix       string         `json:"fix,omitempty"`
}

// findingView is a finding in review_terraform / review_docker, with the why.
type findingView struct {
	Code      string `json:"code"`
	Node      string `json:"node,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	At        string `json:"at,omitempty"`
	Message   string `json:"message"`
	Why       string `json:"why,omitempty"`
	Fix       string `json:"fix,omitempty"`
	Docs      string `json:"docs,omitempty"`
}

// findingNode is the address of a finding's node (its ID when the node
// has no address: docker services).
func findingNode(addrs map[string]string, d model.Diagnostic) string {
	if a := addrs[d.NodeID]; a != "" {
		return a
	}
	return d.NodeID
}

func findingAt(d model.Diagnostic) string {
	if d.File == "" {
		return ""
	}
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d", d.File, d.Line)
	}
	return d.File
}

// findingKey identifies a finding across edits (line numbers move).
func findingKey(addrs map[string]string, d model.Diagnostic) string {
	return d.Code + "\x00" + findingNode(addrs, d) + "\x00" + d.Attribute + "\x00" + d.File + "\x00" + d.Message
}

// bpDelta returns the best-practice findings of after that before did not
// have (before may be nil: every finding is new).
func bpDelta(before, after *model.Workflow) []bpView {
	had := map[string]bool{}
	if before != nil {
		addrs := addressByID(before)
		for _, d := range before.Diagnostics {
			if isBestPractice(d) {
				had[findingKey(addrs, d)] = true
			}
		}
	}
	addrs := addressByID(after)
	var out []bpView
	for _, d := range after.Diagnostics {
		if !isBestPractice(d) || had[findingKey(addrs, d)] {
			continue
		}
		out = append(out, bpView{Severity: d.Severity, Code: d.Code, Node: findingNode(addrs, d), Attribute: d.Attribute, At: findingAt(d), Message: d.Message, Fix: d.Fix})
	}
	return out
}

// reviewSummary is the one-line best-practice summary of get_workflow.
func reviewSummary(wf *model.Workflow) string {
	warnings, infos := 0, 0
	for _, d := range wf.Diagnostics {
		if !isBestPractice(d) {
			continue
		}
		if d.Severity == model.SeverityWarning {
			warnings++
		} else {
			infos++
		}
	}
	if warnings+infos == 0 {
		return ""
	}
	tool := "review_terraform"
	switch {
	case wf.IsDocker():
		tool = "review_docker"
	case wf.IsKubernetes():
		tool = "review_kubernetes"
	}
	return fmt.Sprintf("%s: call %s for details and fixes", countFindings(warnings, infos), tool)
}

func countFindings(warnings, infos int) string {
	return fmt.Sprintf("%s (%s, %d info)", plural(warnings+infos, "best-practice finding"), plural(warnings, "warning"), infos)
}

type reviewResult struct {
	Workflow      string         `json:"workflow"`
	Summary       string         `json:"summary"`
	Structure     *structureView `json:"structure,omitempty"`
	Warnings      []findingView  `json:"warnings,omitempty"`
	Info          []findingView  `json:"info,omitempty"`
	DisabledRules []string       `json:"disabled_rules,omitempty"`
	// OptInRules are docker/Kubernetes rules off by default the user can turn on in
	// Settings (code: title).
	OptInRules []string `json:"opt_in_rules,omitempty"`
	Next       string   `json:"next,omitempty"`
}

// findings splits a workflow's best-practice findings by severity.
func (res *reviewResult) findings(wf *model.Workflow) {
	addrs := addressByID(wf)
	for _, d := range wf.Diagnostics {
		if !isBestPractice(d) {
			continue
		}
		f := findingView{Code: d.Code, Node: findingNode(addrs, d), Attribute: d.Attribute, At: findingAt(d), Message: d.Message, Why: d.Detail, Fix: d.Fix, Docs: d.Docs}
		if d.Severity == model.SeverityWarning {
			res.Warnings = append(res.Warnings, f)
		} else {
			res.Info = append(res.Info, f)
		}
	}
}

func (t *tools) reviewTerraform(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.IsDocker() {
		return t.reviewDockerWorkflow(ctx, wf)
	}
	if wf.IsKubernetes() {
		return t.reviewKubeWorkflow(ctx, wf)
	}
	var st model.Settings
	if err := t.c.get(ctx, "/settings", &st); err != nil {
		return nil, err
	}
	structure, err := t.structure(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	res := reviewResult{Workflow: wf.Name, Structure: structure, DisabledRules: st.BestPractices.DisabledRules}
	if st.BestPractices.Disabled {
		res.Summary = "Best-practice checks are turned off in the Nimbo app (Settings → Best-practice checks)."
		return res, nil
	}
	res.findings(wf)
	if len(res.Warnings)+len(res.Info) == 0 {
		res.Summary = "No best-practice findings: the Terraform follows Nimbo's recommended practices."
		return res, nil
	}
	res.Summary = countFindings(len(res.Warnings), len(res.Info))
	res.Next = "Fix warnings first (security, unpinned versions, providers in modules), then info. Use update_node / set_variable / set_output (or apply_terraform_code for larger edits; extract_module for module candidates, inline_module for single-resource wrappers; update_workflow layout for state per environment; plan_structure to split a large state by layer) and call review_terraform again. Findings never block deploys; leave one only when the user wants it that way."
	return res, nil
}

func (t *tools) reviewDocker(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	return t.reviewDockerWorkflow(ctx, wf)
}

// ruleReview starts the review of a docker or Kubernetes workflow: the
// rules (code prefix) the user turned off, the opt-in ones still off, and
// a summary when the checks are off altogether.
func (t *tools) ruleReview(ctx context.Context, wf *model.Workflow, prefix string) (*reviewResult, error) {
	var st model.Settings
	if err := t.c.get(ctx, "/settings", &st); err != nil {
		return nil, err
	}
	var rules []model.BestPracticeRule
	if err := t.c.get(ctx, "/best-practices/rules", &rules); err != nil {
		return nil, err
	}
	res := &reviewResult{Workflow: wf.Name}
	for _, code := range st.BestPractices.DisabledRules {
		if strings.HasPrefix(code, prefix) {
			res.DisabledRules = append(res.DisabledRules, code)
		}
	}
	for _, r := range rules {
		if r.DefaultOff && strings.HasPrefix(r.Code, prefix) && !slices.Contains(st.BestPractices.EnabledRules, r.Code) {
			res.OptInRules = append(res.OptInRules, r.Code+": "+r.Title)
		}
	}
	if st.BestPractices.Disabled {
		res.Summary = "Best-practice checks are turned off in the Nimbo app (Settings → Best-practice checks)."
	}
	return res, nil
}

func (t *tools) reviewDockerWorkflow(ctx context.Context, wf *model.Workflow) (any, error) {
	res, err := t.ruleReview(ctx, wf, dkPrefix)
	if err != nil {
		return nil, err
	}
	if res.Summary != "" {
		return res, nil
	}
	res.findings(wf)
	if len(res.Warnings)+len(res.Info) == 0 {
		res.Summary = "No best-practice findings: the compose files and Dockerfiles follow Nimbo's recommended Docker practices."
		return res, nil
	}
	res.Summary = countFindings(len(res.Warnings), len(res.Info))
	res.Next = "Fix warnings first (exposed databases, secrets in builds, single-stage builds, cache-busting layer order, missing .dockerignore, shell-form CMD, healthy depends_on), then info. Edit services with update_service (restart, healthcheck, ports with hostIp, init via apply_terraform_code), Dockerfiles and .dockerignore with get_compose + apply_terraform_code, and call review_docker again. Findings never block runs; leave one only when the user wants it that way."
	return res, nil
}
