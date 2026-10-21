package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ismaelosuna7824/nimbo/backend/internal/api"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// session runs the MCP server against a real daemon API (no Docker routes
// are exercised) over in-memory transports.
func session(t *testing.T) (*sdk.ClientSession, *events.Hub) {
	t.Helper()
	cs, hub, _ := sessionURL(t)
	return cs, hub
}

// sessionURL is session plus the daemon's base URL for direct API calls.
func sessionURL(t *testing.T) (*sdk.ClientSession, *events.Hub, string) {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	sb, err := sandbox.New(sandbox.Config{Home: home, Events: hub})
	if err != nil {
		t.Fatal(err)
	}
	conns, err := store.OpenConnections(home)
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := settings.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Config{
		Version: "test", Home: home, Store: st, Connections: conns, Catalog: cat, Sandbox: sb, Events: hub, Settings: prefs,
		LoadTest: loadtest.NewEngine(loadtest.EngineConfig{Home: home, Events: hub, Resolver: sb, Connections: conns}),
	}).Handler())
	t.Cleanup(srv.Close)

	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := NewServer(srv.URL, "test").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, hub, srv.URL
}

func callTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func mustTool(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	text, isErr := callTool(t, cs, name, args)
	if isErr {
		t.Fatalf("%s returned an error: %s", name, text)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s: decode %q: %v", name, text, err)
		}
	}
	return text
}

func TestAgentBuildsWorkflowThroughTools(t *testing.T) {
	cs, hub := session(t)
	updates, cancel := hub.Subscribe("")
	defer cancel()

	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Agent Built"}, &wf)

	var added nodeResult
	mustTool(t, cs, "add_node", map[string]any{
		"workflow":   "Agent Built", // by name
		"type":       "aws_s3_bucket",
		"name":       "uploads",
		"attributes": map[string]string{"bucket": `"agent-uploads"`, "tags": `{ team = "infra" }`},
	}, &added)
	if added.Node.Address != "aws_s3_bucket.uploads" || added.Node.Attributes["bucket"] != `"agent-uploads"` {
		t.Fatalf("add_node result %+v", added.Node)
	}

	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "jobs"}, nil)
	var updated nodeResult
	mustTool(t, cs, "update_node", map[string]any{
		"workflow": wf.ID, "node": "aws_sqs_queue.jobs",
		"set":    map[string]string{"name": "aws_s3_bucket.uploads.id"},
		"rename": "work",
	}, &updated)
	if updated.Node.Address != "aws_sqs_queue.work" {
		t.Fatalf("rename failed: %+v", updated.Node)
	}

	var got workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &got)
	// The bucket starter adds its encryption configuration and public access block.
	if len(got.Nodes) != 4 || !slices.Contains(got.Edges, "aws_s3_bucket.uploads.id -> aws_sqs_queue.work.name") {
		t.Fatalf("get_workflow nodes=%d edges=%v", len(got.Nodes), got.Edges)
	}

	tf := mustTool(t, cs, "get_terraform", map[string]any{"workflow": wf.ID}, nil)
	if !strings.Contains(tf, "# ===== main.tf =====") || !strings.Contains(tf, `resource "aws_s3_bucket" "uploads"`) || !strings.Contains(tf, "aws_s3_bucket.uploads.id") {
		t.Fatalf("get_terraform:\n%s", tf)
	}

	// Broken expressions surface as diagnostics the agent can act on.
	var broken nodeResult
	mustTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.work", "set": map[string]string{"delay_seconds": "1 +"}}, &broken)
	var sawErr bool
	for _, d := range broken.Diagnostics {
		if d.Severity == model.SeverityError && d.Node == "aws_sqs_queue.work" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("no error diagnostic for a broken expression: %+v", broken.Diagnostics)
	}

	if text, isErr := callTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.nope", "set": map[string]string{"a": "1"}}); !isErr || !strings.Contains(text, "aws_sqs_queue.work") {
		t.Fatalf("unknown node should list existing nodes: %v %s", isErr, text)
	}

	select {
	case m := <-updates:
		if m.Type != model.EventWorkflowUpdated {
			t.Fatalf("event %s", m.Type)
		}
	default:
		t.Fatal("agent edits were not published to the UI event stream")
	}
}

func TestApplyTerraformCodeMergesFiles(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Code"}, &wf)
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "q"}, nil)
	mustTool(t, cs, "set_variable", map[string]any{"workflow": wf.ID, "name": "env", "type": "string", "default": `"dev"`}, nil)

	var out workflowView
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{
		"extra.tf": "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b-${var.env}\"\n}\n",
	}}, &out)
	// project, environment and aws_region come with the first AWS resource.
	if len(out.Nodes) != 2 || len(out.Variables) != 4 {
		t.Fatalf("merge lost content: nodes=%d variables=%d", len(out.Nodes), len(out.Variables))
	}
}

func TestUpdateWorkflowNameAndDescription(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "description": "First draft."}, &wf)
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "q"}, nil)

	var out workflowView
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "description": "  Takes orders and queues them.  "}, &out)
	if out.ID != wf.ID || out.Name != "Shop" || out.Description != "Takes orders and queues them." || len(out.Nodes) != 1 {
		t.Fatalf("description update: %+v", out)
	}
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": "Shop", "name": "Online shop"}, &out)
	if out.Name != "Online shop" || out.Description != "Takes orders and queues them." {
		t.Fatalf("rename: %+v", out)
	}
	var got workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &got)
	if got.Name != "Online shop" || got.Description != "Takes orders and queues them." || len(got.Nodes) != 1 {
		t.Fatalf("not saved: %+v", got)
	}
	var cleared workflowView
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "description": ""}, &cleared)
	if cleared.Description != "" || cleared.Name != "Online shop" {
		t.Fatalf("clear description: %+v", cleared)
	}
	for _, args := range []map[string]any{{"workflow": wf.ID}, {"workflow": wf.ID, "name": "  "}} {
		if text, isErr := callTool(t, cs, "update_workflow", args); !isErr {
			t.Errorf("update_workflow %v accepted: %s", args, text)
		}
	}
}

func TestWorkflowLayoutAndStructure(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Layered"}, &wf)
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "q"}, nil)
	for _, env := range []string{"dev", "prod"} {
		mustTool(t, cs, "set_environment", map[string]any{"workflow": wf.ID, "name": env}, nil)
	}

	var got workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &got)
	s := got.Structure
	if s == nil || s.Layout != model.LayoutEnvironments || s.LayoutSetting != "auto" || s.States != 2 || s.ResourcesPerState < 1 || s.Service != "layered" || !strings.Contains(s.State, "one state per environment") {
		t.Fatalf("auto layout with two environments: %+v", s)
	}

	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "layout": "single"}, &got)
	if s := got.Structure; s.Layout != model.LayoutSingle || s.LayoutSetting != model.LayoutSingle || s.States != 1 {
		t.Fatalf("single layout: %+v", s)
	}
	var review struct {
		Structure *structureView `json:"structure"`
		Info      []findingView  `json:"info"`
	}
	mustTool(t, cs, "review_terraform", map[string]any{"workflow": wf.ID}, &review)
	if review.Structure == nil || review.Structure.Layout != model.LayoutSingle || !slices.ContainsFunc(review.Info, func(f findingView) bool { return f.Code == "bp_no_remote_backend" }) {
		t.Fatalf("review in the single layout: %+v", review)
	}

	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "layout": "auto"}, &got)
	if s := got.Structure; s.LayoutSetting != "auto" || s.Layout != model.LayoutEnvironments {
		t.Fatalf("back to auto: %+v", s)
	}
	if text, isErr := callTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "layout": "monorepo"}); !isErr {
		t.Errorf("invalid layout accepted: %s", text)
	}
}

func TestUpdateNodeMovesFile(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Files"}, &wf)
	var added nodeResult
	mustTool(t, cs, "add_node", map[string]any{"workflow": wf.ID, "type": "aws_sqs_queue", "name": "q"}, &added)
	if added.Node.File != "main.tf" {
		t.Fatalf("new node file = %q, want main.tf", added.Node.File)
	}
	var moved nodeResult
	mustTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.q", "file": "queues.tf"}, &moved)
	if moved.Node.File != "queues.tf" {
		t.Fatalf("moved node file = %q", moved.Node.File)
	}
	if text, isErr := callTool(t, cs, "get_terraform", map[string]any{"workflow": wf.ID}); isErr || !strings.Contains(text, "queues.tf") {
		t.Fatalf("get_terraform after move: %s", text)
	}
	mustTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.q", "file": "auto"}, &moved)
	if moved.Node.File != "main.tf" {
		t.Fatalf("node back to the layout in %q", moved.Node.File)
	}
	if _, isErr := callTool(t, cs, "update_node", map[string]any{"workflow": wf.ID, "node": "aws_sqs_queue.q", "file": "../x.tf"}); !isErr {
		t.Fatal("invalid file name accepted")
	}
}
