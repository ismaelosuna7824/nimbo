package mcp

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWorkflowModeThroughTools(t *testing.T) {
	cs, _ := session(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Modes"}, &wf)
	if wf.Mode != "architecture" {
		t.Fatalf("default mode = %q", wf.Mode)
	}
	var full workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Full", "mode": "full"}, &full)
	if full.Mode != "full" {
		t.Fatalf("create mode = %q", full.Mode)
	}
	if text, isErr := callTool(t, cs, "create_workflow", map[string]any{"name": "Bad", "mode": "code"}); !isErr {
		t.Fatalf("invalid mode accepted: %s", text)
	}

	var out workflowView
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "mode": "full"}, &out)
	if out.Mode != "full" {
		t.Fatalf("update mode = %q", out.Mode)
	}
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "description": "kept"}, &out)
	if out.Mode != "full" {
		t.Fatalf("mode lost on another update: %q", out.Mode)
	}
	var got workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &got)
	if got.Mode != "full" {
		t.Fatalf("get_workflow mode = %q", got.Mode)
	}
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "mode": "architecture"}, &out)
	if out.Mode != "architecture" {
		t.Fatalf("back to architecture = %q", out.Mode)
	}
	if text, isErr := callTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "mode": "code"}); !isErr {
		t.Fatalf("invalid mode accepted: %s", text)
	}

	var dk dockerView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Stack", "kind": "docker", "mode": "full"}, &dk)
	if dk.Mode != "full" {
		t.Fatalf("docker create mode = %q", dk.Mode)
	}
	mustTool(t, cs, "update_workflow", map[string]any{"workflow": dk.ID, "mode": "architecture"}, &dk)
	if dk.Mode != "architecture" {
		t.Fatalf("docker update mode = %q", dk.Mode)
	}
}

// Application code and specs written with apply_terraform_code on a
// Terraform workflow are stored as asset files next to the Terraform.
func TestApplyTerraformCodeStoresAppFilesAsAssets(t *testing.T) {
	cs, _, base := sessionURL(t)
	var wf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Orders", "mode": "full"}, &wf)
	tf := `data "archive_file" "orders" {
  type        = "zip"
  source_dir  = "${path.module}/services/orders/src"
  output_path = "${path.module}/build/orders.zip"
}
`
	handler := "export const handler = async () => ({ statusCode: 200, body: \"ok\" });\n"
	project := "# Project\n"
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{
		"main.tf":                       tf,
		"services/orders/src/index.mjs": handler,
		"openspec/project.md":           project,
	}}, nil)

	files := func() map[string]string {
		t.Helper()
		res, err := http.Get(base + "/api/v1/workflows/" + wf.ID + "/files")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var list struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range list.Files {
			out[f.Path] = ""
		}
		return out
	}
	got := files()
	if _, ok := got["services/orders/src/index.mjs"]; !ok {
		t.Fatalf("handler not stored: %v", keys(got))
	}
	if _, ok := got["openspec/project.md"]; !ok {
		t.Fatalf("spec not stored: %v", keys(got))
	}
	var code workflowView
	mustTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}, &code)
	if len(code.Nodes) != 1 {
		t.Fatalf("terraform not imported: %+v", code.Nodes)
	}
	// Writing code re-imports the Terraform; the build mode is the workflow's, not the code's.
	if code.Mode != "full" {
		t.Fatalf("mode after apply_terraform_code = %q, want full", code.Mode)
	}

	var spec []fileView
	mustTool(t, cs, "get_workflow_files", map[string]any{"workflow": wf.ID, "prefix": "openspec/"}, &spec)
	var content string
	for _, f := range spec {
		if f.Path == "openspec/project.md" {
			content = f.Content
		}
	}
	if content != "# Project\n" {
		t.Fatalf("get_workflow_files openspec/ = %+v", spec)
	}

	// Deleting an asset with "" works the same way.
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{"openspec/project.md": ""}}, nil)
	if _, ok := files()["openspec/project.md"]; ok {
		t.Fatal("asset not deleted")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
