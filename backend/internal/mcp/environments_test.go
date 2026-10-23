package mcp

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestEnvironmentTools(t *testing.T) {
	cs, _ := session(t)
	var wf struct {
		ID string `json:"id"`
	}
	mustTool(t, cs, "create_workflow", map[string]any{"name": "hello", "kind": "kubernetes", "template": "k8s-helm-app"}, &wf)

	var list struct {
		Active       string                  `json:"active"`
		Environments []model.EnvironmentInfo `json:"environments"`
	}
	mustTool(t, cs, "create_environment", map[string]any{"workflow": wf.ID, "name": "staging", "from": "prod"}, &list)
	if len(list.Environments) != 3 || list.Environments[2].Name != "staging" || !slices.Equal(list.Environments[2].Files, []string{"chart/values-staging.yaml"}) {
		t.Fatalf("create %+v", list)
	}

	var settings model.EnvSettings
	mustTool(t, cs, "set_environment_values", map[string]any{"workflow": wf.ID, "environment": "staging", "values": map[string]any{"image:podinfo": "6.14.0"}}, &settings)
	var tag model.EnvSetting
	for _, s := range settings.Settings {
		if s.Key == "image:podinfo" {
			tag = s
		}
	}
	if tag.Value != "6.14.0" || tag.Base != "6.15.0" || !tag.Overridden {
		t.Fatalf("image tag %+v", tag)
	}

	var cmp model.EnvCompare
	mustTool(t, cs, "compare_environments", map[string]any{"workflow": wf.ID}, &cmp)
	if len(cmp.Columns) != 4 || len(cmp.Groups) == 0 {
		t.Fatalf("compare %+v", cmp)
	}

	mustTool(t, cs, "update_workflow", map[string]any{"workflow": wf.ID, "environment": "staging"}, nil)
	mustTool(t, cs, "rename_environment", map[string]any{"workflow": wf.ID, "name": "staging", "new_name": "qa"}, &list)
	if list.Active != "qa" {
		t.Fatalf("active after rename %q", list.Active)
	}
	mustTool(t, cs, "delete_environment", map[string]any{"workflow": wf.ID, "name": "qa"}, &list)
	if list.Active != "" || len(list.Environments) != 2 {
		t.Fatalf("after delete %+v", list)
	}

	// Terraform workflows keep set_environment.
	var tf workflowView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "infra"}, &tf)
	if text, isErr := callTool(t, cs, "list_environments", map[string]any{"workflow": tf.ID}); !isErr || !strings.Contains(text, "set_environment") {
		t.Fatalf("terraform: %s", text)
	}
}
