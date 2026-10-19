package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractAndInlineModuleTools(t *testing.T) {
	cs, _ := session(t)
	out, isErr := callTool(t, cs, "create_workflow", map[string]any{"name": "Queue", "template": "aws-queue-worker"})
	if isErr {
		t.Fatalf("create_workflow: %s", out)
	}
	var wf struct{ ID string }
	if err := json.Unmarshal([]byte(out), &wf); err != nil {
		t.Fatal(err)
	}
	nodes := []string{"aws_sqs_queue.jobs", "aws_sqs_queue.dlq"}

	out, isErr = callTool(t, cs, "extract_module", map[string]any{"workflow": wf.ID, "nodes": nodes, "name": "queue", "dry_run": true})
	if isErr || !strings.Contains(out, `"moved"`) || !strings.Contains(out, "module.queue.aws_sqs_queue.jobs") {
		t.Fatalf("dry run: %s", out)
	}
	if got, _ := callTool(t, cs, "get_workflow", map[string]any{"workflow": wf.ID}); strings.Contains(got, "module.queue") {
		t.Fatalf("dry run changed the workflow")
	}

	out, isErr = callTool(t, cs, "extract_module", map[string]any{"workflow": wf.ID, "nodes": nodes, "name": "queue"})
	if isErr || !strings.Contains(out, `"module"`) || !strings.Contains(out, "jobs_arn") {
		t.Fatalf("extract: %s", out)
	}
	if got, _ := callTool(t, cs, "get_terraform", map[string]any{"workflow": wf.ID}); !strings.Contains(got, "module.queue.jobs_arn") {
		t.Fatalf("root does not read the module output:\n%s", got)
	}

	out, isErr = callTool(t, cs, "extract_module", map[string]any{"workflow": wf.ID, "nodes": []string{"aws_lambda_function.worker"}, "name": "Bad Name"})
	if !isErr || !strings.Contains(out, "snake_case") {
		t.Fatalf("bad name: %s", out)
	}

	out, isErr = callTool(t, cs, "inline_module", map[string]any{"workflow": wf.ID, "node": "module.queue"})
	if isErr || !strings.Contains(out, "aws_sqs_queue.jobs") || !strings.Contains(out, `"from":"module.queue.aws_sqs_queue.jobs"`) {
		t.Fatalf("inline: %s", out)
	}
}
