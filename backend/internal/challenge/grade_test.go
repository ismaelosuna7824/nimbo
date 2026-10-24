package challenge

import (
	"encoding/json"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestGradeRunMetricsAndBoundaries(t *testing.T) {
	k := model.ChallengeCheck{ID: "fast", Label: "Fast", Type: model.CheckLoad, Load: &model.ChallengeLoad{Expect: []model.ChallengeMetric{
		{Metric: model.MetricP95, Op: "lt", Value: 100},
		{Metric: model.MetricErrorRate, Op: "lte", Value: 0.01},
	}}}
	run := func(p95, errRate float64) *model.LoadTestRun {
		return &model.LoadTestRun{ID: "r1", Status: model.LoadPassed, Summary: &model.LoadSummary{Requests: 100, ErrorRate: errRate, Latency: model.LatencyStats{P95: p95}}}
	}
	if r := GradeRun(k, run(99.9, 0.01)); !r.Passed || r.Status != model.ChallengePassed || r.RunID != "r1" {
		t.Errorf("just under the bounds: %+v", r)
	}
	// lt is strict, lte is not.
	if r := GradeRun(k, run(100, 0.01)); r.Passed || r.Checks[0].Passed || !r.Checks[1].Passed {
		t.Errorf("p95 at the bound: %+v", r)
	}
	if r := GradeRun(k, run(50, 0.0101)); r.Passed || r.Checks[1].Actual != "1%" {
		t.Errorf("errors over the bound: %+v", r.Checks)
	}
}

func TestGradeRunMissingRuns(t *testing.T) {
	k := model.ChallengeCheck{ID: "x", Type: model.CheckLoad, Load: &model.ChallengeLoad{Expect: []model.ChallengeMetric{{Metric: model.MetricP95, Op: "lt", Value: 1}}}}
	for name, run := range map[string]*model.LoadTestRun{
		"nil":     nil,
		"errored": {ID: "e", Status: model.LoadErrored, Error: "connection refused"},
		"stopped": {ID: "s", Status: model.LoadStopped},
		"empty":   {ID: "z", Status: model.LoadPassed, Summary: &model.LoadSummary{}},
	} {
		r := GradeRun(k, run)
		if r.Passed || len(r.Checks) != 1 || r.Checks[0].Passed {
			t.Errorf("%s: %+v", name, r)
		}
		if name != "empty" && r.Status != model.ChallengeError {
			t.Errorf("%s: status %s", name, r.Status)
		}
	}
}

func TestGradeRunScenarioVerdictOrder(t *testing.T) {
	k := model.ChallengeCheck{ID: "s", Type: model.CheckScenario, Scenario: &model.ChallengeScenario{Expect: model.ChallengeScenarioExpect{Verdict: model.ScenarioDegraded,
		Metrics: []model.ChallengeMetric{{Metric: model.MetricReplicasMax, Op: "gte", Value: 2, Workload: "api"}}}}}
	run := func(verdict string, replicas int) *model.LoadTestRun {
		return &model.LoadTestRun{Status: model.LoadPassed, Summary: &model.LoadSummary{Requests: 1}, Scenario: &model.ScenarioResult{Verdict: verdict},
			Resources: &model.LoadResources{Scaling: &model.LoadScaling{Workloads: []model.WorkloadScaling{{Workload: "api", PeakReplicas: replicas}}}}}
	}
	for verdict, want := range map[string]bool{model.ScenarioSurvived: true, model.ScenarioDegraded: true, model.ScenarioFailed: false} {
		if r := GradeRun(k, run(verdict, 3)); r.Passed != want {
			t.Errorf("%s: passed %v", verdict, r.Passed)
		}
	}
	if r := GradeRun(k, run(model.ScenarioSurvived, 1)); r.Passed || r.Checks[1].Actual != "1" {
		t.Errorf("one replica: %+v", r.Checks)
	}
}

func TestScorePartialPass(t *testing.T) {
	goals := []model.ChallengeGoalResult{
		{Checks: []model.ChallengeCheckResult{{Passed: true}, {Passed: false}}},
		{Checks: []model.ChallengeCheckResult{{Passed: true}}},
		{Checks: []model.ChallengeCheckResult{{Passed: false}}},
		{Checks: []model.ChallengeCheckResult{}}, // not run
	}
	if got := Score(goals); got != 38 { // (0.5 + 1 + 0 + 0) / 4
		t.Errorf("score %d, want 38", got)
	}
	if Score(nil) != 0 {
		t.Error("no goals")
	}
}

func TestWhereMatchers(t *testing.T) {
	var obj any
	_ = json.Unmarshal([]byte(`{"spec":{"replicas":2,"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.27","readinessProbe":{"httpGet":{"path":"/health"}}},{"name":"side","image":"busybox"}]}}},
		"metadata":{"labels":{"pod-security.kubernetes.io/enforce":"restricted"}},"statements":["Allow group a to read buckets in compartment app-dev"]}`), &obj)
	cases := []struct {
		where model.Where
		want  bool
	}{
		{model.Where{"spec.replicas": map[string]any{"min": 2.0}}, true},
		{model.Where{"spec.replicas": map[string]any{"min": 3.0}}, false},
		{model.Where{"spec.replicas": "2"}, true},
		{model.Where{"spec.template.spec.containers.web.readinessProbe": map[string]any{"exists": true}}, true},
		{model.Where{"spec.template.spec.containers.side.readinessProbe": map[string]any{"exists": true}}, false},
		{model.Where{"spec.template.spec.containers[*].image": map[string]any{"in": []any{"busybox"}}}, true},
		{model.Where{"spec.template.spec.containers.readinessProbe.httpGet.path": "/health"}, true},
		{model.Where{"spec.template.spec.containers[1].readinessProbe": map[string]any{"exists": false}}, true},
		{model.Where{`metadata.labels."pod-security.kubernetes.io/enforce"`: "restricted"}, true},
		{model.Where{"statements": map[string]any{"contains": "compartment app-dev"}}, true},
		{model.Where{"spec.missing": map[string]any{"exists": false}}, true},
	}
	for i, c := range cases {
		if got, _ := matchWhere(obj, c.where); got != c.want {
			t.Errorf("case %d %v: got %v", i, c.where, got)
		}
	}
}

func TestWhereTerraformBodies(t *testing.T) {
	body := model.Body{
		Attrs: []model.Attr{
			{Name: "multi_az", Value: model.Value{Literal: json.RawMessage(`true`)}},
			{Name: "kms_key_id", Value: model.Value{Expr: "aws_kms_key.main.arn"}},
			{Name: "tags", Value: model.Value{Literal: json.RawMessage(`{"Environment":"prod"}`)}},
		},
		Blocks: []model.Block{
			{Type: "ingress", Body: model.Body{Attrs: []model.Attr{{Name: "from_port", Value: model.Value{Literal: json.RawMessage(`22`)}}}}},
			{Type: "ingress", Body: model.Body{Attrs: []model.Attr{{Name: "from_port", Value: model.Value{Literal: json.RawMessage(`443`)}}}}},
		},
	}
	v := bodyValue(body)
	cases := []struct {
		where model.Where
		want  bool
	}{
		{model.Where{"multi_az": true}, true},
		{model.Where{"multi_az": "true"}, true},
		{model.Where{"kms_key_id": map[string]any{"exists": true}}, true},
		{model.Where{"kms_key_id": map[string]any{"ref": "aws_kms_key"}}, true},
		{model.Where{"kms_key_id": map[string]any{"ref": "aws_kms"}}, false},
		{model.Where{"tags.Environment": "prod"}, true},
		{model.Where{"ingress.from_port": 443.0}, true},
		{model.Where{"ingress.from_port": map[string]any{"notIn": []any{22.0}}}, true},
		{model.Where{"ingress.from_port": map[string]any{"max": 21.0}}, false},
	}
	for i, c := range cases {
		if got, _ := matchWhere(v, c.where); got != c.want {
			t.Errorf("case %d %v: got %v", i, c.where, got)
		}
	}
}

func TestWhereTerraformMapExpressions(t *testing.T) {
	// A map (or list) with any non-literal value is kept as one expression;
	// its keys and elements are still matched one by one.
	body := model.Body{Attrs: []model.Attr{
		{Name: "app_settings", Value: model.Value{Expr: `{
  DB_HOST     = var.db_host
  DB_PASSWORD = "@Microsoft.KeyVault(SecretUri=${azurerm_key_vault.main.vault_uri}secrets/db-password/)"
  "WORKERS"   = 4
  DEBUG       = false
  nested      = { level = "info", sink = local.sink }
}`}},
		{Name: "subnet_ids", Value: model.Value{Expr: `[aws_subnet.a.id, "subnet-123"]`}},
		{Name: "name", Value: model.Value{Expr: `"${var.project}-api"`}},
	}}
	v := bodyValue(body)
	cases := []struct {
		where model.Where
		want  bool
	}{
		{model.Where{"app_settings.DB_PASSWORD": map[string]any{"contains": "@Microsoft.KeyVault("}}, true},
		{model.Where{"app_settings.DB_HOST": map[string]any{"contains": "@Microsoft.KeyVault("}}, false},
		{model.Where{"app_settings.DB_PASSWORD": map[string]any{"ref": "azurerm_key_vault"}}, true},
		{model.Where{"app_settings.DB_HOST": map[string]any{"ref": "var.db_host"}}, true},
		{model.Where{"app_settings.DB_HOST": "var.db_host"}, true},
		{model.Where{"app_settings.WORKERS": map[string]any{"min": 4.0}}, true},
		{model.Where{"app_settings.DEBUG": false}, true},
		{model.Where{"app_settings.nested.level": "info"}, true},
		{model.Where{"app_settings.nested.sink": map[string]any{"ref": "local.sink"}}, true},
		{model.Where{"app_settings.MISSING": map[string]any{"exists": false}}, true},
		{model.Where{"app_settings": map[string]any{"contains": "@Microsoft.KeyVault("}}, true},
		{model.Where{"subnet_ids[1]": "subnet-123"}, true},
		{model.Where{"subnet_ids[0]": map[string]any{"ref": "aws_subnet.a"}}, true},
		{model.Where{"name.x": map[string]any{"exists": true}}, false},
	}
	for i, c := range cases {
		if got, _ := matchWhere(v, c.where); got != c.want {
			t.Errorf("case %d %v: got %v", i, c.where, got)
		}
	}
}

func TestValidCheckRejectsRuntimeOnDesign(t *testing.T) {
	k := model.ChallengeCheck{ID: "d", Label: "Deploy", Type: model.CheckDeploy}
	if err := validCheck(model.LearnRuntimeDesign, "", k); err == nil {
		t.Error("deploy on a design challenge must be rejected")
	}
	if err := validCheck(model.LearnRuntimeLocal, model.WorkflowDocker, k); err != nil {
		t.Error(err)
	}
}
