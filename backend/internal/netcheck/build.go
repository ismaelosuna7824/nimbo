package netcheck

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// world is the network model of one analysis (AWS part; Azure and GCP keep
// their own smaller models).
type world struct {
	basis model.NetworkBasis
	ents  []*entity
	byID  map[string]*entity // real IDs/ARNs (deployed)
	byAdr map[string]*entity

	vpcs     []*vpc
	subnets  []*subnet
	rts      []*routeTable
	gws      []*gateway
	nacls    []*nacl
	sgs      []*sg
	works    []*workload
	tgs      []*targetGroup
	peerings []*gateway

	vpcOf map[*entity]*vpc
	snOf  map[*entity]*subnet
	rtOf  map[*entity]*routeTable
	gwOf  map[*entity]*gateway
	aclOf map[*entity]*nacl
	sgOf  map[*entity]*sg
	tgOf  map[*entity]*targetGroup
	wOf   map[*entity]*workload

	// Some rule/route/association resources could not be attached (their
	// parent reference is unknown): every table/list of that kind may be
	// incomplete.
	routesIncomplete bool
	assocIncomplete  bool
	aclIncomplete    bool
	sgIncomplete     bool

	notes []string
}

type vpc struct {
	e          *entity
	cidrs      []netip.Prefix
	cidrsKnown bool
	mainRT     *routeTable
	defNACL    *nacl
	defSG      *sg
}

type subnet struct {
	e         *entity
	vpc       *vpc
	cidr      netip.Prefix
	cidrKnown bool
	az        string
	mapPublic bool
	mapKnown  bool
	rt        *routeTable
	rtKnown   bool
	acl       *nacl
	aclKnown  bool
}

type routeTable struct {
	e        *entity // nil: the implicit main route table of vpc
	vpc      *vpc
	routes   []*route
	main     bool
	subnets  []*subnet
	gateways bool // associated with a gateway (edge association)
	usedByVE bool // a gateway VPC endpoint adds routes to it
}

type route struct {
	e          *entity // node declaring it (aws_route or the table)
	dest       netip.Prefix
	destKnown  bool
	prefixList string
	kind       string
	target     *gateway
	targetE    *entity
	targetID   string
	attr       string
}

type gateway struct {
	e           *entity
	kind        string // internet-gateway | egress-only-gateway | nat-gateway | vpc-endpoint | peering | transit-gateway | vpn-gateway
	vpc         *vpc
	vpcKnown    bool
	peer        *vpc // peering
	subnet      *subnet
	public      bool // NAT: public connectivity
	hasEIP      bool
	service     string
	endpointTyp string // Gateway | Interface | GatewayLoadBalancer
	rts         []*routeTable
	subnets     []*subnet
	sgs         []*sg
	privateDNS  bool
}

type nacl struct {
	e        *entity // nil: implicit default NACL (allows everything)
	vpc      *vpc
	isDef    bool
	ingress  []naclRule
	egress   []naclRule
	complete bool
	subnets  []*subnet
}

type naclRule struct {
	num       int
	proto     string
	allow     bool
	cidr      netip.Prefix
	known     bool
	ports     portRange
	e         *entity
	icmpTypes bool
}

type sg struct {
	e        *entity // nil: implicit default security group of vpc
	vpc      *vpc
	vpcKnown bool
	ingress  []*sgRule
	egress   []*sgRule
	complete bool
	isDef    bool
}

type sgRule struct {
	e          *entity
	proto      string
	ports      portRange
	cidrs      []netip.Prefix
	refs       []*sg
	self       bool
	prefixList bool
	unknown    bool // a peer could not be resolved
	desc       string
}

type workload struct {
	e            *entity
	kind         string // instance | function | database | cache | load-balancer | container-service | kubernetes | interface | autoscaling
	subnets      []*subnet
	subnetsKnown bool
	sgs          []*sg
	sgsKnown     bool
	public       bool
	publicKnown  bool
	publicWhy    string
	ports        []int
	inVPC        bool
	inVPCKnown   bool
	privateIP    netip.Addr
	zones        []string
	needsEgress  bool
	// load balancers
	lbType    string
	listeners []*listener
	// databases
	engine string
	// subnet group (databases, caches) for the AZ check
	group *entity
}

type listener struct {
	e     *entity
	port  int
	proto string
	tgs   []*targetGroup
	known bool
}

type targetGroup struct {
	e        *entity
	port     int
	members  []*member
	complete bool
}

type member struct {
	w    *workload
	port int
}

// ---------------------------------------------------------------------------
// Resolution

func newWorld(basis model.NetworkBasis, ents []*entity) *world {
	w := &world{basis: basis, ents: ents, byID: map[string]*entity{}, byAdr: map[string]*entity{},
		vpcOf: map[*entity]*vpc{}, snOf: map[*entity]*subnet{}, rtOf: map[*entity]*routeTable{},
		gwOf: map[*entity]*gateway{}, aclOf: map[*entity]*nacl{}, sgOf: map[*entity]*sg{},
		tgOf: map[*entity]*targetGroup{}, wOf: map[*entity]*workload{}}
	for _, e := range ents {
		w.byAdr[e.addr] = e
		if e.id != "" && basis == model.NetworkBasisDeployed {
			w.byID[e.id] = e
			if arn, ok := e.attrs["arn"].(string); ok && arn != "" {
				w.byID[arn] = e
			}
		}
	}
	return w
}

// resolve maps a reference value to the entity it names. attr is the
// attribute a design placeholder reads ("" for id/arn). known is false when
// the value is unknown or names something outside the analysis (a data
// source, a module, a hard-coded ID of an existing resource).
func (w *world) resolve(v any) (e *entity, attr string, known bool) {
	s, ok := v.(string)
	if !ok {
		return nil, "", v == nil
	}
	if s == "" {
		return nil, "", true
	}
	if strings.HasPrefix(s, refPrefix) {
		addr, at, _ := strings.Cut(strings.TrimPrefix(s, refPrefix), "#")
		e := w.byAdr[addr]
		if e == nil {
			return nil, "", false
		}
		if at == "arn" || at == "self_link" || at == "name" && e.attrs["name"] == nil {
			at = ""
		}
		return e, at, true
	}
	if e := w.byID[s]; e != nil {
		return e, "", true
	}
	return nil, "", false
}

// resolveAs resolves v to an entity of one of types (id, arn or name).
func (w *world) resolveAs(v any, types ...string) (*entity, bool) {
	e, attr, known := w.resolve(v)
	if e != nil && attr == "" {
		for _, t := range types {
			if e.typ == t {
				return e, true
			}
		}
		return nil, false
	}
	if s, ok := v.(string); ok && s != "" && !strings.HasPrefix(s, refPrefix) {
		for _, c := range w.ents {
			for _, t := range types {
				if c.typ == t {
					if n, _ := c.attrs["name"].(string); n == s {
						return c, true
					}
				}
			}
		}
	}
	if e != nil {
		return nil, false
	}
	return nil, known && v == nil
}

func (w *world) ofType(types ...string) []*entity {
	var out []*entity
	for _, e := range w.ents {
		for _, t := range types {
			if e.typ == t {
				out = append(out, e)
			}
		}
	}
	return out
}

// vpcRef resolves a vpc_id value: the VPC, whether it is known.
func (w *world) vpcRef(v any) (*vpc, bool) {
	e, _ := w.resolveAs(v, "aws_vpc", "aws_default_vpc")
	if e == nil {
		return nil, false
	}
	return w.vpcOf[e], true
}

func (w *world) subnetRef(v any) *subnet {
	e, _ := w.resolveAs(v, "aws_subnet", "aws_default_subnet")
	if e == nil {
		return nil
	}
	return w.snOf[e]
}

// subnetsRef resolves a list of subnet references; known is false when an
// item is unknown or outside the analysis.
func (w *world) subnetsRef(v any) ([]*subnet, bool) {
	items, ok := asList(v)
	if !ok {
		return nil, false
	}
	var out []*subnet
	known := true
	for _, it := range items {
		s := w.subnetRef(it)
		if s == nil {
			known = false
			continue
		}
		out = append(out, s)
	}
	return out, known
}

func (w *world) sgsRef(v any) ([]*sg, bool) {
	items, ok := asList(v)
	if !ok {
		return nil, false
	}
	var out []*sg
	known := true
	for _, it := range items {
		e, _ := w.resolveAs(it, "aws_security_group", "aws_default_security_group")
		if e == nil {
			// the VPC's default security group by reference
			if r, attr, _ := w.resolve(it); r != nil && attr == "default_security_group_id" {
				if vp := w.vpcOf[r]; vp != nil {
					out = append(out, w.defaultSG(vp))
					continue
				}
			}
			if g := w.defaultSGByID(it); g != nil {
				out = append(out, g)
				continue
			}
			known = false
			continue
		}
		out = append(out, w.sgOf[e])
	}
	return out, known
}

func asList(v any) ([]any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case []any:
		return x, true
	case string:
		return []any{x}, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Build

func buildAWS(basis model.NetworkBasis, ents []*entity) *world {
	w := newWorld(basis, ents)
	w.buildVPCs()
	w.buildSubnets()
	w.buildGateways()
	w.buildRouteTables()
	w.buildNACLs()
	w.buildSGs()
	w.buildWorkloads()
	w.buildLoadBalancing()
	return w
}

func (w *world) buildVPCs() {
	for _, e := range w.ofType("aws_vpc", "aws_default_vpc") {
		v := &vpc{e: e, cidrsKnown: true}
		s, ok := e.str("cidr_block")
		if p, valid := prefix(s); ok && valid {
			v.cidrs = append(v.cidrs, p)
		} else if e.typ == "aws_vpc" {
			v.cidrsKnown = false
		}
		w.vpcs = append(w.vpcs, v)
		w.vpcOf[e] = v
	}
	for _, e := range w.ofType("aws_vpc_ipv4_cidr_block_association") {
		v, _ := w.vpcRef(e.raw("vpc_id"))
		if v == nil {
			continue
		}
		s, _ := e.str("cidr_block")
		if p, ok := prefix(s); ok {
			v.cidrs = append(v.cidrs, p)
		} else {
			v.cidrsKnown = false
		}
	}
}

func (w *world) buildSubnets() {
	for _, e := range w.ofType("aws_subnet", "aws_default_subnet") {
		s := &subnet{e: e, rtKnown: true, aclKnown: true}
		s.vpc, _ = w.vpcRef(e.raw("vpc_id"))
		if c, ok := e.str("cidr_block"); ok {
			s.cidr, s.cidrKnown = prefix(c)
		}
		s.az, _ = e.str("availability_zone")
		if strings.HasPrefix(s.az, refPrefix) {
			s.az = ""
		}
		if s.az == "" {
			if id, _ := e.str("availability_zone_id"); !strings.HasPrefix(id, refPrefix) {
				s.az = id
			}
		}
		var set bool
		s.mapPublic, set, s.mapKnown = e.boolean("map_public_ip_on_launch")
		if !set && e.typ == "aws_default_subnet" {
			s.mapPublic = true
		}
		w.subnets = append(w.subnets, s)
		w.snOf[e] = s
	}
}

func (w *world) buildGateways() {
	add := func(e *entity, kind string) *gateway {
		g := &gateway{e: e, kind: kind}
		g.vpc, g.vpcKnown = w.vpcRef(e.raw("vpc_id"))
		w.gws = append(w.gws, g)
		w.gwOf[e] = g
		return g
	}
	for _, e := range w.ofType("aws_internet_gateway") {
		add(e, "internet-gateway")
	}
	for _, e := range w.ofType("aws_internet_gateway_attachment") {
		ge, _ := w.resolveAs(e.raw("internet_gateway_id"), "aws_internet_gateway")
		if g := w.gwOf[ge]; g != nil && g.vpc == nil {
			g.vpc, g.vpcKnown = w.vpcRef(e.raw("vpc_id"))
		}
	}
	for _, e := range w.ofType("aws_egress_only_internet_gateway") {
		add(e, "egress-only-gateway")
	}
	for _, e := range w.ofType("aws_vpn_gateway") {
		add(e, "vpn-gateway")
	}
	for _, e := range w.ofType("aws_nat_gateway") {
		g := add(e, "nat-gateway")
		g.subnet = w.subnetRef(e.raw("subnet_id"))
		if g.subnet != nil {
			g.vpc, g.vpcKnown = g.subnet.vpc, g.subnet.vpc != nil
		}
		ct, _ := e.str("connectivity_type")
		g.public = ct == "" || ct == "public"
		alloc, _ := e.str("allocation_id")
		g.hasEIP = alloc != ""
	}
	for _, e := range w.ofType("aws_vpc_endpoint") {
		g := add(e, "vpc-endpoint")
		svc, _ := e.str("service_name")
		g.service = endpointService(svc)
		g.endpointTyp, _ = e.str("vpc_endpoint_type")
		if g.endpointTyp == "" {
			g.endpointTyp = "Gateway"
		}
		g.privateDNS, _, _ = e.boolean("private_dns_enabled")
		g.sgs, _ = w.sgsRef(e.raw("security_group_ids"))
		g.subnets, _ = w.subnetsRef(e.raw("subnet_ids"))
	}
	for _, e := range w.ofType("aws_vpc_peering_connection") {
		g := add(e, "peering")
		g.peer, _ = w.vpcRef(e.raw("peer_vpc_id"))
		w.peerings = append(w.peerings, g)
	}
	for _, e := range w.ofType("aws_ec2_transit_gateway_vpc_attachment") {
		g := add(e, "transit-gateway")
		g.subnets, _ = w.subnetsRef(e.raw("subnet_ids"))
	}
}

// endpointService turns "com.amazonaws.us-east-1.s3" into "s3".
func endpointService(name string) string {
	if strings.HasPrefix(name, refPrefix) {
		return ""
	}
	parts := strings.Split(name, ".")
	if len(parts) >= 4 && parts[0] == "com" {
		return strings.Join(parts[3:], ".")
	}
	return name
}

// peered reports whether a and b are connected by a peering connection.
func (w *world) peered(a, b *vpc) bool {
	for _, p := range w.peerings {
		if p.vpc == a && p.peer == b || p.vpc == b && p.peer == a {
			return true
		}
	}
	return false
}

func (w *world) implicitMainRT(v *vpc) *routeTable {
	if v.mainRT == nil {
		v.mainRT = &routeTable{vpc: v, main: true}
		w.rts = append(w.rts, v.mainRT)
	}
	return v.mainRT
}

// defaultRef reports which VPC default (main route table, default NACL,
// default SG) a reference names: design placeholders read the VPC's
// attribute; deployed IDs match the VPC's state values.
func (w *world) defaultRef(v any, attrs ...string) *vpc {
	if r, attr, _ := w.resolve(v); r != nil {
		for _, a := range attrs {
			if attr == a {
				return w.vpcOf[r]
			}
		}
		return nil
	}
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	for _, vp := range w.vpcs {
		for _, a := range attrs {
			if id, _ := vp.e.attrs[a].(string); id == s {
				return vp
			}
		}
	}
	return nil
}

func (w *world) buildRouteTables() {
	for _, e := range w.ofType("aws_route_table", "aws_default_route_table") {
		rt := &routeTable{e: e}
		if e.typ == "aws_default_route_table" {
			rt.vpc = w.defaultRef(e.raw("default_route_table_id"), "default_route_table_id", "main_route_table_id")
			if rt.vpc == nil {
				rt.vpc, _ = w.vpcRef(e.raw("vpc_id"))
			}
			if rt.vpc != nil {
				rt.main = true
				rt.vpc.mainRT = rt
			}
		} else {
			rt.vpc, _ = w.vpcRef(e.raw("vpc_id"))
		}
		routes, known := e.blocks("route")
		if !known {
			w.routesIncomplete = true
		}
		for _, r := range routes {
			rt.addRoute(w, e, r, "route")
		}
		w.rts = append(w.rts, rt)
		w.rtOf[e] = rt
	}
	// Deployed: a VPC's main route table not managed by Terraform.
	for _, e := range w.ofType("aws_route") {
		rt := w.routeTableRef(e.raw("route_table_id"))
		if rt == nil {
			w.routesIncomplete = true
			continue
		}
		rt.addRoute(w, e, e.attrs, "")
	}
	for _, e := range w.ofType("aws_main_route_table_association") {
		v, _ := w.vpcRef(e.raw("vpc_id"))
		rt := w.routeTableRef(e.raw("route_table_id"))
		if v == nil || rt == nil {
			w.assocIncomplete = true
			continue
		}
		rt.main = true
		v.mainRT = rt
	}
	for _, e := range w.ofType("aws_route_table_association") {
		rt := w.routeTableRef(e.raw("route_table_id"))
		if rt == nil {
			w.assocIncomplete = true
			continue
		}
		if gw, _ := e.str("gateway_id"); gw != "" {
			rt.gateways = true
			continue
		}
		s := w.subnetRef(e.raw("subnet_id"))
		if s == nil {
			w.assocIncomplete = true
			continue
		}
		s.rt = rt
		rt.subnets = append(rt.subnets, s)
	}
	// Gateway VPC endpoints add routes to their route tables.
	for _, e := range w.ofType("aws_vpc_endpoint_route_table_association") {
		ge, _ := w.resolveAs(e.raw("vpc_endpoint_id"), "aws_vpc_endpoint")
		rt := w.routeTableRef(e.raw("route_table_id"))
		if g := w.gwOf[ge]; g != nil && rt != nil {
			g.rts = append(g.rts, rt)
		}
	}
	for _, g := range w.gws {
		if g.kind != "vpc-endpoint" {
			continue
		}
		ids, _ := asList(g.e.raw("route_table_ids"))
		for _, id := range ids {
			if rt := w.routeTableRef(id); rt != nil {
				g.rts = append(g.rts, rt)
			}
		}
		for _, rt := range g.rts {
			rt.usedByVE = true
		}
	}
	for _, s := range w.subnets {
		if s.rt != nil {
			continue
		}
		if s.vpc == nil {
			s.rtKnown = false
			continue
		}
		if w.assocIncomplete {
			s.rtKnown = false
		}
		s.rt = w.implicitMainRT(s.vpc)
		s.rt.subnets = append(s.rt.subnets, s)
	}
}

func (w *world) routeTableRef(v any) *routeTable {
	if e, _ := w.resolveAs(v, "aws_route_table", "aws_default_route_table"); e != nil {
		return w.rtOf[e]
	}
	if vp := w.defaultRef(v, "default_route_table_id", "main_route_table_id"); vp != nil {
		return w.implicitMainRT(vp)
	}
	return nil
}

var routeTargets = []struct{ attr, kind string }{
	{"gateway_id", ""}, {"nat_gateway_id", "nat-gateway"}, {"egress_only_gateway_id", "egress-only-gateway"},
	{"vpc_endpoint_id", "vpc-endpoint"}, {"vpc_peering_connection_id", "peering"},
	{"transit_gateway_id", "transit-gateway"}, {"network_interface_id", "network-interface"},
	{"instance_id", "instance"}, {"local_gateway_id", "local-gateway"}, {"carrier_gateway_id", "carrier-gateway"},
	{"core_network_arn", "core-network"},
}

func (rt *routeTable) addRoute(w *world, e *entity, m map[string]any, path string) {
	r := &route{e: e, kind: "unknown", attr: path}
	dest, dk := mapStr(m, "destination_cidr_block")
	if dest == "" {
		dest, dk = mapStr(m, "cidr_block")
	}
	if dest == "" {
		dest, dk = mapStr(m, "destination_ipv6_cidr_block")
		if dest == "" {
			dest, dk = mapStr(m, "ipv6_cidr_block")
		}
	}
	if dest != "" {
		r.dest, r.destKnown = prefix(dest)
	} else if pl, _ := mapStr(m, "destination_prefix_list_id"); pl != "" {
		r.prefixList = pl
	} else if !dk {
		r.destKnown = false
	}
	for _, t := range routeTargets {
		v := m[t.attr]
		if s, ok := v.(string); ok && s == "" || v == nil {
			continue
		}
		r.targetID, _ = v.(string)
		if r.targetID == "local" {
			r.kind = "local"
			break
		}
		te, _, _ := w.resolve(v)
		r.targetE = te
		if g := w.gwOf[te]; g != nil {
			r.target = g
			r.kind = g.kind
		} else if t.kind != "" {
			r.kind = t.kind
		} else {
			switch {
			case strings.HasPrefix(r.targetID, "igw-"):
				r.kind = "internet-gateway"
			case strings.HasPrefix(r.targetID, "vgw-"):
				r.kind = "vpn-gateway"
			case strings.HasPrefix(r.targetID, "vpce-"):
				r.kind = "vpc-endpoint"
			}
		}
		break
	}
	// Deployed tables list routes added by aws_route too: keep one.
	for _, o := range rt.routes {
		if o.dest == r.dest && o.prefixList == r.prefixList && o.kind == r.kind && (o.targetID == r.targetID || o.target == r.target && r.target != nil) {
			if o.e.typ != "aws_route" && e.typ == "aws_route" {
				o.e, o.attr = e, ""
			}
			return
		}
	}
	rt.routes = append(rt.routes, r)
}

// lookup finds the route traffic to addr takes: longest prefix match, the
// VPC's local route included. ok is false when the table may be
// incomplete and nothing matched.
func (rt *routeTable) lookup(w *world, addr netip.Addr) (r *route, ok bool) {
	best := -1
	if rt.vpc != nil {
		for _, c := range rt.vpc.cidrs {
			if c.Contains(addr) && c.Bits() > best {
				best = c.Bits()
				r = &route{dest: c, destKnown: true, kind: "local"}
			}
		}
	}
	unknownDest := false
	for _, x := range rt.routes {
		if !x.destKnown {
			if x.prefixList == "" {
				unknownDest = true
			}
			continue
		}
		if x.dest.Addr().Is4() == addr.Is4() && x.dest.Contains(addr) && x.dest.Bits() > best {
			best = x.dest.Bits()
			r = x
		}
	}
	if unknownDest || w.routesIncomplete {
		// a route we can't place might be more specific
		if r == nil || r.dest.Bits() < 32 && unknownDest {
			return r, false
		}
	}
	return r, true
}

// defaultRoute returns the route for internet traffic (0.0.0.0/0).
func (rt *routeTable) defaultRoute(w *world) (*route, bool) { return rt.lookup(w, internetAddr) }

// internetAddr stands for "an address on the internet" in route lookups
// (TEST-NET-2, never routed by a VPC).
var internetAddr = netip.MustParseAddr("198.51.100.10")

func (w *world) buildNACLs() {
	for _, e := range w.ofType("aws_network_acl", "aws_default_network_acl") {
		n := &nacl{e: e, complete: true}
		if e.typ == "aws_default_network_acl" {
			n.vpc = w.defaultRef(e.raw("default_network_acl_id"), "default_network_acl_id")
			n.isDef = true
			if n.vpc != nil {
				n.vpc.defNACL = n
			}
		} else {
			n.vpc, _ = w.vpcRef(e.raw("vpc_id"))
		}
		for _, dir := range []string{"ingress", "egress"} {
			blocks, known := e.blocks(dir)
			if !known {
				n.complete = false
			}
			for _, b := range blocks {
				r := naclRuleFrom(b, e, "rule_no", "action")
				if dir == "ingress" {
					n.ingress = append(n.ingress, r)
				} else {
					n.egress = append(n.egress, r)
				}
			}
		}
		ids, known := asList(e.raw("subnet_ids"))
		if !known {
			w.aclIncomplete = true
		}
		for _, id := range ids {
			if s := w.subnetRef(id); s != nil {
				w.attachACL(s, n)
			} else {
				w.aclIncomplete = true
			}
		}
		w.nacls = append(w.nacls, n)
		w.aclOf[e] = n
	}
	for _, e := range w.ofType("aws_network_acl_rule") {
		n := w.naclRef(e.raw("network_acl_id"))
		if n == nil {
			w.aclIncomplete = true
			continue
		}
		r := naclRuleFrom(e.attrs, e, "rule_number", "rule_action")
		if eg, _, _ := e.boolean("egress"); eg {
			n.egress = append(n.egress, r)
		} else {
			n.ingress = append(n.ingress, r)
		}
	}
	for _, e := range w.ofType("aws_network_acl_association") {
		n := w.naclRef(e.raw("network_acl_id"))
		s := w.subnetRef(e.raw("subnet_id"))
		if n == nil || s == nil {
			w.aclIncomplete = true
			continue
		}
		w.attachACL(s, n)
	}
	for _, n := range w.nacls {
		sortRules(n.ingress)
		sortRules(n.egress)
	}
	for _, s := range w.subnets {
		if s.acl != nil {
			continue
		}
		if s.vpc == nil {
			s.aclKnown = false
			continue
		}
		if w.aclIncomplete {
			s.aclKnown = false
		}
		if s.vpc.defNACL == nil {
			s.vpc.defNACL = &nacl{vpc: s.vpc, isDef: true, complete: true}
			w.nacls = append(w.nacls, s.vpc.defNACL)
		}
		w.attachACL(s, s.vpc.defNACL)
	}
}

func (w *world) attachACL(s *subnet, n *nacl) {
	for _, o := range n.subnets {
		if o == s {
			return
		}
	}
	if s.acl != nil && s.acl != n {
		// replace (an explicit association wins over the default)
		if !s.acl.isDef {
			return
		}
		for i, o := range s.acl.subnets {
			if o == s {
				s.acl.subnets = append(s.acl.subnets[:i], s.acl.subnets[i+1:]...)
				break
			}
		}
	}
	s.acl = n
	n.subnets = append(n.subnets, s)
}

func (w *world) naclRef(v any) *nacl {
	if e, _ := w.resolveAs(v, "aws_network_acl", "aws_default_network_acl"); e != nil {
		return w.aclOf[e]
	}
	if vp := w.defaultRef(v, "default_network_acl_id"); vp != nil {
		if vp.defNACL == nil {
			vp.defNACL = &nacl{vpc: vp, isDef: true, complete: true}
			w.nacls = append(w.nacls, vp.defNACL)
		}
		return vp.defNACL
	}
	return nil
}

func naclRuleFrom(m map[string]any, e *entity, numKey, actionKey string) naclRule {
	r := naclRule{e: e, known: true}
	n, _, nk := mapNum(m, numKey)
	r.num = int(n)
	action, ak := mapStr(m, actionKey)
	r.allow = strings.EqualFold(action, "allow")
	proto, pk := mapStr(m, "protocol")
	r.proto = normProto(proto)
	cidr, ck := mapStr(m, "cidr_block")
	if cidr == "" {
		cidr, ck = mapStr(m, "ipv6_cidr_block")
	}
	var valid bool
	r.cidr, valid = prefix(cidr)
	from, _, fk := mapNum(m, "from_port")
	to, _, tk := mapNum(m, "to_port")
	r.ports = portRange{int(from), int(to)}
	if r.proto == "all" || r.proto == "icmp" || r.proto == "icmpv6" {
		r.ports = allPorts
	}
	r.known = nk && ak && pk && ck && valid && fk && tk
	return r
}

func sortRules(rs []naclRule) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].num < rs[j].num })
}

func (w *world) buildSGs() {
	for _, e := range w.ofType("aws_security_group", "aws_default_security_group") {
		g := &sg{e: e, complete: true, isDef: e.typ == "aws_default_security_group"}
		g.vpc, g.vpcKnown = w.vpcRef(e.raw("vpc_id"))
		if g.isDef && g.vpc != nil {
			g.vpc.defSG = g
		}
		w.sgs = append(w.sgs, g)
		w.sgOf[e] = g
	}
	for _, g := range w.sgs {
		for _, dir := range []string{"ingress", "egress"} {
			blocks, known := g.e.blocks(dir)
			if !known {
				g.complete = false
			}
			for _, b := range blocks {
				r := w.inlineSGRule(g, b)
				if dir == "ingress" {
					g.ingress = append(g.ingress, r)
				} else {
					g.egress = append(g.egress, r)
				}
			}
		}
	}
	for _, e := range w.ofType("aws_security_group_rule") {
		g := w.sgRef(e.raw("security_group_id"))
		if g == nil {
			w.sgIncomplete = true
			continue
		}
		r := w.inlineSGRule(g, e.attrs)
		r.e = e
		if src := e.raw("source_security_group_id"); src != nil {
			if o := w.sgRef(src); o != nil {
				r.refs = append(r.refs, o)
			} else {
				r.unknown = true
			}
		}
		if t, _ := e.str("type"); t == "egress" {
			g.egress = append(g.egress, r)
		} else {
			g.ingress = append(g.ingress, r)
		}
	}
	for _, e := range w.ofType("aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule") {
		g := w.sgRef(e.raw("security_group_id"))
		if g == nil {
			w.sgIncomplete = true
			continue
		}
		r := &sgRule{e: e}
		p, _ := e.str("ip_protocol")
		r.proto = normProto(p)
		r.ports = rulePorts(e.attrs, r.proto)
		r.desc, _ = e.str("description")
		for _, k := range []string{"cidr_ipv4", "cidr_ipv6"} {
			if c, ok := e.str(k); ok && c != "" {
				if p, valid := prefix(c); valid {
					r.cidrs = append(r.cidrs, p)
				} else {
					r.unknown = true
				}
			} else if !ok {
				r.unknown = true
			}
		}
		if ref := e.raw("referenced_security_group_id"); ref != nil {
			if o := w.sgRef(ref); o != nil {
				r.refs = append(r.refs, o)
			} else {
				r.unknown = true
			}
		}
		if pl, _ := e.str("prefix_list_id"); pl != "" {
			r.prefixList = true
		}
		if e.typ == "aws_vpc_security_group_egress_rule" {
			g.egress = append(g.egress, r)
		} else {
			g.ingress = append(g.ingress, r)
		}
	}
	if w.sgIncomplete {
		for _, g := range w.sgs {
			g.complete = false
		}
	}
}

func (w *world) sgRef(v any) *sg {
	if e, _ := w.resolveAs(v, "aws_security_group", "aws_default_security_group"); e != nil {
		return w.sgOf[e]
	}
	if vp := w.defaultRef(v, "default_security_group_id"); vp != nil {
		return w.defaultSG(vp)
	}
	return nil
}

func (w *world) defaultSGByID(v any) *sg {
	if vp := w.defaultRef(v, "default_security_group_id"); vp != nil {
		return w.defaultSG(vp)
	}
	return nil
}

// defaultSG returns the VPC's default security group: managed through
// aws_default_security_group, or AWS's default rules (inbound from itself,
// all outbound).
func (w *world) defaultSG(v *vpc) *sg {
	if v.defSG == nil {
		g := &sg{vpc: v, vpcKnown: true, complete: true, isDef: true}
		g.ingress = []*sgRule{{proto: "all", ports: allPorts, self: true, desc: "AWS default: traffic from the same security group"}}
		g.egress = []*sgRule{{proto: "all", ports: allPorts, cidrs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, desc: "AWS default: all outbound"}}
		v.defSG = g
		w.sgs = append(w.sgs, g)
	}
	return v.defSG
}

func rulePorts(m map[string]any, proto string) portRange {
	if proto == "all" || proto == "icmp" || proto == "icmpv6" {
		return allPorts
	}
	from, fs, _ := mapNum(m, "from_port")
	to, ts, _ := mapNum(m, "to_port")
	if !fs && !ts {
		return allPorts
	}
	if !ts {
		to = from
	}
	return portRange{int(from), int(to)}
}

func (w *world) inlineSGRule(g *sg, m map[string]any) *sgRule {
	r := &sgRule{}
	p, _ := mapStr(m, "protocol")
	r.proto = normProto(p)
	r.ports = rulePorts(m, r.proto)
	r.desc, _ = mapStr(m, "description")
	for _, k := range []string{"cidr_blocks", "ipv6_cidr_blocks"} {
		items, known := asStrings(m[k])
		if !known {
			r.unknown = true
		}
		for _, c := range items {
			if p, ok := prefix(c); ok {
				r.cidrs = append(r.cidrs, p)
			} else {
				r.unknown = true
			}
		}
	}
	refs, known := asList(m["security_groups"])
	if !known {
		r.unknown = true
	}
	for _, ref := range refs {
		if o := w.sgRef(ref); o != nil {
			r.refs = append(r.refs, o)
		} else {
			r.unknown = true
		}
	}
	if self, _, _ := mapBool(m, "self"); self {
		r.self = true
	}
	if pls, _ := asStrings(m["prefix_list_ids"]); len(pls) > 0 {
		r.prefixList = true
	}
	return r
}
