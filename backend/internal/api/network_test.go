package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestNetworkSummaryTestInstanceAndReachability(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Net", "templateId": "aws-vpc-basics"}, &wf)
	for _, d := range wf.Diagnostics {
		if strings.HasPrefix(d.Code, "net_") {
			t.Errorf("template has network finding %+v", d)
		}
	}
	var sum model.NetworkSummary
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/network", nil, &sum)
	if sum.Basis != model.NetworkBasisDesign || len(sum.Subnets) != 2 || sum.Subnets[0].Classification != model.SubnetPublic {
		t.Fatalf("summary %+v", sum)
	}

	// A subnet with several instances needs an instance address.
	if got := e.call("POST", "/workflows/"+wf.ID+"/network/test-instance", map[string]string{"subnet": "aws_subnet.public"}, nil); got != http.StatusBadRequest {
		t.Errorf("for_each subnet without key = %d", got)
	}
	var added struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/network/test-instance", map[string]string{"subnet": `aws_subnet.public["a"]`}, &added)
	inst := nodeByID(&added.Workflow, added.NodeID)
	if inst == nil || hclcodec.Address(inst) != "aws_instance.test_web" || inst.Label == "" {
		t.Fatalf("test instance node %+v", inst)
	}
	if errs := errorDiags(&added.Workflow); len(errs) > 0 {
		t.Fatalf("test instance adds errors: %+v", errs)
	}
	for _, d := range added.Workflow.Diagnostics {
		if strings.HasPrefix(d.Code, "net_") || d.Severity == model.SeverityWarning {
			t.Errorf("test instance adds %+v", d)
		}
	}
	assertNoOverlap(t, &added.Workflow)
	var files struct {
		Files map[string]string `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &files)
	all := ""
	for _, c := range files.Files {
		all += c
	}
	for _, want := range []string{`subnet_id                   = aws_subnet.public["a"].id`, "vpc_id      = aws_vpc.main.id", "# Port 80 open to the whole internet", "--bind \"$IP\""} {
		if !strings.Contains(all, want) {
			t.Errorf("exported code lacks %q", want)
		}
	}
	// A second one gets its own name.
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/network/test-instance", map[string]string{"subnet": `aws_subnet.public["b"]`}, &added)
	if n := nodeByID(&added.Workflow, added.NodeID); n == nil || n.Name != "test_web_2" {
		t.Errorf("second test instance %+v", n)
	}

	reach := func(port int, live bool) model.ReachabilityResult {
		var res model.ReachabilityResult
		e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/network/reachability", model.ReachabilityRequest{
			Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: "aws_instance.test_web"},
			Protocol: "tcp", Port: port, Live: live}, &res)
		return res
	}
	if res := reach(80, false); !res.Reachable || res.Basis != model.NetworkBasisDesign {
		t.Errorf("internet -> test_web:80 %+v", res)
	}
	res := reach(22, false)
	if res.Reachable || len(res.Blockers) == 0 || len(res.Suggestions) == 0 {
		t.Errorf("internet -> test_web:22 %+v", res)
	}
	// Live needs a deployment; the analysis is still returned.
	live := reach(80, true)
	if live.Live == nil || !strings.Contains(live.Live.Unavailable, "Deploy") || !live.Reachable {
		t.Errorf("live without a deployment %+v", live.Live)
	}
	for _, h := range live.Path {
		if h.Enforcement != "analysis" {
			t.Errorf("hop %+v without deployment should be analysis-only", h)
		}
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/network/reachability", model.ReachabilityRequest{
		Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: "aws_vpc.main"}, Protocol: "tcp", Port: 80}, nil); got != http.StatusBadRequest {
		t.Errorf("vpc destination = %d", got)
	}
}

func TestNetworkFindingsDontBlockRuns(t *testing.T) {
	e := newEnvDocker(t, "/nonexistent/docker")
	wf := e.createEmpty("Single AZ")
	files := map[string]string{"main.tf": `
resource "aws_vpc" "v" {
  cidr_block = "10.0.0.0/16"
}
resource "aws_subnet" "a" {
  vpc_id            = aws_vpc.v.id
  cidr_block        = "10.0.1.0/24"
  availability_zone = "us-east-1a"
}
resource "aws_db_subnet_group" "g" {
  name       = "g"
  subnet_ids = [aws_subnet.a.id]
}
`}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": files}, &wf)
	found := false
	for _, d := range wf.Diagnostics {
		if d.Code == "net_single_az" && d.Severity == model.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("want net_single_az, got %+v", wf.Diagnostics)
	}
	var body map[string]any
	if got := e.call("POST", "/workflows/"+wf.ID+"/runs", map[string]string{"action": "plan"}, &body); got == http.StatusBadRequest {
		t.Errorf("plan refused for a network finding: %v", body)
	}
}
