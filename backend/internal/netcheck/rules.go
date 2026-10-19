package netcheck

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// peer is the other side of a connection as rules see it.
type peer struct {
	internet bool
	addr     netip.Addr   // exact address (deployed instances)
	rng      netip.Prefix // otherwise: the subnet it lives in
	sgs      []*sg
	sgsKnown bool
	label    string
}

var internetPeer = peer{internet: true, label: "the internet", sgsKnown: true}

func (w *world) peerOf(wl *workload) peer {
	p := peer{sgs: wl.sgs, sgsKnown: wl.sgsKnown, label: wl.e.addr}
	if wl.privateIP.IsValid() {
		p.addr = wl.privateIP
	} else if len(wl.subnets) > 0 && wl.subnets[0].cidrKnown {
		p.rng = wl.subnets[0].cidr
	}
	return p
}

// representative returns an address standing for p in route lookups.
func (p peer) representative() (netip.Addr, bool) {
	switch {
	case p.internet:
		return internetAddr, true
	case p.addr.IsValid():
		return p.addr, true
	case p.rng.IsValid():
		return p.rng.Addr(), true
	}
	return netip.Addr{}, false
}

type match int

const (
	matchNo match = iota
	matchYes
	matchPartial // some internet addresses only
	matchUnknown
)

func cidrMatch(c netip.Prefix, p peer) match {
	switch {
	case p.internet:
		if c.Bits() == 0 && c.Addr().Is4() {
			return matchYes
		}
		if c.Addr().Is4() && !c.Addr().IsPrivate() && !c.Addr().IsLoopback() {
			return matchPartial
		}
		return matchNo
	case p.addr.IsValid():
		if c.Contains(p.addr) {
			return matchYes
		}
		return matchNo
	case p.rng.IsValid():
		if c.Bits() <= p.rng.Bits() && c.Contains(p.rng.Addr()) {
			return matchYes
		}
		if c.Overlaps(p.rng) {
			return matchUnknown
		}
		return matchNo
	}
	return matchUnknown
}

// sgVerdict evaluates security groups (stateful: only the direction that
// opens the connection matters). inbound: groups of the destination with p
// the source; outbound: groups of the source with p the destination.
type sgResult struct {
	verdict string
	reason  string
	rule    *sgRule
	group   *sg
}

func evalSG(groups []*sg, known bool, inbound bool, proto string, port int, p peer, service bool) sgResult {
	dir := "inbound"
	if !inbound {
		dir = "outbound"
	}
	if !known && len(groups) == 0 {
		return sgResult{verdict: model.HopUnknown, reason: "Its security groups are only known at deploy time."}
	}
	var partial *sgRule
	var partialG *sg
	unknownSeen := !known
	for _, g := range groups {
		rules := g.ingress
		if !inbound {
			rules = g.egress
		}
		if !g.complete {
			unknownSeen = true
		}
		for _, r := range rules {
			if !protoMatches(r.proto, proto) {
				continue
			}
			if proto != "icmp" && r.proto != "all" && (port < r.ports.from || port > r.ports.to) {
				continue
			}
			m := matchNo
			for _, c := range r.cidrs {
				switch cidrMatch(c, p) {
				case matchYes:
					m = matchYes
				case matchPartial:
					if m == matchNo {
						m = matchPartial
					}
				case matchUnknown:
					if m != matchYes {
						m = matchUnknown
					}
				}
			}
			if m != matchYes && !p.internet {
				for _, ref := range r.refs {
					for _, o := range p.sgs {
						if o == ref {
							m = matchYes
						}
					}
				}
				if r.self {
					for _, o := range p.sgs {
						if o == g {
							m = matchYes
						}
					}
				}
				if m == matchNo && (len(r.refs) > 0 || r.self) && !p.sgsKnown {
					m = matchUnknown
				}
			}
			if m != matchYes && r.prefixList {
				if service {
					m = matchYes
				} else if m == matchNo {
					m = matchUnknown
				}
			}
			if m != matchYes && r.unknown && m == matchNo {
				m = matchUnknown
			}
			switch m {
			case matchYes:
				return sgResult{verdict: model.HopAllow, reason: fmt.Sprintf("Rule allows %s %s %s %s.", dir, ruleDesc(r), dirWord(inbound), p.label), rule: r, group: g}
			case matchPartial:
				partial, partialG = r, g
			case matchUnknown:
				unknownSeen = true
			}
		}
	}
	if partial != nil {
		return sgResult{verdict: model.HopAllow, reason: fmt.Sprintf("Allowed only from %s (other internet addresses are blocked).", cidrList(partial.cidrs)), rule: partial, group: partialG}
	}
	if unknownSeen {
		return sgResult{verdict: model.HopUnknown, reason: "Some rules depend on values known only at deploy time."}
	}
	what := fmt.Sprintf("%s port %d", strings.ToUpper(proto), port)
	if proto == "icmp" {
		what = "ICMP (ping)"
	}
	if len(groups) == 0 {
		return sgResult{verdict: model.HopDeny, reason: fmt.Sprintf("No security group allows %s %s %s.", dir, what, dirWord(inbound)+" "+p.label)}
	}
	return sgResult{verdict: model.HopDeny, reason: fmt.Sprintf("No rule allows %s %s %s %s.", dir, what, dirWord(inbound), p.label)}
}

func dirWord(inbound bool) string {
	if inbound {
		return "from"
	}
	return "to"
}

func ruleDesc(r *sgRule) string {
	if r.proto == "all" {
		return "all traffic"
	}
	if r.proto == "icmp" {
		return "ICMP"
	}
	return fmt.Sprintf("%s %s", strings.ToUpper(r.proto), r.ports)
}

func cidrList(cs []netip.Prefix) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, ", ")
}

// naclResult is the outcome of a network ACL evaluation.
type naclResult struct {
	verdict string
	reason  string
	rule    *naclRule
	port    int
}

// evalNACL evaluates a stateless network ACL for every port of ports
// (rules are checked in rule-number order; the first match decides; no
// match is the implicit deny).
func evalNACL(n *nacl, egress bool, proto string, ports portRange, p peer) naclResult {
	if n == nil {
		return naclResult{verdict: model.HopUnknown, reason: "Its network ACL is only known at deploy time."}
	}
	if n.e == nil {
		return naclResult{verdict: model.HopAllow, reason: "The VPC's default network ACL allows all traffic."}
	}
	if !n.complete {
		return naclResult{verdict: model.HopUnknown, reason: "Some rules are only known at deploy time."}
	}
	rules := n.ingress
	if egress {
		rules = n.egress
	}
	samples := []int{ports.from, ports.to}
	for _, r := range rules {
		for _, x := range []int{r.ports.from - 1, r.ports.from, r.ports.to, r.ports.to + 1} {
			if x >= ports.from && x <= ports.to {
				samples = append(samples, x)
			}
		}
	}
	sort.Ints(samples)
	var unknownRes *naclResult
	for i, port := range samples {
		if i > 0 && samples[i-1] == port {
			continue
		}
		res := naclAt(rules, proto, port, p)
		switch res.verdict {
		case model.HopDeny:
			return res
		case model.HopUnknown:
			if unknownRes == nil {
				unknownRes = &res
			}
		}
	}
	if unknownRes != nil {
		return *unknownRes
	}
	r := naclAt(rules, proto, ports.from, p)
	if ports.from != ports.to {
		r.reason = fmt.Sprintf("Ports %s are allowed.", ports)
	}
	return r
}

func naclAt(rules []naclRule, proto string, port int, p peer) naclResult {
	for i := range rules {
		r := &rules[i]
		if !r.known {
			return naclResult{verdict: model.HopUnknown, reason: fmt.Sprintf("Rule %d is only known at deploy time.", r.num), rule: r}
		}
		if !protoMatches(r.proto, proto) {
			continue
		}
		if proto != "icmp" && r.proto != "all" && (port < r.ports.from || port > r.ports.to) {
			continue
		}
		switch cidrMatch(r.cidr, p) {
		case matchYes:
			if r.allow {
				return naclResult{verdict: model.HopAllow, reason: fmt.Sprintf("Rule %d allows it.", r.num), rule: r, port: port}
			}
			return naclResult{verdict: model.HopDeny, reason: fmt.Sprintf("Rule %d denies %s port %d %s.", r.num, strings.ToUpper(proto), port, peerPhrase(p)), rule: r, port: port}
		case matchUnknown:
			return naclResult{verdict: model.HopUnknown, reason: fmt.Sprintf("Rule %d may or may not cover %s.", r.num, p.label), rule: r}
		}
	}
	return naclResult{verdict: model.HopDeny, reason: fmt.Sprintf("No rule allows %s port %d %s, so the final \"*\" rule denies it.", strings.ToUpper(proto), port, peerPhrase(p)), port: port}
}

func peerPhrase(p peer) string {
	if p.internet {
		return "for the internet"
	}
	return "for " + p.label
}
