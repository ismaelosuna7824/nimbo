package mcp

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestConvertToKubernetesTool(t *testing.T) {
	cs, _ := session(t)
	var wf dockerView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "template": "docker-wordpress-mysql"}, &wf)

	var preview struct {
		Report model.KubeConvertReport `json:"report"`
		Files  map[string]string       `json:"files"`
	}
	mustTool(t, cs, "convert_to_kubernetes", map[string]any{"workflow": wf.ID, "format": "manifests", "dry_run": true}, &preview)
	if preview.Report.Name != "shop" || preview.Files["k8s/wordpress.yaml"] == "" || len(preview.Report.Mappings) == 0 {
		t.Fatalf("preview %+v", preview.Report)
	}

	var res struct {
		Report   model.KubeConvertReport `json:"report"`
		Workflow struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"workflow"`
		Next string `json:"next"`
	}
	mustTool(t, cs, "convert_to_kubernetes", map[string]any{"workflow": wf.ID, "format": "kustomize"}, &res)
	if res.Workflow.Kind != model.WorkflowKubernetes || res.Workflow.ID == "" || res.Next == "" || res.Report.Sources[0].Path != "k8s/base" {
		t.Fatalf("convert %+v", res)
	}
	// Kubernetes workflows don't convert again.
	if text, isErr := callTool(t, cs, "convert_to_kubernetes", map[string]any{"workflow": res.Workflow.ID}); !isErr {
		t.Errorf("converted a kubernetes workflow: %s", text)
	}
}
