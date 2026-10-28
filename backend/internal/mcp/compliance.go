package mcp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
)

// compliance_report: GET /workflows/{id}/compliance (and .../report for the
// Markdown).

const groupCompliance = "Compliance"

func registerComplianceTools(r *registry, t *tools) {
	r.group = groupCompliance
	addTool(r, "compliance_report", "Compliance report",
		"Read-only pre-audit of a workflow's DESIGN against the security benchmarks of its platforms (CIS AWS / Azure / GCP / OCI Foundations for Terraform clouds, CIS Docker, CIS Kubernetes) — never a certification. "+
			"Per framework: \"N / M automatic controls met\", counts per status and the controls: violated (a finding of its rules on any resource it applies to — reason says how many of them, e.g. \"1 of 3 resources have findings\"), with its rules and findings (resource address, message, fix); manual controls (account settings, MFA, processes a design can't show) the user hasn't attested, with guidance; not_evaluated ones (a rule is turned off in Settings) with the reason; met, attested and not_applicable ids. "+
			"Fix violations with the usual edit tools (the findings are the best-practice rules' own), then call it again. Only the user attests manual controls, in the app's Compliance tab. markdown=true returns the exportable Markdown report of one framework instead.",
		t.complianceReport)
}

type complianceReportIn struct {
	Workflow  string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Framework string `json:"framework,omitempty" jsonschema:"framework id (e.g. cis-aws-7.0, cis-docker-1.8.0); omit for every framework that applies"`
	Markdown  bool   `json:"markdown,omitempty" jsonschema:"return the Markdown report (header, summary, a table per section, attestations) of framework, or of the first that applies"`
}

type complianceControlView struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Section  string   `json:"section"`
	Status   string   `json:"status"`
	Rules    []string `json:"rules,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Guidance string   `json:"guidance,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

type complianceFrameworkView struct {
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	Version       string                  `json:"version"`
	Source        string                  `json:"source"`
	Summary       string                  `json:"summary"`
	Counts        map[string]int          `json:"counts"`
	Open          []complianceControlView `json:"open"`
	Met           []string                `json:"met"`
	Attested      []string                `json:"attested,omitempty"`
	NotApplicable []string                `json:"not_applicable,omitempty"`
}

// maxComplianceFindings caps the findings listed per control.
const maxComplianceFindings = 5

func (t *tools) complianceReport(ctx context.Context, in complianceReportIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	base := "/workflows/" + pathEscape(wf.ID) + "/compliance"
	if in.Markdown {
		var md string
		path := base + "/report?format=md"
		if in.Framework != "" {
			path += "&framework=" + url.QueryEscape(in.Framework)
		}
		if err := t.c.get(ctx, path, &md); err != nil {
			return nil, err
		}
		return md, nil
	}
	if in.Framework != "" {
		base += "?framework=" + url.QueryEscape(in.Framework)
	}
	var res struct {
		Frameworks []compliance.FrameworkInfo `json:"frameworks"`
		Results    []compliance.Result        `json:"results"`
		Disclaimer string                     `json:"disclaimer"`
	}
	if err := t.c.get(ctx, base, &res); err != nil {
		return nil, err
	}
	if len(res.Frameworks) == 0 {
		return map[string]any{"workflow": wf.ID, "frameworks": []any{}, "summary": "No compliance framework applies to this workflow yet (Terraform workflows get their clouds' CIS Foundations once they have resources)."}, nil
	}
	out := []complianceFrameworkView{}
	for _, r := range res.Results {
		v := complianceFrameworkView{ID: r.Framework.ID, Name: r.Framework.Name, Version: r.Framework.Version, Source: r.Framework.Source,
			Summary: fmt.Sprintf("%d / %d automatic controls met", r.Met, r.Graded), Counts: map[string]int{}, Open: []complianceControlView{}, Met: []string{}}
		for s, n := range r.Counts {
			v.Counts[string(s)] = n
		}
		for _, sec := range compliance.Sections(r) {
			for _, c := range sec {
				switch c.Status {
				case compliance.StatusMet:
					v.Met = append(v.Met, c.ID)
					continue
				case compliance.StatusAttested:
					v.Attested = append(v.Attested, c.ID)
					continue
				case compliance.StatusNotApplicable:
					v.NotApplicable = append(v.NotApplicable, c.ID)
					continue
				}
				cv := complianceControlView{ID: c.ID, Title: c.Title, Section: c.Section, Status: string(c.Status), Rules: c.Rules, Reason: c.Reason}
				if c.Status == compliance.StatusManual {
					cv.Guidance = c.Guidance
				}
				for i, f := range c.Findings {
					if i == maxComplianceFindings {
						cv.Findings = append(cv.Findings, fmt.Sprintf("… %d more", len(c.Findings)-i))
						break
					}
					line := f.Code + ": " + f.Message
					if f.Address != "" {
						line = f.Address + " " + line
					}
					if f.Fix != "" {
						line += " — fix: " + f.Fix
					}
					cv.Findings = append(cv.Findings, line)
				}
				v.Open = append(v.Open, cv)
			}
		}
		out = append(out, v)
	}
	return map[string]any{"workflow": wf.ID, "disclaimer": res.Disclaimer, "frameworks": out}, nil
}
