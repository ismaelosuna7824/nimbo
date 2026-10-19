package netcheck

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// finding builds a net_* diagnostic on entity e.
func finding(sev model.Severity, code string, e *entity, attr, msg, why, fix string) model.Diagnostic {
	return model.Diagnostic{Severity: sev, Code: code, Message: msg, Detail: why, Fix: fix, NodeID: e.nodeID(), Attribute: attr}
}

func contains(outer, inner netip.Prefix) bool {
	return outer.Addr().Is4() == inner.Addr().Is4() && outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

// checks runs the AWS configuration checks.
func (w *world) checks() []model.Diagnostic {
	var out []model.Diagnostic
	add := func(d model.Diagnostic) { out = append(out, d) }

	// Subnet CIDR inside its VPC; no overlaps inside a VPC.
	byVPC := map[*vpc][]*subnet{}
	for _, s := range w.subnets {
		if s.vpc == nil || !s.cidrKnown || s.e.multi {
			continue
		}
		byVPC[s.vpc] = append(byVPC[s.vpc], s)
		if !s.vpc.cidrsKnown || len(s.vpc.cidrs) == 0 || !s.cidr.Addr().Is4() {
			continue
		}
		inside := false
		for _, c := range s.vpc.cidrs {
			if contains(c, s.cidr) {
				inside = true
			}
		}
		if !inside {
			add(finding(model.SeverityError, "net_subnet_outside_network", s.e, "cidr_block",
				fmt.Sprintf("Subnet %s (%s) is outside its VPC %s (%s)", s.e.addr, s.cidr, s.vpc.e.addr, cidrList(s.vpc.cidrs)),
				"A subnet is a slice of its VPC's address range; AWS refuses to create one outside it.",
				fmt.Sprintf("Pick a CIDR inside %s, e.g. with cidrsubnet(%s.cidr_block, 8, <n>).", cidrList(s.vpc.cidrs), s.vpc.e.addr)))
		}
	}
	for _, v := range w.vpcs {
		subs := byVPC[v]
		for i := range subs {
			for j := i + 1; j < len(subs); j++ {
				a, b := subs[i], subs[j]
				if a.cidr.Overlaps(b.cidr) {
					add(finding(model.SeverityError, "net_subnet_overlap", b.e, "cidr_block",
						fmt.Sprintf("Subnets %s (%s) and %s (%s) overlap", a.e.addr, a.cidr, b.e.addr, b.cidr),
						"Every address in a VPC belongs to exactly one subnet; AWS refuses overlapping subnets.",
						"Give each subnet its own range (e.g. cidrsubnet(<vpc cidr>, 8, 1), 8, 2, ...)."))
				}
			}
		}
	}

	// Peered VPCs must not overlap.
	for _, p := range w.peerings {
		if p.vpc == nil || p.peer == nil || !p.vpc.cidrsKnown || !p.peer.cidrsKnown {
			continue
		}
		for _, a := range p.vpc.cidrs {
			for _, b := range p.peer.cidrs {
				if a.Overlaps(b) {
					add(finding(model.SeverityError, "net_peering_overlap", p.e, "peer_vpc_id",
						fmt.Sprintf("Peered VPCs %s (%s) and %s (%s) have overlapping address ranges", p.vpc.e.addr, a, p.peer.e.addr, b),
						"Traffic between peered VPCs is routed by address; AWS can't peer VPCs whose CIDRs overlap.",
						"Give the VPCs non-overlapping CIDRs (e.g. 10.0.0.0/16 and 10.1.0.0/16)."))
				}
			}
		}
	}

	// Public intent without an internet gateway route; IGW route on a
	// private-named subnet.
	for _, s := range w.subnets {
		if s.e.multi || s.rt == nil || !s.rtKnown {
			continue
		}
		r, ok := s.rt.defaultRoute(w)
		if !ok {
			continue
		}
		toIGW := r != nil && r.kind == "internet-gateway"
		if s.mapKnown && s.mapPublic && !toIGW {
			add(finding(model.SeverityWarning, "net_public_subnet_no_igw", s.e, "map_public_ip_on_launch",
				fmt.Sprintf("Subnet %s gives instances public IPs but has no route to an internet gateway", s.e.addr),
				"A public IP only works when the subnet's route table sends internet traffic (0.0.0.0/0) to an internet gateway; otherwise nothing can reach those instances and they can't reach out.",
				fmt.Sprintf("Associate %s with a route table that has 0.0.0.0/0 → aws_internet_gateway, or set map_public_ip_on_launch = false if it should be private.", s.e.addr)))
		}
		if toIGW && looksPrivate(s.e) {
			add(finding(model.SeverityInfo, "net_private_subnet_igw", s.e, "",
				fmt.Sprintf("Subnet %s is named private but routes to the internet gateway", s.e.addr),
				"A subnet whose route table sends 0.0.0.0/0 to an internet gateway is public: anything in it with a public IP is reachable from the internet.",
				"Route the private subnets' 0.0.0.0/0 to a NAT gateway instead (outbound only), or rename the subnet."))
		}
	}
	for _, wl := range w.works {
		if wl.e.typ != "aws_instance" || len(wl.subnets) != 1 {
			continue
		}
		s := wl.subnets[0]
		pub, set, known := wl.e.boolean("associate_public_ip_address")
		if !known || !set || !pub || s.mapPublic || s.rt == nil || !s.rtKnown {
			continue
		}
		if r, ok := s.rt.defaultRoute(w); ok && (r == nil || r.kind != "internet-gateway") {
			add(finding(model.SeverityWarning, "net_public_subnet_no_igw", wl.e, "associate_public_ip_address",
				fmt.Sprintf("%s asks for a public IP, but its subnet %s has no route to an internet gateway", wl.e.addr, s.e.addr),
				"The public IP is useless without a route to an internet gateway: nothing can reach the instance from the internet.",
				"Move the instance to a public subnet, or drop associate_public_ip_address."))
		}
	}

	// Private subnets hosting resources that need outbound access.
	for _, wl := range w.works {
		if !wl.needsEgress || !wl.subnetsKnown || len(wl.subnets) == 0 || wl.e.multi {
			continue
		}
		v := wl.vpc()
		if v == nil || w.hasEndpoints(v) {
			continue
		}
		var stuck []string
		unknownRoute := false
		publicSubnetNoIP := false
		for _, s := range wl.subnets {
			if s.rt == nil || !s.rtKnown {
				unknownRoute = true
				continue
			}
			r, ok := s.rt.defaultRoute(w)
			if !ok {
				unknownRoute = true
				continue
			}
			switch {
			case r == nil || r.kind == "local" || r.kind == "egress-only-gateway":
				stuck = append(stuck, s.e.addr)
			case r.kind == "internet-gateway":
				pub := wl.public && wl.publicKnown
				if wl.kind == "kubernetes" && wl.e.typ == "aws_eks_node_group" {
					pub = s.mapPublic
				}
				if wl.e.typ == "aws_eks_cluster" {
					pub = true // control plane ENIs; nodes are checked separately
				}
				if !pub {
					stuck = append(stuck, s.e.addr)
					publicSubnetNoIP = true
				}
			}
		}
		if unknownRoute || len(stuck) == 0 {
			continue
		}
		attr := map[string]string{"function": "vpc_config", "container-service": "network_configuration", "kubernetes": "subnet_ids"}[wl.kind]
		if wl.e.typ == "aws_eks_cluster" {
			attr = "vpc_config"
		}
		why := "It runs in subnets without a NAT gateway route and the VPC has no VPC endpoints, so it can't reach the internet or AWS APIs (pulling images, calling S3/DynamoDB/SQS, sending logs...)."
		if publicSubnetNoIP {
			why = "It sits in a subnet that routes to an internet gateway, but it has no public IP (Lambda functions never get one), so the internet gateway can't carry its traffic. The VPC has no VPC endpoints either."
		}
		add(finding(model.SeverityWarning, "net_no_egress", wl.e, attr,
			fmt.Sprintf("%s has no outbound access from %s", wl.e.addr, strings.Join(stuck, ", ")),
			why,
			"Add a NAT gateway in a public subnet and route 0.0.0.0/0 to it from these subnets, or add VPC endpoints for the AWS services it calls (aws_vpc_endpoint)."))
	}

	// NAT gateways must sit in a public subnet.
	for _, g := range w.gws {
		if g.kind != "nat-gateway" || !g.public || g.subnet == nil || g.subnet.rt == nil || !g.subnet.rtKnown || g.e.multi {
			continue
		}
		if r, ok := g.subnet.rt.defaultRoute(w); ok && (r == nil || r.kind != "internet-gateway") {
			add(finding(model.SeverityWarning, "net_nat_no_igw", g.e, "subnet_id",
				fmt.Sprintf("NAT gateway %s is in %s, which has no route to an internet gateway", g.e.addr, g.subnet.e.addr),
				"A NAT gateway forwards private traffic through the internet gateway of its own subnet; in a private subnet it has no way out.",
				"Put the NAT gateway in a public subnet (route 0.0.0.0/0 → internet gateway)."))
		}
	}

	// Route tables nobody uses.
	if !w.assocIncomplete {
		for _, rt := range w.rts {
			if rt.e == nil || rt.main || rt.gateways || rt.usedByVE || len(rt.subnets) > 0 || rt.e.multi {
				continue
			}
			add(finding(model.SeverityWarning, "net_route_table_unassociated", rt.e, "",
				fmt.Sprintf("Route table %s is not associated with any subnet", rt.e.addr),
				"A route table only takes effect for the subnets associated with it; subnets without an association use the VPC's main route table instead.",
				fmt.Sprintf("Add an aws_route_table_association linking %s to its subnets (or remove it).", rt.e.addr)))
		}
	}

	// Routes to gateways of another VPC.
	for _, rt := range w.rts {
		if rt.vpc == nil {
			continue
		}
		for _, r := range rt.routes {
			g := r.target
			if g == nil || r.e == nil {
				continue
			}
			wrong := false
			switch g.kind {
			case "peering":
				wrong = g.vpc != nil && g.peer != nil && g.vpc != rt.vpc && g.peer != rt.vpc
			case "transit-gateway":
			default:
				wrong = g.vpc != nil && g.vpc != rt.vpc
			}
			if wrong {
				attr := "route"
				if r.e.typ == "aws_route" {
					attr = targetAttr(r)
				}
				add(finding(model.SeverityError, "net_route_cross_vpc", r.e, attr,
					fmt.Sprintf("Route table %s (in %s) routes to %s, which belongs to another VPC", rtLabel(rt), rt.vpc.e.addr, g.e.addr),
					"Routes can only target gateways attached to the route table's own VPC; AWS rejects the route.",
					"Point the route at a gateway of "+rt.vpc.e.addr+"."))
			}
		}
	}

	// Security groups across VPCs.
	for _, g := range w.sgs {
		if g.vpc == nil {
			continue
		}
		for _, rules := range [][]*sgRule{g.ingress, g.egress} {
			for _, r := range rules {
				for _, o := range r.refs {
					if o.vpc != nil && o.vpc != g.vpc && !w.peered(o.vpc, g.vpc) {
						e := g.e
						if r.e != nil {
							e = r.e
						}
						if e == nil {
							continue
						}
						add(finding(model.SeverityError, "net_sg_cross_vpc", e, "",
							fmt.Sprintf("%s refers to security group %s of another VPC (%s)", sgName(g), sgName(o), o.vpc.e.addr),
							"A security group rule can only reference groups of the same VPC (or of a peered VPC).",
							"Reference a security group of the same VPC, or allow the other VPC's CIDR instead."))
					}
				}
			}
		}
	}
	for _, wl := range w.works {
		var v *vpc
		for _, s := range wl.subnets {
			if s.vpc != nil {
				v = s.vpc
				break
			}
		}
		if v == nil {
			continue
		}
		for _, g := range wl.sgs {
			if g.vpc != nil && g.vpc != v && g.e != nil {
				add(finding(model.SeverityError, "net_sg_cross_vpc", wl.e, sgAttr(wl),
					fmt.Sprintf("%s uses security group %s of %s, but its subnets are in %s", wl.e.addr, g.e.addr, g.vpc.e.addr, v.e.addr),
					"A resource can only use security groups of the VPC its subnets belong to; AWS rejects the change.",
					"Use a security group created in "+v.e.addr+"."))
			}
		}
	}

	// NACLs that drop replies of internet-facing resources.
	for _, s := range w.subnets {
		n := s.acl
		if n == nil || n.e == nil || !n.complete || !s.aclKnown {
			continue
		}
		serving, egressing := w.internetFacing(s)
		if serving != "" {
			if r := evalNACL(n, true, "tcp", ephemeral, internetPeer); r.verdict == model.HopDeny {
				add(finding(model.SeverityWarning, "net_nacl_ephemeral", n.e, "egress",
					fmt.Sprintf("Network ACL %s blocks replies to the internet from %s (port %d)", n.e.addr, serving, r.port),
					"Network ACLs are stateless: replies to inbound connections leave on ephemeral ports 1024-65535 and need their own outbound rule. Without it clients connect but never get an answer.",
					fmt.Sprintf("Add an egress rule to %s allowing TCP 1024-65535 to 0.0.0.0/0.", n.e.addr)))
			}
		}
		if egressing != "" {
			if r := evalNACL(n, false, "tcp", ephemeral, internetPeer); r.verdict == model.HopDeny {
				add(finding(model.SeverityWarning, "net_nacl_ephemeral", n.e, "ingress",
					fmt.Sprintf("Network ACL %s blocks replies from the internet to %s (port %d)", n.e.addr, egressing, r.port),
					"Network ACLs are stateless: replies to outbound connections come back on ephemeral ports 1024-65535 and need their own inbound rule.",
					fmt.Sprintf("Add an ingress rule to %s allowing TCP 1024-65535 from 0.0.0.0/0.", n.e.addr)))
			}
		}
	}

	// Multi-AZ requirements.
	for _, wl := range w.works {
		if wl.e.multi || !wl.subnetsKnown || len(wl.subnets) == 0 {
			continue
		}
		zones, allKnown := distinctZones(wl.subnets)
		if !allKnown || len(zones) >= 2 {
			continue
		}
		switch {
		case wl.kind == "load-balancer" && wl.lbType == "application":
			add(finding(model.SeverityError, "net_single_az", wl.e, lbSubnetAttr(wl),
				fmt.Sprintf("Application load balancer %s has subnets in only one availability zone (%s)", wl.e.addr, strings.Join(zones, ", ")),
				"AWS requires an application load balancer to span at least two availability zones, so it keeps serving when one zone fails.",
				"Add a subnet in a second availability zone (e.g. one public subnet per AZ)."))
		case wl.e.typ == "aws_eks_cluster":
			add(finding(model.SeverityError, "net_single_az", wl.e, "vpc_config",
				fmt.Sprintf("EKS cluster %s has subnets in only one availability zone (%s)", wl.e.addr, strings.Join(zones, ", ")),
				"EKS requires subnets in at least two availability zones for its control plane.",
				"Add subnets in a second availability zone."))
		case wl.e.typ == "aws_elasticache_replication_group":
			multi, _, _ := wl.e.boolean("multi_az_enabled")
			failover, _, _ := wl.e.boolean("automatic_failover_enabled")
			if multi || failover {
				add(finding(model.SeverityWarning, "net_single_az", wl.e, "subnet_group_name",
					fmt.Sprintf("%s asks for Multi-AZ/failover but its subnet group covers one availability zone (%s)", wl.e.addr, strings.Join(zones, ", ")),
					"Replicas can only fail over to another zone when the subnet group has subnets there.",
					"Add a subnet in a second availability zone to the subnet group."))
			}
		}
	}
	for _, g := range w.ofType("aws_db_subnet_group") {
		if g.multi {
			continue
		}
		subs, known := w.subnetsRef(g.raw("subnet_ids"))
		if !known || len(subs) == 0 {
			continue
		}
		zones, allKnown := distinctZones(subs)
		if allKnown && len(zones) < 2 {
			add(finding(model.SeverityError, "net_single_az", g, "subnet_ids",
				fmt.Sprintf("DB subnet group %s covers only one availability zone (%s)", g.addr, strings.Join(zones, ", ")),
				"AWS requires a DB subnet group to have subnets in at least two availability zones, even for a single-AZ database.",
				"Add a subnet in a second availability zone (e.g. one isolated database subnet per AZ)."))
		}
	}
	return out
}

func targetAttr(r *route) string {
	for _, t := range routeTargets {
		if v, ok := r.e.attrs[t.attr].(string); ok && v != "" {
			return t.attr
		}
	}
	return ""
}

func sgAttr(wl *workload) string {
	switch wl.e.typ {
	case "aws_instance":
		return "vpc_security_group_ids"
	case "aws_lambda_function":
		return "vpc_config"
	case "aws_lb", "aws_alb":
		return "security_groups"
	case "aws_ecs_service":
		return "network_configuration"
	case "aws_elasticache_cluster", "aws_elasticache_replication_group":
		return "security_group_ids"
	}
	return "vpc_security_group_ids"
}

func lbSubnetAttr(wl *workload) string {
	if wl.e.raw("subnet_mapping") != nil && wl.e.raw("subnets") == nil {
		return "subnet_mapping"
	}
	return "subnets"
}

func sgName(g *sg) string {
	if g.e != nil {
		return g.e.addr
	}
	if g.vpc != nil {
		return "the default security group of " + g.vpc.e.addr
	}
	return "a default security group"
}

func distinctZones(subs []*subnet) ([]string, bool) {
	seen := map[string]bool{}
	for _, s := range subs {
		if s.az == "" {
			return nil, false
		}
		seen[s.az] = true
	}
	zones := sortedKeys(seen)
	sort.Strings(zones)
	return zones, true
}

func (w *world) hasEndpoints(v *vpc) bool {
	for _, g := range w.gws {
		if g.kind == "vpc-endpoint" && (g.vpc == v || !g.vpcKnown) {
			return true
		}
	}
	return false
}

// internetFacing names what in subnet s accepts connections from the
// internet (serving) and what opens connections to it (egressing: NAT
// gateways and public instances).
func (w *world) internetFacing(s *subnet) (serving, egressing string) {
	for _, wl := range w.works {
		if !wl.public || !wl.publicKnown || !slicesContains(wl.subnets, s) {
			continue
		}
		switch wl.kind {
		case "load-balancer":
			if serving == "" {
				serving = wl.e.addr
			}
		case "instance", "container-service", "autoscaling":
			if evalSG(wl.sgs, wl.sgsKnown, true, "tcp", 443, internetPeer, false).verdict == model.HopAllow ||
				evalSG(wl.sgs, wl.sgsKnown, true, "tcp", 80, internetPeer, false).verdict == model.HopAllow {
				if serving == "" {
					serving = wl.e.addr
				}
			}
		}
	}
	for _, g := range w.gws {
		if g.kind == "nat-gateway" && g.public && g.subnet == s {
			egressing = g.e.addr
		}
	}
	return serving, egressing
}

func slicesContains[T comparable](xs []T, x T) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// looksPrivate: the subnet's address, for_each key or Name tag says
// "private".
func looksPrivate(e *entity) bool {
	s := strings.ToLower(e.addr + " " + e.tagName())
	return strings.Contains(s, "private")
}
