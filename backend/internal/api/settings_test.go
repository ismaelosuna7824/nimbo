package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSettingsEndpoints(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(func() { testCatalog.SetDefaultAWSRegion("") })

	var got model.Settings
	e.mustCall(http.StatusOK, "GET", "/settings", nil, &got)
	if got.DefaultMode != "guided" || got.Images[model.CloudAWS] != "floci/floci:latest" {
		t.Fatalf("defaults = %+v", got)
	}

	e.mustCall(http.StatusBadRequest, "PUT", "/settings", model.Settings{DefaultAWSRegion: "mars-1"}, nil)

	in := got
	in.DefaultMode = "pro"
	in.DefaultAWSRegion = "eu-central-1"
	in.Images = map[model.Cloud]string{model.CloudAWS: "floci/floci:2.3.0"}
	e.mustCall(http.StatusOK, "PUT", "/settings", in, &got)
	if got.Images[model.CloudAWS] != "floci/floci:2.3.0" || got.Images[model.CloudAzure] != "floci/floci-az:latest" {
		t.Fatalf("saved = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.home, "settings.json")); err != nil {
		t.Fatal(err)
	}

	// The region applies to the region variable AddNode creates from now on.
	wf := e.createEmpty("regions")
	wf, _ = e.addNode(wf.ID, "aws_sqs_queue")
	if len(wf.Providers) != 1 || wf.Providers[0].Body.Attrs[0].Value.Expr != "var.aws_region" {
		t.Errorf("providers = %+v", wf.Providers)
	}
	for _, v := range wf.Variables {
		if v.Name == "aws_region" && v.Body.Attrs[1].Value.Expr != `"eu-central-1"` {
			t.Errorf("aws_region = %+v", v.Body)
		}
	}
}

func bpCodes(t *testing.T, wf *model.Workflow) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, d := range wf.Diagnostics {
		if strings.HasPrefix(d.Code, "bp_") {
			out[d.Code]++
			if d.Severity == model.SeverityError {
				t.Errorf("error-severity best-practice finding %+v", d)
			}
		}
	}
	return out
}

func TestBestPracticeSettings(t *testing.T) {
	e := newEnv(t)
	var rules []model.BestPracticeRule
	e.mustCall(http.StatusOK, "GET", "/best-practices/rules", nil, &rules)
	if len(rules) < 15 || rules[0].Code != "bp_variable_type" || rules[0].Title == "" {
		t.Fatalf("rules = %+v", rules)
	}

	// A queue with a fixed name and an undocumented variable.
	created := e.createEmpty("bp")
	wf := &created
	wf.Locals = []model.Attr{{Name: "name_prefix", Value: model.Value{Expr: `"demo"`}}}
	wf.Variables = []model.NamedBlock{{Name: "size", Body: model.Body{Attrs: []model.Attr{{Name: "type", Value: model.Value{Expr: "number"}}}}}}
	wf.Nodes = []model.Node{{Kind: model.KindResource, Type: "aws_sqs_queue", Name: "q", Body: model.Body{Attrs: []model.Attr{{Name: "name", Value: model.Value{Expr: `"jobs"`}}}}}}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, wf)
	codes := bpCodes(t, wf)
	if codes["bp_variable_description"] != 1 || codes["bp_name_prefix"] != 1 {
		t.Fatalf("findings = %v (%+v)", codes, wf.Diagnostics)
	}

	var st model.Settings
	e.mustCall(http.StatusOK, "GET", "/settings", nil, &st)
	if st.BestPractices.Disabled || st.BestPractices.DisabledRules == nil {
		t.Fatalf("default best practices = %+v", st.BestPractices)
	}
	st.BestPractices.DisabledRules = []string{"bp_name_prefix", "bp_name_prefix"}
	e.mustCall(http.StatusOK, "PUT", "/settings", st, &st)
	if len(st.BestPractices.DisabledRules) != 1 {
		t.Errorf("disabled rules not deduplicated: %v", st.BestPractices.DisabledRules)
	}
	// GET reflects the toggle without an edit.
	wf = &model.Workflow{}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+created.ID, nil, wf)
	if codes := bpCodes(t, wf); codes["bp_name_prefix"] != 0 || codes["bp_variable_description"] != 1 {
		t.Errorf("after disabling bp_name_prefix: %v", codes)
	}
	st.BestPractices.Disabled = true
	e.mustCall(http.StatusOK, "PUT", "/settings", st, &st)
	wf = &model.Workflow{}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+created.ID, nil, wf)
	if codes := bpCodes(t, wf); len(codes) != 0 {
		t.Errorf("checks off but findings %v", codes)
	}
	st.BestPractices.DisabledRules = []string{"not a rule"}
	e.mustCall(http.StatusBadRequest, "PUT", "/settings", st, nil)
}

func TestAgentsEndpoints(t *testing.T) {
	e := newEnv(t)
	var list []model.AgentClient
	e.mustCall(http.StatusOK, "GET", "/agents", nil, &list)
	if len(list) != 7 || list[0].ID != "claude-code" {
		t.Fatalf("agents = %+v", list)
	}
	var a model.AgentClient
	e.mustCall(http.StatusOK, "POST", "/agents/cursor/install", nil, &a)
	if !a.Configured || a.ConfigPath != filepath.Join(e.home, "user", ".cursor", "mcp.json") {
		t.Fatalf("installed = %+v", a)
	}
	e.mustCall(http.StatusOK, "DELETE", "/agents/cursor", nil, &a)
	if a.Configured {
		t.Error("still configured")
	}
	e.mustCall(http.StatusNotFound, "POST", "/agents/emacs/install", nil, nil)

	var cmd mcpCommand
	e.mustCall(http.StatusOK, "GET", "/mcp/command", nil, &cmd)
	if cmd.Command != "/opt/nimbo/bin/nimbo" || len(cmd.Args) != 1 || cmd.Problem != "" {
		t.Errorf("command = %+v", cmd)
	}
	var cat struct {
		Tools   []struct{ Name, Group string } `json:"tools"`
		Prompts []struct{ Name string }        `json:"prompts"`
	}
	e.mustCall(http.StatusOK, "GET", "/mcp/catalog", nil, &cat)
	if len(cat.Tools) == 0 || cat.Tools[0].Name == "" || cat.Tools[0].Group == "" {
		t.Errorf("catalog = %+v", cat)
	}
}
