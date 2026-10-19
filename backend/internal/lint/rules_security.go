package lint

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ---------------------------------------------------------------------------
// bp_open_ingress

var adminPorts = map[int]string{
	22: "SSH", 23: "Telnet", 3389: "RDP", 3306: "MySQL", 5432: "PostgreSQL", 1433: "SQL Server",
	1521: "Oracle", 27017: "MongoDB", 6379: "Redis", 9200: "Elasticsearch", 5439: "Redshift",
	11211: "Memcached", 2379: "etcd", 5984: "CouchDB", 9042: "Cassandra",
}

const allPorts = -1

// exposedPorts lists the admin/database ports in [from, to] ("22 (SSH)");
// from == allPorts means every port.
func exposedPorts(from, to int) []string {
	if from == allPorts {
		return []string{"all ports"}
	}
	var ports []int
	for p := range adminPorts {
		if p >= from && p <= to {
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	var out []string
	for _, p := range ports {
		out = append(out, fmt.Sprintf("%d (%s)", p, adminPorts[p]))
	}
	return out
}

func isOpenCIDR(s string) bool { return s == "0.0.0.0/0" || s == "::/0" }

func anyOpen(ss []string) bool { return slices.ContainsFunc(ss, isOpenCIDR) }

// portRange reads from/to ports and a protocol; ok is false when they are
// not constants.
func portRange(b *model.Body, protoAttr string) (from, to int, ok bool) {
	proto, _ := attrString(b, protoAttr)
	if n, isNum := attrNumber(b, protoAttr); isNum && n == -1 {
		proto = "-1"
	}
	switch strings.ToLower(proto) {
	case "-1", "all":
		return allPorts, allPorts, true
	case "icmp", "icmpv6", "58", "1":
		return 0, 0, false
	}
	f, okF := attrNumber(b, "from_port")
	t, okT := attrNumber(b, "to_port")
	if !okF || !okT {
		return 0, 0, false
	}
	return int(f), int(t), true
}

// parsePorts reads GCP/Azure port specs: "22", "1000-2000", "*".
func parsePorts(specs []string) [][2]int {
	var out [][2]int
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "*" {
			out = append(out, [2]int{allPorts, allPorts})
			continue
		}
		lo, hi, isRange := strings.Cut(s, "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 == nil && err2 == nil {
			out = append(out, [2]int{a, b})
		}
	}
	return out
}

func checkOpenIngress(p *pass) {
	report := func(n *model.Node, path, source string, ports []string) {
		if len(ports) == 0 {
			return
		}
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s allows %s to %s", addr(n), source, strings.Join(ports, ", ")),
			why:     "Admin and database ports open to the whole internet are scanned within minutes and brute-forced; a weak password or unpatched service is then remotely exploitable.",
			fix:     "Restrict the source to your VPN/office CIDR or a security group (source_security_group_id), or reach the host through SSM Session Manager or a bastion.",
		})
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_security_group":
			for i, blk := range blocks(&n.Body, "ingress") {
				if !anyOpen(append(attrStrings(&blk.Body, "cidr_blocks"), attrStrings(&blk.Body, "ipv6_cidr_blocks")...)) {
					continue
				}
				if from, to, ok := portRange(&blk.Body, "protocol"); ok {
					report(n, fmt.Sprintf("ingress[%d]", i), "0.0.0.0/0", exposedPorts(from, to))
				}
			}
		case "aws_security_group_rule":
			if t, _ := attrString(&n.Body, "type"); t != "ingress" {
				continue
			}
			if !anyOpen(append(attrStrings(&n.Body, "cidr_blocks"), attrStrings(&n.Body, "ipv6_cidr_blocks")...)) {
				continue
			}
			if from, to, ok := portRange(&n.Body, "protocol"); ok {
				report(n, "cidr_blocks", "0.0.0.0/0", exposedPorts(from, to))
			}
		case "aws_vpc_security_group_ingress_rule":
			if !anyOpen(append(attrStrings(&n.Body, "cidr_ipv4"), attrStrings(&n.Body, "cidr_ipv6")...)) {
				continue
			}
			if from, to, ok := portRange(&n.Body, "ip_protocol"); ok {
				report(n, "cidr_ipv4", "0.0.0.0/0", exposedPorts(from, to))
			}
		case "google_compute_firewall":
			if d, _ := attrString(&n.Body, "direction"); d != "" && !strings.EqualFold(d, "INGRESS") {
				continue
			}
			if !anyOpen(attrStrings(&n.Body, "source_ranges")) {
				continue
			}
			for i, blk := range blocks(&n.Body, "allow") {
				proto, _ := attrString(&blk.Body, "protocol")
				specs := attrStrings(&blk.Body, "ports")
				var exposed []string
				switch {
				case proto == "all" || (proto == "tcp" || proto == "udp") && getAttr(&blk.Body, "ports") == nil:
					exposed = exposedPorts(allPorts, allPorts)
				case proto == "tcp" || proto == "udp":
					for _, r := range parsePorts(specs) {
						exposed = append(exposed, exposedPorts(r[0], r[1])...)
					}
				}
				report(n, fmt.Sprintf("allow[%d]", i), "0.0.0.0/0", exposed)
			}
		case "azurerm_network_security_rule":
			p.azureRule(n, &n.Body, "", report)
		case "azurerm_network_security_group":
			for i, blk := range blocks(&n.Body, "security_rule") {
				p.azureRule(n, &blk.Body, fmt.Sprintf("security_rule[%d]", i), report)
			}
		}
	}
}

func (p *pass) azureRule(n *model.Node, b *model.Body, path string, report func(*model.Node, string, string, []string)) {
	dir, _ := attrString(b, "direction")
	access, _ := attrString(b, "access")
	if !strings.EqualFold(dir, "Inbound") || !strings.EqualFold(access, "Allow") {
		return
	}
	sources := append(attrStrings(b, "source_address_prefixes"), attrStrings(b, "source_address_prefix")...)
	open := ""
	for _, s := range sources {
		switch strings.ToLower(s) {
		case "*", "0.0.0.0/0", "internet", "any":
			open = s
		}
	}
	if open == "" {
		return
	}
	var exposed []string
	for _, r := range parsePorts(append(attrStrings(b, "destination_port_ranges"), attrStrings(b, "destination_port_range")...)) {
		exposed = append(exposed, exposedPorts(r[0], r[1])...)
	}
	if path == "" {
		path = "source_address_prefix"
	}
	report(n, path, fmt.Sprintf("%q", open), exposed)
}

// ---------------------------------------------------------------------------
// bp_public_bucket

// publicIntent: the bucket is meant to be public (static website or a
// Public = "true" tag/label).
func (p *pass) publicIntent(bucket *model.Node) bool {
	if bucket == nil {
		return false
	}
	for _, attr := range []string{"tags", "labels"} {
		if a := getAttr(&bucket.Body, attr); a != nil {
			if m, ok := literal(a.Value).(map[string]any); ok {
				for k, v := range m {
					if strings.EqualFold(k, "public") && fmt.Sprint(v) == "true" {
						return true
					}
				}
			}
		}
	}
	if len(blocks(&bucket.Body, "website")) > 0 || len(blocks(&bucket.Body, "static_website")) > 0 {
		return true
	}
	for _, n := range p.ofType("aws_s3_bucket_website_configuration", "azurerm_storage_account_static_website") {
		for _, attr := range []string{"bucket", "storage_account_id"} {
			if a := getAttr(&n.Body, attr); a != nil && referencesNode(a.Value, bucket) {
				return true
			}
		}
	}
	return false
}

func checkPublicBucket(p *pass) {
	why := "Public buckets are the most common source of data leaks; anything uploaded later becomes world-readable too."
	intentFix := "If the bucket is meant to be public (a static website), add a website configuration or tag it Public = \"true\"; otherwise "
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_s3_bucket_public_access_block":
			bucket := p.referencedNode(&n.Body, "bucket")
			var off []string
			for _, flag := range []string{"block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"} {
				if a := getAttr(&n.Body, flag); a == nil || hasLiteralFalse(&n.Body, flag) {
					off = append(off, flag)
				}
			}
			if len(off) == 0 || p.publicIntent(bucket) {
				continue
			}
			p.report(finding{
				node: n, attribute: off[0],
				message: fmt.Sprintf("%s leaves %s off", addr(n), strings.Join(off, ", ")),
				why:     why,
				fix:     intentFix + "set all four flags to true.",
			})
		case "aws_s3_bucket_acl", "aws_s3_bucket":
			acl, _ := attrString(&n.Body, "acl")
			if acl != "public-read" && acl != "public-read-write" && acl != "authenticated-read" {
				continue
			}
			bucket := n
			if n.Type == "aws_s3_bucket_acl" {
				bucket = p.referencedNode(&n.Body, "bucket")
			}
			if p.publicIntent(bucket) {
				continue
			}
			p.report(finding{
				node: n, attribute: "acl",
				message: fmt.Sprintf("%s grants the %s ACL", addr(n), acl),
				why:     why,
				fix:     intentFix + "use acl = \"private\" (or drop the ACL: buckets are private by default).",
			})
		case "aws_s3_bucket_policy":
			bucket := p.referencedNode(&n.Body, "bucket")
			a := getAttr(&n.Body, "policy")
			if a == nil || p.publicIntent(bucket) {
				continue
			}
			for _, st := range policyStatements(a.Value) {
				if st.allow() && st.publicPrincipal && !st.condition {
					p.report(finding{
						node: n, attribute: "policy",
						message: fmt.Sprintf("%s allows anyone (Principal \"*\") to %s", addr(n), strings.Join(st.actions, ", ")),
						why:     why,
						fix:     intentFix + "name the principals (accounts, roles, CloudFront origin access) or add a Condition.",
					})
					break
				}
			}
		case "google_storage_bucket_iam_member", "google_storage_bucket_iam_binding", "google_storage_bucket_access_control":
			members := append(attrStrings(&n.Body, "members"), attrStrings(&n.Body, "member")...)
			members = append(members, attrStrings(&n.Body, "entity")...)
			for _, m := range members {
				if m == "allUsers" || m == "allAuthenticatedUsers" {
					if p.publicIntent(p.referencedNode(&n.Body, "bucket")) {
						break
					}
					p.report(finding{
						node: n, attribute: "member",
						message: fmt.Sprintf("%s grants bucket access to %s", addr(n), m),
						why:     why,
						fix:     intentFix + "grant the role to specific users, groups or service accounts.",
					})
					break
				}
			}
		case "azurerm_storage_container":
			access, _ := attrString(&n.Body, "container_access_type")
			if access != "blob" && access != "container" {
				continue
			}
			account := p.referencedNode(&n.Body, "storage_account_id")
			if account == nil {
				account = p.referencedNode(&n.Body, "storage_account_name")
			}
			if p.publicIntent(account) {
				continue
			}
			p.report(finding{
				node: n, attribute: "container_access_type",
				message: fmt.Sprintf("%s allows anonymous read access (container_access_type = %q)", addr(n), access),
				why:     why,
				fix:     intentFix + "set container_access_type = \"private\" and share with SAS tokens.",
			})
		case "azurerm_storage_account":
			if attrIsTrue(&n.Body, "allow_nested_items_to_be_public") && !p.publicIntent(n) {
				p.report(finding{
					node: n, attribute: "allow_nested_items_to_be_public",
					message: fmt.Sprintf("%s allows public containers and blobs", addr(n)),
					why:     why,
					fix:     intentFix + "set allow_nested_items_to_be_public = false.",
				})
			}
		}
	}
}

// ---------------------------------------------------------------------------
// bp_unencrypted_storage

// missingOrFalse: unset or a literal false (a reference counts as intent).
func missingOrFalse(b *model.Body, name string) bool {
	return getAttr(b, name) == nil || hasLiteralFalse(b, name)
}

func checkUnencrypted(p *pass) {
	ebsDefault := false
	for _, n := range p.ofType("aws_ebs_encryption_by_default") {
		ebsDefault = ebsDefault || !hasLiteralFalse(&n.Body, "enabled")
	}
	why := "Encryption at rest protects data on lost or decommissioned disks and snapshots, and most compliance standards require it; it costs nothing on these services."
	report := func(n *model.Node, sev model.Severity, attr, msg, fix string) {
		p.report(finding{severity: sev, node: n, attribute: attr, message: msg, why: why, fix: fix})
	}
	for _, n := range p.resources() {
		b := &n.Body
		switch n.Type {
		case "aws_s3_bucket":
			if len(blocks(b, "server_side_encryption_configuration")) > 0 || p.referencedBy(n, "aws_s3_bucket_server_side_encryption_configuration", "bucket") {
				continue
			}
			report(n, model.SeverityInfo, "", fmt.Sprintf("%s has no server-side encryption configuration", addr(n)),
				"Add an aws_s3_bucket_server_side_encryption_configuration for the bucket (sse_algorithm = \"AES256\", or \"aws:kms\" with a key). S3 encrypts new objects with SSE-S3 by default; declaring it keeps the intent in code.")
		case "aws_dynamodb_table":
			enabled := false
			for _, blk := range blocks(b, "server_side_encryption") {
				enabled = enabled || !missingOrFalse(&blk.Body, "enabled")
			}
			if !enabled {
				report(n, model.SeverityInfo, "server_side_encryption", fmt.Sprintf("%s uses the default AWS-owned encryption key", addr(n)),
					"Add server_side_encryption { enabled = true } (AWS managed KMS key, auditable in CloudTrail) or set kms_key_arn to your own key.")
			}
		case "aws_db_instance":
			engine, _ := attrString(b, "engine")
			if getAttr(b, "replicate_source_db") != nil || strings.HasPrefix(engine, "aurora") || !missingOrFalse(b, "storage_encrypted") {
				continue
			}
			report(n, "", "storage_encrypted", fmt.Sprintf("%s stores data unencrypted", addr(n)), "Set storage_encrypted = true (optionally kms_key_id). It can only be set when the instance is created.")
		case "aws_rds_cluster":
			mode, _ := attrString(b, "engine_mode")
			if mode == "serverless" || getAttr(b, "snapshot_identifier") != nil || getAttr(b, "replication_source_identifier") != nil || !missingOrFalse(b, "storage_encrypted") {
				continue
			}
			report(n, "", "storage_encrypted", fmt.Sprintf("%s stores data unencrypted", addr(n)), "Set storage_encrypted = true (optionally kms_key_id). It can only be set when the cluster is created.")
		case "aws_ebs_volume":
			if ebsDefault || getAttr(b, "snapshot_id") != nil || !missingOrFalse(b, "encrypted") {
				continue
			}
			report(n, "", "encrypted", fmt.Sprintf("%s is not encrypted", addr(n)), "Set encrypted = true, or enable aws_ebs_encryption_by_default for the account.")
		case "aws_instance":
			if ebsDefault {
				continue
			}
			for _, bt := range []string{"root_block_device", "ebs_block_device"} {
				for i, blk := range blocks(b, bt) {
					if getAttr(&blk.Body, "snapshot_id") == nil && missingOrFalse(&blk.Body, "encrypted") {
						report(n, "", fmt.Sprintf("%s[%d].encrypted", bt, i), fmt.Sprintf("%s has an unencrypted %s", addr(n), bt), "Set encrypted = true in the block, or enable aws_ebs_encryption_by_default for the account.")
					}
				}
			}
		case "aws_efs_file_system":
			if missingOrFalse(b, "encrypted") {
				report(n, "", "encrypted", fmt.Sprintf("%s is not encrypted", addr(n)), "Set encrypted = true (optionally kms_key_id). It can only be set when the file system is created.")
			}
		case "aws_elasticache_replication_group":
			if missingOrFalse(b, "at_rest_encryption_enabled") {
				report(n, "", "at_rest_encryption_enabled", fmt.Sprintf("%s does not encrypt data at rest", addr(n)), "Set at_rest_encryption_enabled = true (and transit_encryption_enabled = true).")
			}
		}
	}
}

// referencedBy reports whether a resource of typ refers to n in attr.
func (p *pass) referencedBy(n *model.Node, typ, attr string) bool {
	for _, other := range p.ofType(typ) {
		if a := getAttr(&other.Body, attr); a != nil && referencesNode(a.Value, n) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// bp_iam_wildcard

// statement is the part of an IAM policy statement the rules read.
type statement struct {
	effect          string
	actions         []string
	resources       []string
	publicPrincipal bool
	condition       bool
}

func (s statement) allow() bool { return !strings.EqualFold(s.effect, "Deny") }

func (s statement) wildcard() bool {
	star := func(v string) bool { return v == "*" || v == "*:*" }
	return s.allow() && slices.ContainsFunc(s.actions, star) && slices.Contains(s.resources, "*")
}

// policyStatements reads the statements of a policy attribute: a JSON
// string (heredoc or literal) or jsonencode({...}) with constant Action and
// Resource values.
func policyStatements(v model.Value) []statement {
	if s, ok := literal(v).(string); ok {
		return jsonStatements(s)
	}
	e := parse(v)
	if e == nil {
		return nil
	}
	var out []statement
	hclsyntax.VisitAll(e, func(node hclsyntax.Node) hcl.Diagnostics {
		obj, ok := node.(*hclsyntax.ObjectConsExpr)
		if !ok {
			return nil
		}
		var st statement
		isStatement := false
		for _, item := range obj.Items {
			key := objectKey(item.KeyExpr)
			switch key {
			case "Effect":
				st.effect = firstString(constStrings(item.ValueExpr))
			case "Action":
				st.actions, isStatement = constStrings(item.ValueExpr), true
			case "Resource":
				st.resources = constStrings(item.ValueExpr)
			case "Principal":
				st.publicPrincipal = principalStar(item.ValueExpr)
			case "Condition":
				st.condition = true
			}
		}
		if isStatement {
			out = append(out, st)
		}
		return nil
	})
	return out
}

func firstString(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

func objectKey(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		if kw := hcl.ExprAsKeyword(k.Wrapped); kw != "" {
			return kw
		}
		e = k.Wrapped
	}
	if v, diags := e.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
		return v.AsString()
	}
	return ""
}

// constStrings evaluates a constant string or list of strings.
func constStrings(e hclsyntax.Expression) []string {
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
		return nil
	}
	if v.Type() == cty.String {
		return []string{v.AsString()}
	}
	if !v.CanIterateElements() {
		return nil
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, el := it.Element()
		if !el.IsNull() && el.Type() == cty.String {
			out = append(out, el.AsString())
		}
	}
	return out
}

func principalStar(e hclsyntax.Expression) bool {
	if ss := constStrings(e); len(ss) == 1 && ss[0] == "*" {
		return true
	}
	if obj, ok := e.(*hclsyntax.ObjectConsExpr); ok {
		for _, item := range obj.Items {
			if objectKey(item.KeyExpr) == "AWS" && slices.Contains(constStrings(item.ValueExpr), "*") {
				return true
			}
		}
	}
	return false
}

func jsonStatements(doc string) []statement {
	var policy struct {
		Statement json.RawMessage
	}
	if json.Unmarshal([]byte(doc), &policy) != nil || len(policy.Statement) == 0 {
		return nil
	}
	var raws []map[string]json.RawMessage
	if json.Unmarshal(policy.Statement, &raws) != nil {
		var one map[string]json.RawMessage
		if json.Unmarshal(policy.Statement, &one) != nil {
			return nil
		}
		raws = []map[string]json.RawMessage{one}
	}
	var out []statement
	for _, r := range raws {
		st := statement{actions: jsonStrings(r["Action"]), resources: jsonStrings(r["Resource"]), condition: r["Condition"] != nil}
		_ = json.Unmarshal(r["Effect"], &st.effect)
		if p := r["Principal"]; p != nil {
			var obj map[string]json.RawMessage
			if json.Unmarshal(p, &obj) == nil {
				st.publicPrincipal = slices.Contains(jsonStrings(obj["AWS"]), "*")
			} else {
				st.publicPrincipal = slices.Contains(jsonStrings(p), "*")
			}
		}
		out = append(out, st)
	}
	return out
}

func jsonStrings(raw json.RawMessage) []string {
	if raw == nil {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

// identityPolicyAttrs are the identity policy attributes per resource type.
var identityPolicyAttrs = map[string]string{
	"aws_iam_policy": "policy", "aws_iam_role_policy": "policy", "aws_iam_user_policy": "policy", "aws_iam_group_policy": "policy",
}

func checkIAMWildcard(p *pass) {
	report := func(n *model.Node, attr string) {
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s allows every action (\"*\") on every resource (\"*\")", addr(n)),
			why:     "Administrator access turns any bug or leaked credential of whoever holds the policy into full control of the account.",
			fix:     "List only the actions the workload calls (e.g. [\"sqs:SendMessage\"]) and scope Resource to the ARNs it uses (aws_sqs_queue.<name>.arn).",
		})
	}
	for i := range p.wf.Nodes {
		n := &p.wf.Nodes[i]
		switch {
		case n.Kind == model.KindData && n.Type == "aws_iam_policy_document":
			for j, blk := range blocks(&n.Body, "statement") {
				effect, _ := attrString(&blk.Body, "effect")
				st := statement{effect: effect, actions: attrStrings(&blk.Body, "actions"), resources: attrStrings(&blk.Body, "resources")}
				if st.wildcard() {
					report(n, fmt.Sprintf("statement[%d]", j))
				}
			}
		case n.Kind == model.KindResource:
			attrs := map[string]*model.Attr{}
			if name, ok := identityPolicyAttrs[n.Type]; ok {
				attrs[name] = getAttr(&n.Body, name)
			}
			if n.Type == "aws_iam_role" {
				for j, blk := range blocks(&n.Body, "inline_policy") {
					attrs[fmt.Sprintf("inline_policy[%d].policy", j)] = getAttr(&blk.Body, "policy")
				}
			}
			for path, a := range attrs {
				if a == nil {
					continue
				}
				if slices.ContainsFunc(policyStatements(a.Value), statement.wildcard) {
					report(n, path)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// bp_log_retention

// lambdaLogName is the HCL template a log group name must equal to belong
// to a function named by fnSrc: "/aws/lambda/<function name>".
func lambdaLogName(fnSrc string) string {
	fnSrc = strings.TrimSpace(fnSrc)
	if strings.HasPrefix(fnSrc, `"`) && strings.HasSuffix(fnSrc, `"`) && len(fnSrc) >= 2 {
		return `"/aws/lambda/` + fnSrc[1:len(fnSrc)-1] + `"`
	}
	return `"/aws/lambda/${` + fnSrc + `}"`
}

func checkLogRetention(p *pass) {
	groups := p.ofType("aws_cloudwatch_log_group")
	why := "CloudWatch keeps Lambda logs forever by default: storage cost grows without bound and old logs (possibly with personal data) are never purged."
	for _, fn := range p.ofType("aws_lambda_function") {
		var group *model.Node
		for _, blk := range blocks(&fn.Body, "logging_config") {
			group = p.referencedNode(&blk.Body, "log_group")
		}
		want := ""
		if a := getAttr(&fn.Body, "function_name"); a != nil {
			want = lambdaLogName(source(a.Value))
		}
		for _, g := range groups {
			if group != nil {
				break
			}
			a := getAttr(&g.Body, "name")
			if a != nil && (referencesNode(a.Value, fn) || want != "" && strings.TrimSpace(source(a.Value)) == want) {
				group = g
			}
		}
		if group == nil {
			p.report(finding{
				node:    fn,
				message: fmt.Sprintf("%s has no log group with a retention period", addr(fn)),
				why:     why,
				fix:     fmt.Sprintf("Add resource \"aws_cloudwatch_log_group\" with name = \"/aws/lambda/${%s.function_name}\" and retention_in_days = 14 (or what your policy requires).", addr(fn)),
			})
			continue
		}
		if days, ok := attrNumber(&group.Body, "retention_in_days"); getAttr(&group.Body, "retention_in_days") == nil || ok && days == 0 {
			p.report(finding{
				node: group, attribute: "retention_in_days",
				message: fmt.Sprintf("%s (logs of %s) keeps logs forever", addr(group), addr(fn)),
				why:     why,
				fix:     "Set retention_in_days = 14 (or what your policy requires).",
			})
		}
	}
}
