package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Best-practice findings (diagnostics with code "bp_*") are kept out of the
// "diagnostics" of tool results: review_terraform lists them, and every
// mutating tool returns the ones its change introduced as "best_practices"
// so the agent can fix them right away.

const bpPrefix = "bp_"

func isBestPractice(d model.Diagnostic) bool { return strings.HasPrefix(d.Code, bpPrefix) }

// bpView is the compact form of a finding in mutation results.
type bpView struct {
	Severity  model.Severity `json:"severity"`
	Code      string         `json:"code"`
	Node      string         `json:"node,omitempty"`
	Attribute string         `json:"attribute,omitempty"`
	Message   string         `json:"message"`
	Fix       string         `json:"fix,omitempty"`
}

// findingView is a finding in review_terraform, with the why.
type findingView struct {
	Code      string `json:"code"`
	Node      string `json:"node,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Message   string `json:"message"`
	Why       string `json:"why,omitempty"`
	Fix       string `json:"fix,omitempty"`
}

func findingKey(addrs map[string]string, d model.Diagnostic) string {
	return d.Code + "\x00" + addrs[d.NodeID] + "\x00" + d.Attribute + "\x00" + d.Message
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
		out = append(out, bpView{Severity: d.Severity, Code: d.Code, Node: addrs[d.NodeID], Attribute: d.Attribute, Message: d.Message, Fix: d.Fix})
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
	return fmt.Sprintf("%s: call review_terraform for details and fixes", countFindings(warnings, infos))
}

func countFindings(warnings, infos int) string {
	plural := func(n int, word string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", word)
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	return fmt.Sprintf("%s (%s, %d info)", plural(warnings+infos, "best-practice finding"), plural(warnings, "warning"), infos)
}

type reviewResult struct {
	Workflow      string        `json:"workflow"`
	Summary       string        `json:"summary"`
	Warnings      []findingView `json:"warnings,omitempty"`
	Info          []findingView `json:"info,omitempty"`
	DisabledRules []string      `json:"disabled_rules,omitempty"`
	Next          string        `json:"next,omitempty"`
}

func (t *tools) reviewTerraform(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var st model.Settings
	if err := t.c.get(ctx, "/settings", &st); err != nil {
		return nil, err
	}
	res := reviewResult{Workflow: wf.Name, DisabledRules: st.BestPractices.DisabledRules}
	if st.BestPractices.Disabled {
		res.Summary = "Best-practice checks are turned off in the Nimbo app (Settings → Best-practice checks)."
		return res, nil
	}
	addrs := addressByID(wf)
	for _, d := range wf.Diagnostics {
		if !isBestPractice(d) {
			continue
		}
		f := findingView{Code: d.Code, Node: addrs[d.NodeID], Attribute: d.Attribute, Message: d.Message, Why: d.Detail, Fix: d.Fix}
		if d.Severity == model.SeverityWarning {
			res.Warnings = append(res.Warnings, f)
		} else {
			res.Info = append(res.Info, f)
		}
	}
	if len(res.Warnings)+len(res.Info) == 0 {
		res.Summary = "No best-practice findings: the Terraform follows Nimbo's recommended practices."
		return res, nil
	}
	res.Summary = countFindings(len(res.Warnings), len(res.Info))
	res.Next = "Fix warnings first (security), then info. Use update_node / set_variable / set_output (or apply_terraform_code for larger edits; extract_module for module candidates) and call review_terraform again. Findings never block deploys; leave one only when the user wants it that way."
	return res, nil
}
