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

func TestReviewDockerAndBestPracticeDeltas(t *testing.T) {
	cs, _ := session(t)
	// Templates are clean examples.
	var wf dockerView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "template": "docker-node-postgres-redis"}, &wf)
	if len(wf.BestPractices) != 0 || wf.Review != "" {
		t.Fatalf("template has findings: %+v %q", wf.BestPractices, wf.Review)
	}
	var clean reviewResult
	mustTool(t, cs, "review_docker", map[string]any{"workflow": wf.ID}, &clean)
	if len(clean.Warnings)+len(clean.Info) != 0 || !strings.HasPrefix(clean.Summary, "No best-practice findings") || len(clean.OptInRules) == 0 {
		t.Fatalf("template review = %+v", clean)
	}

	// Palette services follow the practices too (restart, 127.0.0.1, volume, healthcheck).
	var added struct {
		Workflow dockerView `json:"workflow"`
	}
	mustTool(t, cs, "add_service", map[string]any{"workflow": wf.ID, "catalog_id": "mongo"}, &added)
	if len(added.Workflow.BestPractices) != 0 {
		t.Fatalf("catalog service introduced findings: %+v", added.Workflow.BestPractices)
	}
	mustTool(t, cs, "add_service", map[string]any{"workflow": wf.ID, "build_context": "web", "starter": "node"}, &added)
	if len(added.Workflow.BestPractices) != 0 {
		t.Fatalf("starter introduced findings: %+v", added.Workflow.BestPractices)
	}

	// Publishing the database on every interface is reported by the edit.
	var updated dockerView
	mustTool(t, cs, "update_service", map[string]any{"workflow": wf.ID, "service": "db", "set": map[string]any{"ports": []map[string]string{{"host": "5432", "container": "5432"}}}}, &updated)
	if len(updated.BestPractices) != 1 || updated.BestPractices[0].Code != "dk_db_published" || !strings.HasPrefix(updated.BestPractices[0].At, "compose.yaml:") || updated.BestPractices[0].Fix == "" {
		t.Fatalf("update_service best_practices = %+v", updated.BestPractices)
	}
	for _, d := range updated.Diagnostics {
		if strings.Contains(d, "dk_db_published") {
			t.Errorf("advice in diagnostics: %s", d)
		}
	}
	if !strings.Contains(updated.Review, "review_docker") {
		t.Errorf("review summary = %q", updated.Review)
	}

	// A Dockerfile written by code: its findings come back with file:line.
	var coded dockerView
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{
		"web/Dockerfile": "FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nRUN npm ci\nUSER node\nEXPOSE 8080\nCMD node server.js\n",
	}}, &coded)
	got := map[string]string{}
	for _, f := range coded.BestPractices {
		got[f.Code] = f.At
	}
	if got["dk_layer_order"] != "web/Dockerfile:3" || got["dk_shell_form"] != "web/Dockerfile:7" || got["dk_db_published"] != "" {
		t.Errorf("apply_terraform_code best_practices = %+v", coded.BestPractices)
	}

	var review reviewResult
	mustTool(t, cs, "review_docker", map[string]any{"workflow": wf.ID}, &review)
	codes := map[string]findingView{}
	for _, f := range append(review.Warnings, review.Info...) {
		codes[f.Code] = f
		if f.Why == "" || f.Fix == "" || !strings.HasPrefix(f.Docs, "https://") || f.At == "" {
			t.Errorf("finding without why/fix/docs/at: %+v", f)
		}
	}
	if _, ok := codes["dk_db_published"]; !ok || codes["dk_layer_order"].Node != "service:web" || review.Next == "" {
		t.Errorf("review = %+v", review)
	}
	// review_terraform on a docker workflow gives the docker review.
	var viaTF reviewResult
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &viaTF)
	if len(viaTF.Warnings) != len(review.Warnings) {
		t.Errorf("review_terraform on docker = %+v", viaTF)
	}
}

func TestReviewKubernetesAndBestPracticeDeltas(t *testing.T) {
	cs, _ := session(t)
	var wf kubeView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "template": "k8s-microservices"}, &wf)
	if wf.Kind != "kubernetes" || len(wf.Objects) == 0 || len(wf.BestPractices) != 0 || wf.Review != "" {
		t.Fatalf("template = %+v", wf)
	}
	var clean reviewResult
	mustTool(t, cs, "review_kubernetes", map[string]any{"workflow": wf.ID}, &clean)
	if len(clean.Warnings)+len(clean.Info) != 0 || !strings.HasPrefix(clean.Summary, "No best-practice findings") || len(clean.OptInRules) == 0 {
		t.Fatalf("template review = %+v", clean)
	}

	// A palette image is clean; an untagged image is reported by the edit.
	var added struct {
		Nodes    []string `json:"nodes"`
		Workflow kubeView `json:"workflow"`
	}
	mustTool(t, cs, "add_kubernetes_object", map[string]any{"workflow": wf.ID, "catalog_id": "image:postgres", "name": "db", "namespace": "shop"}, &added)
	for _, f := range added.Workflow.BestPractices {
		if f.Severity != "info" {
			t.Fatalf("catalog image introduced %s %s", f.Severity, f.Code)
		}
	}
	if len(added.Nodes) == 0 {
		t.Fatalf("catalog image: nodes %v findings %+v", added.Nodes, added.Workflow.BestPractices)
	}
	var updated kubeView
	mustTool(t, cs, "update_kubernetes_object", map[string]any{"workflow": wf.ID, "node": "Deployment/shop/backend", "set": map[string]any{"spec.template.spec.containers[0].image": "ghcr.io/stefanprodan/podinfo"}}, &updated)
	if len(updated.BestPractices) != 1 || updated.BestPractices[0].Code != "k8s_latest_tag" || !strings.HasPrefix(updated.BestPractices[0].At, "k8s/backend.yaml:") {
		t.Fatalf("update best_practices = %+v", updated.BestPractices)
	}
	if !strings.Contains(updated.Review, "review_kubernetes") {
		t.Errorf("review summary = %q", updated.Review)
	}
	var review reviewResult
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &review)
	if len(review.Warnings) != 1 || review.Warnings[0].Why == "" || !strings.HasPrefix(review.Warnings[0].Docs, "https://") {
		t.Errorf("review = %+v", review)
	}
}
