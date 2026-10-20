package mcp

import (
	"strings"
	"testing"
)

func TestNetworkTools(t *testing.T) {
	cs, _ := session(t)
	var wf struct {
		ID string `json:"id"`
	}
	mustTool(t, cs, "create_workflow", map[string]any{"name": "net", "template": "aws-vpc-basics"}, &wf)
	if wf.ID == "" {
		t.Fatal("no workflow")
	}
	var net networkView
	mustTool(t, cs, "check_network", map[string]any{"workflow": wf.ID}, &net)
	if net.Basis != "design" || len(net.Subnets) != 2 || net.Subnets[0].Class != "public" || len(net.Findings) != 0 {
		t.Fatalf("check_network %+v", net)
	}
	var added struct {
		Node string `json:"node"`
	}
	mustTool(t, cs, "add_test_instance", map[string]any{"workflow": wf.ID, "subnet": `aws_subnet.public["a"]`}, &added)
	if added.Node != "aws_instance.test_web" {
		t.Fatalf("add_test_instance %+v", added)
	}
	var res struct {
		Reachable bool     `json:"reachable"`
		Blockers  []string `json:"blockers"`
		Path      []struct {
			Component string `json:"component"`
			NodeID    string `json:"nodeId"`
		} `json:"path"`
	}
	mustTool(t, cs, "check_reachability", map[string]any{"workflow": wf.ID, "from": "internet", "to": "aws_instance.test_web", "port": 22}, &res)
	if res.Reachable || len(res.Blockers) == 0 || !strings.Contains(res.Blockers[0], "aws_security_group.test_web") {
		t.Fatalf("check_reachability :22 %+v", res)
	}
	mustTool(t, cs, "check_reachability", map[string]any{"workflow": wf.ID, "from": "internet", "to": "aws_instance.test_web", "port": 80}, &res)
	if !res.Reachable {
		t.Fatalf("check_reachability :80 %+v", res)
	}
	if text, isErr := callTool(t, cs, "check_reachability", map[string]any{"workflow": wf.ID, "from": "internet", "to": "internet", "port": 80}); !isErr || !strings.Contains(text, "both") {
		t.Errorf("internet -> internet: %s", text)
	}
}

// check_reachability says it covers AWS only, for GCP/Azure endpoints both
// in a workflow with an AWS network and in one without.
func TestCheckReachabilityAWSOnly(t *testing.T) {
	cs, _ := session(t)
	var wf struct {
		ID string `json:"id"`
	}
	mustTool(t, cs, "create_workflow", map[string]any{"name": "mixed", "template": "aws-vpc-basics"}, &wf)
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": wf.ID, "files": map[string]string{
		"gcp.tf": "resource \"google_cloud_run_v2_service\" \"api\" {\n  name     = \"api\"\n  location = \"us-central1\"\n}\n",
	}}, nil)
	var az struct {
		ID string `json:"id"`
	}
	mustTool(t, cs, "create_workflow", map[string]any{"name": "azure"}, &az)
	mustTool(t, cs, "apply_terraform_code", map[string]any{"workflow": az.ID, "files": map[string]string{
		"main.tf": "resource \"azurerm_linux_web_app\" \"api\" {\n  name = \"api\"\n}\n",
	}}, nil)
	for _, c := range []struct{ workflow, to string }{{wf.ID, "google_cloud_run_v2_service.api"}, {az.ID, "azurerm_linux_web_app.api"}} {
		text, isErr := callTool(t, cs, "check_reachability", map[string]any{"workflow": c.workflow, "from": "internet", "to": c.to, "port": 443})
		if !isErr || !strings.Contains(text, "AWS only today") || !strings.Contains(text, c.to) {
			t.Errorf("%s: %s", c.to, text)
		}
	}
}
