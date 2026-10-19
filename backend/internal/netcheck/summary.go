package netcheck

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Summary returns the network model for GET /workflows/{id}/network.
func (a *Analysis) Summary() model.NetworkSummary {
	w := a.aws
	s := model.NetworkSummary{
		WorkflowID: a.wf.ID, Basis: a.basis, BasisReason: a.basisReason,
		Networks: []model.NetNetwork{}, Subnets: []model.NetSubnet{}, RouteTables: []model.NetRouteTable{},
		Gateways: []model.NetGateway{}, SecurityGroups: []model.NetSecurityGroup{}, Placements: []model.NetPlacement{},
		Findings: a.findings, Notes: append([]string{}, a.notes...),
	}
	for _, v := range w.vpcs {
		n := model.NetNetwork{NodeID: v.e.nodeID(), Address: v.e.addr, Cloud: model.CloudAWS, Name: v.e.tagName(), CIDRs: []string{}}
		if v.cidrsKnown {
			for _, c := range v.cidrs {
				n.CIDRs = append(n.CIDRs, c.String())
			}
		}
		if a.basis == model.NetworkBasisDeployed {
			n.ID = v.e.id
		}
		s.Networks = append(s.Networks, n)
	}
	for _, sn := range w.subnets {
		out := model.NetSubnet{NodeID: sn.e.nodeID(), Address: sn.e.addr, Name: sn.e.tagName(), Zone: sn.az, MapPublicIP: sn.mapPublic && sn.mapKnown}
		if sn.vpc != nil {
			out.Network = sn.vpc.e.addr
		}
		if sn.cidrKnown {
			out.CIDR = sn.cidr.String()
		}
		if sn.rt != nil && sn.rtKnown {
			out.RouteTable = rtLabel(sn.rt)
		}
		if sn.acl != nil && sn.aclKnown {
			out.NACL = naclName(sn.acl)
		}
		if a.basis == model.NetworkBasisDeployed {
			out.ID = sn.e.id
		}
		out.Classification, out.Why = w.classify(sn)
		s.Subnets = append(s.Subnets, out)
	}
	for _, rt := range w.rts {
		out := model.NetRouteTable{Address: rtLabel(rt), Main: rt.main, Subnets: []string{}, Routes: []model.NetRoute{}}
		if rt.e != nil {
			out.NodeID = rt.e.nodeID()
		}
		if rt.vpc != nil {
			out.Network = rt.vpc.e.addr
			for _, c := range rt.vpc.cidrs {
				out.Routes = append(out.Routes, model.NetRoute{Destination: c.String(), TargetKind: "local", Target: "local"})
			}
		}
		for _, sn := range rt.subnets {
			out.Subnets = append(out.Subnets, sn.e.addr)
		}
		for _, r := range rt.routes {
			nr := model.NetRoute{TargetKind: r.kind, Target: r.targetID, NodeID: r.e.nodeID()}
			switch {
			case r.prefixList != "":
				nr.Destination = "prefix list " + r.prefixList
			case r.destKnown:
				nr.Destination = r.dest.String()
			default:
				nr.Destination = "(known after deploy)"
			}
			if r.target != nil {
				nr.Target = r.target.e.addr
			} else if strings.HasPrefix(nr.Target, refPrefix) {
				nr.Target = ""
			}
			out.Routes = append(out.Routes, nr)
		}
		s.RouteTables = append(s.RouteTables, out)
	}
	for _, g := range w.gws {
		out := model.NetGateway{NodeID: g.e.nodeID(), Address: g.e.addr, Kind: g.kind, Service: g.service}
		if g.vpc != nil {
			out.Network = g.vpc.e.addr
		}
		if g.subnet != nil {
			out.Subnet = g.subnet.e.addr
		}
		switch g.kind {
		case "internet-gateway":
			out.Detail = "Connects the VPC to the internet. Subnets that route 0.0.0.0/0 here are public."
		case "nat-gateway":
			if g.public {
				out.Detail = "Lets resources in private subnets start connections to the internet (outbound only), using its Elastic IP."
			} else {
				out.Detail = "Private NAT: translates addresses toward other networks, not the internet."
			}
		case "egress-only-gateway":
			out.Detail = "Outbound-only internet access for IPv6."
		case "vpc-endpoint":
			out.Detail = fmt.Sprintf("%s endpoint: reaches %s without going through the internet.", g.endpointTyp, nonEmpty(g.service, "an AWS service"))
		case "peering":
			if g.peer != nil {
				out.Detail = "Connects to " + g.peer.e.addr + "."
			}
		case "transit-gateway":
			out.Detail = "Attachment to a transit gateway (its routing is not analyzed)."
		}
		s.Gateways = append(s.Gateways, out)
	}
	for _, g := range w.sgs {
		s.SecurityGroups = append(s.SecurityGroups, sgSummary(g))
	}
	for _, wl := range w.works {
		p := model.NetPlacement{NodeID: wl.e.nodeID(), Address: wl.e.addr, Kind: wl.kind, Subnets: []string{}, Zones: append([]string{}, wl.zones...),
			SecurityGroups: []string{}, Public: wl.public && wl.publicKnown, PublicWhy: wl.publicWhy, Ports: wl.ports, InVPC: wl.inVPC}
		if wl.kind == "autoscaling" {
			p.Kind = "instance"
		}
		if v := wl.vpc(); v != nil {
			p.Network = v.e.addr
		}
		for _, sn := range wl.subnets {
			p.Subnets = append(p.Subnets, sn.e.addr)
		}
		for _, g := range wl.sgs {
			p.SecurityGroups = append(p.SecurityGroups, sgName(g))
		}
		if p.Zones == nil {
			p.Zones = []string{}
		}
		s.Placements = append(s.Placements, p)
	}
	s.Networks = append(s.Networks, a.others.networks...)
	s.Subnets = append(s.Subnets, a.others.subnets...)
	s.SecurityGroups = append(s.SecurityGroups, a.others.sgs...)
	for _, d := range a.findings {
		switch d.Severity {
		case model.SeverityError:
			s.Counts.Errors++
		case model.SeverityWarning:
			s.Counts.Warnings++
		default:
			s.Counts.Info++
		}
	}
	return s
}

func naclName(n *nacl) string {
	if n.e == nil {
		return "default network ACL of " + n.vpc.e.addr + " (allows all)"
	}
	return n.e.addr
}

// classify explains whether a subnet is public, private or isolated.
func (w *world) classify(s *subnet) (string, string) {
	if s.e.multi && !s.rtKnown {
		return model.SubnetUnknown, "Its instances and route table are only known at deploy time."
	}
	if s.rt == nil || !s.rtKnown {
		return model.SubnetUnknown, "Which route table it uses is only known at deploy time."
	}
	r, ok := s.rt.defaultRoute(w)
	if !ok {
		return model.SubnetUnknown, "Some routes of " + rtLabel(s.rt) + " are only known at deploy time."
	}
	via := "Its route table " + rtLabel(s.rt)
	if s.rt.e == nil {
		via = "It uses the VPC's main route table, which"
	}
	switch {
	case r != nil && r.kind == "internet-gateway":
		why := fmt.Sprintf("%s sends internet traffic (%s) to the internet gateway %s. Resources here that have a public IP can be reached from the internet.", via, r.dest, gwName(r))
		if !s.mapPublic {
			why += " Instances only get a public IP if they ask for one (map_public_ip_on_launch is off)."
		}
		return model.SubnetPublic, why
	case r != nil && r.kind == "nat-gateway":
		return model.SubnetPrivate, fmt.Sprintf("%s sends internet traffic to NAT gateway %s: resources here can reach out to the internet, but nothing on the internet can start a connection to them.", via, gwName(r))
	case r == nil || r.kind == "local":
		why := via + " has no route to the internet: only traffic inside the VPC (and to VPC endpoints) works. Good for databases."
		if s.mapPublic {
			why += " map_public_ip_on_launch is on, but public IPs are useless without an internet gateway route."
		}
		return model.SubnetIsolated, why
	case r.kind == "egress-only-gateway":
		return model.SubnetIsolated, via + " only has IPv6 outbound access (egress-only internet gateway); IPv4 stays inside the VPC."
	}
	return model.SubnetPrivate, fmt.Sprintf("%s sends internet traffic to a %s, so it isn't reachable from the internet directly.", via, kindName(r.kind))
}

var anywhere4 = netip.MustParsePrefix("0.0.0.0/0")

func sgSummary(g *sg) model.NetSecurityGroup {
	out := model.NetSecurityGroup{Address: sgName(g), Ingress: []model.NetRule{}, Egress: []model.NetRule{}}
	if g.e != nil {
		out.NodeID = g.e.nodeID()
	}
	if g.vpc != nil {
		out.Network = g.vpc.e.addr
	}
	conv := func(r *sgRule) model.NetRule {
		nr := model.NetRule{Protocol: r.proto, Ports: r.ports.String(), Description: r.desc}
		if r.proto == "icmp" || r.proto == "all" {
			nr.Ports = "all"
		}
		var peers []string
		for _, c := range r.cidrs {
			if c.Bits() == 0 {
				peers = append(peers, "anywhere")
			} else {
				peers = append(peers, c.String())
			}
		}
		for _, o := range r.refs {
			peers = append(peers, sgName(o))
		}
		if r.self {
			peers = append(peers, "itself")
		}
		if r.prefixList {
			peers = append(peers, "a prefix list")
		}
		if r.unknown {
			peers = append(peers, "(known after deploy)")
		}
		nr.Peer = strings.Join(peers, ", ")
		if r.e != nil {
			nr.NodeID = r.e.nodeID()
		} else if g.e != nil {
			nr.NodeID = g.e.nodeID()
		}
		return nr
	}
	for _, r := range g.ingress {
		out.Ingress = append(out.Ingress, conv(r))
	}
	for _, r := range g.egress {
		out.Egress = append(out.Egress, conv(r))
	}
	out.Summary = "Inbound: " + rulesPhrase(out.Ingress, "from", "nothing (all inbound traffic is blocked)") + ". Outbound: " +
		rulesPhrase(out.Egress, "to", "nothing (Terraform removes AWS's default allow-all egress rule)") + "."
	if !g.complete {
		out.Summary += " Some rules are only known at deploy time."
	}
	if open := openAdminPorts(g); len(open) > 0 {
		out.Summary += " " + strings.Join(open, ", ") + " open to the internet — see the bp_open_ingress best practice."
	}
	return out
}

func rulesPhrase(rules []model.NetRule, prep, empty string) string {
	if len(rules) == 0 {
		return empty
	}
	byPeer := map[string][]string{}
	var peers []string
	for _, r := range rules {
		what := strings.ToUpper(r.Protocol) + " " + r.Ports
		switch {
		case r.Protocol == "all":
			what = "all traffic"
		case r.Protocol == "icmp":
			what = "ping (ICMP)"
		case r.Protocol == "tcp" || r.Protocol == "udp":
			what = "port " + r.Ports
			if r.Ports == "all" {
				what = "all ports"
			}
			if r.Protocol == "udp" {
				what = "UDP " + what
			}
		}
		if _, ok := byPeer[r.Peer]; !ok {
			peers = append(peers, r.Peer)
		}
		byPeer[r.Peer] = append(byPeer[r.Peer], what)
	}
	var parts []string
	for _, p := range peers {
		parts = append(parts, strings.Join(byPeer[p], ", ")+" "+prep+" "+p)
	}
	return strings.Join(parts, "; ")
}

var adminPortNames = map[int]string{22: "SSH (22)", 3389: "RDP (3389)", 3306: "MySQL (3306)", 5432: "PostgreSQL (5432)", 1433: "SQL Server (1433)", 6379: "Redis (6379)", 27017: "MongoDB (27017)"}

func openAdminPorts(g *sg) []string {
	var out []string
	ports := make([]int, 0, len(adminPortNames))
	for p := range adminPortNames {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		for _, r := range g.ingress {
			if (r.proto == "all" || r.proto == "tcp") && r.ports.from <= p && p <= r.ports.to && slicesContains(r.cidrs, anywhere4) {
				out = append(out, adminPortNames[p])
				break
			}
		}
	}
	return out
}
