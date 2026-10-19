package netcheck

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// dbPorts are the default ports of RDS engines.
func dbPort(engine string) int {
	switch {
	case strings.Contains(engine, "postgres"):
		return 5432
	case strings.Contains(engine, "mysql"), strings.Contains(engine, "mariadb"):
		return 3306
	case strings.HasPrefix(engine, "sqlserver"):
		return 1433
	case strings.HasPrefix(engine, "oracle"):
		return 1521
	case engine == "redis", engine == "valkey":
		return 6379
	case engine == "memcached":
		return 11211
	}
	return 0
}

func (w *world) addWorkload(e *entity, kind string) *workload {
	wl := &workload{e: e, kind: kind, subnetsKnown: true, sgsKnown: true, inVPC: true, inVPCKnown: true, publicKnown: true}
	w.works = append(w.works, wl)
	w.wOf[e] = wl
	return wl
}

// placeSGs sets the security groups; none given means the VPC's default
// security group.
func (w *world) placeSGs(wl *workload, v any) {
	wl.sgs, wl.sgsKnown = w.sgsRef(v)
	if len(wl.sgs) == 0 && wl.sgsKnown {
		if vp := wl.vpc(); vp != nil {
			wl.sgs = []*sg{w.defaultSG(vp)}
		}
	}
}

func (wl *workload) vpc() *vpc {
	for _, s := range wl.subnets {
		if s.vpc != nil {
			return s.vpc
		}
	}
	for _, g := range wl.sgs {
		if g.vpc != nil {
			return g.vpc
		}
	}
	return nil
}

// subnetGroup resolves a DB/cache/Redshift subnet group by name or ID.
func (w *world) subnetGroup(wl *workload, v any, types ...string) {
	if v == nil {
		wl.subnetsKnown = false
		wl.inVPCKnown = false
		return
	}
	g, _ := w.resolveAs(v, types...)
	if g == nil {
		wl.subnetsKnown = false
		return
	}
	wl.group = g
	wl.subnets, wl.subnetsKnown = w.subnetsRef(g.raw("subnet_ids"))
}

func (w *world) buildWorkloads() {
	for _, e := range w.ofType("aws_instance") {
		wl := w.addWorkload(e, "instance")
		if s := w.subnetRef(e.raw("subnet_id")); s != nil {
			wl.subnets = []*subnet{s}
		} else {
			nis, _ := e.blocks("network_interface")
			for _, ni := range nis {
				if ne, _ := w.resolveAs(ni["network_interface_id"], "aws_network_interface"); ne != nil {
					if s := w.subnetRef(ne.raw("subnet_id")); s != nil {
						wl.subnets = append(wl.subnets, s)
					}
				}
			}
			if len(wl.subnets) == 0 {
				wl.subnetsKnown = false
			}
		}
		sgv := e.raw("vpc_security_group_ids")
		if sgv == nil || isEmptyList(sgv) {
			sgv = e.raw("security_groups")
		}
		w.placeSGs(wl, sgv)
		if ip, ok := e.str("private_ip"); ok {
			if a, err := netip.ParseAddr(ip); err == nil {
				wl.privateIP = a
			}
		}
		w.instancePublic(wl)
		wl.needsEgress = false
	}
	for _, e := range w.ofType("aws_network_interface") {
		wl := w.addWorkload(e, "interface")
		if s := w.subnetRef(e.raw("subnet_id")); s != nil {
			wl.subnets = []*subnet{s}
		} else {
			wl.subnetsKnown = false
		}
		w.placeSGs(wl, e.raw("security_groups"))
		wl.public, wl.publicWhy = false, "Network interfaces get a public address only through an Elastic IP."
		w.eipFor(wl)
	}
	for _, e := range w.ofType("aws_lambda_function") {
		wl := w.addWorkload(e, "function")
		vcs, known := e.blocks("vpc_config")
		subnetsSet := false
		for _, vc := range vcs {
			if ids, _ := asList(vc["subnet_ids"]); len(ids) > 0 || isUnknown(vc["subnet_ids"]) {
				subnetsSet = true
				wl.subnets, wl.subnetsKnown = w.subnetsRef(vc["subnet_ids"])
				w.placeSGs(wl, vc["security_group_ids"])
			}
		}
		if !known {
			wl.inVPCKnown = false
			wl.subnetsKnown = false
		} else if !subnetsSet {
			wl.inVPC = false
			wl.public, wl.publicWhy = false, "The function runs outside your VPC, in AWS's own network: it can reach the internet and AWS services, but nothing on the internet can open a connection to it."
			continue
		}
		wl.needsEgress = true
		wl.public, wl.publicWhy = false, "Functions inside a VPC never get a public IP address; they reach the internet only through a NAT gateway (or AWS services through VPC endpoints)."
	}
	for _, e := range w.ofType("aws_db_instance", "aws_rds_cluster") {
		kind := "database"
		wl := w.addWorkload(e, kind)
		w.subnetGroup(wl, e.raw("db_subnet_group_name"), "aws_db_subnet_group")
		w.placeSGs(wl, e.raw("vpc_security_group_ids"))
		wl.engine, _ = e.str("engine")
		port, set, _ := e.number("port")
		if !set {
			port = float64(dbPort(wl.engine))
		}
		if port > 0 {
			wl.ports = []int{int(port)}
		}
		pub, _, known := e.boolean("publicly_accessible")
		wl.publicKnown = known
		wl.public = pub
		if pub {
			wl.publicWhy = "publicly_accessible = true: the database gets a public DNS name that resolves to a public IP (it still needs public subnets and a security group that allows you)."
		} else {
			wl.publicWhy = "publicly_accessible is false: the database only has a private address inside the VPC."
		}
	}
	for _, e := range w.ofType("aws_elasticache_cluster", "aws_elasticache_replication_group") {
		wl := w.addWorkload(e, "cache")
		w.subnetGroup(wl, e.raw("subnet_group_name"), "aws_elasticache_subnet_group")
		w.placeSGs(wl, e.raw("security_group_ids"))
		wl.engine, _ = e.str("engine")
		if wl.engine == "" {
			wl.engine = "redis"
		}
		port, set, _ := e.number("port")
		if !set {
			port = float64(dbPort(wl.engine))
		}
		if port > 0 {
			wl.ports = []int{int(port)}
		}
		wl.publicWhy = "ElastiCache nodes only have private addresses: they are reachable from inside the VPC only."
	}
	for _, e := range w.ofType("aws_redshift_cluster") {
		wl := w.addWorkload(e, "database")
		w.subnetGroup(wl, e.raw("cluster_subnet_group_name"), "aws_redshift_subnet_group")
		w.placeSGs(wl, e.raw("vpc_security_group_ids"))
		wl.engine = "redshift"
		port, set, _ := e.number("port")
		if !set {
			port = 5439
		}
		wl.ports = []int{int(port)}
		pub, _, known := e.boolean("publicly_accessible")
		wl.public, wl.publicKnown = pub, known
		if pub {
			wl.publicWhy = "publicly_accessible = true: the cluster gets a public endpoint."
		} else {
			wl.publicWhy = "publicly_accessible is false: the cluster only has private addresses."
		}
	}
	for _, e := range w.ofType("aws_ecs_service") {
		ncs, known := e.blocks("network_configuration")
		if known && len(ncs) == 0 {
			continue // bridge/host networking on EC2: placement follows the container instances
		}
		wl := w.addWorkload(e, "container-service")
		wl.needsEgress = true
		if !known {
			wl.subnetsKnown, wl.sgsKnown, wl.publicKnown = false, false, false
			continue
		}
		nc := ncs[0]
		wl.subnets, wl.subnetsKnown = w.subnetsRef(nc["subnets"])
		w.placeSGs(wl, nc["security_groups"])
		pub, _, pk := mapBool(nc, "assign_public_ip")
		wl.public, wl.publicKnown = pub, pk
		if pub {
			wl.publicWhy = "assign_public_ip = true: each task gets a public IP address."
		} else {
			wl.publicWhy = "assign_public_ip is false: tasks only have private addresses (they need a NAT gateway or VPC endpoints to pull images and call AWS)."
		}
	}
	for _, e := range w.ofType("aws_eks_cluster") {
		wl := w.addWorkload(e, "kubernetes")
		wl.needsEgress = true
		vcs, _ := e.blocks("vpc_config")
		if len(vcs) == 0 {
			wl.subnetsKnown, wl.sgsKnown = false, false
			continue
		}
		wl.subnets, wl.subnetsKnown = w.subnetsRef(vcs[0]["subnet_ids"])
		w.placeSGs(wl, vcs[0]["security_group_ids"])
		pub, set, pk := mapBool(vcs[0], "endpoint_public_access")
		wl.public, wl.publicKnown = pub || !set, pk
		wl.ports = []int{443}
		if wl.public {
			wl.publicWhy = "The Kubernetes API endpoint is public (endpoint_public_access defaults to true); worker nodes stay in their subnets."
		} else {
			wl.publicWhy = "endpoint_public_access = false: the Kubernetes API is reachable only from inside the VPC."
		}
	}
	for _, e := range w.ofType("aws_eks_node_group") {
		wl := w.addWorkload(e, "kubernetes")
		wl.needsEgress = true
		wl.subnets, wl.subnetsKnown = w.subnetsRef(e.raw("subnet_ids"))
		wl.sgsKnown = false
		wl.publicWhy = "Worker nodes take the subnet's public-IP setting."
	}
	for _, e := range w.ofType("aws_autoscaling_group") {
		wl := w.addWorkload(e, "autoscaling")
		wl.subnets, wl.subnetsKnown = w.subnetsRef(e.raw("vpc_zone_identifier"))
		wl.sgsKnown = false
		lts, _ := e.blocks("launch_template")
		for _, lt := range lts {
			lte, _ := w.resolveAs(lt["id"], "aws_launch_template")
			if lte == nil {
				lte, _ = w.resolveAs(lt["name"], "aws_launch_template")
			}
			if lte == nil {
				continue
			}
			sgv := lte.raw("vpc_security_group_ids")
			nis, _ := lte.blocks("network_interfaces")
			for _, ni := range nis {
				if ni["security_groups"] != nil {
					sgv = ni["security_groups"]
				}
				if pub, set, _ := mapBool(ni, "associate_public_ip_address"); set {
					wl.public = pub
				}
			}
			w.placeSGs(wl, sgv)
		}
		if lc, _ := w.resolveAs(e.raw("launch_configuration"), "aws_launch_configuration"); lc != nil {
			w.placeSGs(wl, lc.raw("security_groups"))
			wl.public, _, _ = lc.boolean("associate_public_ip_address")
		}
		wl.publicWhy = "Instances launched by the group follow the launch template and subnet public-IP settings."
	}
	for _, wl := range w.works {
		wl.zones = zonesOf(wl.subnets)
		if wl.e.typ == "aws_instance" || wl.e.typ == "aws_db_instance" {
			if az, _ := wl.e.str("availability_zone"); az != "" && !strings.HasPrefix(az, refPrefix) && len(wl.zones) == 0 {
				wl.zones = []string{az}
			}
		}
	}
}

func isEmptyList(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 0
}

func zonesOf(subnets []*subnet) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range subnets {
		if s.az != "" && !seen[s.az] {
			seen[s.az] = true
			out = append(out, s.az)
		}
	}
	sort.Strings(out)
	return out
}

// instancePublic decides whether an instance gets a public IP address.
func (w *world) instancePublic(wl *workload) {
	e := wl.e
	if w.basis == model.NetworkBasisDeployed {
		if ip, _ := e.str("public_ip"); ip != "" {
			wl.public, wl.publicWhy = true, "It has the public IP address "+ip+"."
			return
		}
	}
	if w.eipFor(wl) {
		return
	}
	pub, set, known := e.boolean("associate_public_ip_address")
	switch {
	case !known:
		wl.publicKnown = false
		wl.publicWhy = "associate_public_ip_address is only known at deploy time."
	case set && pub:
		wl.public, wl.publicWhy = true, "associate_public_ip_address = true: the instance gets a public IP address at launch."
	case set && !pub:
		wl.publicWhy = "associate_public_ip_address = false: the instance only has a private address."
	case len(wl.subnets) == 1:
		s := wl.subnets[0]
		if !s.mapKnown {
			wl.publicKnown = false
			wl.publicWhy = "Its subnet's map_public_ip_on_launch is only known at deploy time."
		} else if s.mapPublic {
			wl.public, wl.publicWhy = true, "Its subnet "+s.e.addr+" has map_public_ip_on_launch = true, so it gets a public IP address at launch."
		} else {
			wl.publicWhy = "Its subnet " + s.e.addr + " does not assign public IP addresses (map_public_ip_on_launch is false) and the instance doesn't ask for one."
		}
	default:
		wl.publicKnown = false
		wl.publicWhy = "Its subnet is not known, so whether it gets a public IP isn't either."
	}
}

// eipFor marks wl public when an Elastic IP is attached to it.
func (w *world) eipFor(wl *workload) bool {
	matches := func(v any) bool {
		e, attr, _ := w.resolve(v)
		if e == wl.e && (attr == "" || attr == "primary_network_interface_id") {
			return true
		}
		return false
	}
	for _, e := range w.ofType("aws_eip") {
		if matches(e.raw("instance")) || matches(e.raw("network_interface")) {
			wl.public, wl.publicWhy = true, "The Elastic IP "+e.addr+" is attached to it."
			return true
		}
	}
	for _, e := range w.ofType("aws_eip_association") {
		if matches(e.raw("instance_id")) || matches(e.raw("network_interface_id")) {
			wl.public, wl.publicWhy = true, "The Elastic IP association "+e.addr+" gives it a public address."
			return true
		}
	}
	return false
}

func (w *world) buildLoadBalancing() {
	for _, e := range w.ofType("aws_lb_target_group", "aws_alb_target_group") {
		tg := &targetGroup{e: e, complete: true}
		p, _, _ := e.number("port")
		tg.port = int(p)
		w.tgs = append(w.tgs, tg)
		w.tgOf[e] = tg
	}
	tgRef := func(v any) *targetGroup {
		e, _ := w.resolveAs(v, "aws_lb_target_group", "aws_alb_target_group")
		return w.tgOf[e]
	}
	for _, e := range w.ofType("aws_lb_target_group_attachment", "aws_alb_target_group_attachment") {
		tg := tgRef(e.raw("target_group_arn"))
		if tg == nil {
			continue
		}
		te, _, _ := w.resolve(e.raw("target_id"))
		wl := w.wOf[te]
		if wl == nil {
			tg.complete = false
			continue
		}
		p, set, _ := e.number("port")
		if !set {
			p = float64(tg.port)
		}
		tg.members = append(tg.members, &member{w: wl, port: int(p)})
	}
	for _, wl := range w.works {
		switch wl.e.typ {
		case "aws_autoscaling_group":
			arns, _ := asList(wl.e.raw("target_group_arns"))
			for _, a := range arns {
				if tg := tgRef(a); tg != nil {
					tg.members = append(tg.members, &member{w: wl, port: tg.port})
				}
			}
		case "aws_ecs_service":
			lbs, _ := wl.e.blocks("load_balancer")
			for _, lb := range lbs {
				if tg := tgRef(lb["target_group_arn"]); tg != nil {
					p, set, _ := mapNum(lb, "container_port")
					if !set {
						p = float64(tg.port)
					}
					tg.members = append(tg.members, &member{w: wl, port: int(p)})
				}
			}
		}
	}
	for _, e := range w.ofType("aws_autoscaling_attachment") {
		tg := tgRef(e.raw("lb_target_group_arn"))
		asg, _ := w.resolveAs(e.raw("autoscaling_group_name"), "aws_autoscaling_group")
		if tg != nil && w.wOf[asg] != nil {
			tg.members = append(tg.members, &member{w: w.wOf[asg], port: tg.port})
		}
	}
	for _, e := range w.ofType("aws_lb", "aws_alb") {
		wl := w.addWorkload(e, "load-balancer")
		wl.lbType, _ = e.str("load_balancer_type")
		if wl.lbType == "" {
			wl.lbType = "application"
		}
		if v := e.raw("subnets"); v != nil {
			wl.subnets, wl.subnetsKnown = w.subnetsRef(v)
		}
		maps, known := e.blocks("subnet_mapping")
		if !known {
			wl.subnetsKnown = false
		}
		for _, m := range maps {
			if s := w.subnetRef(m["subnet_id"]); s != nil {
				wl.subnets = append(wl.subnets, s)
			} else {
				wl.subnetsKnown = false
			}
		}
		if e.raw("security_groups") != nil || wl.lbType == "application" {
			w.placeSGs(wl, e.raw("security_groups"))
		}
		internal, _, ik := e.boolean("internal")
		wl.public, wl.publicKnown = !internal, ik
		if internal {
			wl.publicWhy = "internal = true: the load balancer only has private addresses."
		} else {
			wl.publicWhy = "The load balancer is internet-facing (internal = false), so it gets public addresses in its subnets."
		}
		wl.zones = zonesOf(wl.subnets)
	}
	for _, e := range w.ofType("aws_lb_listener", "aws_alb_listener") {
		lbE, _ := w.resolveAs(e.raw("load_balancer_arn"), "aws_lb", "aws_alb")
		lb := w.wOf[lbE]
		if lb == nil {
			continue
		}
		l := &listener{e: e, known: true}
		p, set, pk := e.number("port")
		l.port, l.known = int(p), pk
		l.proto, _ = e.str("protocol")
		if !set {
			l.known = false
		}
		acts, _ := e.blocks("default_action")
		for _, a := range acts {
			l.tgs = append(l.tgs, actionTGs(a, tgRef)...)
		}
		lb.listeners = append(lb.listeners, l)
		if l.known {
			lb.ports = append(lb.ports, l.port)
		}
	}
	for _, e := range w.ofType("aws_lb_listener_rule", "aws_alb_listener_rule") {
		le, _ := w.resolveAs(e.raw("listener_arn"), "aws_lb_listener", "aws_alb_listener")
		for _, lb := range w.works {
			for _, l := range lb.listeners {
				if l.e == le && le != nil {
					acts, _ := e.blocks("action")
					for _, a := range acts {
						l.tgs = append(l.tgs, actionTGs(a, tgRef)...)
					}
				}
			}
		}
	}
}

func actionTGs(a map[string]any, tgRef func(any) *targetGroup) []*targetGroup {
	var out []*targetGroup
	if tg := tgRef(a["target_group_arn"]); tg != nil {
		out = append(out, tg)
	}
	fwds, _ := asBlocks(a["forward"])
	for _, f := range fwds {
		tgsb, _ := asBlocks(f["target_group"])
		for _, t := range tgsb {
			if tg := tgRef(t["arn"]); tg != nil {
				out = append(out, tg)
			}
		}
	}
	return out
}
