package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// networkView is the compact network summary of check_network.
type networkView struct {
	Basis          model.NetworkBasis `json:"basis"`
	BasisReason    string             `json:"basis_reason"`
	Networks       []string           `json:"networks,omitempty"`
	Subnets        []netSubnetView    `json:"subnets,omitempty"`
	RouteTables    []string           `json:"route_tables,omitempty"`
	Gateways       []string           `json:"gateways,omitempty"`
	SecurityGroups []string           `json:"security_groups,omitempty"`
	Placements     []placementView    `json:"placements,omitempty"`
	Findings       []netFindingView   `json:"findings"`
	Notes          []string           `json:"notes,omitempty"`
	Hint           string             `json:"hint"`
}

type netSubnetView struct {
	Subnet string `json:"subnet"`
	CIDR   string `json:"cidr,omitempty"`
	Zone   string `json:"zone,omitempty"`
	Class  string `json:"class"`
	Why    string `json:"why"`
}

type placementView struct {
	Node           string   `json:"node"`
	Kind           string   `json:"kind"`
	Subnets        []string `json:"subnets,omitempty"`
	SecurityGroups []string `json:"security_groups,omitempty"`
	Public         bool     `json:"public"`
	Why            string   `json:"why,omitempty"`
}

type netFindingView struct {
	Severity  model.Severity `json:"severity"`
	Code      string         `json:"code"`
	Node      string         `json:"node,omitempty"`
	Attribute string         `json:"attribute,omitempty"`
	Message   string         `json:"message"`
	Why       string         `json:"why,omitempty"`
	Fix       string         `json:"fix,omitempty"`
}

func (t *tools) checkNetwork(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var s model.NetworkSummary
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/network", &s); err != nil {
		return nil, err
	}
	addrs := addressByID(wf)
	out := networkView{Basis: s.Basis, BasisReason: s.BasisReason, Findings: []netFindingView{}, Notes: s.Notes}
	for _, n := range s.Networks {
		out.Networks = append(out.Networks, strings.TrimSpace(n.Address+" "+strings.Join(n.CIDRs, ", ")))
	}
	for _, sn := range s.Subnets {
		out.Subnets = append(out.Subnets, netSubnetView{Subnet: sn.Address, CIDR: sn.CIDR, Zone: sn.Zone, Class: sn.Classification, Why: sn.Why})
	}
	for _, rt := range s.RouteTables {
		var routes []string
		for _, r := range rt.Routes {
			routes = append(routes, r.Destination+" → "+strings.TrimSpace(r.TargetKind+" "+strings.TrimPrefix(r.Target, "local")))
		}
		subs := "no subnets"
		if len(rt.Subnets) > 0 {
			subs = strings.Join(rt.Subnets, ", ")
		}
		out.RouteTables = append(out.RouteTables, rt.Address+" ("+subs+"): "+strings.Join(routes, "; "))
	}
	for _, g := range s.Gateways {
		out.Gateways = append(out.Gateways, g.Address+" ("+g.Kind+")")
	}
	for _, g := range s.SecurityGroups {
		out.SecurityGroups = append(out.SecurityGroups, g.Address+": "+g.Summary)
	}
	for _, p := range s.Placements {
		out.Placements = append(out.Placements, placementView{Node: p.Address, Kind: p.Kind, Subnets: p.Subnets, SecurityGroups: p.SecurityGroups, Public: p.Public, Why: p.PublicWhy})
	}
	for _, d := range s.Findings {
		node := addrs[d.NodeID]
		if node == "" {
			node = d.NodeID
		}
		out.Findings = append(out.Findings, netFindingView{Severity: d.Severity, Code: d.Code, Node: node, Attribute: d.Attribute, Message: d.Message, Why: d.Detail, Fix: d.Fix})
	}
	switch {
	case len(s.Networks) == 0:
		out.Hint = "The workflow has no networks (VPCs, VNets, VPC networks) to check."
	case len(out.Findings) > 0:
		out.Hint = "Fix the findings (errors first) with update_node, then call check_network again. Use check_reachability to test a specific path (e.g. internet → load balancer on 443)."
	default:
		out.Hint = "No network findings. Use check_reachability to confirm the paths the design needs (e.g. internet → web server on 443, app → database on 5432, database → internet blocked)."
	}
	return out, nil
}

type reachabilityIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	From     string `json:"from" jsonschema:"\"internet\" or a node address/ID (instance, Lambda, database, cache, load balancer, ECS service, EKS cluster)"`
	To       string `json:"to" jsonschema:"\"internet\" or a node address/ID (also AWS services such as an S3 bucket or DynamoDB table)"`
	Protocol string `json:"protocol,omitempty" jsonschema:"tcp (default), udp or icmp"`
	Port     int    `json:"port,omitempty" jsonschema:"destination port, e.g. 22, 80, 443, 3306, 5432, 6379 (ignored for icmp)"`
	Live     bool   `json:"live,omitempty" jsonschema:"also run real connections in the deployed sandbox (EC2 instances: from this computer, between instances, to the internet); each hop then says whether the emulator verified it (live) or only the analysis did"`
}

func endpointOf(s string) model.NetEndpoint {
	if strings.EqualFold(s, "internet") || s == "0.0.0.0/0" {
		return model.NetEndpoint{Kind: "internet"}
	}
	return model.NetEndpoint{Kind: "node", NodeID: s}
}

func (t *tools) checkReachability(ctx context.Context, in reachabilityIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	req := model.ReachabilityRequest{Source: endpointOf(in.From), Destination: endpointOf(in.To), Protocol: in.Protocol, Port: in.Port, Live: in.Live}
	var res model.ReachabilityResult
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/network/reachability", req, &res); err != nil {
		return nil, err
	}
	addrs := addressByID(wf)
	for i := range res.Path {
		if a := addrs[res.Path[i].NodeID]; a != "" {
			res.Path[i].NodeID = a
		}
	}
	return res, nil
}

type testInstanceIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Subnet   string `json:"subnet" jsonschema:"aws_subnet address or node ID; for count/for_each subnets one instance, e.g. aws_subnet.public[\"a\"]"`
}

func (t *tools) addTestInstance(ctx context.Context, in testInstanceIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/network/test-instance", model.TestInstanceRequest{Subnet: in.Subnet}, &res); err != nil {
		return nil, err
	}
	node := ""
	for i := range res.Workflow.Nodes {
		if res.Workflow.Nodes[i].ID == res.NodeID {
			node = address(&res.Workflow.Nodes[i])
		}
	}
	return map[string]any{
		"node":        node,
		"diagnostics": nodeDiagnostics(&res.Workflow),
		"hint":        "Deploy, then get_endpoints shows the instance's host URL and check_reachability from=internet to=" + node + " port=80 live=true connects to it.",
	}, nil
}
