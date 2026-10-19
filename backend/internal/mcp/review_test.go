package mcp

import (
	"strings"
	"testing"
)

func TestReviewTerraformAndBestPracticeDeltas(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Reviewed"}, &wf)
	if len(wf.BestPractices) != 0 {
		t.Fatalf("empty workflow has findings: %+v", wf.BestPractices)
	}

	// Starters follow the practices: no findings.
	var added nodeResult
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_lambda_function", "name": "api"}, &added)
	if len(added.BestPractices) != 0 {
		t.Fatalf("starter introduced findings: %+v", added.BestPractices)
	}
	var clean reviewResult
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &clean)
	if len(clean.Warnings)+len(clean.Info) != 0 || !strings.HasPrefix(clean.Summary, "No best-practice findings") {
		t.Fatalf("fresh workflow review = %+v", clean)
	}

	// A fixed name is reported by the edit that introduced it, compactly.
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "jobs", "attributes": map[string]string{"name": `"jobs"`}}, &added)
	if len(added.BestPractices) != 1 || added.BestPractices[0].Code != "bp_name_prefix" || added.BestPractices[0].Node != "aws_sqs_queue.jobs" || added.BestPractices[0].Fix == "" {
		t.Fatalf("add_node best_practices = %+v", added.BestPractices)
	}
	for _, d := range added.Diagnostics {
		if strings.HasPrefix(d.Code, "bp_") {
			t.Errorf("best-practice finding in diagnostics: %+v", d)
		}
	}

	// set_variable without a description; set_output exposing a secret.
	out := mustTool(t, cs, "set_variable", map[string]any{"workflow": wf.ID, "name": "db_password", "type": "string"}, nil)
	for _, code := range []string{"bp_variable_description", "bp_sensitive_not_marked"} {
		if !strings.Contains(out, code) {
			t.Errorf("set_variable result lacks %s: %s", code, out)
		}
	}

	var review reviewResult
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &review)
	if len(review.Warnings) != 1 || review.Warnings[0].Code != "bp_sensitive_not_marked" || review.Warnings[0].Why == "" {
		t.Errorf("warnings = %+v", review.Warnings)
	}
	codes := map[string]bool{}
	for _, f := range review.Info {
		codes[f.Code] = true
		if f.Fix == "" || f.Why == "" {
			t.Errorf("finding without fix/why: %+v", f)
		}
	}
	if !codes["bp_name_prefix"] || !codes["bp_variable_description"] || review.Next == "" {
		t.Errorf("info = %+v", review.Info)
	}
	var got workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &got)
	if !strings.Contains(got.Review, "review_terraform") {
		t.Errorf("get_workflow review summary = %q", got.Review)
	}

	// Fixing a finding reports nothing new.
	var fixed nodeResult
	mustTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.jobs", "set": map[string]string{"name": `"${local.name_prefix}-jobs"`}}, &fixed)
	if len(fixed.BestPractices) != 0 {
		t.Errorf("fix introduced findings: %+v", fixed.BestPractices)
	}
	mustTool(t, cs, "set_variable", map[string]any{"workflow": wf.ID, "name": "db_password", "description": "Database password.", "sensitive": true}, nil)
	review = reviewResult{}
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &review)
	if len(review.Warnings)+len(review.Info) != 0 {
		t.Errorf("after fixes: %+v", review)
	}
}
