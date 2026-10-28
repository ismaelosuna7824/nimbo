package compliance

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Disclaimer heads every compliance view and report.
const Disclaimer = "Pre-audit based on the design, not a certification. Controls a design can't show (account settings, MFA, processes) are manual: only you can attest them."

// StatusLabel names a status for people.
func StatusLabel(s Status) string {
	switch s {
	case StatusMet:
		return "Met"
	case StatusViolated:
		return "Violated"
	case StatusNotApplicable:
		return "Not applicable"
	case StatusNotEvaluated:
		return "Not evaluated"
	case StatusManual:
		return "Manual"
	case StatusAttested:
		return "Attested"
	}
	return string(s)
}

// Sections groups r's controls by section, in the framework's order, each
// in Statuses order (violated first), keeping the benchmark order within a
// status.
func Sections(r Result) [][]ControlResult {
	var order []string
	by := map[string][]ControlResult{}
	for _, c := range r.Controls {
		if _, ok := by[c.Section]; !ok {
			order = append(order, c.Section)
		}
		by[c.Section] = append(by[c.Section], c)
	}
	out := make([][]ControlResult, 0, len(order))
	for _, s := range order {
		list := by[s]
		slices.SortStableFunc(list, func(a, b ControlResult) int {
			return slices.Index(Statuses, a.Status) - slices.Index(Statuses, b.Status)
		})
		out = append(out, list)
	}
	return out
}

// Markdown renders r for workflow as the exported report, dated now.
func Markdown(workflow string, r Result, now time.Time) string {
	var b strings.Builder
	f := r.Framework
	fmt.Fprintf(&b, "# Compliance pre-audit: %s\n\n", f.Name)
	fmt.Fprintf(&b, "- **Workflow:** %s\n", workflow)
	fmt.Fprintf(&b, "- **Framework:** %s v%s (`%s`) — %s\n", f.Name, f.Version, f.ID, f.Source)
	fmt.Fprintf(&b, "- **Date:** %s\n\n", now.Format("2006-01-02"))
	fmt.Fprintf(&b, "> %s\n\n", Disclaimer)

	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "**%d / %d automatic controls met.**\n\n", r.Met, r.Graded)
	b.WriteString("| Status | Controls |\n|---|---|\n")
	for _, s := range Statuses {
		fmt.Fprintf(&b, "| %s | %d |\n", StatusLabel(s), r.Counts[s])
	}
	b.WriteString("\n")

	var attested []ControlResult
	for _, sec := range Sections(r) {
		fmt.Fprintf(&b, "## %s\n\n", cell(sec[0].Section))
		b.WriteString("| Control | Status | Rules | Evidence | Guidance |\n|---|---|---|---|---|\n")
		for _, c := range sec {
			rules := "manual"
			if len(c.Rules) > 0 {
				rules = "`" + strings.Join(c.Rules, "`, `") + "`"
			}
			fmt.Fprintf(&b, "| %s %s | %s | %s | %s | %s |\n", c.ID, cell(c.Title), StatusLabel(c.Status), rules, cell(evidence(c)), cell(c.Guidance))
			if c.Attestation != nil {
				attested = append(attested, c)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Attestations\n\n")
	if len(attested) == 0 {
		b.WriteString("No manual control has been attested.\n")
		return b.String()
	}
	b.WriteString("| Control | Note | By | Date |\n|---|---|---|---|\n")
	for _, c := range attested {
		a := c.Attestation
		fmt.Fprintf(&b, "| %s %s | %s | %s | %s |\n", c.ID, cell(c.Title), cell(a.Note), cell(a.By), a.At.In(now.Location()).Format("2006-01-02"))
	}
	return b.String()
}

// evidence is the evidence cell of a control: its findings, why it has no
// verdict, or what was checked.
func evidence(c ControlResult) string {
	switch c.Status {
	case StatusViolated:
		lines := make([]string, 0, len(c.Findings)+1)
		lines = append(lines, c.Reason)
		for _, f := range c.Findings {
			line := f.Message
			if f.Address != "" {
				line = f.Address + ": " + line
			}
			if f.Fix != "" {
				line += " Fix: " + f.Fix
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "<br>")
	case StatusMet:
		return fmt.Sprintf("No findings on %d resource%s.", len(c.Resources), plural(len(c.Resources)))
	case StatusAttested:
		return "Attested: " + c.Attestation.Note
	case StatusManual:
		return "Not attested yet."
	}
	return c.Reason
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// cell makes s safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
