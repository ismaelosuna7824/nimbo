package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// setOpen reports id (or nothing when "") as open in the desktop app.
func setOpen(t *testing.T, baseURL, id string) {
	t.Helper()
	body := []byte(`{"openWorkflowId":null}`)
	if id != "" {
		body, _ = json.Marshal(map[string]string{"openWorkflowId": id})
	}
	req, _ := http.NewRequest(http.MethodPut, baseURL+"/api/v1/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /session: %s", res.Status)
	}
}

func TestCurrentWorkflowResolution(t *testing.T) {
	cs, _, base := sessionURL(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Open One"}, &wf)

	// Nothing open: every spelling of "the open workflow" fails clearly.
	for _, args := range []map[string]any{{}, {"workflow": "current"}, {"workflow": "open"}} {
		text, isErr := callTool(t, cs, "get_workflow", args)
		if !isErr || !strings.Contains(text, "no workflow is open") {
			t.Fatalf("get_workflow %v without open workflow: isErr=%v %s", args, isErr, text)
		}
	}
	if text := mustTool(t, cs, "get_open_workflow", nil, nil); !strings.Contains(text, "No workflow is open") {
		t.Fatalf("get_open_workflow without open workflow: %s", text)
	}

	setOpen(t, base, wf.ID)
	var open workflowView
	mustTool(t, cs, "get_open_workflow", nil, &open)
	if open.ID != wf.ID {
		t.Fatalf("get_open_workflow = %q, want %q", open.ID, wf.ID)
	}
	// Omitted, "current" and "open" all edit the open workflow.
	for i, args := range []map[string]any{
		{"type": "aws_sqs_queue", "name": "a"},
		{"workflow": "current", "type": "aws_sqs_queue", "name": "b"},
		{"workflow": "Open", "type": "aws_sqs_queue", "name": "c"},
	} {
		var res nodeResult
		mustTool(t, cs, "add_node", args, &res)
		if res.Node.Address != "aws_sqs_queue."+string(rune('a'+i)) {
			t.Fatalf("add_node %v: %+v", args, res.Node)
		}
	}
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": "current"}, &open)
	if len(open.Nodes) != 3 {
		t.Fatalf("open workflow has %d nodes, want 3", len(open.Nodes))
	}
	var list []workflowItem
	mustTool(t, cs, "list_workflows", nil, &list)
	if len(list) != 1 || !list[0].Open {
		t.Fatalf("list_workflows does not flag the open workflow: %+v", list)
	}

	// Deleting the open workflow closes it.
	if _, err := http.DefaultClient.Do(mustRequest(t, http.MethodDelete, base+"/api/v1/workflows/"+wf.ID)); err != nil {
		t.Fatal(err)
	}
	if text, isErr := callTool(t, cs, "get_workflow", map[string]any{"workflow": "current"}); !isErr || !strings.Contains(text, "no workflow is open") {
		t.Fatalf("deleted open workflow still resolves: %s", text)
	}
	setOpen(t, base, "")
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestArchivedWorkflowsAreHidden(t *testing.T) {
	cs, _ := session(t)
	var keep, old workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "keep"}, &keep)
	mustTool(t, cs, "create_workflow", map[string]any{"name": "old"}, &old)

	var res struct{ Archived bool }
	mustTool(t, cs, "archive_workflow", map[string]any{"workflow": "old"}, &res)
	if !res.Archived {
		t.Fatal("archive_workflow did not archive")
	}
	names := func(args map[string]any) []string {
		var list []workflowItem
		mustTool(t, cs, "list_workflows", args, &list)
		var out []string
		for _, w := range list {
			out = append(out, w.Name)
		}
		return out
	}
	if got := names(nil); len(got) != 1 || got[0] != "keep" {
		t.Fatalf("list_workflows shows archived: %v", got)
	}
	if got := names(map[string]any{"include_archived": true}); len(got) != 2 {
		t.Fatalf("include_archived: %v", got)
	}
	mustTool(t, cs, "unarchive_workflow", map[string]any{"workflow": old.ID}, &res)
	if res.Archived || len(names(nil)) != 2 {
		t.Fatalf("unarchive_workflow: %+v", res)
	}
}

func TestToolCatalogMatchesServer(t *testing.T) {
	cs, _ := session(t)
	ctx := context.Background()
	var listed []*sdk.Tool
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed = append(listed, tool)
	}
	cat := ToolCatalog()
	if len(listed) != len(cat) {
		t.Fatalf("server lists %d tools, catalog %d", len(listed), len(cat))
	}
	byName := map[string]ToolInfo{}
	for _, ti := range cat {
		if ti.Title == "" || ti.Description == "" || ti.Group == "" {
			t.Errorf("incomplete catalog entry %+v", ti)
		}
		if _, dup := byName[ti.Name]; dup {
			t.Errorf("duplicate tool %s", ti.Name)
		}
		byName[ti.Name] = ti
	}
	for _, tool := range listed {
		ti, ok := byName[tool.Name]
		if !ok || ti.Title != tool.Title || ti.Description != tool.Description {
			t.Errorf("tool %s differs from the catalog: %+v", tool.Name, ti)
		}
	}
}

func TestPrompts(t *testing.T) {
	cs, _ := session(t)
	ctx := context.Background()
	res, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cat := PromptCatalog()
	if len(res.Prompts) != len(cat) || len(cat) != 5 {
		t.Fatalf("server lists %d prompts, catalog %d", len(res.Prompts), len(cat))
	}
	for i, p := range res.Prompts {
		var args []string
		for _, a := range p.Arguments {
			args = append(args, a.Name)
		}
		if p.Name != cat[i].Name || p.Title != cat[i].Title || strings.Join(args, ",") != strings.Join(cat[i].Arguments, ",") {
			t.Errorf("prompt %d: server %s%v, catalog %+v", i, p.Name, args, cat[i])
		}
	}

	text := func(name string, args map[string]string) string {
		t.Helper()
		got, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return got.Messages[0].Content.(*sdk.TextContent).Text
	}
	if got := text("extend_workflow", map[string]string{"request": "add an SQS queue"}); !strings.Contains(got, "add an SQS queue") || !strings.Contains(got, `"current"`) {
		t.Fatalf("extend_workflow defaults to current: %s", got)
	}
	if got := text("load_test", map[string]string{"workflow": "shop", "goal": "p95 < 200ms"}); !strings.Contains(got, `"shop"`) || !strings.Contains(got, "p95 < 200ms") {
		t.Fatalf("load_test: %s", got)
	}
	if got := text("create_infrastructure", map[string]string{"request": "an HTTP API", "cloud": "gcp"}); !strings.Contains(got, "Cloud: gcp") {
		t.Fatalf("create_infrastructure: %s", got)
	}
	if got := text("test_deployed_service", nil); !strings.Contains(got, "confirm") {
		t.Fatalf("test_deployed_service lacks confirmation: %s", got)
	}
	if _, err := cs.GetPrompt(ctx, &sdk.GetPromptParams{Name: "import_terraform_folder"}); err == nil || !strings.Contains(err.Error(), `"dir"`) {
		t.Fatalf("missing required argument: %v", err)
	}
}
