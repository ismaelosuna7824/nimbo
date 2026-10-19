package netcheck

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ErrBadRequest wraps reachability requests that can't be answered
// (unknown node, nothing network-related, invalid protocol/port).
var ErrBadRequest = errors.New("bad reachability request")

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, args...))
}

// awsServices maps resource types that live outside any VPC (reached
// through public AWS APIs, VPC endpoints or a NAT) to their endpoint
// service name and a display name.
var awsServices = map[string][2]string{
	"aws_s3_bucket":                        {"s3", "Amazon S3"},
	"aws_dynamodb_table":                   {"dynamodb", "DynamoDB"},
	"aws_sqs_queue":                        {"sqs", "Amazon SQS"},
	"aws_sns_topic":                        {"sns", "Amazon SNS"},
	"aws_secretsmanager_secret":            {"secretsmanager", "Secrets Manager"},
	"aws_ssm_parameter":                    {"ssm", "Systems Manager Parameter Store"},
	"aws_kms_key":                          {"kms", "AWS KMS"},
	"aws_ecr_repository":                   {"ecr.dkr", "Amazon ECR"},
	"aws_cloudwatch_log_group":             {"logs", "CloudWatch Logs"},
	"aws_kinesis_stream":                   {"kinesis-streams", "Kinesis"},
	"aws_sfn_state_machine":                {"states", "Step Functions"},
	"aws_cloudwatch_event_bus":             {"events", "EventBridge"},
	"aws_apigatewayv2_api":                 {"execute-api", "API Gateway"},
	"aws_api_gateway_rest_api":             {"execute-api", "API Gateway"},
	"aws_lambda_function_url":              {"lambda-url", "Lambda function URL"},
	"aws_cognito_user_pool":                {"cognito-idp", "Cognito"},
	"aws_elasticsearch_domain":             {"es", "OpenSearch"},
	"aws_opensearch_domain":                {"es", "OpenSearch"},
	"aws_kinesis_firehose_delivery_stream": {"kinesis-firehose", "Firehose"},
}

// end is one resolved side of a reachability question.
type end struct {
	internet bool
	w        *workload
	svc      string // AWS service endpoint name
	svcLabel string
	svcE     *entity
	label    string
	nodeID   string
}

type path struct {
	w           *world
	hops        []model.ReachabilityHop
	blockers    []string
	suggestions []string
}

func (p *path) add(component, layer, nodeID, verdict, reason string, fix ...string) {
	p.hops = append(p.hops, model.ReachabilityHop{Component: component, Layer: layer, NodeID: nodeID, Verdict: verdict, Reason: reason})
	if verdict == model.HopDeny {
		p.blockers = append(p.blockers, component+": "+reason)
		for _, f := range fix {
			if f != "" && !slices.Contains(p.suggestions, f) {
				p.suggestions = append(p.suggestions, f)
			}
		}
	}
}

func (p *path) denied() bool {
	for _, h := range p.hops {
		if h.Verdict == model.HopDeny {
			return true
		}
	}
	return false
}

// Reachability answers whether traffic can flow from req.Source to
// req.Destination (AWS semantics: stateful security groups, stateless
// network ACLs with ephemeral return ports, longest-prefix route lookup,
// internet gateways need a public IP, NAT for private egress, VPC
// endpoints for AWS services, load balancer listeners forwarding to their
// targets).
func (a *Analysis) Reachability(req model.ReachabilityRequest) (model.ReachabilityResult, error) {
	proto := strings.ToLower(req.Protocol)
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" && proto != "icmp" {
		return model.ReachabilityResult{}, badRequest("protocol must be tcp, udp or icmp")
	}
	if proto != "icmp" && (req.Port < 1 || req.Port > 65535) {
		return model.ReachabilityResult{}, badRequest("port must be between 1 and 65535")
	}
	src, err := a.resolveEnd(req.Source, "source")
	if err != nil {
		return model.ReachabilityResult{}, err
	}
	dst, err := a.resolveEnd(req.Destination, "destination")
	if err != nil {
		return model.ReachabilityResult{}, err
	}
	if src.internet && dst.internet {
		return model.ReachabilityResult{}, badRequest("source and destination can't both be the internet")
	}
	if !src.internet && !dst.internet && src.nodeID == dst.nodeID && src.label == dst.label {
		return model.ReachabilityResult{}, badRequest("source and destination are the same resource")
	}
	if src.svc != "" {
		return model.ReachabilityResult{}, badRequest("%s is an AWS service: it doesn't open connections into your network; pick it as the destination", src.label)
	}
	p := &path{w: a.aws}
	port := req.Port
	if proto == "icmp" {
		port = 0
	}
	switch {
	case dst.svc != "":
		a.toService(p, src, dst, proto)
	case src.internet:
		a.fromInternet(p, dst.w, proto, port, internetPeer, "")
	case dst.internet:
		a.toInternet(p, src.w, proto, port)
	default:
		a.between(p, src.w, dst.w, proto, port)
	}
	res := model.ReachabilityResult{Basis: a.basis, Path: p.hops, Blockers: nonNilStrings(p.blockers), Suggestions: nonNilStrings(p.suggestions)}
	res.Verdict = model.ReachYes
	for _, h := range p.hops {
		if h.Verdict == model.HopDeny {
			res.Verdict = model.ReachNo
			break
		}
		if h.Verdict == model.HopUnknown {
			res.Verdict = model.ReachUnknown
		}
	}
	res.Reachable = res.Verdict == model.ReachYes
	what := fmt.Sprintf("%s port %d", strings.ToUpper(proto), req.Port)
	if proto == "icmp" {
		what = "ping (ICMP)"
	}
	switch res.Verdict {
	case model.ReachYes:
		res.Summary = fmt.Sprintf("%s can reach %s on %s.", capital(src.label), dst.label, what)
	case model.ReachNo:
		res.Summary = fmt.Sprintf("%s can't reach %s on %s.", capital(src.label), dst.label, what)
	default:
		res.Summary = fmt.Sprintf("Nimbo can't tell yet whether %s can reach %s on %s: some values are only known after deploying.", src.label, dst.label, what)
	}
	return res, nil
}

// capital capitalizes prose labels ("the internet"), not addresses.
func capital(s string) string {
	if !strings.HasPrefix(s, "the ") {
		return s
	}
	return "The " + s[4:]
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *Analysis) resolveEnd(ep model.NetEndpoint, role string) (end, error) {
	switch ep.Kind {
	case "internet":
		return end{internet: true, label: "the internet"}, nil
	case "node":
	default:
		return end{}, badRequest("%s kind must be internet or node", role)
	}
	if ep.NodeID == "" {
		return end{}, badRequest("%s nodeId is required", role)
	}
	var node *model.Node
	for i := range a.wf.Nodes {
		n := &a.wf.Nodes[i]
		if n.ID == ep.NodeID || addrOf(n) == ep.NodeID || strings.HasPrefix(ep.NodeID, addrOf(n)+"[") {
			node = n
			break
		}
	}
	if node == nil {
		return end{}, badRequest("%s %q is not a node of the workflow", role, ep.NodeID)
	}
	if a.aws == nil {
		return end{}, badRequest("reachability analysis covers AWS networks")
	}
	var candidates []*workload
	for _, wl := range a.aws.works {
		if wl.e.node == node {
			candidates = append(candidates, wl)
		}
	}
	for _, wl := range candidates {
		if wl.e.addr == ep.NodeID {
			return end{w: wl, label: wl.e.addr, nodeID: node.ID}, nil
		}
	}
	if len(candidates) > 0 {
		return end{w: candidates[0], label: candidates[0].e.addr, nodeID: node.ID}, nil
	}
	if svc, ok := awsServices[node.Type]; ok {
		return end{svc: svc[0], svcLabel: svc[1], label: addrOf(node) + " (" + svc[1] + ")", nodeID: node.ID}, nil
	}
	return end{}, badRequest("%s %s is not something traffic flows to or from: pick an instance, function, database, cache, load balancer, ECS service or EKS cluster (or an AWS service such as an S3 bucket as destination)", role, addrOf(node))
}

// firstSubnet returns the subnet a workload is analyzed through.
func (p *path) firstSubnet(wl *workload, role string) *subnet {
	if len(wl.subnets) == 0 {
		reason := "Its subnet is only known at deploy time."
		if wl.subnetsKnown {
			reason = "It is not placed in any subnet of this workflow (default VPC or a subnet created elsewhere)."
		}
		p.add(wl.e.addr, "endpoint", wl.e.nodeID(), model.HopUnknown, reason)
		return nil
	}
	return wl.subnets[0]
}

func subnetNote(wl *workload) string {
	if len(wl.subnets) > 1 {
		return fmt.Sprintf(" (checked through %s, the first of its %d subnets)", wl.subnets[0].e.addr, len(wl.subnets))
	}
	return ""
}

// serviceHop checks that the destination listens on the port.
func (p *path) serviceHop(wl *workload, proto string, port int) {
	switch wl.kind {
	case "database", "cache":
		if len(wl.ports) == 0 {
			p.add(wl.e.addr, "service", wl.e.nodeID(), model.HopUnknown, "Its port is only known at deploy time.")
			return
		}
		if proto != "tcp" || wl.ports[0] != port {
			p.add(wl.e.addr, "service", wl.e.nodeID(), model.HopDeny, fmt.Sprintf("It listens on TCP port %d, not %s %d.", wl.ports[0], strings.ToUpper(proto), port),
				fmt.Sprintf("Connect to port %d (the %s port of %s).", wl.ports[0], wl.engine, wl.e.addr))
			return
		}
		p.add(wl.e.addr, "service", wl.e.nodeID(), model.HopAllow, fmt.Sprintf("The %s endpoint listens on port %d.", nonEmpty(wl.engine, wl.kind), port))
	case "instance", "autoscaling", "container-service", "interface":
		p.add(wl.e.addr, "service", wl.e.nodeID(), model.HopAllow, fmt.Sprintf("The network lets the traffic in; something on it must listen on port %d.", port))
	case "kubernetes":
		p.add(wl.e.addr, "service", wl.e.nodeID(), model.HopAllow, "The Kubernetes API endpoint listens on 443.")
	}
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func isFunction(wl *workload) bool { return wl.kind == "function" }

func (p *path) functionDest(wl *workload) {
	p.add(wl.e.addr, "endpoint", wl.e.nodeID(), model.HopDeny, "Lambda functions don't accept network connections: they run when invoked.",
		"Invoke the function through the Lambda API, a function URL (aws_lambda_function_url) or API Gateway, or put a load balancer in front of it.")
}

// ---------------------------------------------------------------------------
// internet -> workload

func (a *Analysis) fromInternet(p *path, dst *workload, proto string, port int, src peer, srcLabel string) {
	w := a.aws
	if srcLabel == "" {
		p.add("Internet", "endpoint", "", model.HopAllow, "The connection starts from an address on the internet.")
	}
	if isFunction(dst) {
		p.functionDest(dst)
		return
	}
	// public address
	switch {
	case !dst.publicKnown:
		p.add("Public address of "+dst.e.addr, "address", dst.e.nodeID(), model.HopUnknown, dst.publicWhy)
	case !dst.public:
		fix := "Give it a public IP (associate_public_ip_address = true in a public subnet, or an Elastic IP), or put an internet-facing load balancer in front of it."
		switch dst.kind {
		case "database":
			fix = "Keep databases private and connect from inside the VPC; only for a demo set publicly_accessible = true and use public subnets."
		case "load-balancer":
			fix = "Set internal = false to make the load balancer internet-facing."
		case "cache":
			fix = "Caches can't be public: connect from a resource inside the VPC."
		}
		p.add("Public address of "+dst.e.addr, "address", dst.e.nodeID(), model.HopDeny, "It has no public address. "+dst.publicWhy, fix)
	default:
		p.add("Public address of "+dst.e.addr, "address", dst.e.nodeID(), model.HopAllow, dst.publicWhy)
	}
	s := p.firstSubnet(dst, "destination")
	if s == nil {
		return
	}
	// internet gateway attached to the VPC
	var igw *gateway
	igwUnknown := false
	for _, g := range w.gws {
		if g.kind == "internet-gateway" {
			if g.vpc == s.vpc && s.vpc != nil {
				igw = g
			} else if !g.vpcKnown {
				igwUnknown = true
			}
		}
	}
	vpcName := "its VPC"
	if s.vpc != nil {
		vpcName = s.vpc.e.addr
	}
	switch {
	case igw != nil:
		p.add("Internet gateway "+igw.e.addr, "gateway", igw.e.nodeID(), model.HopAllow, "Attached to "+vpcName+": the VPC's door to the internet.")
	case igwUnknown || s.vpc == nil:
		p.add("Internet gateway", "gateway", "", model.HopUnknown, "Which VPC the internet gateway belongs to is only known at deploy time.")
	default:
		p.add("Internet gateway", "gateway", s.vpc.e.nodeID(), model.HopDeny, vpcName+" has no internet gateway, so nothing on the internet can reach it.",
			"Add an aws_internet_gateway attached to "+vpcName+" and a route 0.0.0.0/0 to it in the public subnets' route table.")
	}
	a.routeToInternet(p, s, true)
	a.naclHops(p, s, proto, port, src, true)
	sgs := evalSG(dst.sgs, dst.sgsKnown, true, proto, port, src, false)
	p.add(sgLabel(dst, sgs), "security-group", sgNode(dst, sgs), sgs.verdict, sgs.reason, sgFix(dst, proto, port, "the internet (0.0.0.0/0)"))
	if dst.kind == "load-balancer" {
		a.listenerHops(p, dst, proto, port)
		return
	}
	p.serviceHop(dst, proto, port)
}

// routeToInternet checks a public subnet's route back to the internet
// (needed for replies to inbound connections).
func (a *Analysis) routeToInternet(p *path, s *subnet, inbound bool) {
	w := a.aws
	if s.rt == nil || !s.rtKnown {
		p.add("Route table of "+s.e.addr, "route", s.e.nodeID(), model.HopUnknown, "Which route table the subnet uses is only known at deploy time.")
		return
	}
	r, ok := s.rt.defaultRoute(w)
	label := "Route table " + rtLabel(s.rt)
	switch {
	case r != nil && r.kind == "internet-gateway":
		p.add(label, "route", rtNode(s.rt, r), model.HopAllow, fmt.Sprintf("Sends %s to the internet gateway (subnet %s is public).", r.dest, s.e.addr))
	case !ok:
		p.add(label, "route", rtNode(s.rt, r), model.HopUnknown, "Some routes are only known at deploy time.")
	default:
		why := "has no route to the internet gateway"
		if r != nil && r.kind != "local" {
			why = fmt.Sprintf("sends internet traffic to a %s, not the internet gateway", kindName(r.kind))
		}
		reason := fmt.Sprintf("Subnet %s %s, so it is not a public subnet: replies can't get back to the internet.", s.e.addr, why)
		p.add(label, "route", rtNode(s.rt, r), model.HopDeny, reason,
			fmt.Sprintf("Associate %s with a route table that has a route 0.0.0.0/0 → internet gateway (aws_route_table_association), or reach the resource through a load balancer in public subnets.", s.e.addr))
	}
}

func rtLabel(rt *routeTable) string {
	if rt.e == nil {
		return "main route table of " + rt.vpc.e.addr
	}
	return rt.e.addr
}

func rtNode(rt *routeTable, r *route) string {
	if r != nil && r.e != nil {
		return r.e.nodeID()
	}
	if rt.e != nil {
		return rt.e.nodeID()
	}
	if rt.vpc != nil {
		return rt.vpc.e.nodeID()
	}
	return ""
}

func aclLabel(s *subnet) string {
	if s.acl == nil {
		return "Network ACL of " + s.e.addr
	}
	if s.acl.e == nil {
		return "Default network ACL (" + s.e.addr + ")"
	}
	return "Network ACL " + s.acl.e.addr
}

func aclNode(s *subnet, r *naclRule) string {
	if r != nil && r.e != nil {
		return r.e.nodeID()
	}
	if s.acl != nil && s.acl.e != nil {
		return s.acl.e.nodeID()
	}
	return ""
}

// naclHops checks a subnet's network ACL for a connection with the peer:
// inbound=true means the connection enters the subnet (request inbound,
// reply outbound on ephemeral ports); false means it leaves it.
func (a *Analysis) naclHops(p *path, s *subnet, proto string, port int, other peer, inbound bool) {
	if !s.aclKnown {
		p.add(aclLabel(s), "nacl", aclNode(s, nil), model.HopUnknown, "Which network ACL the subnet uses is only known at deploy time.")
		return
	}
	req := portRange{port, port}
	ret := ephemeral
	if proto == "icmp" {
		req, ret = portRange{0, 0}, portRange{0, 0}
	}
	first := evalNACL(s.acl, !inbound, proto, req, other)
	second := evalNACL(s.acl, inbound, proto, ret, other)
	dir1, dir2 := "inbound", "outbound"
	if !inbound {
		dir1, dir2 = "outbound", "inbound"
	}
	label := aclLabel(s)
	p.add(label+" ("+dir1+")", "nacl", aclNode(s, first.rule), first.verdict, first.reason,
		fmt.Sprintf("Add an allow rule to %s for %s port %d %s (network ACLs are stateless).", label, strings.ToUpper(proto), port, map[bool]string{true: "inbound", false: "outbound"}[inbound]))
	reason := second.reason
	if second.verdict == model.HopAllow && proto != "icmp" {
		reason = "Replies on ephemeral ports 1024-65535 are allowed " + dir2 + "."
	} else if second.verdict == model.HopDeny {
		reason = "Replies use ephemeral ports 1024-65535; " + strings.TrimSuffix(lowerFirst(second.reason), ".") + ". Network ACLs are stateless, so the reply is dropped."
	}
	p.add(label+" ("+dir2+" replies)", "nacl", aclNode(s, second.rule), second.verdict, reason,
		fmt.Sprintf("Add an %s allow rule to %s for TCP 1024-65535 (ephemeral ports for replies).", dir2, label))
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func sgLabel(wl *workload, r sgResult) string {
	if r.group != nil {
		if r.group.e != nil {
			return "Security group " + r.group.e.addr
		}
		return "Default security group"
	}
	if len(wl.sgs) == 1 && wl.sgs[0].e != nil {
		return "Security group " + wl.sgs[0].e.addr
	}
	if len(wl.sgs) == 1 {
		return "Default security group of " + wl.sgs[0].vpc.e.addr
	}
	return "Security groups of " + wl.e.addr
}

func sgNode(wl *workload, r sgResult) string {
	if r.rule != nil && r.rule.e != nil {
		return r.rule.e.nodeID()
	}
	if r.group != nil && r.group.e != nil {
		return r.group.e.nodeID()
	}
	for _, g := range wl.sgs {
		if g.e != nil {
			return g.e.nodeID()
		}
	}
	return wl.e.nodeID()
}

func sgFix(wl *workload, proto string, port int, from string) string {
	name := "its security group"
	for _, g := range wl.sgs {
		if g.e != nil {
			name = g.e.addr
			break
		}
	}
	if proto == "icmp" {
		return fmt.Sprintf("Add an ingress rule to %s allowing ICMP from %s.", name, from)
	}
	return fmt.Sprintf("Add an ingress rule to %s allowing %s port %d from %s (see the bp_open_ingress best practice before opening admin or database ports to the internet).", name, strings.ToUpper(proto), port, from)
}

func sgEgressFix(wl *workload, proto string, port int, to string) string {
	name := "its security group"
	for _, g := range wl.sgs {
		if g.e != nil {
			name = g.e.addr
			break
		}
	}
	return fmt.Sprintf("Add an egress rule to %s allowing %s port %d to %s (Terraform removes AWS's default allow-all egress rule from aws_security_group).", name, strings.ToUpper(proto), port, to)
}

// listenerHops follows a load balancer listener to its targets.
func (a *Analysis) listenerHops(p *path, lb *workload, proto string, port int) {
	var l *listener
	unknownL := false
	for _, x := range lb.listeners {
		if !x.known {
			unknownL = true
			continue
		}
		if x.port == port {
			l = x
		}
	}
	switch {
	case l != nil:
		p.add(fmt.Sprintf("Listener :%d", port), "listener", l.e.nodeID(), model.HopAllow, fmt.Sprintf("%s listens on port %d.", lb.e.addr, port))
	case unknownL:
		p.add(fmt.Sprintf("Listener :%d", port), "listener", lb.e.nodeID(), model.HopUnknown, "Some listener ports are only known at deploy time.")
		return
	default:
		ports := "no listeners"
		if len(lb.ports) > 0 {
			ports = "listeners on " + intsList(lb.ports)
		}
		p.add(fmt.Sprintf("Listener :%d", port), "listener", lb.e.nodeID(), model.HopDeny, fmt.Sprintf("%s has %s, none on port %d.", lb.e.addr, ports, port),
			fmt.Sprintf("Add an aws_lb_listener on port %d to %s.", port, lb.e.addr))
		return
	}
	if len(l.tgs) == 0 {
		p.add("Forwarding", "target", l.e.nodeID(), model.HopAllow, "The listener answers itself (redirect or fixed response); nothing is forwarded.")
		return
	}
	tg := l.tgs[0]
	if len(tg.members) == 0 {
		v := model.HopUnknown
		reason := "No targets are registered with " + tg.e.addr + " in this workflow (they may be registered at run time)."
		p.add("Target group "+tg.e.addr, "target", tg.e.nodeID(), v, reason)
		return
	}
	m := tg.members[0]
	p.add("Target group "+tg.e.addr, "target", tg.e.nodeID(), model.HopAllow, fmt.Sprintf("Forwards to %s on port %d.", m.w.e.addr, m.port))
	if lb.lbType == "network" && len(lb.sgs) == 0 {
		// NLB without security groups keeps the client's address: the
		// target sees the internet.
		s := p.firstSubnet(m.w, "target")
		if s == nil {
			return
		}
		a.naclHops(p, s, proto, m.port, internetPeer, true)
		sgs := evalSG(m.w.sgs, m.w.sgsKnown, true, proto, m.port, internetPeer, false)
		p.add(sgLabel(m.w, sgs), "security-group", sgNode(m.w, sgs), sgs.verdict, sgs.reason, sgFix(m.w, proto, m.port, "the internet (the network load balancer keeps client addresses)"))
		p.serviceHop(m.w, proto, m.port)
		return
	}
	a.between(p, lb, m.w, proto, m.port)
}

func intsList(xs []int) string {
	var parts []string
	for _, x := range xs {
		parts = append(parts, fmt.Sprint(x))
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// workload -> internet

func (a *Analysis) toInternet(p *path, src *workload, proto string, port int) {
	if src.kind == "function" && src.inVPCKnown && !src.inVPC {
		p.add(src.e.addr, "endpoint", src.e.nodeID(), model.HopAllow, "The function runs outside your VPC: AWS gives it internet access.")
		p.add("Internet", "endpoint", "", model.HopAllow, "Reached through AWS's network.")
		return
	}
	p.add(src.e.addr, "endpoint", src.e.nodeID(), model.HopAllow, "The connection starts here"+subnetNote(src)+".")
	sgs := evalSG(src.sgs, src.sgsKnown, false, proto, port, internetPeer, false)
	p.add(sgLabel(src, sgs)+" (outbound)", "security-group", sgNode(src, sgs), sgs.verdict, sgs.reason, sgEgressFix(src, proto, port, "0.0.0.0/0"))
	s := p.firstSubnet(src, "source")
	if s == nil {
		return
	}
	a.naclHops(p, s, proto, port, internetPeer, false)
	a.egressRoute(p, src, s, proto, port)
	if !p.denied() {
		p.add("Internet", "endpoint", "", model.HopAllow, "The destination is an address on the internet.")
	}
}

// egressRoute follows the route a subnet's internet-bound traffic takes.
func (a *Analysis) egressRoute(p *path, src *workload, s *subnet, proto string, port int) {
	w := a.aws
	if s.rt == nil || !s.rtKnown {
		p.add("Route table of "+s.e.addr, "route", s.e.nodeID(), model.HopUnknown, "Which route table the subnet uses is only known at deploy time.")
		return
	}
	r, ok := s.rt.defaultRoute(w)
	label := "Route table " + rtLabel(s.rt)
	natFix := fmt.Sprintf("Add a NAT gateway (aws_nat_gateway with an Elastic IP) in a public subnet and a route 0.0.0.0/0 → NAT gateway in %s's route table.", s.e.addr)
	switch {
	case !ok:
		p.add(label, "route", rtNode(s.rt, r), model.HopUnknown, "Some routes are only known at deploy time.")
	case r == nil || r.kind == "local":
		p.add(label, "route", rtNode(s.rt, r), model.HopDeny, fmt.Sprintf("No route to the internet: subnet %s is isolated (only traffic inside the VPC works).", s.e.addr), natFix)
	case r.kind == "internet-gateway":
		p.add(label, "route", rtNode(s.rt, r), model.HopAllow, fmt.Sprintf("Sends %s to the internet gateway.", r.dest))
		switch {
		case !src.publicKnown:
			p.add("Public address of "+src.e.addr, "address", src.e.nodeID(), model.HopUnknown, src.publicWhy)
		case !src.public:
			p.add("Internet gateway", "gateway", gwNode(r), model.HopDeny, "The internet gateway only carries traffic for resources with a public IP, and "+src.e.addr+" has none. "+src.publicWhy,
				"Give it a public IP, or move it to a private subnet whose route table sends 0.0.0.0/0 to a NAT gateway.")
		default:
			p.add("Internet gateway "+gwName(r), "gateway", gwNode(r), model.HopAllow, "Translates its public IP for the internet.")
		}
	case r.kind == "nat-gateway":
		p.add(label, "route", rtNode(s.rt, r), model.HopAllow, fmt.Sprintf("Sends %s to NAT gateway %s.", r.dest, gwName(r)))
		g := r.target
		switch {
		case g == nil:
			p.add("NAT gateway", "gateway", "", model.HopUnknown, "The NAT gateway is outside this analysis.")
		case !g.public:
			p.add("NAT gateway "+g.e.addr, "gateway", g.e.nodeID(), model.HopDeny, "It is a private NAT gateway (connectivity_type = \"private\"): it can't reach the internet.", "Use a public NAT gateway with an Elastic IP.")
		case g.subnet == nil:
			p.add("NAT gateway "+g.e.addr, "gateway", g.e.nodeID(), model.HopUnknown, "Its subnet is only known at deploy time.")
		default:
			nr, nok := g.subnet.rt.defaultRoute(w)
			switch {
			case g.subnet.rt == nil || !g.subnet.rtKnown || !nok:
				p.add("NAT gateway "+g.e.addr, "gateway", g.e.nodeID(), model.HopUnknown, "Its subnet's routes are only known at deploy time.")
			case nr != nil && nr.kind == "internet-gateway":
				p.add("NAT gateway "+g.e.addr, "gateway", g.e.nodeID(), model.HopAllow, fmt.Sprintf("Sits in public subnet %s and translates private addresses to its Elastic IP.", g.subnet.e.addr))
				p.add("Internet gateway "+gwName(nr), "gateway", gwNode(nr), model.HopAllow, "Carries the NAT gateway's traffic to the internet.")
			default:
				p.add("NAT gateway "+g.e.addr, "gateway", g.e.nodeID(), model.HopDeny, fmt.Sprintf("It sits in %s, which has no route to the internet gateway.", g.subnet.e.addr),
					"Place the NAT gateway in a public subnet (one whose route table sends 0.0.0.0/0 to the internet gateway).")
			}
		}
	case r.kind == "egress-only-gateway":
		p.add(label, "route", rtNode(s.rt, r), model.HopDeny, "IPv4 internet traffic goes to an egress-only internet gateway, which only carries IPv6.", natFix)
	default:
		p.add(label, "route", rtNode(s.rt, r), model.HopUnknown, fmt.Sprintf("Internet traffic leaves through a %s; Nimbo doesn't analyze what's beyond it.", kindName(r.kind)))
	}
}

func gwName(r *route) string {
	if r.target != nil {
		return r.target.e.addr
	}
	return r.targetID
}

func gwNode(r *route) string {
	if r.target != nil {
		return r.target.e.nodeID()
	}
	return ""
}

// ---------------------------------------------------------------------------
// workload -> workload

func (a *Analysis) between(p *path, src, dst *workload, proto string, port int) {
	w := a.aws
	if isFunction(dst) {
		p.functionDest(dst)
		return
	}
	if src.kind == "function" && src.inVPCKnown && !src.inVPC {
		p.add(src.e.addr, "endpoint", src.e.nodeID(), model.HopAllow, "The function runs outside your VPC, so it connects like any internet client.")
		if dst.publicKnown && !dst.public {
			p.add("Private address of "+dst.e.addr, "address", dst.e.nodeID(), model.HopDeny, "A function outside the VPC can only reach public addresses, and "+dst.e.addr+" is private.",
				"Attach the function to the VPC (vpc_config with private subnets and a security group) so it can reach private resources.")
			return
		}
		a.fromInternet(p, dst, proto, port, internetPeer, src.e.addr)
		return
	}
	p.add(src.e.addr, "endpoint", src.e.nodeID(), model.HopAllow, "The connection starts here"+subnetNote(src)+".")
	dp := w.peerOf(dst)
	sp := w.peerOf(src)
	out := evalSG(src.sgs, src.sgsKnown, false, proto, port, dp, false)
	p.add(sgLabel(src, out)+" (outbound)", "security-group", sgNode(src, out), out.verdict, out.reason, sgEgressFix(src, proto, port, dst.e.addr+"'s security group"))
	ss := p.firstSubnet(src, "source")
	ds := p.firstSubnet(dst, "destination")
	if ss == nil || ds == nil {
		return
	}
	if ss == ds {
		p.add("Same subnet "+ss.e.addr, "nacl", ss.e.nodeID(), model.HopAllow, "Both are in the same subnet: network ACLs only filter traffic entering or leaving a subnet.")
	} else {
		a.naclHops(p, ss, proto, port, dp, false)
	}
	switch {
	case ss.vpc == nil || ds.vpc == nil:
		p.add("Route", "route", "", model.HopUnknown, "Which VPC the subnets belong to is only known at deploy time.")
	case ss.vpc == ds.vpc:
		p.add("Local route ("+rtLabel(ss.rt)+")", "route", rtNode(ss.rt, nil), model.HopAllow, fmt.Sprintf("Both are in %s: every route table has a local route for the VPC's own addresses.", ss.vpc.e.addr))
	default:
		a.crossVPCRoute(p, ss, dp)
		a.crossVPCRoute(p, ds, sp)
	}
	if ss != ds {
		a.naclHops(p, ds, proto, port, sp, true)
	}
	in := evalSG(dst.sgs, dst.sgsKnown, true, proto, port, sp, false)
	p.add(sgLabel(dst, in), "security-group", sgNode(dst, in), in.verdict, in.reason, sgFix(dst, proto, port, src.e.addr+"'s security group"))
	if dst.kind == "load-balancer" {
		a.listenerHops(p, dst, proto, port)
		return
	}
	p.serviceHop(dst, proto, port)
}

func (a *Analysis) crossVPCRoute(p *path, from *subnet, to peer) {
	w := a.aws
	addr, ok := to.representative()
	label := "Route table " + rtLabel(from.rt)
	if !ok || from.rt == nil || !from.rtKnown {
		p.add(label, "route", rtNode(from.rt, nil), model.HopUnknown, "The addresses or routes involved are only known at deploy time.")
		return
	}
	r, rok := from.rt.lookup(w, addr)
	switch {
	case !rok:
		p.add(label, "route", rtNode(from.rt, r), model.HopUnknown, "Some routes are only known at deploy time.")
	case r != nil && r.kind == "peering" && r.target != nil && (r.target.vpc == from.vpc || r.target.peer == from.vpc):
		p.add(label, "route", rtNode(from.rt, r), model.HopAllow, fmt.Sprintf("Sends %s through peering connection %s.", r.dest, r.target.e.addr))
	case r != nil && r.kind == "transit-gateway":
		p.add(label, "route", rtNode(from.rt, r), model.HopUnknown, fmt.Sprintf("Sends %s to a transit gateway; Nimbo doesn't analyze transit gateway route tables.", r.dest))
	default:
		p.add(label, "route", rtNode(from.rt, r), model.HopDeny, fmt.Sprintf("%s has no route to %s (%s) in the other VPC.", from.e.addr, to.label, addr),
			"Connect the VPCs (aws_vpc_peering_connection or a transit gateway) and add routes for the other VPC's CIDR in both subnets' route tables.")
	}
}

// ---------------------------------------------------------------------------
// -> AWS service

func (a *Analysis) toService(p *path, src, dst end, proto string) {
	w := a.aws
	if src.internet {
		p.add("Internet", "endpoint", "", model.HopAllow, "The connection starts from an address on the internet.")
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopAllow, dst.svcLabel+" has a public HTTPS endpoint; access is controlled by IAM and resource policies, not the network.")
		return
	}
	wl := src.w
	if proto != "tcp" {
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopDeny, "AWS service APIs use HTTPS (TCP 443).", "Use TCP port 443.")
		return
	}
	if wl.kind == "function" && wl.inVPCKnown && !wl.inVPC {
		p.add(wl.e.addr, "endpoint", wl.e.nodeID(), model.HopAllow, "The function runs outside your VPC: it reaches AWS services directly.")
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopAllow, "Reached over AWS's network (IAM decides what the function may do).")
		return
	}
	s := p.firstSubnet(wl, "source")
	if s == nil {
		return
	}
	var gwEP, ifEP *gateway
	for _, g := range w.gws {
		if g.kind != "vpc-endpoint" || g.service != dst.svc || g.vpc != s.vpc {
			continue
		}
		if strings.EqualFold(g.endpointTyp, "Gateway") && slices.Contains(g.rts, s.rt) {
			gwEP = g
		}
		if strings.EqualFold(g.endpointTyp, "Interface") && g.privateDNS {
			ifEP = g
		}
	}
	switch {
	case gwEP != nil:
		p.add(wl.e.addr, "endpoint", wl.e.nodeID(), model.HopAllow, "The connection starts here"+subnetNote(wl)+".")
		out := evalSG(wl.sgs, wl.sgsKnown, false, proto, 443, internetPeer, true)
		p.add(sgLabel(wl, out)+" (outbound)", "security-group", sgNode(wl, out), out.verdict, out.reason, sgEgressFix(wl, proto, 443, "0.0.0.0/0 or the "+dst.svc+" prefix list"))
		a.naclHops(p, s, proto, 443, internetPeer, false)
		p.add("Gateway endpoint "+gwEP.e.addr, "gateway", gwEP.e.nodeID(), model.HopAllow, fmt.Sprintf("Route table %s sends %s traffic to the gateway endpoint: it never leaves AWS's network.", rtLabel(s.rt), dst.svcLabel))
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopAllow, "Access is then decided by IAM and the resource's policy.")
	case ifEP != nil:
		p.add(wl.e.addr, "endpoint", wl.e.nodeID(), model.HopAllow, "The connection starts here"+subnetNote(wl)+".")
		ep := &workload{e: ifEP.e, kind: "interface", subnets: ifEP.subnets, subnetsKnown: true, sgs: ifEP.sgs, sgsKnown: true}
		if len(ep.sgs) == 0 && s.vpc != nil {
			ep.sgs = []*sg{w.defaultSG(s.vpc)}
		}
		dp := w.peerOf(ep)
		dp.label = "interface endpoint " + ifEP.e.addr
		out := evalSG(wl.sgs, wl.sgsKnown, false, proto, 443, dp, false)
		p.add(sgLabel(wl, out)+" (outbound)", "security-group", sgNode(wl, out), out.verdict, out.reason, sgEgressFix(wl, proto, 443, "the endpoint"))
		in := evalSG(ep.sgs, true, true, proto, 443, w.peerOf(wl), false)
		p.add("Interface endpoint "+ifEP.e.addr, "gateway", ifEP.e.nodeID(), in.verdict, "Its security group: "+lowerFirst(in.reason), sgFix(ep, proto, 443, wl.e.addr))
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopAllow, "Private DNS sends "+dst.svcLabel+" calls to the endpoint; IAM decides the rest.")
	default:
		a.toInternet(p, wl, proto, 443)
		if len(p.hops) > 0 && p.hops[len(p.hops)-1].Component == "Internet" {
			p.hops = p.hops[:len(p.hops)-1]
		}
		if p.denied() {
			p.suggestions = append(p.suggestions, fmt.Sprintf("Or add a VPC endpoint for %s (com.amazonaws.<region>.%s) so the call stays inside AWS without internet access.", dst.svcLabel, dst.svc))
			return
		}
		p.add(dst.svcLabel, "service", dst.nodeID, model.HopAllow, "Reached over the internet through its public endpoint (no VPC endpoint for "+dst.svc+").")
	}
}
