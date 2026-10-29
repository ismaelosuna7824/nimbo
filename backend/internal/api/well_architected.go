package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/advisor"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/netcheck"
	"github.com/ismaelosuna7824/nimbo/backend/internal/wellarch"
)

// Well-Architected: the provider guidance per platform (wellarch) and a
// review of a workflow grouped by pillar, from the same engines the app
// runs (lint + netcheck for Terraform, compose rules for docker, kuberules
// for Kubernetes) with every rule on.

// wellArchitectedGuide is GET /well-architected/{platform}.
func (s *Server) wellArchitectedGuide(w http.ResponseWriter, r *http.Request) error {
	platform := strings.ToLower(r.PathValue("platform"))
	g, found := wellarch.Get(platform)
	if !found {
		return errorf(http.StatusNotFound, "unknown platform %q (one of %s)", platform, strings.Join(wellarch.Platforms(), ", "))
	}
	return ok(w, g)
}

// wafFinding is a finding of the review with its rule's title.
type wafFinding struct {
	model.Diagnostic
	Title string `json:"title,omitempty"`
}

// wafCounts counts the findings of one pillar per severity
// (errors are network/syntax problems that also break deploys).
type wafCounts struct {
	Error   int `json:"error,omitempty"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// Pillar status of a review.
const (
	pillarOK     = "ok"     // no findings
	pillarAdvice = "advice" // info findings only
	pillarRisk   = "risk"   // warnings or errors
)

// wafPillar is the review of one pillar.
type wafPillar struct {
	Pillar   model.Pillar `json:"pillar"`
	Findings []wafFinding `json:"findings"`
	Counts   wafCounts    `json:"counts"`
	Status   string       `json:"status"`
}

// wafGuidance points at the framework of one platform
// (GET /well-architected/{platform} has its practices).
type wafGuidance struct {
	Platform  string            `json:"platform"`
	Name      string            `json:"name"`
	Framework string            `json:"framework"`
	Sources   []wellarch.Source `json:"sources"`
}

// wafReview is GET /workflows/{id}/well-architected.
type wafReview struct {
	WorkflowID string `json:"workflowId"`
	// Platforms are the workflow's clouds (Terraform) or docker / kubernetes.
	Platforms []string      `json:"platforms"`
	Pillars   []wafPillar   `json:"pillars"`
	Guidance  []wafGuidance `json:"guidance"`
	// Recommendations are the companions the design lacks (advisor).
	Recommendations []advisor.Recommendation `json:"recommendations"`
	Note            string                   `json:"note"`
}

const wellArchitectedNote = "The review runs every rule, including opt-in rules and rules turned off in Settings; the workflow's own diagnostics follow the user's settings. Each finding is listed under its rule's main pillar. Production-only practices (multi-AZ, deletion protection, backup retention) are advice for a local sandbox."

// workflowWellArchitected is GET /workflows/{id}/well-architected.
func (s *Server) workflowWellArchitected(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	rules := slices.Concat(lint.Rules(), compose.Rules(), kuberules.Rules())
	cfg := model.BestPracticeSettings{DisabledRules: []string{}, EnabledRules: make([]string, 0, len(rules))}
	meta := make(map[string]model.BestPracticeRule, len(rules))
	for _, rule := range rules {
		cfg.EnabledRules = append(cfg.EnabledRules, rule.Code)
		meta[rule.Code] = rule
	}
	diags := s.gradingDiagnostics(r.Context(), wf, cfg)
	out := buildWAFReview(wf, diags, meta)
	out.Recommendations = s.recommend(wf)
	return ok(w, out)
}

// workflowRecommendations is GET /workflows/{id}/recommendations: the
// companions the design lacks (WAF on a public API, DLQs, …), highest
// value first; empty once the design has them.
func (s *Server) workflowRecommendations(w http.ResponseWriter, r *http.Request) error {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, s.recommend(wf))
}

func (s *Server) recommend(wf *model.Workflow) []advisor.Recommendation {
	if recs := advisor.Recommend(wf, s.cfg.Catalog); recs != nil {
		return recs
	}
	return []advisor.Recommendation{}
}

// wellArchitectedReview groups diags by the first pillar of their rule.
func buildWAFReview(wf *model.Workflow, diags []model.Diagnostic, meta map[string]model.BestPracticeRule) wafReview {
	out := wafReview{WorkflowID: wf.ID, Platforms: wellArchitectedPlatforms(wf), Guidance: []wafGuidance{}, Note: wellArchitectedNote}
	for _, p := range out.Platforms {
		if g, found := wellarch.Get(p); found {
			out.Guidance = append(out.Guidance, wafGuidance{Platform: g.Platform, Name: g.Name, Framework: g.Framework, Sources: g.Sources})
		}
	}
	byPillar := map[model.Pillar][]wafFinding{}
	for _, d := range diags {
		title, pillars := findingRule(d.Code, meta)
		p := model.PillarOperations
		if len(pillars) > 0 {
			p = pillars[0]
		}
		byPillar[p] = append(byPillar[p], wafFinding{Diagnostic: d, Title: title})
	}
	for _, p := range model.Pillars {
		pr := wafPillar{Pillar: p, Findings: byPillar[p], Status: pillarOK}
		if pr.Findings == nil {
			pr.Findings = []wafFinding{}
		}
		// Most severe first, stable within a severity.
		slices.SortStableFunc(pr.Findings, func(a, b wafFinding) int {
			return severityRank(a.Severity) - severityRank(b.Severity)
		})
		for _, f := range pr.Findings {
			switch f.Severity {
			case model.SeverityError:
				pr.Counts.Error++
			case model.SeverityWarning:
				pr.Counts.Warning++
			default:
				pr.Counts.Info++
			}
		}
		switch {
		case pr.Counts.Error+pr.Counts.Warning > 0:
			pr.Status = pillarRisk
		case pr.Counts.Info > 0:
			pr.Status = pillarAdvice
		}
		out.Pillars = append(out.Pillars, pr)
	}
	return out
}

func severityRank(s model.Severity) int {
	switch s {
	case model.SeverityError:
		return 0
	case model.SeverityWarning:
		return 1
	}
	return 2
}

// wellArchitectedPlatforms are the wellarch platforms of wf: docker,
// kubernetes, or the clouds of a Terraform workflow.
func wellArchitectedPlatforms(wf *model.Workflow) []string {
	switch {
	case wf.IsDocker():
		return []string{"docker"}
	case wf.IsKubernetes():
		return []string{"kubernetes"}
	}
	out := []string{}
	for _, c := range wf.Clouds {
		if p := string(c); !slices.Contains(out, p) {
			if _, found := wellarch.Get(p); found {
				out = append(out, p)
			}
		}
	}
	return out
}

// findingRule is the title and pillars of the rule behind code: rule
// metadata, or the network check table for net_* findings.
func findingRule(code string, meta map[string]model.BestPracticeRule) (string, []model.Pillar) {
	if rule, found := meta[code]; found {
		return rule.Title, rule.Pillars
	}
	if n, found := netPillars[code]; found {
		return n.title, n.pillars
	}
	if strings.HasPrefix(code, netcheck.Prefix) {
		return "", []model.Pillar{model.PillarReliability}
	}
	return "", nil
}

type netRule struct {
	title   string
	pillars []model.Pillar
}

var (
	netReliability = []model.Pillar{model.PillarReliability}
	netSecurity    = []model.Pillar{model.PillarSecurity, model.PillarReliability}
)

// netPillars maps the network findings (netcheck has no rule metadata):
// broken addressing and routing is reliability, exposure is security.
var netPillars = map[string]netRule{
	"net_subnet_outside_network":   {"Subnets inside their network's address space", netReliability},
	"net_subnet_overlap":           {"Subnet ranges don't overlap", netReliability},
	"net_peering_overlap":          {"Peered networks don't overlap", netReliability},
	"net_route_cross_vpc":          {"Routes target gateways of the same network", netReliability},
	"net_sg_cross_vpc":             {"Security groups belong to the workload's network", netReliability},
	"net_public_subnet_no_igw":     {"Public subnets route to an internet gateway", netReliability},
	"net_private_subnet_igw":       {"Private subnets have no direct internet route", netSecurity},
	"net_no_egress":                {"Workloads can reach what they depend on", netReliability},
	"net_nat_no_igw":               {"NAT gateways sit in a public subnet", netReliability},
	"net_route_table_unassociated": {"Route tables are associated with subnets", netReliability},
	"net_nacl_ephemeral":           {"Network ACLs allow ephemeral return ports", netReliability},
	"net_rule_priority_duplicate":  {"Firewall rule priorities are unique", netSecurity},
	"net_single_az":                {"Spread across at least two availability zones", netReliability},
}
