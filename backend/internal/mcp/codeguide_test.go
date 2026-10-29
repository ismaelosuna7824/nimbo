package mcp

import (
	"strings"
	"testing"
)

func TestCodeGuidelines(t *testing.T) {
	cs, _ := session(t)

	// Generic: architecture smoke handler for AWS Lambda.
	var g codeGuidelinesView
	mustTool(t, cs, "code_guidelines", map[string]any{}, &g)
	if g.Mode != "architecture" || g.Language != "javascript" || g.Platform != "aws" || g.Target != "function" ||
		g.Packaging.Kind != "zip" || !strings.Contains(g.Packaging.Terraform, `source_dir  = "${path.module}/services/api/src"`) ||
		len(g.Files) == 0 || len(g.Compute) == 0 || len(g.Spec.Gate) == 0 || !strings.Contains(g.Next, "/smoke") {
		t.Fatalf("default guide = %+v", g)
	}

	// Full mode without a language: options to propose.
	g = codeGuidelinesView{}
	mustTool(t, cs, "code_guidelines", map[string]any{"mode": "full", "platform": "gcp", "target": "container"}, &g)
	if len(g.Languages) != 6 || len(g.Files) != 0 || !strings.Contains(g.Next, "language") {
		t.Fatalf("full without language = %+v", g)
	}

	// The workflow's mode and platform are the defaults.
	var wf dockerView
	mustTool(t, cs, "create_workflow", map[string]any{"name": "Shop", "template": "docker-node-postgres-redis", "mode": "full"}, &wf)
	g = codeGuidelinesView{}
	mustTool(t, cs, "code_guidelines", map[string]any{"workflow": wf.ID, "language": "go", "style": "modular-monolith"}, &g)
	if g.Mode != "full" || g.Platform != "docker" || g.Target != "container" || g.Packaging.Kind != "compose-build" ||
		!strings.Contains(g.Packaging.How, "build: ./services/api") || !strings.Contains(g.Next, "NEEDS CLARIFICATION") {
		t.Fatalf("workflow guide = %+v", g)
	}

	for _, args := range []map[string]any{
		{"platform": "heroku"},
		{"platform": "docker", "target": "function"},
		{"mode": "full", "language": "java", "platform": "azure", "target": "function"},
		{"workflow": "no-such-workflow"},
	} {
		if text, isErr := callTool(t, cs, "code_guidelines", args); !isErr {
			t.Errorf("%v: no error: %s", args, text)
		}
	}
}
