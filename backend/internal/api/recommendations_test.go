package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/advisor"
)

const publicAPI = `resource "aws_api_gateway_rest_api" "api" {
  name = "shop"
}

resource "aws_api_gateway_stage" "live" {
  rest_api_id   = aws_api_gateway_rest_api.api.id
  deployment_id = "abc123"
  stage_name    = "live"
}
`

const wafAssociation = `
resource "aws_wafv2_web_acl_association" "live" {
  resource_arn = aws_api_gateway_stage.live.arn
  web_acl_arn  = "arn:aws:wafv2:us-east-1:000000000000:regional/webacl/x/y"
}
`

func recIDs(recs []advisor.Recommendation) []string {
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.ID
	}
	return ids
}

func TestWorkflowRecommendations(t *testing.T) {
	e := newEnv(t)
	empty := e.createEmpty("Empty")
	var none []advisor.Recommendation
	e.mustCall(http.StatusOK, "GET", "/workflows/"+empty.ID+"/recommendations", nil, &none)
	if none == nil || len(none) != 0 {
		t.Fatalf("empty workflow recommendations = %#v, want []", none)
	}

	wf := e.createEmpty("Shop API")
	applyCode(t, e, wf.ID, map[string]string{"main.tf": publicAPI})
	var recs []advisor.Recommendation
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/recommendations", nil, &recs)
	i := slices.IndexFunc(recs, func(r advisor.Recommendation) bool { return r.ID == "aws_public_api_waf" })
	if i < 0 {
		t.Fatalf("no aws_public_api_waf in %v", recIDs(recs))
	}
	if r := recs[i]; r.Title == "" || r.Why == "" || len(r.Add) == 0 || r.Scope == "" || r.Local == "" || r.Docs == "" || len(r.Pillars) == 0 {
		t.Errorf("incomplete recommendation %+v", r)
	}

	// The review carries the same list.
	var review wafReview
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/well-architected", nil, &review)
	if !slices.Equal(recIDs(review.Recommendations), recIDs(recs)) {
		t.Errorf("review recommendations %v, endpoint %v", recIDs(review.Recommendations), recIDs(recs))
	}

	// Adding the companion makes it disappear.
	applyCode(t, e, wf.ID, map[string]string{"main.tf": publicAPI + wafAssociation})
	recs = nil
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/recommendations", nil, &recs)
	if slices.Contains(recIDs(recs), "aws_public_api_waf") {
		t.Errorf("aws_public_api_waf still recommended with a WAF association: %v", recIDs(recs))
	}

	e.mustCall(http.StatusNotFound, "GET", "/workflows/nope/recommendations", nil, nil)
}
