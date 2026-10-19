package netcheck

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// otherClouds covers the Azure and GCP checks that map cleanly onto
// AWS's: address ranges of virtual networks/subnets, overlaps (also
// between peered VNets) and duplicate NSG rule priorities. Public/private
// classification and reachability are AWS-only.
type otherClouds struct {
	networks []model.NetNetwork
	subnets  []model.NetSubnet
	sgs      []model.NetSecurityGroup
	findings []model.Diagnostic
}

type azSubnet struct {
	e      *entity // the subnet resource, or the VNet for inline subnets
	name   string
	cidrs  []netip.Prefix
	known  bool
	attr   string
	vnet   *entity
	inline bool
}

func buildOthers(w *world) *otherClouds {
	o := &otherClouds{}
	o.azure(w)
	o.gcp(w)
	return o
}

func prefixes(vals []string) ([]netip.Prefix, bool) {
	var out []netip.Prefix
	for _, s := range vals {
		p, ok := prefix(s)
		if !ok {
			return nil, false
		}
		out = append(out, p)
	}
	return out, true
}

func (o *otherClouds) azure(w *world) {
	vnets := w.ofType("azurerm_virtual_network")
	space := map[*entity][]netip.Prefix{}
	spaceKnown := map[*entity]bool{}
	var subs []*azSubnet
	for _, v := range vnets {
		vals, known := v.strs("address_space")
		ps, valid := prefixes(vals)
		space[v], spaceKnown[v] = ps, known && valid
		nn := model.NetNetwork{NodeID: v.nodeID(), Address: v.addr, Cloud: model.CloudAzure, Name: v.tagName(), CIDRs: []string{}}
		if spaceKnown[v] {
			for _, p := range ps {
				nn.CIDRs = append(nn.CIDRs, p.String())
			}
		}
		o.networks = append(o.networks, nn)
		blocks, _ := v.blocks("subnet")
		for i, b := range blocks {
			name, _ := mapStr(b, "name")
			vals, k := asStrings(b["address_prefixes"])
			if len(vals) == 0 {
				if s, ok := mapStr(b, "address_prefix"); ok && s != "" {
					vals = []string{s}
				}
			}
			ps, valid := prefixes(vals)
			subs = append(subs, &azSubnet{e: v, name: name, cidrs: ps, known: k && valid, attr: fmt.Sprintf("subnet[%d]", i), vnet: v, inline: true})
		}
	}
	for _, e := range w.ofType("azurerm_subnet") {
		vals, k := e.strs("address_prefixes")
		ps, valid := prefixes(vals)
		vn, _ := w.resolveAs(e.raw("virtual_network_name"), "azurerm_virtual_network")
		subs = append(subs, &azSubnet{e: e, name: e.addr, cidrs: ps, known: k && valid, attr: "address_prefixes", vnet: vn})
	}
	for _, s := range subs {
		ns := model.NetSubnet{NodeID: s.e.nodeID(), Address: s.e.addr, Name: s.name, Classification: model.SubnetUnknown,
			Why: "Public/private classification covers AWS subnets; Azure subnets reach the internet through their VNet's outbound rules."}
		if s.inline {
			ns.Address = s.e.addr + " subnet " + s.name
		}
		if s.vnet != nil {
			ns.Network = s.vnet.addr
		}
		if s.known && len(s.cidrs) > 0 {
			ns.CIDR = s.cidrs[0].String()
		}
		o.subnets = append(o.subnets, ns)
		if !s.known || s.vnet == nil || !spaceKnown[s.vnet] || len(space[s.vnet]) == 0 || s.e.multi {
			continue
		}
		for _, c := range s.cidrs {
			inside := false
			for _, sp := range space[s.vnet] {
				if contains(sp, c) {
					inside = true
				}
			}
			if !inside {
				o.findings = append(o.findings, finding(model.SeverityError, "net_subnet_outside_network", s.e, s.attr,
					fmt.Sprintf("Subnet %s (%s) is outside its virtual network %s (%s)", s.name, c, s.vnet.addr, cidrList(space[s.vnet])),
					"A subnet's address prefix must fall inside its virtual network's address space; Azure refuses it otherwise.",
					"Pick a prefix inside the VNet's address_space (cidrsubnet helps)."))
			}
		}
	}
	for i := range subs {
		for j := i + 1; j < len(subs); j++ {
			a, b := subs[i], subs[j]
			if a.vnet == nil || a.vnet != b.vnet || !a.known || !b.known {
				continue
			}
			for _, x := range a.cidrs {
				for _, y := range b.cidrs {
					if x.Overlaps(y) {
						o.findings = append(o.findings, finding(model.SeverityError, "net_subnet_overlap", b.e, b.attr,
							fmt.Sprintf("Subnets %s (%s) and %s (%s) overlap", a.name, x, b.name, y),
							"Subnets of a virtual network can't share addresses; Azure refuses overlapping prefixes.",
							"Give each subnet its own address prefix."))
					}
				}
			}
		}
	}
	for _, p := range w.ofType("azurerm_virtual_network_peering") {
		a, _ := w.resolveAs(p.raw("virtual_network_name"), "azurerm_virtual_network")
		b, _ := w.resolveAs(p.raw("remote_virtual_network_id"), "azurerm_virtual_network")
		if a == nil || b == nil || !spaceKnown[a] || !spaceKnown[b] {
			continue
		}
		for _, x := range space[a] {
			for _, y := range space[b] {
				if x.Overlaps(y) {
					o.findings = append(o.findings, finding(model.SeverityError, "net_peering_overlap", p, "remote_virtual_network_id",
						fmt.Sprintf("Peered virtual networks %s (%s) and %s (%s) overlap", a.addr, x, b.addr, y),
						"Azure can't peer virtual networks with overlapping address spaces.",
						"Give the VNets non-overlapping address spaces."))
				}
			}
		}
	}
	// NSG rules: priorities must be unique per direction.
	type nsgRule struct {
		e        *entity
		attr     string
		name     string
		priority int
		dir      string
		known    bool
		access   string
		ports    string
		source   string
		proto    string
	}
	rules := map[*entity][]nsgRule{}
	nsgs := w.ofType("azurerm_network_security_group")
	for _, g := range nsgs {
		blocks, _ := g.blocks("security_rule")
		for i, b := range blocks {
			pr, _, pk := mapNum(b, "priority")
			dir, dk := mapStr(b, "direction")
			name, _ := mapStr(b, "name")
			access, _ := mapStr(b, "access")
			port, _ := mapStr(b, "destination_port_range")
			src, _ := mapStr(b, "source_address_prefix")
			proto, _ := mapStr(b, "protocol")
			rules[g] = append(rules[g], nsgRule{e: g, attr: fmt.Sprintf("security_rule[%d].priority", i), name: name, priority: int(pr), dir: dir, known: pk && dk, access: access, ports: port, source: src, proto: proto})
		}
	}
	for _, r := range w.ofType("azurerm_network_security_rule") {
		g, _ := w.resolveAs(r.raw("network_security_group_name"), "azurerm_network_security_group")
		if g == nil {
			continue
		}
		pr, _, pk := r.number("priority")
		dir, dk := r.str("direction")
		access, _ := r.str("access")
		port, _ := r.str("destination_port_range")
		src, _ := r.str("source_address_prefix")
		proto, _ := r.str("protocol")
		rules[g] = append(rules[g], nsgRule{e: r, attr: "priority", name: r.addr, priority: int(pr), dir: dir, known: pk && dk, access: access, ports: port, source: src, proto: proto})
	}
	for _, g := range nsgs {
		seen := map[string]nsgRule{}
		sg := model.NetSecurityGroup{NodeID: g.nodeID(), Address: g.addr, Ingress: []model.NetRule{}, Egress: []model.NetRule{}}
		for _, r := range rules[g] {
			nr := model.NetRule{Protocol: strings.ToLower(nonEmpty(r.proto, "all")), Ports: nonEmpty(r.ports, "all"), Peer: peerName(r.source), Description: r.access + " (priority " + fmt.Sprint(r.priority) + ")", NodeID: r.e.nodeID()}
			if strings.EqualFold(r.dir, "Outbound") {
				sg.Egress = append(sg.Egress, nr)
			} else {
				sg.Ingress = append(sg.Ingress, nr)
			}
			if !r.known || r.e.multi {
				continue
			}
			k := strings.ToLower(r.dir) + "/" + fmt.Sprint(r.priority)
			if prev, ok := seen[k]; ok {
				o.findings = append(o.findings, finding(model.SeverityError, "net_rule_priority_duplicate", r.e, r.attr,
					fmt.Sprintf("Rules %s and %s of %s share priority %d (%s)", prev.name, r.name, g.addr, r.priority, r.dir),
					"Azure evaluates network security group rules by priority, which must be unique per direction; it refuses duplicates.",
					"Give each rule its own priority (100, 110, 120, ...)."))
				continue
			}
			seen[k] = r
		}
		sg.Summary = fmt.Sprintf("%d inbound and %d outbound rules", len(sg.Ingress), len(sg.Egress))
		o.sgs = append(o.sgs, sg)
	}
}

func peerName(s string) string {
	switch s {
	case "*", "Internet", "0.0.0.0/0":
		return "anywhere"
	case "":
		return "any"
	}
	return s
}

func (o *otherClouds) gcp(w *world) {
	type gsub struct {
		e      *entity
		ranges []netip.Prefix
		names  []string
		known  bool
		net    *entity
	}
	for _, n := range w.ofType("google_compute_network") {
		o.networks = append(o.networks, model.NetNetwork{NodeID: n.nodeID(), Address: n.addr, Cloud: model.CloudGCP, Name: n.tagName(), CIDRs: []string{}})
	}
	var subs []*gsub
	for _, e := range w.ofType("google_compute_subnetwork") {
		s := &gsub{e: e, known: true}
		s.net, _ = w.resolveAs(e.raw("network"), "google_compute_network")
		c, ok := e.str("ip_cidr_range")
		if p, valid := prefix(c); ok && valid {
			s.ranges = append(s.ranges, p)
			s.names = append(s.names, "primary")
		} else {
			s.known = false
		}
		sec, k := e.blocks("secondary_ip_range")
		if !k {
			s.known = false
		}
		for _, b := range sec {
			c, _ := mapStr(b, "ip_cidr_range")
			name, _ := mapStr(b, "range_name")
			if p, valid := prefix(c); valid {
				s.ranges = append(s.ranges, p)
				s.names = append(s.names, "secondary "+name)
			} else {
				s.known = false
			}
		}
		ns := model.NetSubnet{NodeID: e.nodeID(), Address: e.addr, Name: e.tagName(), Classification: model.SubnetUnknown,
			Why: "Public/private classification covers AWS subnets; on GCP, internet access depends on external IPs and Cloud NAT."}
		if s.net != nil {
			ns.Network = s.net.addr
		}
		if len(s.ranges) > 0 {
			ns.CIDR = s.ranges[0].String()
		}
		region, _ := e.str("region")
		ns.Zone = region
		o.subnets = append(o.subnets, ns)
		subs = append(subs, s)
	}
	for i := range subs {
		for j := i; j < len(subs); j++ {
			a, b := subs[i], subs[j]
			if a.net == nil || a.net != b.net || !a.known || !b.known || a.e.multi || b.e.multi {
				continue
			}
			for x := range a.ranges {
				start := 0
				if i == j {
					start = x + 1
				}
				for y := start; y < len(b.ranges); y++ {
					if a.ranges[x].Overlaps(b.ranges[y]) {
						o.findings = append(o.findings, finding(model.SeverityError, "net_subnet_overlap", b.e, "ip_cidr_range",
							fmt.Sprintf("Ranges %s %s (%s) and %s %s (%s) overlap", a.e.addr, a.names[x], a.ranges[x], b.e.addr, b.names[y], b.ranges[y]),
							"Primary and secondary ranges of the subnets in one VPC network must not overlap; GCP refuses them.",
							"Give each range its own CIDR."))
					}
				}
			}
		}
	}
	for _, f := range w.ofType("google_compute_firewall") {
		sg := model.NetSecurityGroup{NodeID: f.nodeID(), Address: f.addr, Ingress: []model.NetRule{}, Egress: []model.NetRule{}}
		dir, _ := f.str("direction")
		srcs, _ := f.strs("source_ranges")
		if dir == "EGRESS" {
			srcs, _ = f.strs("destination_ranges")
		}
		peerTxt := strings.Join(srcs, ", ")
		if slicesContains(srcs, "0.0.0.0/0") {
			peerTxt = "anywhere"
		}
		for _, kind := range []string{"allow", "deny"} {
			blocks, _ := f.blocks(kind)
			for _, b := range blocks {
				proto, _ := mapStr(b, "protocol")
				ports, _ := asStrings(b["ports"])
				nr := model.NetRule{Protocol: proto, Ports: nonEmpty(strings.Join(ports, ", "), "all"), Peer: nonEmpty(peerTxt, "any"), Description: kind, NodeID: f.nodeID()}
				if dir == "EGRESS" {
					sg.Egress = append(sg.Egress, nr)
				} else {
					sg.Ingress = append(sg.Ingress, nr)
				}
			}
		}
		sg.Summary = fmt.Sprintf("Firewall rule (%s): %d allow/deny entries", strings.ToLower(nonEmpty(dir, "INGRESS")), len(sg.Ingress)+len(sg.Egress))
		o.sgs = append(o.sgs, sg)
	}
}
