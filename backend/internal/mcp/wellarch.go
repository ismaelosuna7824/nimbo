package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/wellarch"
)

// well_architected_guidance: GET /well-architected/{platform};
// review_well_architected: GET /workflows/{id}/well-architected.

const groupWellArchitected = "Well-Architected"

func registerWellArchitectedTools(r *registry, t *tools) {
	r.group = groupWellArchitected
	addTool(r, "well_architected_guidance", "Well-Architected guidance",
		"Call BEFORE designing (or redesigning) a workflow to apply its provider's Well-Architected practices: AWS Well-Architected, Azure Well-Architected, Google Cloud Architecture Framework, OCI Well-Architected, or Nimbo's Docker Compose / Kubernetes guides. "+
			"Returns the framework, its sources and, per pillar (security, reliability, performance, cost, operations), a summary and practices: title, why, how (concrete Terraform resource types/attributes or compose/Kubernetes fields) and the Nimbo rule codes that check it. "+
			"pillar narrows it to one pillar. Design with these secure, reliable and scalable defaults; review_well_architected checks the result.",
		t.wellArchitectedGuidance)
	addTool(r, "review_well_architected", "Review Well-Architected",
		"Call before finishing a design: reviews the workflow against the Well-Architected pillars of its platforms (Terraform clouds, docker or Kubernetes). Runs every Nimbo rule — opt-in rules and rules turned off in Settings included — plus the network checks (Terraform), and groups the findings by pillar (security, reliability, performance, cost, operations): status ok | advice | risk, counts and each finding (severity, code, rule title, node, file:line, why, fix, docs). "+
			"Fix warnings (and errors) first with the usual edit tools, then call it again; explain to the user any finding you leave (production-only advice such as multi-AZ or deletion protection is fine to leave in a local sandbox unless the user wants production parity).",
		t.reviewWellArchitected)
}

type wellArchitectedGuidanceIn struct {
	Platform string `json:"platform" jsonschema:"aws, azure, gcp, oci, docker or kubernetes"`
	Pillar   string `json:"pillar,omitempty" jsonschema:"security, reliability, performance, cost or operations; omit for every pillar"`
}

type wellArchitectedGuideView struct {
	wellarch.Guide
	Next string `json:"next"`
}

// platformAliases maps provider/kind names agents use to wellarch platforms.
var platformAliases = map[string]string{
	"amazon": "aws", "azurerm": "azure", "google": "gcp", "gcloud": "gcp", "oracle": "oci",
	"compose": "docker", "docker-compose": "docker", "k8s": "kubernetes", "kube": "kubernetes",
}

func (t *tools) wellArchitectedGuidance(ctx context.Context, in wellArchitectedGuidanceIn) (any, error) {
	platform := strings.ToLower(strings.TrimSpace(in.Platform))
	if a, ok := platformAliases[platform]; ok {
		platform = a
	}
	if platform == "" {
		return nil, fmt.Errorf("platform is required: one of %s", strings.Join(wellarch.Platforms(), ", "))
	}
	var g wellarch.Guide
	if err := t.c.get(ctx, "/well-architected/"+pathEscape(platform), &g); err != nil {
		return nil, err
	}
	if p := model.Pillar(strings.ToLower(strings.TrimSpace(in.Pillar))); p != "" {
		var keep []wellarch.PillarGuide
		for _, pg := range g.Pillars {
			if pg.Pillar == p {
				keep = append(keep, pg)
			}
		}
		if keep == nil {
			return nil, fmt.Errorf("unknown pillar %q: one of %s", in.Pillar, joinPillars())
		}
		g.Pillars = keep
	}
	return wellArchitectedGuideView{Guide: g, Next: "Design with these practices as defaults (secure, reliable, scalable); production-only ones (multi-AZ, deletion protection, long backup retention) are advice in the local sandbox: mention them and apply them when the user wants production parity. Call review_well_architected before you finish."}, nil
}

func joinPillars() string {
	s := make([]string, len(model.Pillars))
	for i, p := range model.Pillars {
		s[i] = string(p)
	}
	return strings.Join(s, ", ")
}

// wafReview is the API's review (api.wafReview).
type wafReview struct {
	WorkflowID string   `json:"workflowId"`
	Platforms  []string `json:"platforms"`
	Pillars    []struct {
		Pillar   model.Pillar `json:"pillar"`
		Findings []struct {
			model.Diagnostic
			Title string `json:"title"`
		} `json:"findings"`
		Counts struct {
			Error   int `json:"error"`
			Warning int `json:"warning"`
			Info    int `json:"info"`
		} `json:"counts"`
		Status string `json:"status"`
	} `json:"pillars"`
	Guidance []struct {
		Platform  string            `json:"platform"`
		Name      string            `json:"name"`
		Framework string            `json:"framework"`
		Sources   []wellarch.Source `json:"sources"`
	} `json:"guidance"`
	Note string `json:"note"`
}

type wafFindingView struct {
	Severity  model.Severity `json:"severity"`
	Code      string         `json:"code"`
	Title     string         `json:"title,omitempty"`
	Node      string         `json:"node,omitempty"`
	Attribute string         `json:"attribute,omitempty"`
	At        string         `json:"at,omitempty"`
	Message   string         `json:"message"`
	Why       string         `json:"why,omitempty"`
	Fix       string         `json:"fix,omitempty"`
	Docs      string         `json:"docs,omitempty"`
}

type wafPillarView struct {
	Pillar   model.Pillar     `json:"pillar"`
	Status   string           `json:"status"`
	Counts   map[string]int   `json:"counts"`
	Findings []wafFindingView `json:"findings,omitempty"`
}

type wafReviewView struct {
	Workflow  string          `json:"workflow"`
	Platforms []string        `json:"platforms"`
	Summary   string          `json:"summary"`
	Pillars   []wafPillarView `json:"pillars"`
	// Frameworks: "AWS Well-Architected Framework (https://…)".
	Frameworks []string `json:"frameworks,omitempty"`
	Note       string   `json:"note"`
	Next       string   `json:"next,omitempty"`
}

func (t *tools) reviewWellArchitected(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var res wafReview
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/well-architected", &res); err != nil {
		return nil, err
	}
	addrs := addressByID(wf)
	out := wafReviewView{Workflow: wf.Name, Platforms: res.Platforms, Pillars: []wafPillarView{}, Note: res.Note}
	for _, g := range res.Guidance {
		f := g.Framework
		if len(g.Sources) > 0 {
			f += " (" + g.Sources[0].URL + ")"
		}
		out.Frameworks = append(out.Frameworks, f)
	}
	risky, parts := 0, []string{}
	for _, p := range res.Pillars {
		v := wafPillarView{Pillar: p.Pillar, Status: p.Status, Counts: map[string]int{"warning": p.Counts.Warning, "info": p.Counts.Info}}
		if p.Counts.Error > 0 {
			v.Counts["error"] = p.Counts.Error
		}
		for _, f := range p.Findings {
			v.Findings = append(v.Findings, wafFindingView{Severity: f.Severity, Code: f.Code, Title: f.Title, Node: findingNode(addrs, f.Diagnostic), Attribute: f.Attribute,
				At: findingAt(f.Diagnostic), Message: f.Message, Why: f.Detail, Fix: f.Fix, Docs: f.Docs})
		}
		risky += p.Counts.Error + p.Counts.Warning
		if p.Status != "ok" {
			parts = append(parts, fmt.Sprintf("%s %s (%s)", p.Pillar, p.Status, countSeverities(p.Counts.Error, p.Counts.Warning, p.Counts.Info)))
		}
		out.Pillars = append(out.Pillars, v)
	}
	if len(parts) == 0 {
		out.Summary = "Every pillar is ok: no findings from any rule."
		return out, nil
	}
	out.Summary = strings.Join(parts, "; ")
	edit := "update_node / set_variable / apply_terraform_code (check_network for net_* findings)"
	switch {
	case wf.IsDocker():
		edit = "update_service / connect_services / apply_terraform_code"
	case wf.IsKubernetes():
		edit = "update_kubernetes_object / add_kubernetes_object / apply_terraform_code"
	}
	out.Next = "Fix errors and warnings first (risk pillars), then info, with " + edit + "; then call review_well_architected again. Tell the user about every finding you leave and why; production-only advice (multi-AZ, deletion protection, backup retention, replicas) may stay in a local sandbox unless the user wants production parity."
	if risky == 0 {
		out.Next = "Only advice is left: apply the info findings that fit the user's goal with " + edit + ", call review_well_architected again, and explain the ones you leave (production-only advice may stay in a local sandbox unless the user wants production parity)."
	}
	return out, nil
}

func countSeverities(errs, warnings, infos int) string {
	var s []string
	if errs > 0 {
		s = append(s, plural(errs, "error"))
	}
	if warnings > 0 {
		s = append(s, plural(warnings, "warning"))
	}
	if infos > 0 {
		s = append(s, fmt.Sprintf("%d info", infos))
	}
	return strings.Join(s, ", ")
}
