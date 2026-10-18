package api

import (
	"net/http"
	"os"
	"path/filepath"
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

	// The region applies to provider blocks AddNode creates from now on.
	wf := e.createEmpty("regions")
	wf, _ = e.addNode(wf.ID, "aws_sqs_queue")
	if len(wf.Providers) != 1 || wf.Providers[0].Body.Attrs[0].Value.Expr != `"eu-central-1"` {
		t.Errorf("providers = %+v", wf.Providers)
	}
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
