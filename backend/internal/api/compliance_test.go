package api

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Test-only frameworks: the API is tested on real rules without depending on
// the controls of the published benchmarks.
func init() {
	compliance.Register(compliance.Framework{
		ID: "test-cis-aws", Name: "Test AWS", Version: "1.0.0", Platform: "aws", Source: "https://example.com/aws",
		Controls: []compliance.Control{
			{ID: "1.1", Title: "Buckets tagged", Section: "Storage", Rules: []string{"bp_missing_tags"}, AppliesTo: []string{"aws_s3_bucket"}, Guidance: "Tag buckets."},
			{ID: "1.2", Title: "Databases encrypted", Section: "Database", Rules: []string{"bp_unencrypted_storage"}, AppliesTo: []string{"aws_db_instance"}, Guidance: "Encrypt."},
			{ID: "2.1", Title: "Root MFA", Section: "Identity", Manual: true, Guidance: "Turn on root MFA."},
		},
	})
	compliance.Register(compliance.Framework{
		ID: "test-cis-docker", Name: "Test Docker", Version: "1.0.0", Platform: "docker", Source: "https://example.com/docker",
		Controls: []compliance.Control{{ID: "1", Title: "Manual", Section: "Host", Manual: true, Guidance: "Harden the host."}},
	})
}

type complianceBody struct {
	Frameworks []compliance.FrameworkInfo `json:"frameworks"`
	Results    []compliance.Result        `json:"results"`
	Disclaimer string                     `json:"disclaimer"`
}

func complianceControl(t *testing.T, b complianceBody, id string) compliance.ControlResult {
	t.Helper()
	if len(b.Results) != 1 {
		t.Fatalf("results %+v", b.Results)
	}
	for _, c := range b.Results[0].Controls {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no control %s", id)
	return compliance.ControlResult{}
}

func TestCompliance(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Shop")
	e.addNode(wf.ID, "aws_s3_bucket")
	wf, _ = e.addNode(wf.ID, "aws_s3_bucket")
	base := "/workflows/" + wf.ID + "/compliance"

	var all complianceBody
	e.mustCall(http.StatusOK, "GET", base, nil, &all)
	ids := []string{}
	for _, f := range all.Frameworks {
		ids = append(ids, f.ID)
		if f.Platform != "aws" {
			t.Errorf("%s offered to an AWS workflow", f.ID)
		}
	}
	if !slices.Contains(ids, "test-cis-aws") || len(all.Results) != len(all.Frameworks) || !strings.Contains(all.Disclaimer, "not a certification") {
		t.Fatalf("frameworks %v, %d results", ids, len(all.Results))
	}

	// The grade agrees with the workflow's own findings.
	var cur model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &cur)
	untagged := 0
	for _, d := range cur.Diagnostics {
		if d.Code == "bp_missing_tags" && nodeByID(&cur, d.NodeID) != nil && nodeByID(&cur, d.NodeID).Type == "aws_s3_bucket" {
			untagged++
		}
	}
	want := map[int]compliance.Status{0: compliance.StatusMet, 1: compliance.StatusViolated, 2: compliance.StatusViolated}[untagged]
	var one complianceBody
	e.mustCall(http.StatusOK, "GET", base+"?framework=test-cis-aws", nil, &one)
	if c := complianceControl(t, one, "1.1"); c.Status != want || len(c.Resources) != 2 {
		t.Errorf("1.1 with %d untagged buckets = %s %+v", untagged, c.Status, c.Resources)
	}
	if c := complianceControl(t, one, "1.2"); c.Status != compliance.StatusNotApplicable {
		t.Errorf("1.2 = %+v", c)
	}
	e.mustCall(http.StatusBadRequest, "GET", base+"?framework=test-cis-docker", nil, nil)
	e.mustCall(http.StatusNotFound, "GET", base+"?framework=nope", nil, nil)

	// A rule turned off in Settings: the control is not evaluated.
	var st model.Settings
	e.mustCall(http.StatusOK, "GET", "/settings", nil, &st)
	st.BestPractices.DisabledRules = []string{"bp_missing_tags"}
	e.mustCall(http.StatusOK, "PUT", "/settings", st, nil)
	e.mustCall(http.StatusOK, "GET", base+"?framework=test-cis-aws", nil, &one)
	if c := complianceControl(t, one, "1.1"); c.Status != compliance.StatusNotEvaluated || !strings.Contains(c.Reason, "bp_missing_tags") {
		t.Errorf("disabled rule: %+v", c)
	}

	// Attestations: manual controls only, persisted, removable.
	att := base + "/attestations/test-cis-aws/2.1"
	e.mustCall(http.StatusBadRequest, "PUT", att, map[string]string{"note": " "}, nil)
	e.mustCall(http.StatusBadRequest, "PUT", base+"/attestations/test-cis-aws/1.1", map[string]string{"note": "x"}, nil)
	e.mustCall(http.StatusNotFound, "PUT", base+"/attestations/test-cis-aws/9.9", map[string]string{"note": "x"}, nil)
	var a compliance.Attestation
	e.mustCall(http.StatusOK, "PUT", att, map[string]string{"note": "Root MFA with a hardware key"}, &a)
	if a.Note != "Root MFA with a hardware key" || a.At.IsZero() {
		t.Errorf("attestation %+v", a)
	}
	e.mustCall(http.StatusOK, "GET", base+"?framework=test-cis-aws", nil, &one)
	if c := complianceControl(t, one, "2.1"); c.Status != compliance.StatusAttested || c.Attestation == nil {
		t.Errorf("attested: %+v", c)
	}

	res, err := http.Get(e.srv.URL + "/api/v1" + base + "/report?framework=test-cis-aws&format=md")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	md := string(body)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/markdown") ||
		!strings.Contains(res.Header.Get("Content-Disposition"), `filename=shop-test-cis-aws-`) ||
		!strings.Contains(md, "- **Workflow:** Shop") || !strings.Contains(md, "not a certification") || !strings.Contains(md, "Root MFA with a hardware key") {
		t.Errorf("report %d %v:\n%s", res.StatusCode, res.Header, md)
	}
	e.mustCall(http.StatusBadRequest, "GET", base+"/report?format=pdf", nil, nil)

	e.mustCall(http.StatusNoContent, "DELETE", att, nil, nil)
	e.mustCall(http.StatusOK, "GET", base+"?framework=test-cis-aws", nil, &one)
	if c := complianceControl(t, one, "2.1"); c.Status != compliance.StatusManual {
		t.Errorf("after undo: %+v", c)
	}
}
