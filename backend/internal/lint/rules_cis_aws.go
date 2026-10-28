package lint

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Rules for the design-visible recommendations of the CIS Amazon Web
// Services Foundations Benchmark that the generic security rules don't
// decide; compliance/cis_aws.go maps them to the benchmark's controls.
// Rules that would fire on most designs, or that restate bp_open_ingress
// with the benchmark's exact port sets, are opt-in so Problems stays quiet;
// compliance grading runs them anyway.
func init() {
	rules = append(rules,
		cisRule(info("bp_iam_password_length", "Password policy requires 14+ characters", CategorySecurity,
			"An aws_iam_account_password_policy sets minimum_password_length to 14 or more."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/iam-controls.html#iam-15", false, checkPasswordLength),
		cisRule(info("bp_iam_password_reuse", "Password policy prevents reuse", CategorySecurity,
			"An aws_iam_account_password_policy sets password_reuse_prevention to 24 (the last 24 passwords can't be reused)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/iam-controls.html#iam-16", false, checkPasswordReuse),
		cisRule(info("bp_iam_user_policy", "IAM users get permissions through groups", CategorySecurity,
			"Policies are attached to groups or roles, not directly to IAM users (inline user policies, user policy attachments)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/iam-controls.html#iam-2", false, checkUserPolicy),
		cisRule(warning("bp_iam_admin_attached", "No full-admin policies attached", CategorySecurity,
			"Users, groups and roles don't get the AWS managed AdministratorAccess policy or a policy document that allows * on *."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/iam-controls.html#iam-1", false, checkAdminAttached),
		cisRule(info("bp_s3_deny_http", "Buckets deny plain HTTP requests", CategorySecurity,
			"Every S3 bucket has a bucket policy that denies requests made without TLS (Condition aws:SecureTransport = false)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/s3-controls.html#s3-5", true, checkS3DenyHTTP),
		cisRule(info("bp_s3_block_public_access", "S3 Block Public Access is fully on", CategorySecurity,
			"Every bucket- and account-level S3 public access block turns on all four settings, also for buckets meant to be public (serve those through CloudFront)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/s3-controls.html#s3-8", true, checkS3BlockPublicAccess),
		cisRule(warning("bp_rds_public", "Databases are not publicly accessible", CategorySecurity,
			"RDS instances and clusters don't set publicly_accessible = true."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/rds-controls.html#rds-2", false, checkRDSPublic),
		cisRule(info("bp_rds_minor_upgrade", "Databases take minor version upgrades", CategorySecurity,
			"RDS instances and clusters keep auto_minor_version_upgrade on, so security patches land in the maintenance window."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/rds-controls.html#rds-13", false, checkRDSMinorUpgrade),
		cisRule(info("bp_rds_multi_az", "Databases are Multi-AZ", CategorySecurity,
			"RDS DB instances set multi_az = true, so a standby in another Availability Zone takes over on failure."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/rds-controls.html#rds-5", true, checkRDSMultiAZ),
		cisRule(info("bp_cloudtrail_multi_region", "CloudTrail records all regions", CategorySecurity,
			"Trails are multi-Region, logging, and record all read and write management events."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/cloudtrail-controls.html#cloudtrail-1", false, checkTrailMultiRegion),
		cisRule(info("bp_cloudtrail_log_validation", "CloudTrail validates log files", CategorySecurity,
			"Trails set enable_log_file_validation = true, so tampered or deleted log files are detectable."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/cloudtrail-controls.html#cloudtrail-4", false, checkTrailLogValidation),
		cisRule(info("bp_cloudtrail_kms", "CloudTrail logs are encrypted with KMS", CategorySecurity,
			"Trails set kms_key_id, so their log files are encrypted with a customer managed KMS key."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/cloudtrail-controls.html#cloudtrail-2", false, checkTrailKMS),
		cisRule(info("bp_cloudtrail_bucket_logging", "CloudTrail bucket has access logging", CategorySecurity,
			"The S3 bucket a trail writes to (when it is in the design) has server access logging."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/cloudtrail-controls.html#cloudtrail-7", false, checkTrailBucketLogging),
		cisRule(info("bp_cloudtrail_s3_write_events", "CloudTrail logs S3 object writes", CategorySecurity,
			"A trail records S3 object-level write data events (event_selector data_resource AWS::S3::Object, or an advanced event selector)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/s3-controls.html#s3-22", true, func(p *pass) { checkTrailS3Events(p, true) }),
		cisRule(info("bp_cloudtrail_s3_read_events", "CloudTrail logs S3 object reads", CategorySecurity,
			"A trail records S3 object-level read data events (event_selector data_resource AWS::S3::Object, or an advanced event selector)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/s3-controls.html#s3-23", true, func(p *pass) { checkTrailS3Events(p, false) }),
		cisRule(info("bp_kms_rotation", "KMS keys rotate", CategorySecurity,
			"Customer managed symmetric encryption KMS keys set enable_key_rotation = true."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/kms-controls.html#kms-4", false, checkKMSRotation),
		cisRule(info("bp_vpc_flow_logs", "VPCs have flow logs", CategorySecurity,
			"Every VPC has an aws_flow_log that records rejected (or all) traffic."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-6", true, checkVPCFlowLogs),
		cisRule(info("bp_nacl_admin_ports", "Network ACLs don't open SSH/RDP to the internet", CategorySecurity,
			"Network ACL ingress rules don't allow 0.0.0.0/0 or ::/0 to port 22 or 3389 (TCP, UDP or all protocols)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-21", true, checkNACLAdminPorts),
		cisRule(warning("bp_sg_admin_ipv4", "Security groups don't open SSH/RDP to 0.0.0.0/0", CategorySecurity,
			"Security group ingress rules don't allow 0.0.0.0/0 to port 22 or 3389 (TCP, UDP or all protocols)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-53", true, func(p *pass) { checkSGOpen(p, "0.0.0.0/0", cisAdminPorts) }),
		cisRule(warning("bp_sg_admin_ipv6", "Security groups don't open SSH/RDP to ::/0", CategorySecurity,
			"Security group ingress rules don't allow ::/0 to port 22 or 3389 (TCP, UDP or all protocols)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-54", true, func(p *pass) { checkSGOpen(p, "::/0", cisAdminPorts) }),
		cisRule(warning("bp_sg_open_cifs", "Security groups don't open CIFS/SMB to the internet", CategorySecurity,
			"Security group ingress rules don't allow 0.0.0.0/0 or ::/0 to port 445 (CIFS/SMB)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-19", true, func(p *pass) { checkSGOpen(p, "", []int{445}) }),
		cisRule(info("bp_default_sg_closed", "Default security groups allow no traffic", CategorySecurity,
			"Every VPC manages its default security group (aws_default_security_group) with no ingress or egress rules, and no rule resource adds to it."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-2", true, checkDefaultSGClosed),
		cisRule(info("bp_imdsv2", "EC2 requires IMDSv2", CategorySecurity,
			"Instances, launch templates and launch configurations set metadata_options { http_tokens = \"required\" } (or disable the metadata endpoint)."),
			"https://docs.aws.amazon.com/securityhub/latest/userguide/ec2-controls.html#ec2-8", false, checkIMDSv2),
	)
}

func cisRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass)) rule {
	r.Docs = docs
	r.DefaultOff = optIn
	return rule{r, check}
}

// ---------------------------------------------------------------------------
// IAM

func checkPasswordLength(p *pass) {
	for _, n := range p.ofType("aws_iam_account_password_policy") {
		v, ok := attrNumber(&n.Body, "minimum_password_length")
		set := getAttr(&n.Body, "minimum_password_length") != nil
		if set && (!ok || v >= 14) {
			continue // 14+, or an expression Nimbo can't evaluate
		}
		msg := fmt.Sprintf("%s leaves minimum_password_length at the AWS default, shorter than 14", addr(n))
		if set {
			msg = fmt.Sprintf("%s allows passwords of %g characters", addr(n), v)
		}
		p.report(finding{
			node: n, attribute: "minimum_password_length", message: msg,
			why: "Short console passwords fall to brute force and password spraying; length is what makes a password hard to guess.",
			fix: "Set minimum_password_length = 14 (or more).",
		})
	}
}

func checkPasswordReuse(p *pass) {
	for _, n := range p.ofType("aws_iam_account_password_policy") {
		v, ok := attrNumber(&n.Body, "password_reuse_prevention")
		set := getAttr(&n.Body, "password_reuse_prevention") != nil
		if set && (!ok || v >= 24) {
			continue
		}
		msg := fmt.Sprintf("%s doesn't set password_reuse_prevention: users can reuse old passwords", addr(n))
		if set {
			msg = fmt.Sprintf("%s only remembers the last %g passwords", addr(n), v)
		}
		p.report(finding{
			node: n, attribute: "password_reuse_prevention", message: msg,
			why: "A password that leaked once stays dangerous if users can rotate back to it.",
			fix: "Set password_reuse_prevention = 24.",
		})
	}
}

// nonEmpty: the attribute is set to something other than a literal empty
// list (an expression counts: it most likely lists something).
func nonEmpty(b *model.Body, name string) bool {
	a := getAttr(b, name)
	if a == nil {
		return false
	}
	l, isList := literal(a.Value).([]any)
	return !isList || len(l) > 0
}

func checkUserPolicy(p *pass) {
	for _, n := range p.resources() {
		attr := ""
		switch n.Type {
		case "aws_iam_user_policy", "aws_iam_user_policy_attachment":
			attr = "user"
		case "aws_iam_policy_attachment":
			attr = "users"
		case "aws_iam_user_policies_exclusive":
			attr = "policy_names"
		case "aws_iam_user_policy_attachments_exclusive":
			attr = "policy_arns"
		default:
			continue
		}
		if n.Type != "aws_iam_user_policy" && n.Type != "aws_iam_user_policy_attachment" && !nonEmpty(&n.Body, attr) {
			continue
		}
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s gives an IAM user permissions directly", addr(n)),
			why:     "Permissions granted user by user drift apart and are easy to forget when people change roles; groups keep access reviewable in one place.",
			fix:     "Attach the policy to an aws_iam_group (aws_iam_group_policy_attachment) and add the user to it with aws_iam_user_group_membership.",
		})
	}
}

// adminPolicyRe matches the AWS managed AdministratorAccess policy ARN in an
// expression's source, in any partition and also when the partition is
// interpolated ("arn:${data.aws_partition.current.partition}:iam::aws:…").
var adminPolicyRe = regexp.MustCompile(`:iam::aws:policy/AdministratorAccess"`)

// adminAttachAttrs are the attributes that attach managed policies.
var adminAttachAttrs = map[string]string{
	"aws_iam_role_policy_attachment":             "policy_arn",
	"aws_iam_user_policy_attachment":             "policy_arn",
	"aws_iam_group_policy_attachment":            "policy_arn",
	"aws_iam_policy_attachment":                  "policy_arn",
	"aws_iam_role":                               "managed_policy_arns",
	"aws_iam_role_policy_attachments_exclusive":  "policy_arns",
	"aws_iam_user_policy_attachments_exclusive":  "policy_arns",
	"aws_iam_group_policy_attachments_exclusive": "policy_arns",
}

// wildcardDocument: the policy attribute name of b uses an
// aws_iam_policy_document data source that allows * on *. bp_iam_wildcard
// reports the data source itself; this reports where it gets attached.
func (p *pass) wildcardDocument(b *model.Body, name string) *model.Node {
	d := p.referencedNode(b, name)
	if d == nil || d.Kind != model.KindData || d.Type != "aws_iam_policy_document" {
		return nil
	}
	for _, blk := range blocks(&d.Body, "statement") {
		effect, _ := attrString(&blk.Body, "effect")
		if (statement{effect: effect, actions: attrStrings(&blk.Body, "actions"), resources: attrStrings(&blk.Body, "resources")}).wildcard() {
			return d
		}
	}
	return nil
}

func checkAdminAttached(p *pass) {
	why := "Full administrator access turns any bug or leaked credential of whoever holds it into control of the whole account."
	for _, n := range p.resources() {
		if name, ok := adminAttachAttrs[n.Type]; ok {
			if a := getAttr(&n.Body, name); a != nil && adminPolicyRe.MatchString(source(a.Value)) {
				p.report(finding{
					node: n, attribute: name,
					message: fmt.Sprintf("%s attaches the AdministratorAccess managed policy", addr(n)),
					why:     why,
					fix:     "Attach a policy scoped to the actions and resources the principal needs; keep administrator access to a break-glass role assumed with MFA (or IAM Identity Center).",
				})
			}
		}
		paths := map[string]*model.Body{}
		if _, ok := identityPolicyAttrs[n.Type]; ok {
			paths["policy"] = &n.Body
		}
		if n.Type == "aws_iam_role" {
			for i, blk := range blocks(&n.Body, "inline_policy") {
				paths[fmt.Sprintf("inline_policy[%d].policy", i)] = &blk.Body
			}
		}
		for path, b := range paths {
			if d := p.wildcardDocument(b, "policy"); d != nil {
				p.report(finding{
					node: n, attribute: path,
					message: fmt.Sprintf("%s grants %s, which allows every action on every resource", addr(n), addr(d)),
					why:     why,
					fix:     "Scope the statements of the policy document to the actions the workload calls and the ARNs it uses.",
				})
			}
		}
	}
}

// ---------------------------------------------------------------------------
// S3

// deniesHTTP reports whether the policy in attribute name of b denies
// requests without TLS (a Deny statement conditioned on aws:SecureTransport
// = false). known is false when the policy is an expression Nimbo can't read
// (templatefile, a variable, …).
func (p *pass) deniesHTTP(b *model.Body, name string) (denies, known bool) {
	a := getAttr(b, name)
	if a == nil {
		return false, true
	}
	if d := p.referencedNode(b, name); d != nil && d.Kind == model.KindData && d.Type == "aws_iam_policy_document" {
		for _, st := range blocks(&d.Body, "statement") {
			if effect, _ := attrString(&st.Body, "effect"); effect != "Deny" {
				continue
			}
			for _, c := range blocks(&st.Body, "condition") {
				v, _ := attrString(&c.Body, "variable")
				if strings.EqualFold(v, "aws:SecureTransport") && slices.Contains(attrStrings(&c.Body, "values"), "false") {
					return true, true
				}
			}
		}
		return false, true
	}
	if s, ok := literal(a.Value).(string); ok {
		return jsonDeniesHTTP(s), true
	}
	e := parse(a.Value)
	if call, ok := e.(*hclsyntax.FunctionCallExpr); !ok || call.Name != "jsonencode" {
		return false, false
	}
	src := []byte(source(a.Value) + "\n")
	hclsyntax.VisitAll(e, func(node hclsyntax.Node) hcl.Diagnostics {
		obj, ok := node.(*hclsyntax.ObjectConsExpr)
		if !ok || denies {
			return nil
		}
		deny, cond := false, ""
		for _, item := range obj.Items {
			switch objectKey(item.KeyExpr) {
			case "Effect":
				deny = firstString(constStrings(item.ValueExpr)) == "Deny"
			case "Condition":
				cond = string(item.ValueExpr.Range().SliceBytes(src))
			}
		}
		denies = deny && strings.Contains(strings.ToLower(cond), "aws:securetransport") && strings.Contains(cond, "false")
		return nil
	})
	return denies, true
}

// jsonDeniesHTTP is deniesHTTP for a JSON policy document.
func jsonDeniesHTTP(doc string) bool {
	var policy struct {
		Statement json.RawMessage
	}
	if json.Unmarshal([]byte(doc), &policy) != nil {
		return false
	}
	var sts []struct {
		Effect    string
		Condition map[string]map[string]any
	}
	if json.Unmarshal(policy.Statement, &sts) != nil {
		var one struct {
			Effect    string
			Condition map[string]map[string]any
		}
		if json.Unmarshal(policy.Statement, &one) != nil {
			return false
		}
		sts = append(sts, one)
	}
	for _, st := range sts {
		if st.Effect != "Deny" {
			continue
		}
		for _, keys := range st.Condition {
			for k, v := range keys {
				if strings.EqualFold(k, "aws:SecureTransport") && strings.Contains(fmt.Sprint(v), "false") {
					return true
				}
			}
		}
	}
	return false
}

func checkS3DenyHTTP(p *pass) {
	for _, bucket := range p.ofType("aws_s3_bucket") {
		policies := []*model.Body{}
		if getAttr(&bucket.Body, "policy") != nil {
			policies = append(policies, &bucket.Body)
		}
		for _, n := range p.ofType("aws_s3_bucket_policy") {
			if a := getAttr(&n.Body, "bucket"); a != nil && referencesNode(a.Value, bucket) {
				policies = append(policies, &n.Body)
			}
		}
		ok, unknown := false, false
		for _, b := range policies {
			denies, known := p.deniesHTTP(b, "policy")
			ok = ok || denies
			unknown = unknown || !known
		}
		if ok || unknown {
			continue
		}
		msg := fmt.Sprintf("%s has no bucket policy", addr(bucket))
		if len(policies) > 0 {
			msg = fmt.Sprintf("%s's bucket policy doesn't deny plain HTTP requests", addr(bucket))
		}
		p.report(finding{
			node: bucket, message: msg,
			why: "S3 accepts HTTP as well as HTTPS; without a deny, a misconfigured client can send data and credentials in clear text.",
			fix: "Add a bucket policy statement { Effect = \"Deny\", Principal = \"*\", Action = \"s3:*\", Resource = [bucket ARN, \"${bucket ARN}/*\"], Condition = { Bool = { \"aws:SecureTransport\" = \"false\" } } }.",
		})
	}
}

var publicAccessFlags = []string{"block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"}

func checkS3BlockPublicAccess(p *pass) {
	for _, n := range p.ofType("aws_s3_bucket_public_access_block", "aws_s3_account_public_access_block") {
		var off []string
		for _, flag := range publicAccessFlags {
			if missingOrFalse(&n.Body, flag) {
				off = append(off, flag)
			}
		}
		if len(off) == 0 {
			continue
		}
		p.report(finding{
			node: n, attribute: off[0],
			message: fmt.Sprintf("%s leaves %s off", addr(n), strings.Join(off, ", ")),
			why:     "Block Public Access is the switch that keeps a bucket private whatever ACLs or policies are added later; CIS expects all four settings on for every bucket.",
			fix:     "Set all four flags to true. Serve public content through CloudFront with origin access control instead of opening the bucket.",
		})
	}
}

// ---------------------------------------------------------------------------
// RDS

var rdsTypes = []string{"aws_db_instance", "aws_rds_cluster", "aws_rds_cluster_instance"}

func checkRDSPublic(p *pass) {
	for _, n := range p.ofType(rdsTypes...) {
		if attrIsTrue(&n.Body, "publicly_accessible") {
			p.report(finding{
				node: n, attribute: "publicly_accessible",
				message: fmt.Sprintf("%s is publicly accessible", addr(n)),
				why:     "A public endpoint resolves to an internet address: the database is one security group rule away from being reachable (and brute-forced) from anywhere.",
				fix:     "Set publicly_accessible = false and reach the database from inside the VPC (application subnets, a bastion or SSM port forwarding).",
			})
		}
	}
}

func checkRDSMinorUpgrade(p *pass) {
	for _, n := range p.ofType(rdsTypes...) {
		if hasLiteralFalse(&n.Body, "auto_minor_version_upgrade") {
			p.report(finding{
				node: n, attribute: "auto_minor_version_upgrade",
				message: fmt.Sprintf("%s turns automatic minor version upgrades off", addr(n)),
				why:     "Minor versions carry the engine's security fixes; without automatic upgrades they wait for someone to remember.",
				fix:     "Remove auto_minor_version_upgrade = false (it defaults to true); upgrades run in the maintenance window.",
			})
		}
	}
}

func checkRDSMultiAZ(p *pass) {
	for _, n := range p.ofType("aws_db_instance") {
		engine, _ := attrString(&n.Body, "engine")
		// Read replicas follow their source, and Aurora instances get their
		// availability from the cluster's other instances.
		if getAttr(&n.Body, "replicate_source_db") != nil || strings.HasPrefix(engine, "aurora") || !missingOrFalse(&n.Body, "multi_az") {
			continue
		}
		p.report(finding{
			node: n, attribute: "multi_az",
			message: fmt.Sprintf("%s runs in a single Availability Zone", addr(n)),
			why:     "A single-AZ database goes down with its zone and during some maintenance; Multi-AZ keeps a synchronous standby that takes over automatically.",
			fix:     "Set multi_az = true (it roughly doubles the instance cost).",
		})
	}
}

// ---------------------------------------------------------------------------
// CloudTrail

// fieldEquals returns the equals values of the field selector for field in
// an advanced event selector.
func fieldEquals(sel *model.Body, field string) ([]string, bool) {
	for _, fs := range blocks(sel, "field_selector") {
		if f, _ := attrString(&fs.Body, "field"); f == field {
			return attrStrings(&fs.Body, "equals"), true
		}
	}
	return nil, false
}

// managementGap says why a trail doesn't record every read and write
// management event ("" when it does). Without selectors a trail records
// them all.
func managementGap(b *model.Body) (attr, why string) {
	if sels := blocks(b, "event_selector"); len(sels) > 0 {
		for _, s := range sels {
			rw, ok := attrString(&s.Body, "read_write_type")
			if !hasLiteralFalse(&s.Body, "include_management_events") && (!ok || rw == "All") {
				return "", ""
			}
		}
		return "event_selector", "its event selectors don't record all read and write management events"
	}
	if sels := blocks(b, "advanced_event_selector"); len(sels) > 0 {
		for _, s := range sels {
			cat, _ := fieldEquals(&s.Body, "eventCategory")
			if _, readOnly := fieldEquals(&s.Body, "readOnly"); slices.Contains(cat, "Management") && !readOnly {
				return "", ""
			}
		}
		return "advanced_event_selector", "its advanced event selectors don't record all read and write management events"
	}
	return "", ""
}

func checkTrailMultiRegion(p *pass) {
	why := "Attackers use the regions nobody watches; a multi-Region trail recording every management event is the account's audit log."
	for _, n := range p.ofType("aws_cloudtrail") {
		if missingOrFalse(&n.Body, "is_multi_region_trail") {
			p.report(finding{
				node: n, attribute: "is_multi_region_trail",
				message: fmt.Sprintf("%s only records its own region", addr(n)),
				why:     why, fix: "Set is_multi_region_trail = true.",
			})
		}
		if hasLiteralFalse(&n.Body, "enable_logging") {
			p.report(finding{
				node: n, attribute: "enable_logging",
				message: fmt.Sprintf("%s has logging turned off", addr(n)),
				why:     why, fix: "Remove enable_logging = false (it defaults to true).",
			})
		}
		if attr, gap := managementGap(&n.Body); gap != "" {
			p.report(finding{
				node: n, attribute: attr,
				message: fmt.Sprintf("%s: %s", addr(n), gap),
				why:     why, fix: "Keep an event selector with read_write_type = \"All\" and include_management_events = true (or an advanced selector on eventCategory = Management without a readOnly filter).",
			})
		}
	}
}

func checkTrailLogValidation(p *pass) {
	for _, n := range p.ofType("aws_cloudtrail") {
		if missingOrFalse(&n.Body, "enable_log_file_validation") {
			p.report(finding{
				node: n, attribute: "enable_log_file_validation",
				message: fmt.Sprintf("%s doesn't validate its log files", addr(n)),
				why:     "Without digest files nobody can tell whether a log file was changed or deleted after CloudTrail delivered it.",
				fix:     "Set enable_log_file_validation = true.",
			})
		}
	}
}

func checkTrailKMS(p *pass) {
	for _, n := range p.ofType("aws_cloudtrail") {
		if s, ok := attrString(&n.Body, "kms_key_id"); getAttr(&n.Body, "kms_key_id") == nil || ok && s == "" {
			p.report(finding{
				node: n, attribute: "kms_key_id",
				message: fmt.Sprintf("%s doesn't encrypt its logs with a KMS key", addr(n)),
				why:     "With SSE-KMS, reading the logs also needs kms:Decrypt on the key: one more control on who sees the account's activity, audited in CloudTrail itself.",
				fix:     "Set kms_key_id to an aws_kms_key ARN whose key policy lets cloudtrail.amazonaws.com use it.",
			})
		}
	}
}

func checkTrailBucketLogging(p *pass) {
	for _, n := range p.ofType("aws_cloudtrail") {
		bucket := p.referencedNode(&n.Body, "s3_bucket_name")
		if bucket == nil || bucket.Type != "aws_s3_bucket" {
			continue // a bucket outside the design: can't be checked here
		}
		if len(blocks(&bucket.Body, "logging")) > 0 || p.referencedBy(bucket, "aws_s3_bucket_logging", "bucket") {
			continue
		}
		p.report(finding{
			node: n, attribute: "s3_bucket_name",
			message: fmt.Sprintf("%s writes to %s, which has no server access logging", addr(n), addr(bucket)),
			why:     "Access logs of the trail bucket show who read or deleted the audit logs themselves.",
			fix:     fmt.Sprintf("Add an aws_s3_bucket_logging for %s that targets a separate log bucket.", addr(bucket)),
		})
	}
}

// logsS3Objects reports whether a trail records S3 object-level write (or
// read) data events.
func logsS3Objects(b *model.Body, write bool) bool {
	want := "ReadOnly"
	if write {
		want = "WriteOnly"
	}
	for _, s := range blocks(b, "event_selector") {
		if rw, ok := attrString(&s.Body, "read_write_type"); ok && rw != "All" && rw != want {
			continue
		}
		for _, dr := range blocks(&s.Body, "data_resource") {
			if t, _ := attrString(&dr.Body, "type"); t == "AWS::S3::Object" {
				return true
			}
		}
	}
	for _, s := range blocks(b, "advanced_event_selector") {
		cat, _ := fieldEquals(&s.Body, "eventCategory")
		types, _ := fieldEquals(&s.Body, "resources.type")
		if !slices.Contains(cat, "Data") || !slices.Contains(types, "AWS::S3::Object") {
			continue
		}
		ro, filtered := fieldEquals(&s.Body, "readOnly")
		if !filtered || slices.Contains(ro, fmt.Sprint(!write)) {
			return true
		}
	}
	return false
}

func checkTrailS3Events(p *pass, write bool) {
	trails := p.ofType("aws_cloudtrail")
	if slices.ContainsFunc(trails, func(n *model.Node) bool { return logsS3Objects(&n.Body, write) }) {
		return
	}
	kind, rw, verb := "read", "ReadOnly", "downloaded"
	if write {
		kind, rw, verb = "write", "WriteOnly", "uploaded, overwrote or deleted"
	}
	for _, n := range trails {
		p.report(finding{
			node: n, attribute: "event_selector",
			message: fmt.Sprintf("No trail records S3 object-level %s events (%s doesn't)", kind, addr(n)),
			why:     "Management events don't show who " + verb + " an object; object-level data events do.",
			fix:     fmt.Sprintf("Add event_selector { read_write_type = \"All\" (or %q) data_resource { type = \"AWS::S3::Object\" values = [\"arn:aws:s3\"] } } to the trail (data events are billed per event).", rw),
		})
	}
}

// ---------------------------------------------------------------------------
// KMS

func checkKMSRotation(p *pass) {
	for _, n := range p.ofType("aws_kms_key") {
		b := &n.Body
		// Only symmetric encryption keys in the AWS KMS key store can rotate.
		usage, usageOK := attrString(b, "key_usage")
		spec, specOK := attrString(b, "customer_master_key_spec")
		switch {
		case getAttr(b, "custom_key_store_id") != nil,
			getAttr(b, "key_usage") != nil && (!usageOK || usage != "ENCRYPT_DECRYPT"),
			getAttr(b, "customer_master_key_spec") != nil && (!specOK || spec != "SYMMETRIC_DEFAULT"),
			!missingOrFalse(b, "enable_key_rotation"):
			continue
		}
		p.report(finding{
			node: n, attribute: "enable_key_rotation",
			message: fmt.Sprintf("%s doesn't rotate its key material", addr(n)),
			why:     "Rotation limits how much data one key version protects; old versions keep decrypting, so it is free of downtime.",
			fix:     "Set enable_key_rotation = true (yearly by default; rotation_period_in_days changes it).",
		})
	}
}

// ---------------------------------------------------------------------------
// VPC and EC2

func checkVPCFlowLogs(p *pass) {
	logs := p.ofType("aws_flow_log")
	for _, vpc := range p.ofType("aws_vpc", "aws_default_vpc") {
		covered, acceptOnly := false, false
		for _, fl := range logs {
			if a := getAttr(&fl.Body, "vpc_id"); a == nil || !referencesNode(a.Value, vpc) {
				continue
			}
			if t, ok := attrString(&fl.Body, "traffic_type"); ok && t == "ACCEPT" {
				acceptOnly = true
				continue
			}
			covered = true
		}
		if covered {
			continue
		}
		msg := fmt.Sprintf("%s has no flow log", addr(vpc))
		if acceptOnly {
			msg = fmt.Sprintf("%s's flow logs only record accepted traffic", addr(vpc))
		}
		p.report(finding{
			node: vpc, message: msg,
			why: "Flow logs are the record of who tried to reach what in the network; rejected connections are often the first sign of scanning or a compromised host.",
			fix: fmt.Sprintf("Add an aws_flow_log with vpc_id = %s.id and traffic_type = \"REJECT\" (or \"ALL\"), delivering to CloudWatch Logs or S3.", addr(vpc)),
		})
	}
}

var cisAdminPorts = []int{22, 3389}

// protoPorts reads the port range of a rule for TCP, UDP or all protocols
// (from == allPorts); ok is false for other protocols or non-constant ports.
func protoPorts(b *model.Body, protoAttr string) (from, to int, ok bool) {
	proto, _ := attrString(b, protoAttr)
	if n, isNum := attrNumber(b, protoAttr); isNum {
		proto = fmt.Sprint(n)
	}
	switch strings.ToLower(proto) {
	case "-1", "all":
		return allPorts, allPorts, true
	case "tcp", "udp", "6", "17":
		f, okF := attrNumber(b, "from_port")
		t, okT := attrNumber(b, "to_port")
		return int(f), int(t), okF && okT
	}
	return 0, 0, false
}

// hitPorts lists the ports of want inside [from, to] (all of them for
// allPorts).
func hitPorts(from, to int, want []int) []string {
	var out []string
	for _, port := range want {
		if from == allPorts || from <= port && port <= to {
			out = append(out, fmt.Sprint(port))
		}
	}
	return out
}

func checkNACLAdminPorts(p *pass) {
	check := func(n *model.Node, b *model.Body, actionAttr, path string) {
		if action, _ := attrString(b, actionAttr); !strings.EqualFold(action, "allow") {
			return
		}
		cidrs := append(attrStrings(b, "cidr_block"), attrStrings(b, "ipv6_cidr_block")...)
		i := slices.IndexFunc(cidrs, isOpenCIDR)
		if i < 0 {
			return
		}
		from, to, ok := protoPorts(b, "protocol")
		hit := hitPorts(from, to, cisAdminPorts)
		if !ok || len(hit) == 0 {
			return
		}
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s allows %s to port %s", addr(n), cidrs[i], strings.Join(hit, " and ")),
			why:     "SSH and RDP open to the internet are scanned and brute-forced within minutes; the network ACL is the subnet-wide layer that should already stop it.",
			fix:     "Allow port 22/3389 only from your VPN or office CIDR (and keep ephemeral return-traffic ranges above 3389, e.g. 49152-65535), or reach hosts through SSM Session Manager.",
		})
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_network_acl", "aws_default_network_acl":
			for i, blk := range blocks(&n.Body, "ingress") {
				check(n, &blk.Body, "action", fmt.Sprintf("ingress[%d]", i))
			}
		case "aws_network_acl_rule":
			if !attrIsTrue(&n.Body, "egress") {
				check(n, &n.Body, "rule_action", "cidr_block")
			}
		}
	}
}

// sgIngress visits the ingress rules of security groups: inline blocks of
// aws_security_group / aws_default_security_group and the standalone rule
// resources, with their sources and the attribute holding the protocol.
func (p *pass) sgIngress(fn func(n *model.Node, path string, b *model.Body, sources []string, protoAttr string)) {
	for _, n := range p.resources() {
		switch n.Type {
		case "aws_security_group", "aws_default_security_group":
			for i, blk := range blocks(&n.Body, "ingress") {
				fn(n, fmt.Sprintf("ingress[%d]", i), &blk.Body, append(attrStrings(&blk.Body, "cidr_blocks"), attrStrings(&blk.Body, "ipv6_cidr_blocks")...), "protocol")
			}
		case "aws_security_group_rule":
			if t, _ := attrString(&n.Body, "type"); t == "ingress" {
				fn(n, "cidr_blocks", &n.Body, append(attrStrings(&n.Body, "cidr_blocks"), attrStrings(&n.Body, "ipv6_cidr_blocks")...), "protocol")
			}
		case "aws_vpc_security_group_ingress_rule":
			fn(n, "cidr_ipv4", &n.Body, append(attrStrings(&n.Body, "cidr_ipv4"), attrStrings(&n.Body, "cidr_ipv6")...), "ip_protocol")
		}
	}
}

// checkSGOpen reports security group ingress from source (any open CIDR
// when "") to one of ports.
func checkSGOpen(p *pass, source string, ports []int) {
	p.sgIngress(func(n *model.Node, path string, b *model.Body, sources []string, protoAttr string) {
		open := slices.IndexFunc(sources, func(s string) bool { return s == source || source == "" && isOpenCIDR(s) })
		if open < 0 {
			return
		}
		from, to, ok := protoPorts(b, protoAttr)
		hit := hitPorts(from, to, ports)
		if !ok || len(hit) == 0 {
			return
		}
		if ipv6 := strings.Contains(sources[open], ":"); ipv6 && n.Type == "aws_vpc_security_group_ingress_rule" {
			path = "cidr_ipv6"
		} else if ipv6 && n.Type == "aws_security_group_rule" {
			path = "ipv6_cidr_blocks"
		}
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s allows %s to port %s", addr(n), sources[open], strings.Join(hit, " and ")),
			why:     "Remote administration and file-sharing ports open to the internet are scanned within minutes, brute-forced and hit by worms (SMB was WannaCry's way in).",
			fix:     "Restrict the source to your VPN/office CIDR or a security group (referenced_security_group_id), or reach hosts through SSM Session Manager.",
		})
	})
}

// sgHasRules: the default security group declares ingress or egress rules
// (blocks, dynamic blocks or a non-empty list).
func sgHasRules(b *model.Body) bool {
	for _, dir := range []string{"ingress", "egress"} {
		if len(blocks(b, dir)) > 0 {
			return true
		}
		if a := getAttr(b, dir); a != nil {
			if l, isList := literal(a.Value).([]any); isList && len(l) > 0 {
				return true
			}
		}
	}
	return slices.ContainsFunc(blocks(b, "dynamic"), func(d *model.Block) bool {
		return len(d.Labels) > 0 && (d.Labels[0] == "ingress" || d.Labels[0] == "egress")
	})
}

func checkDefaultSGClosed(p *pass) {
	why := "AWS gives every VPC a default security group that allows all outbound traffic and all traffic between its members; anything launched without a group lands in it."
	fix := "Manage it with an empty aws_default_security_group { vpc_id = <vpc>.id } (Terraform then removes every rule) and put workloads in least-privilege groups of their own."
	vpcs := p.ofType("aws_vpc", "aws_default_vpc")
	managed := map[*model.Node]bool{}
	report := func(n *model.Node, attr, msg string) {
		p.report(finding{node: n, attribute: attr, message: msg, why: why, fix: fix})
	}
	for _, sg := range p.ofType("aws_default_security_group") {
		vpc := p.referencedNode(&sg.Body, "vpc_id")
		if getAttr(&sg.Body, "vpc_id") == nil {
			// Without vpc_id it manages the default VPC's group.
			for _, v := range vpcs {
				if v.Type == "aws_default_vpc" {
					managed[v] = true
				}
			}
		}
		if vpc != nil {
			managed[vpc] = true
		}
		if !sgHasRules(&sg.Body) {
			continue
		}
		report(sg, "", fmt.Sprintf("%s keeps traffic rules on the default security group", addr(sg)))
		if vpc != nil {
			report(vpc, "", fmt.Sprintf("%s's default security group (%s) allows traffic", addr(vpc), addr(sg)))
		}
	}
	for _, r := range p.ofType("aws_security_group_rule", "aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule") {
		a := getAttr(&r.Body, "security_group_id")
		if a == nil {
			continue
		}
		target := p.referencedNode(&r.Body, "security_group_id")
		if target == nil {
			continue
		}
		msg := fmt.Sprintf("%s adds a rule to %s", addr(r), addr(target))
		switch {
		case target.Type == "aws_default_security_group":
		case (target.Type == "aws_vpc" || target.Type == "aws_default_vpc") && slices.ContainsFunc(references(a.Value), func(ref string) bool {
			return strings.HasSuffix(ref, ".default_security_group_id")
		}):
			msg = fmt.Sprintf("%s adds a rule to the default security group of %s", addr(r), addr(target))
		default:
			continue
		}
		report(target, "", msg)
	}
	for _, v := range vpcs {
		if !managed[v] {
			report(v, "", fmt.Sprintf("%s leaves its default security group with AWS's allow rules", addr(v)))
		}
	}
}

func checkIMDSv2(p *pass) {
	accountRequired := false
	for _, n := range p.ofType("aws_ec2_instance_metadata_defaults") {
		if t, _ := attrString(&n.Body, "http_tokens"); t == "required" {
			accountRequired = true
		}
	}
	why := "IMDSv1 answers plain GET requests, so a server-side request forgery bug in the app hands out the instance role's credentials; IMDSv2 needs a session token that SSRF can't get."
	for _, n := range p.ofType("aws_instance", "aws_launch_template", "aws_launch_configuration") {
		opts := blocks(&n.Body, "metadata_options")
		var tokens string
		tokensSet := false
		if len(opts) > 0 {
			b := &opts[0].Body
			if e, _ := attrString(b, "http_endpoint"); e == "disabled" {
				continue
			}
			if a := getAttr(b, "http_tokens"); a != nil {
				var ok bool
				if tokens, ok = attrString(b, "http_tokens"); !ok {
					continue // an expression: can't tell
				}
				tokensSet = true
			}
		}
		switch {
		case tokens == "required":
		case tokensSet:
			p.report(finding{
				severity: model.SeverityWarning, node: n, attribute: "metadata_options[0].http_tokens",
				message: fmt.Sprintf("%s allows IMDSv1 (http_tokens = %q)", addr(n), tokens),
				why:     why, fix: "Set http_tokens = \"required\" in metadata_options.",
			})
		case !accountRequired:
			p.report(finding{
				node: n, attribute: "metadata_options",
				message: fmt.Sprintf("%s doesn't require IMDSv2", addr(n)),
				why:     why, fix: "Add metadata_options { http_tokens = \"required\" } (or require it account-wide with aws_ec2_instance_metadata_defaults).",
			})
		}
	}
}
