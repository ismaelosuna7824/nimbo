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
