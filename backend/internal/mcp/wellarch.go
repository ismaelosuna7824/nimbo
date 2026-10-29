package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/advisor"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/wellarch"
)

// well_architected_guidance: GET /well-architected/{platform};
// review_well_architected: GET /workflows/{id}/well-architected;
// recommend_improvements: GET /workflows/{id}/recommendations.

const groupWellArchitected = "Well-Architected"

func registerWellArchitectedTools(r *registry, t *tools) {
	r.group = groupWellArchitected
	addTool(r, "architecture_patterns", "Architecture patterns",
		"Call at the start of a design, after understanding the requirement (exposure, traffic and peaks, latency, consistency, data sensitivity, availability, team maturity), to choose an architecture style (n-tier, web-queue-worker, microservices, event-driven, serverless, modular monolith, streaming) and cloud design patterns (API gateway, BFF, cache-aside, CQRS, event sourcing, saga, outbox, pub/sub, queue-based load leveling, async request-reply, circuit breaker, strangler fig…), each mapped to the platform's services (AWS, Azure, GCP, OCI Terraform types, Docker Compose, Kubernetes) with what runs locally. "+
			"requirement (English or Spanish) returns ranked matches, styles first: id, name, kind, problem, useWhen, avoidWhen and the block for platform; pattern returns one pattern in full (trade-offs, practices, blocks, related, templates, sources). Without either it lists the catalog. "+
			"Prefer the simplest style and fewest patterns that meet the requirement (a CRUD API stays a simple n-tier/serverless design; avoidWhen is the over-engineering guard); explain the choice, its trade-offs and what you deliberately left out to the user.",
		t.architecturePatterns)
	addTool(r, "well_architected_guidance", "Well-Architected guidance",
		"Call BEFORE designing (or redesigning) a workflow to apply its provider's Well-Architected practices: AWS Well-Architected, Azure Well-Architected, Google Cloud Architecture Framework, OCI Well-Architected, or Nimbo's Docker Compose / Kubernetes guides. "+
			"Returns the framework, its sources and, per pillar (security, reliability, performance, cost, operations), a summary and practices: title, why, how (concrete Terraform resource types/attributes or compose/Kubernetes fields) and the Nimbo rule codes that check it. "+
			"pillar narrows it to one pillar. Design with these secure, reliable and scalable defaults; review_well_architected checks the result.",
		t.wellArchitectedGuidance)
	addTool(r, "review_well_architected", "Review Well-Architected",
		"Call before finishing a design: reviews the workflow against the Well-Architected pillars of its platforms (Terraform clouds, docker or Kubernetes). Runs every Nimbo rule — opt-in rules and rules turned off in Settings included — plus the network checks (Terraform), and groups the findings by pillar (security, reliability, performance, cost, operations): status ok | advice | risk, counts and each finding (severity, code, rule title, node, file:line, why, fix, docs). "+
			"Fix warnings (and errors) first with the usual edit tools, then call it again; explain to the user any finding you leave (production-only advice such as multi-AZ or deletion protection is fine to leave in a local sandbox unless the user wants production parity).",
		t.reviewWellArchitected)
	addTool(r, "recommend_improvements", "Recommend improvements",
		"Call after the first draft of a design and again before finishing: the companions a senior engineer would add to THIS design (e.g. a public API → WAF with a rate-based rule and stage throttling; a published local service → a rate-limiting reverse proxy; a queue consumer → a DLQ), highest value first. "+
			"Each item: id, title, why, pillars, nodes that triggered it, add (resource types/keys to add and their purpose), snippet (minimal HCL/YAML using the design's real addresses), scope (recommended | production), local (runs locally, export only, or the local equivalent), skip (when not to add it), rules that check it and docs. "+
			"Propose each item to the user with its one-line why; apply only what they accept or what clearly fits the request. When the user gave you the design (diagram, image, existing workflow), never apply an item that adds, removes or replaces components without their OK. Production-scope items are proposals in a local sandbox and export-only ones are not emulated locally (say so). Applied items disappear on the next call; an empty list means nothing is left.",
		t.recommendImprovements)
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
	return wellArchitectedGuideView{Guide: g, Next: "Design with these practices as defaults (secure, reliable, scalable) and lay files and environments out as layout shows; production-only ones (multi-AZ, deletion protection, long backup retention) are advice in the local sandbox: mention them and apply them when the user wants production parity. After the first draft call recommend_improvements; call review_well_architected before you finish."}, nil
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
	Recommendations []advisor.Recommendation `json:"recommendations"`
	Note            string                   `json:"note"`
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
	Frameworks      []string         `json:"frameworks,omitempty"`
	Recommendations []recommendBrief `json:"recommendations,omitempty"`
	Note            string           `json:"note"`
	Next            string           `json:"next,omitempty"`
}

// recommendBrief is a recommendation in review_well_architected
// (recommend_improvements has the full item with its snippet).
type recommendBrief struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Why   string `json:"why"`
	Scope string `json:"scope"`
	Local string `json:"local"`
}

type recommendationsView struct {
	Workflow        string                   `json:"workflow"`
	Recommendations []advisor.Recommendation `json:"recommendations"`
	Next            string                   `json:"next"`
}

func (t *tools) recommendImprovements(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	out := recommendationsView{Workflow: wf.Name, Recommendations: []advisor.Recommendation{}}
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/recommendations", &out.Recommendations); err != nil {
		return nil, err
	}
	if len(out.Recommendations) == 0 {
		out.Next = "Nothing to add: the design already has the companions Nimbo recommends. Call review_well_architected before you finish."
		return out, nil
	}
	out.Next = "Propose each item to the user with its one-line why (say which are production-scope and which are export-only, not emulated locally); apply the accepted ones (snippet via apply_terraform_code, or the usual edit tools) and call recommend_improvements again; then review_well_architected."
	return out, nil
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
	for _, rec := range res.Recommendations {
		out.Recommendations = append(out.Recommendations, recommendBrief{ID: rec.ID, Title: rec.Title, Why: rec.Why, Scope: rec.Scope, Local: rec.Local})
	}
	recsNext := ""
	if len(out.Recommendations) > 0 {
		recsNext = fmt.Sprintf(" %d recommendation(s) left: call recommend_improvements for their snippets and propose each to the user.", len(out.Recommendations))
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
		out.Next = strings.TrimSpace(recsNext)
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
	out.Next += recsNext
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
