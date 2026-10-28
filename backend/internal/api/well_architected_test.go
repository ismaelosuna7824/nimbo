package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/wellarch"
)

func TestWellArchitectedGuide(t *testing.T) {
	e := newEnv(t)
	for _, p := range wellarch.Platforms() {
		var g wellarch.Guide
		e.mustCall(http.StatusOK, "GET", "/well-architected/"+p, nil, &g)
		if g.Platform != p || g.Framework == "" || len(g.Sources) == 0 || len(g.Pillars) != len(model.Pillars) {
			t.Errorf("%s guide = %+v", p, g)
		}
	}
	e.mustCall(http.StatusNotFound, "GET", "/well-architected/heroku", nil, nil)
}

// pillarOf returns the pillar a review lists code under ("" if absent).
func pillarOf(r wafReview, code string) (model.Pillar, wafFinding) {
	for _, p := range r.Pillars {
		for _, f := range p.Findings {
			if f.Code == code {
				return p.Pillar, f
			}
		}
	}
	return "", wafFinding{}
}

func checkReviewShape(t *testing.T, r wafReview) {
	t.Helper()
	if len(r.Pillars) != len(model.Pillars) || !strings.Contains(r.Note, "every rule") {
		t.Fatalf("review = %+v", r)
	}
	seen := map[string]bool{}
	for i, p := range r.Pillars {
		if p.Pillar != model.Pillars[i] {
			t.Errorf("pillar %d = %s, want %s", i, p.Pillar, model.Pillars[i])
		}
		var c wafCounts
		for _, f := range p.Findings {
			key := f.Code + "\x00" + f.NodeID + "\x00" + f.Attribute + "\x00" + f.Message + "\x00" + f.File
			if seen[key] {
				t.Errorf("finding listed twice: %+v", f)
			}
			seen[key] = true
			switch f.Severity {
			case model.SeverityError:
				c.Error++
			case model.SeverityWarning:
				c.Warning++
			default:
				c.Info++
			}
		}
		want := pillarOK
		if c.Error+c.Warning > 0 {
			want = pillarRisk
		} else if c.Info > 0 {
			want = pillarAdvice
		}
		if c != p.Counts || p.Status != want {
			t.Errorf("%s counts %+v status %s, findings give %+v %s", p.Pillar, p.Counts, p.Status, c, want)
		}
	}
}

func TestWellArchitectedReviewTerraform(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Shop")
	applyCode(t, e, wf.ID, map[string]string{"main.tf": `resource "aws_security_group" "db" {
  name = "db"
  ingress {
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_instance" "main" {
  identifier              = "main"
  engine                  = "postgres"
  instance_class          = "db.t3.micro"
  allocated_storage       = 20
  backup_retention_period = 0
  vpc_security_group_ids  = [aws_security_group.db.id]
}
`})
	var r wafReview
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/well-architected", nil, &r)
	checkReviewShape(t, r)
	if r.WorkflowID != wf.ID || !slices.Equal(r.Platforms, []string{"aws"}) || len(r.Guidance) != 1 || r.Guidance[0].Platform != "aws" || r.Guidance[0].Framework == "" || len(r.Guidance[0].Sources) == 0 {
		t.Errorf("platforms %v guidance %+v", r.Platforms, r.Guidance)
	}
	if p, f := pillarOf(r, "bp_open_ingress"); p != model.PillarSecurity || f.Title == "" || f.Severity != model.SeverityWarning {
		t.Errorf("bp_open_ingress under %q: %+v", p, f)
	}
	if p, _ := pillarOf(r, "bp_rds_backup_retention"); p != model.PillarReliability {
		t.Errorf("bp_rds_backup_retention under %q", p)
	}
	// Opt-in (DefaultOff) rules run in a review: single-AZ RDS is advice.
	if p, f := pillarOf(r, "bp_rds_multi_az"); p != model.PillarReliability || f.Severity != model.SeverityInfo {
		t.Errorf("bp_rds_multi_az under %q: %+v", p, f)
	}
	if r.Pillars[0].Status != pillarRisk || r.Pillars[1].Status != pillarRisk {
		t.Errorf("security %s, reliability %s", r.Pillars[0].Status, r.Pillars[1].Status)
	}
	// Every finding sits under its rule's first pillar.
	meta := map[string]model.BestPracticeRule{}
	for _, rule := range lint.Rules() {
		meta[rule.Code] = rule
	}
	for _, p := range r.Pillars {
		for _, f := range p.Findings {
			if rule, found := meta[f.Code]; found && len(rule.Pillars) > 0 && rule.Pillars[0] != p.Pillar {
				t.Errorf("%s under %s, rule pillars %v", f.Code, p.Pillar, rule.Pillars)
			}
		}
	}

	// A review ignores the user's disables.
	var st model.Settings
	e.mustCall(http.StatusOK, "GET", "/settings", nil, &st)
	st.BestPractices.Disabled = true
	e.mustCall(http.StatusOK, "PUT", "/settings", st, nil)
	var off wafReview
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/well-architected", nil, &off)
	if p, _ := pillarOf(off, "bp_open_ingress"); p != model.PillarSecurity {
		t.Errorf("checks off in Settings hide bp_open_ingress from the review")
	}

	e.mustCall(http.StatusNotFound, "GET", "/workflows/nope/well-architected", nil, nil)
}

func TestWellArchitectedReviewNetworkFindings(t *testing.T) {
	if title, pillars := findingRule("net_single_az", nil); title == "" || pillars[0] != model.PillarReliability {
		t.Errorf("net_single_az = %q %v", title, pillars)
	}
	if _, pillars := findingRule("net_something_new", nil); len(pillars) != 1 || pillars[0] != model.PillarReliability {
		t.Errorf("unknown net_ finding pillars %v", pillars)
	}
	r := buildWAFReview(&model.Workflow{ID: "w"}, []model.Diagnostic{{Severity: model.SeverityError, Code: "net_subnet_overlap", Message: "overlap"}, {Severity: model.SeverityInfo, Code: "x_unknown", Message: "m"}}, nil)
	if p, _ := pillarOf(r, "net_subnet_overlap"); p != model.PillarReliability || r.Pillars[1].Counts.Error != 1 || r.Pillars[1].Status != pillarRisk {
		t.Errorf("net finding: %+v", r.Pillars[1])
	}
	if p, _ := pillarOf(r, "x_unknown"); p != model.PillarOperations {
		t.Errorf("rule-less finding under %q", p)
	}
}

func TestWellArchitectedReviewDocker(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"name": "shop", "kind": "docker"})
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"changes": map[string]string{"compose.yaml": "services:\n  db:\n    image: postgres\n    ports:\n      - \"5432:5432\"\n"}}, nil)
	var r wafReview
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/well-architected", nil, &r)
	checkReviewShape(t, r)
	if !slices.Equal(r.Platforms, []string{"docker"}) || len(r.Guidance) != 1 || r.Guidance[0].Platform != "docker" {
		t.Errorf("platforms %v guidance %+v", r.Platforms, r.Guidance)
	}
	meta := map[string]model.BestPracticeRule{}
	for _, rule := range compose.Rules() {
		meta[rule.Code] = rule
	}
	n := 0
	for _, p := range r.Pillars {
		for _, f := range p.Findings {
			n++
			rule, found := meta[f.Code]
			if !strings.HasPrefix(f.Code, "dk_") || !found || f.Title != rule.Title {
				t.Errorf("non-docker or unknown finding %+v", f)
			}
		}
	}
	if p, _ := pillarOf(r, "dk_db_published"); p != model.PillarSecurity || n < 2 {
		t.Errorf("dk_db_published under %q, %d findings", p, n)
	}
}
