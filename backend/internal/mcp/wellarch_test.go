package mcp

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/advisor"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestWellArchitectedGuidance(t *testing.T) {
	cs, _ := session(t)
	var g wellArchitectedGuideView
	mustTool(t, cs, "well_architected_guidance", map[string]any{"platform": "AWS"}, &g)
	if g.Platform != "aws" || g.Framework == "" || len(g.Sources) == 0 || len(g.Pillars) != len(model.Pillars) || !strings.Contains(g.Next, "review_well_architected") || !strings.Contains(g.Next, "recommend_improvements") ||
		g.Layout == nil || !strings.Contains(g.Layout.Tree, "environments/") {
		t.Fatalf("aws guidance = %+v", g)
	}
	for _, pg := range g.Pillars {
		if len(pg.Practices) == 0 || pg.Practices[0].Title == "" || len(pg.Practices[0].How) == 0 {
			t.Errorf("%s pillar without practices: %+v", pg.Pillar, pg)
		}
	}

	// Provider aliases and one pillar.
	g = wellArchitectedGuideView{}
	mustTool(t, cs, "well_architected_guidance", map[string]any{"platform": "azurerm", "pillar": "reliability"}, &g)
	if g.Platform != "azure" || len(g.Pillars) != 1 || g.Pillars[0].Pillar != model.PillarReliability {
		t.Fatalf("azure reliability = %+v", g)
	}
	g = wellArchitectedGuideView{}
	mustTool(t, cs, "well_architected_guidance", map[string]any{"platform": "k8s", "pillar": "performance"}, &g)
	if g.Platform != "kubernetes" || len(g.Pillars) != 1 {
		t.Fatalf("kubernetes performance = %+v", g)
	}

	for _, args := range []map[string]any{{"platform": "heroku"}, {"platform": "gcp", "pillar": "speed"}, {}} {
		if text, isErr := callTool(t, cs, "well_architected_guidance", args); !isErr {
			t.Errorf("%v: no error: %s", args, text)
		}
	}
}

func TestReviewWellArchitected(t *testing.T) {
	cs, _ := session(t)
	var wf dockerView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "template": "docker-node-postgres-redis"}, &wf)
	mustTool(t, cs, "update_service", map[string]any{"workflow": wf.ID, "service": "db", "set": map[string]any{"ports": []map[string]string{{"host": "5432", "container": "5432"}}}}, nil)

	var r wafReviewView
	mustTool(t, cs, "review_well_architected", map[string]any{"workflow": wf.ID}, &r)
	if r.Workflow != "Shop" || !slices.Equal(r.Platforms, []string{"docker"}) || len(r.Frameworks) != 1 || !strings.Contains(r.Note, "every rule") {
		t.Fatalf("review = %+v", r)
	}
	if len(r.Pillars) != len(model.Pillars) {
		t.Fatalf("pillars = %+v", r.Pillars)
	}
	sec := r.Pillars[0]
	if sec.Pillar != model.PillarSecurity || sec.Status != "risk" || sec.Counts["warning"] == 0 {
		t.Fatalf("security = %+v", sec)
	}
	i := slices.IndexFunc(sec.Findings, func(f wafFindingView) bool { return f.Code == "dk_db_published" })
	if i < 0 {
		t.Fatalf("dk_db_published not under security: %+v", sec.Findings)
	}
	if f := sec.Findings[i]; f.Title == "" || f.Why == "" || f.Fix == "" || !strings.HasPrefix(f.At, "compose.yaml:") || f.Node == "" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(r.Summary, "security risk") || !strings.Contains(r.Next, "update_service") || !strings.Contains(r.Next, "review_well_architected") {
		t.Errorf("summary %q next %q", r.Summary, r.Next)
	}

	// Terraform: the workflow's clouds, edits named for Terraform.
	var tf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Queues"}, &tf)
	mustTool(t, cs, "add_node", map[string]any{"workflow": tf.ID, "type": "aws_sqs_queue", "name": "jobs", "attributes": map[string]string{"name": `"jobs"`}}, nil)
	r = wafReviewView{}
	mustTool(t, cs, "review_well_architected", map[string]any{"workflow": tf.ID}, &r)
	codes := []string{}
	for _, p := range r.Pillars {
		for _, f := range p.Findings {
			codes = append(codes, f.Code)
			if f.Code == "bp_name_prefix" && f.Node != "aws_sqs_queue.jobs" {
				t.Errorf("finding node = %q", f.Node)
			}
		}
	}
	if !slices.Equal(r.Platforms, []string{"aws"}) || !slices.Contains(codes, "bp_name_prefix") || !strings.Contains(r.Next, "update_node") {
		t.Errorf("terraform review = %+v", r)
	}
}

func TestRecommendImprovements(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop API"}, &wf)
	api := `resource "aws_api_gateway_rest_api" "api" {
  name = "shop"
}

resource "aws_api_gateway_stage" "live" {
  rest_api_id   = aws_api_gateway_rest_api.api.id
  deployment_id = "abc123"
  stage_name    = "live"
}
`
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{"main.tf": api}}, nil)

	var r recommendationsView
	mustTool(t, cs, "recommend_improvements", map[string]any{"workflow": wf.ID}, &r)
	i := slices.IndexFunc(r.Recommendations, func(rec advisor.Recommendation) bool { return rec.ID == "aws_public_api_waf" })
	if r.Workflow != "Shop API" || i < 0 || !strings.Contains(r.Next, "recommend_improvements") {
		t.Fatalf("recommendations = %+v", r)
	}
	if rec := r.Recommendations[i]; rec.Why == "" || len(rec.Add) == 0 || rec.Scope == "" || rec.Local == "" {
		t.Errorf("recommendation = %+v", rec)
	}

	// review_well_architected lists it compactly and points at the tool.
	var review wafReviewView
	mustTool(t, cs, "review_well_architected", map[string]any{"workflow": wf.ID}, &review)
	if !slices.ContainsFunc(review.Recommendations, func(b recommendBrief) bool { return b.ID == "aws_public_api_waf" && b.Why != "" }) || !strings.Contains(review.Next, "recommend_improvements") {
		t.Errorf("review recommendations %+v next %q", review.Recommendations, review.Next)
	}

	// Applying the companion removes it.
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{"main.tf": api + `
resource "aws_wafv2_web_acl_association" "live" {
  resource_arn = aws_api_gateway_stage.live.arn
  web_acl_arn  = "arn:aws:wafv2:us-east-1:000000000000:regional/webacl/x/y"
}
`}}, nil)
	r = recommendationsView{}
	mustTool(t, cs, "recommend_improvements", map[string]any{"workflow": wf.ID}, &r)
	if slices.ContainsFunc(r.Recommendations, func(rec advisor.Recommendation) bool { return rec.ID == "aws_public_api_waf" }) {
		t.Errorf("still recommended after the association: %+v", r.Recommendations)
	}

	if text, isErr := callTool(t, cs, "recommend_improvements", map[string]any{"workflow": "nope"}); !isErr {
		t.Errorf("unknown workflow: %s", text)
	}
}
