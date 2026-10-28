package compliance

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// testFramework is a tiny benchmark over made-up rule codes: the grading
// only looks at codes, so it doesn't depend on the real rules.
var testFramework = Framework{
	ID: "test-aws-1.0", Name: "Test AWS", Version: "1.0.0", Platform: "aws", Source: "https://example.com",
	Controls: []Control{
		{ID: "1.1", Title: "Buckets encrypted", Section: "Storage", Rules: []string{"bp_test_enc"}, AppliesTo: []string{"aws_s3_bucket"}},
		{ID: "1.2", Title: "Buckets private", Section: "Storage", Rules: []string{"bp_test_public", "bp_test_enc"}, AppliesTo: []string{"aws_s3_bucket"}},
		{ID: "2.1", Title: "Databases encrypted", Section: "Database", Rules: []string{"bp_test_db"}, AppliesTo: []string{"aws_db_instance"}},
		{ID: "3.1", Title: "Root MFA", Section: "Identity", Manual: true, Guidance: "Turn on MFA for the root user."},
		{ID: "3.2", Title: "Design-wide rule", Section: "Identity", Rules: []string{"bp_test_wide"}},
		{ID: "3.3", Title: "Queue keys rotated", Section: "Identity", Manual: true, AppliesTo: []string{"aws_sqs_queue"}},
	},
}

func node(id, typ string) model.Node {
	return model.Node{ID: id, Kind: model.KindResource, Type: typ, Name: strings.TrimPrefix(id, "n-")}
}

func tfWorkflow() *model.Workflow {
	return &model.Workflow{
		Clouds: []model.Cloud{model.CloudAWS},
		Nodes: []model.Node{
			node("n-logs", "aws_s3_bucket"), node("n-site", "aws_s3_bucket"),
			{ID: "d-ami", Kind: model.KindData, Type: "aws_s3_bucket", Name: "ami"},
		},
	}
}

func diag(code, node string) model.Diagnostic {
	return model.Diagnostic{Severity: model.SeverityInfo, Code: code, NodeID: node, Message: code + " on " + node, Fix: "fix it"}
}

func control(t *testing.T, r Result, id string) ControlResult {
	t.Helper()
	i := slices.IndexFunc(r.Controls, func(c ControlResult) bool { return c.ID == id })
	if i < 0 {
		t.Fatalf("no control %s", id)
	}
	return r.Controls[i]
}

func TestEvaluateStatuses(t *testing.T) {
	wf := tfWorkflow()
	diags := []model.Diagnostic{
		diag("bp_test_enc", "n-logs"),
		diag("bp_test_public", "n-logs"),
		diag("bp_test_public", "n-site"),
		diag("bp_test_enc", "d-ami"), // data source: not a resource
		diag("bp_other", "n-site"),   // not a rule of the framework
	}
	r := Evaluate(testFramework, wf, diags, model.BestPracticeSettings{}, nil)

	// One failing bucket of two fails the control: that's how an auditor reads it.
	enc := control(t, r, "1.1")
	if enc.Status != StatusViolated || enc.Reason != "1 of 2 resources have findings." || len(enc.Findings) != 1 || enc.Findings[0].Address != "aws_s3_bucket.logs" || len(enc.Resources) != 2 {
		t.Errorf("1.1 = %+v", enc)
	}
	if !enc.Resources[0].Failing || enc.Resources[1].Failing {
		t.Errorf("1.1 failing flags = %+v", enc.Resources)
	}
	if c := control(t, r, "1.2"); c.Status != StatusViolated || c.Reason != "2 of 2 resources have findings." || len(c.Findings) != 3 {
		t.Errorf("1.2 = %+v", c)
	}
	if c := control(t, r, "2.1"); c.Status != StatusNotApplicable || !strings.Contains(c.Reason, "aws_db_instance") {
		t.Errorf("2.1 = %+v", c)
	}
	if c := control(t, r, "3.1"); c.Status != StatusManual {
		t.Errorf("3.1 = %+v", c)
	}
	if c := control(t, r, "3.2"); c.Status != StatusMet || len(c.Resources) != 2 {
		t.Errorf("3.2 = %+v", c)
	}
	if c := control(t, r, "3.3"); c.Status != StatusNotApplicable {
		t.Errorf("manual control without its resources = %+v", c)
	}
	if r.Met != 1 || r.Graded != 3 || r.Counts[StatusViolated] != 2 || r.Counts[StatusNotApplicable] != 2 || r.Counts[StatusManual] != 1 {
		t.Errorf("summary met=%d graded=%d counts=%v", r.Met, r.Graded, r.Counts)
	}
}

// A rule that is off can't find anything, so its controls must never read
// as met; opt-in rules are not "off" (GradingSettings runs them).
func TestEvaluateDisabledRuleIsNotEvaluated(t *testing.T) {
	wf := tfWorkflow()
	r := Evaluate(testFramework, wf, nil, model.BestPracticeSettings{DisabledRules: []string{"bp_test_enc"}}, nil)
	for _, id := range []string{"1.1", "1.2"} {
		if c := control(t, r, id); c.Status != StatusNotEvaluated || !strings.Contains(c.Reason, "bp_test_enc") {
			t.Errorf("%s = %+v", id, c)
		}
	}
	if c := control(t, r, "3.2"); c.Status != StatusMet {
		t.Errorf("3.2 = %+v", c)
	}
	// No resource of the control's type: not applicable wins.
	if c := control(t, Evaluate(testFramework, &model.Workflow{}, nil, model.BestPracticeSettings{DisabledRules: []string{"bp_test_enc"}}, nil), "1.1"); c.Status != StatusNotApplicable {
		t.Errorf("1.1 on an empty design = %+v", c)
	}

	r = Evaluate(testFramework, wf, nil, model.BestPracticeSettings{Disabled: true}, nil)
	for _, c := range r.Controls {
		if c.Status == StatusMet || c.Status == StatusViolated {
			t.Errorf("all checks off: %s graded %s", c.ID, c.Status)
		}
	}
	if r.Graded != 0 {
		t.Errorf("graded = %d", r.Graded)
	}

	cfg := GradingSettings(model.BestPracticeSettings{DisabledRules: []string{"bp_test_db"}, EnabledRules: []string{"bp_x"}}, testFramework)
	for _, code := range []string{"bp_x", "bp_test_enc", "bp_test_public", "bp_test_wide"} {
		if !slices.Contains(cfg.EnabledRules, code) {
			t.Errorf("grading settings miss %s: %v", code, cfg.EnabledRules)
		}
	}
	if !slices.Equal(cfg.DisabledRules, []string{"bp_test_db"}) {
		t.Errorf("grading settings turned a rule back on: %v", cfg.DisabledRules)
	}
}

func TestEvaluateDesignWideFinding(t *testing.T) {
	r := Evaluate(testFramework, tfWorkflow(), []model.Diagnostic{diag("bp_test_wide", "")}, model.BestPracticeSettings{}, nil)
	if c := control(t, r, "3.2"); c.Status != StatusViolated || len(c.Findings) != 1 || c.Findings[0].Address != "" {
		t.Errorf("3.2 = %+v", c)
	}
	// An empty design has nothing to grade.
	if c := control(t, Evaluate(testFramework, &model.Workflow{}, nil, model.BestPracticeSettings{}, nil), "3.2"); c.Status != StatusNotApplicable {
		t.Errorf("3.2 on an empty design = %+v", c)
	}
}

func TestEvaluateAttested(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	att := Attestations{
		Key(testFramework.ID, "3.1"): {Note: "MFA on (hardware key)", At: at},
		Key(testFramework.ID, "1.1"): {Note: "automatic controls can't be attested", At: at},
		Key("other-1.0", "3.1"):      {Note: "other framework", At: at},
	}
	r := Evaluate(testFramework, tfWorkflow(), []model.Diagnostic{diag("bp_test_enc", "n-logs")}, model.BestPracticeSettings{}, att)
	if c := control(t, r, "3.1"); c.Status != StatusAttested || c.Attestation == nil || c.Attestation.Note != "MFA on (hardware key)" {
		t.Errorf("3.1 = %+v", c)
	}
	if c := control(t, r, "1.1"); c.Status != StatusViolated || c.Attestation != nil {
		t.Errorf("1.1 = %+v", c)
	}
	if r.Counts[StatusAttested] != 1 || r.Counts[StatusManual] != 0 {
		t.Errorf("counts = %v", r.Counts)
	}
}

func TestEvaluateDockerAndKubernetes(t *testing.T) {
	dk := Framework{ID: "test-docker", Platform: "docker", Controls: []Control{
		{ID: "5.1", Section: "Runtime", Rules: []string{"dk_test_root"}, AppliesTo: []string{model.ComposeService}},
		{ID: "5.2", Section: "Runtime", Rules: []string{"dk_test_net"}, AppliesTo: []string{model.ComposeNetwork}},
	}}
	wf := &model.Workflow{Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Graph: model.ComposeGraph{Nodes: []model.ComposeNode{
		{ID: "service:web", Kind: model.ComposeService, Name: "web"},
		{ID: "service:db", Kind: model.ComposeService, Name: "db"},
		{ID: "stage:web:build", Kind: model.ComposeBuildStage, Name: "build", Parent: "service:web"},
		{ID: "extends:base.yaml#svc", Kind: model.ComposeExternal, Name: "svc"},
	}}}}
	// A Dockerfile stage finding counts against its service.
	r := Evaluate(dk, wf, []model.Diagnostic{diag("dk_test_root", "stage:web:build")}, model.BestPracticeSettings{}, nil)
	if c := control(t, r, "5.1"); c.Status != StatusViolated || c.Findings[0].NodeID != "service:web" || c.Findings[0].Address != "web" {
		t.Errorf("5.1 = %+v", c)
	}
	if c := control(t, r, "5.2"); c.Status != StatusNotApplicable {
		t.Errorf("5.2 = %+v", c)
	}
	if got := Platforms(wf); !slices.Equal(got, []string{"docker"}) {
		t.Errorf("docker platforms = %v", got)
	}

	k8s := Framework{ID: "test-k8s", Platform: "kubernetes", Controls: []Control{
		{ID: "5.2.1", Section: "Pods", Rules: []string{"k8s_test_priv"}, AppliesTo: []string{"Deployment", "StatefulSet"}},
	}}
	kw := &model.Workflow{Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Graph: model.KubeGraph{Nodes: []model.KubeNode{
		{ID: "Deployment/shop/api", Kind: "Deployment", Name: "api", Namespace: "shop"},
		{ID: "Service/shop/api", Kind: "Service", Name: "api", Namespace: "shop"},
	}}}}
	r = Evaluate(k8s, kw, []model.Diagnostic{diag("k8s_test_priv", "Deployment/shop/api"), diag("k8s_test_priv", "Service/shop/api")}, model.BestPracticeSettings{}, nil)
	if c := control(t, r, "5.2.1"); c.Status != StatusViolated || len(c.Resources) != 1 || len(c.Findings) != 1 || c.Findings[0].Address != "Deployment/shop/api" {
		t.Errorf("5.2.1 = %+v", c)
	}
}

func TestForWorkflow(t *testing.T) {
	wf := &model.Workflow{Clouds: []model.Cloud{model.CloudAzure, model.CloudGCP}}
	if got := Platforms(wf); !slices.Equal(got, []string{"azurerm", "google"}) {
		t.Errorf("platforms = %v", got)
	}
	for _, f := range ForWorkflow(wf) {
		if f.Platform != "azurerm" && f.Platform != "google" {
			t.Errorf("%s offered to an Azure + GCP workflow", f.ID)
		}
	}
	if got := ForWorkflow(&model.Workflow{}); len(got) != 0 {
		t.Errorf("empty Terraform workflow offered %d frameworks", len(got))
	}
}

// AppliesToNode narrows a control to some resources of its type (a SQL Server
// flag control on SQL Server instances only).
func TestEvaluateAppliesToNode(t *testing.T) {
	engine := func(n *model.Node) bool { return n.Name != "pg" }
	f := Framework{ID: "test-gcp", Platform: "google", Controls: []Control{
		{ID: "6.3.1", Section: "SQL", Rules: []string{"bp_test_flag"}, AppliesTo: []string{"google_sql_database_instance"}, AppliesToNode: engine},
	}}
	pg := &model.Workflow{Nodes: []model.Node{node("n-pg", "google_sql_database_instance")}}
	c := control(t, Evaluate(f, pg, []model.Diagnostic{diag("bp_test_flag", "n-pg")}, model.BestPracticeSettings{}, nil), "6.3.1")
	if c.Status != StatusNotApplicable || len(c.Findings) != 0 || !strings.Contains(c.Reason, "kind this control is about") {
		t.Errorf("PostgreSQL only = %+v", c)
	}
	both := &model.Workflow{Nodes: []model.Node{node("n-pg", "google_sql_database_instance"), node("n-mssql", "google_sql_database_instance")}}
	// The finding on the instance the control isn't about doesn't count.
	c = control(t, Evaluate(f, both, []model.Diagnostic{diag("bp_test_flag", "n-pg")}, model.BestPracticeSettings{}, nil), "6.3.1")
	if c.Status != StatusMet || len(c.Resources) != 1 || c.Resources[0].NodeID != "n-mssql" {
		t.Errorf("SQL Server next to PostgreSQL = %+v", c)
	}
	c = control(t, Evaluate(f, both, []model.Diagnostic{diag("bp_test_flag", "n-mssql")}, model.BestPracticeSettings{}, nil), "6.3.1")
	if c.Status != StatusViolated {
		t.Errorf("SQL Server finding = %+v", c)
	}
}

// A rule flagging several settings decides several controls: Counts keeps
// each control to the findings on its own setting, so one setting never
// fails another setting's control.
func TestEvaluateCountsNarrowsASharedRule(t *testing.T) {
	f := Framework{ID: "test-counts", Controls: []Control{
		{ID: "acl", Rules: []string{"bp_test_public"}, AppliesTo: []string{"aws_s3_bucket"}, Counts: onAttribute("acl")},
		{ID: "policy", Rules: []string{"bp_test_public"}, AppliesTo: []string{"aws_s3_bucket"}, Counts: onAttribute("policy")},
		{ID: "any", Rules: []string{"bp_test_public"}, AppliesTo: []string{"aws_s3_bucket"}},
	}}
	acl := diag("bp_test_public", "n-logs")
	acl.Attribute = "acl"
	r := Evaluate(f, tfWorkflow(), []model.Diagnostic{acl}, model.BestPracticeSettings{}, nil)
	if c := control(t, r, "acl"); c.Status != StatusViolated || len(c.Findings) != 1 {
		t.Errorf("acl = %+v", c)
	}
	if c := control(t, r, "policy"); c.Status != StatusMet || len(c.Findings) != 0 {
		t.Errorf("policy = %+v", c)
	}
	if c := control(t, r, "any"); c.Status != StatusViolated {
		t.Errorf("without Counts every finding counts: %+v", c)
	}

	on := onAttribute("hostPID")
	for attr, want := range map[string]bool{"hostPID": true, "spec.template.spec.hostPID": true, "spec.hostPIDs": false, "spec.template.spec.hostNetwork": false, "": false} {
		if on(model.Diagnostic{Attribute: attr}) != want {
			t.Errorf("onAttribute(hostPID)(%q) = %v", attr, !want)
		}
	}
}
