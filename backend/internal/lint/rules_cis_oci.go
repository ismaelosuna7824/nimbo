package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// OCI rules backing the CIS Oracle Cloud Infrastructure Foundations
// Benchmark (compliance/cis_oci.go). The ones marked optIn (customer-managed
// keys, flow logs, versioning, instance hardening, ...) are compliance
// requirements rather than general advice: the catalog templates and fresh
// starters don't follow them, so they stay out of Problems until the user
// turns them on, and compliance grading runs them anyway.
func init() {
	rules = append(rules,
		ociRule(warning("bp_oci_ssh_open", "OCI: no SSH from the internet", CategorySecurity,
			"Security lists and network security groups don't allow ingress from 0.0.0.0/0 to TCP port 22 (CIS OCI 2.1, 2.3)."),
			"https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securitylists.htm", false, checkOCISSHOpen),
		ociRule(warning("bp_oci_rdp_open", "OCI: no RDP from the internet", CategorySecurity,
			"Security lists and network security groups don't allow ingress from 0.0.0.0/0 to TCP port 3389 (CIS OCI 2.2, 2.4)."),
			"https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/networksecuritygroups.htm", false, checkOCIRDPOpen),
		ociRule(info("bp_oci_default_security_list", "OCI: default security lists are locked down", CategorySecurity,
			"Every VCN manages its default security list (oci_core_default_security_list) and it allows no traffic from 0.0.0.0/0 and no egress to it except ICMP (CIS OCI 2.5)."),
			"https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securitylists.htm", true, checkOCIDefaultSecurityList),
		ociRule(info("bp_oci_unrestricted_endpoint", "OCI: service endpoints restrict their sources", CategorySecurity,
			"Autonomous Databases, Integration and Analytics instances are private (in a VCN) or have an allowlist without 0.0.0.0/0 (CIS OCI 2.6, 2.7, 2.8)."),
			"https://docs.oracle.com/en-us/iaas/autonomous-database-serverless/doc/access-control-rules-autonomous.html", false, checkOCIUnrestrictedEndpoint),
		ociRule(info("bp_oci_legacy_imds", "OCI: instances disable the legacy metadata endpoint", CategorySecurity,
			"Compute instances set instance_options.are_legacy_imds_endpoints_disabled = true so only IMDSv2 answers (CIS OCI 3.1)."),
			"https://docs.oracle.com/en-us/iaas/Content/Compute/Tasks/gettingmetadata.htm", true, checkOCILegacyIMDS),
		ociRule(info("bp_oci_secure_boot", "OCI: instances use Secure Boot", CategorySecurity,
			"Compute instances are shielded instances with platform_config.is_secure_boot_enabled = true (CIS OCI 3.2)."),
			"https://docs.oracle.com/en-us/iaas/Content/Compute/References/shielded-instances.htm", true, checkOCISecureBoot),
		ociRule(info("bp_oci_in_transit_encryption", "OCI: instances encrypt volume traffic", CategorySecurity,
			"Compute instances enable in-transit encryption of their paravirtualized volume attachments (is_pv_encryption_in_transit_enabled; CIS OCI 3.3)."),
			"https://docs.oracle.com/en-us/iaas/Content/Block/Concepts/overview.htm", true, checkOCIInTransitEncryption),
		ociRule(info("bp_oci_flow_logs", "OCI: subnets have flow logs", CategorySecurity,
			"Every subnet is covered by a VCN flow log: an oci_logging_log with source service \"flowlogs\" for the subnet or its VCN (CIS OCI 4.13)."),
			"https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/vcn-flow-logs.htm", true, checkOCIFlowLogs),
		ociRule(info("bp_oci_key_rotation", "OCI: keys rotate at least yearly", CategorySecurity,
			"Master encryption keys in a virtual private vault enable automatic rotation with an interval of 365 days or less (CIS OCI 4.16)."),
			"https://docs.oracle.com/en-us/iaas/Content/KeyManagement/Tasks/tasks_tasks_managingkeys_topic_edit_auto_key_rotation.htm", false, checkOCIKeyRotation),
		ociRule(info("bp_oci_bucket_write_logs", "OCI: buckets log writes", CategorySecurity,
			"Every Object Storage bucket has a service log (oci_logging_log) with service \"objectstorage\" and category \"write\" (CIS OCI 4.17)."),
			"https://docs.oracle.com/en-us/iaas/Content/Logging/Reference/details_for_object_storage.htm", true, checkOCIBucketWriteLogs),
		ociRule(warning("bp_oci_public_bucket", "OCI: buckets are private unless meant to be public", CategorySecurity,
			"Object Storage buckets keep access_type = \"NoPublicAccess\" unless tagged Public = \"true\" in freeform_tags (CIS OCI 5.1.1)."),
			"https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/managingbuckets.htm", false, checkOCIPublicBucket),
		ociRule(info("bp_oci_customer_managed_key", "OCI: storage uses customer-managed keys", CategorySecurity,
			"Buckets, block volumes, boot volumes and file systems are encrypted with a Vault key (kms_key_id) instead of the Oracle-managed default (CIS OCI 5.1.2, 5.2.1, 5.2.2, 5.3.1)."),
			"https://docs.oracle.com/en-us/iaas/Content/KeyManagement/Concepts/keyoverview.htm", true, checkOCICustomerManagedKey),
		ociRule(info("bp_oci_bucket_versioning", "OCI: buckets keep object versions", CategorySecurity,
			"Object Storage buckets set versioning = \"Enabled\" so overwritten or deleted objects can be recovered (CIS OCI 5.1.3)."),
			"https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/usingversioning.htm", true, checkOCIBucketVersioning),
		ociRule(warning("bp_oci_policy_manage_all", "OCI: only Administrators manage all resources in the tenancy", CategorySecurity,
			"IAM policies don't let any group but Administrators (nor any-user or dynamic groups) manage all-resources in tenancy (CIS OCI 1.2)."),
			"https://docs.oracle.com/en-us/iaas/Content/Identity/Concepts/commonpolicies.htm", false, checkOCIPolicyManageAll),
		ociRule(info("bp_oci_policy_admin_group", "OCI: IAM admins can't change the Administrators group", CategorySecurity,
			"Policies granting use or manage on users, groups or all-resources in tenancy add where target.group.name != 'Administrators' (CIS OCI 1.3)."),
			"https://docs.oracle.com/en-us/iaas/Content/Identity/Concepts/commonpolicies.htm", false, checkOCIPolicyAdminGroup),
		ociRule(info("bp_oci_storage_admin_delete", "OCI: storage admins can't delete what they manage", CategorySecurity,
			"Policies letting a group manage buckets, objects, volumes or file systems exclude the delete permissions (where request.permission != '..._DELETE'), or grant only them (CIS OCI 1.15)."),
			"https://docs.oracle.com/en-us/iaas/Content/Security/Reference/iam_security.htm", false, checkOCIStorageAdminDelete),
	)
}

func ociRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass)) rule {
	r.Docs = docs
	r.DefaultOff = optIn
	return rule{r, check}
}

// ---------------------------------------------------------------------------
// bp_oci_ssh_open, bp_oci_rdp_open (CIS 2.1-2.4)

// ruleBodies returns the bodies of the typ blocks of b with their path,
// including the content of dynamic "typ" blocks (whose values are mostly
// references, but a literal source/port in them is still a finding).
func ruleBodies(b *model.Body, typ string) (bodies []*model.Body, paths []string) {
	for i, blk := range blocks(b, typ) {
		bodies = append(bodies, &blk.Body)
		paths = append(paths, fmt.Sprintf("%s[%d]", typ, i))
	}
	for _, d := range blocks(b, "dynamic") {
		if len(d.Labels) == 1 && d.Labels[0] == typ {
			for _, c := range blocks(&d.Body, "content") {
				bodies = append(bodies, &c.Body)
				paths = append(paths, typ)
			}
		}
	}
	return bodies, paths
}

// ociProtocol reads an OCI rule protocol: "all", "6" (TCP), "17", "1", ...
// Written as a number or a string; "" when not constant.
func ociProtocol(b *model.Body) string {
	if n, ok := attrNumber(b, "protocol"); ok {
		return fmt.Sprint(int(n))
	}
	s, _ := attrString(b, "protocol")
	return strings.ToLower(s)
}

// ociPortRange reads the destination port range of an OCI TCP rule: the
// min/max of opts (security lists) or of opts.destination_port_range (NSG
// rules). No options block means every port; ok is false when the ports
// are not constants.
func ociPortRange(b *model.Body, nested bool) (from, to int, ok bool) {
	opts := blocks(b, "tcp_options")
	if len(opts) == 0 {
		return 0, 65535, true
	}
	r := &opts[0].Body
	if nested {
		dst := blocks(r, "destination_port_range")
		if len(dst) == 0 {
			return 0, 65535, true
		}
		r = &dst[0].Body
	}
	lo, okLo := attrNumber(r, "min")
	hi, okHi := attrNumber(r, "max")
	if !okLo || !okHi {
		return 0, 0, false
	}
	return int(lo), int(hi), true
}

// openIngressRule reports whether the security list / NSG rule body lets
// the internet reach TCP port.
func openIngressRule(b *model.Body, port int, nested bool) bool {
	if st, ok := attrString(b, "source_type"); ok && !strings.EqualFold(st, "CIDR_BLOCK") {
		return false
	}
	if !anyOpen(attrStrings(b, "source")) {
		return false
	}
	switch ociProtocol(b) {
	case "all":
		return true
	case "6":
		from, to, ok := ociPortRange(b, nested)
		return ok && from <= port && port <= to
	}
	return false
}

func checkOCIPortOpen(p *pass, port int, service, fix string) {
	why := service + " open to the whole internet is scanned within minutes and brute-forced; a weak credential or unpatched service is then remotely exploitable."
	report := func(n *model.Node, path, kind string) {
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s allows %s (port %d) from the internet", addr(n), service, port),
			why:     why,
			fix:     fmt.Sprintf("Restrict the %s source to your VPN/office CIDR or a network security group, %s", kind, fix),
		})
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "oci_core_security_list", "oci_core_default_security_list":
			bodies, paths := ruleBodies(&n.Body, "ingress_security_rules")
			for i, b := range bodies {
				if openIngressRule(b, port, false) {
					report(n, paths[i], "rule's")
				}
			}
		case "oci_core_network_security_group_security_rule":
			if d, _ := attrString(&n.Body, "direction"); strings.EqualFold(d, "INGRESS") && openIngressRule(&n.Body, port, true) {
				report(n, "source", "rule's")
			}
		}
	}
}

func checkOCISSHOpen(p *pass) {
	checkOCIPortOpen(p, 22, "SSH", "or reach the hosts through the OCI Bastion service.")
}

func checkOCIRDPOpen(p *pass) {
	checkOCIPortOpen(p, 3389, "RDP", "or reach the hosts through the OCI Bastion service (RDP port forwarding).")
}

// ---------------------------------------------------------------------------
// bp_oci_default_security_list (CIS 2.5)

func checkOCIDefaultSecurityList(p *pass) {
	why := "The default security list of a VCN applies to every subnet that doesn't name its own lists; as created by OCI it allows SSH from 0.0.0.0/0 and all egress."
	defaults := p.ofType("oci_core_default_security_list")
	for _, vcn := range p.ofType("oci_core_vcn") {
		managed := slices.ContainsFunc(defaults, func(d *model.Node) bool {
			a := getAttr(&d.Body, "manage_default_resource_id")
			return a != nil && referencesNode(a.Value, vcn)
		})
		if !managed {
			p.report(finding{
				node:    vcn,
				message: fmt.Sprintf("%s keeps OCI's default security list (SSH from 0.0.0.0/0, all egress)", addr(vcn)),
				why:     why,
				fix:     fmt.Sprintf("Add an oci_core_default_security_list with manage_default_resource_id = %s.default_security_list_id and at most ICMP rules from the VCN's CIDR; give subnets their own security lists or NSGs.", addr(vcn)),
			})
		}
	}
	for _, n := range defaults {
		ingress, inPaths := ruleBodies(&n.Body, "ingress_security_rules")
		egress, outPaths := ruleBodies(&n.Body, "egress_security_rules")
		var bad []string
		for i, b := range ingress {
			if anyOpen(attrStrings(b, "source")) {
				bad = append(bad, inPaths[i])
			}
		}
		for i, b := range egress {
			if anyOpen(attrStrings(b, "destination")) && ociProtocol(b) != "1" {
				bad = append(bad, outPaths[i])
			}
		}
		if len(bad) == 0 {
			continue
		}
		p.report(finding{
			node: n, attribute: bad[0],
			message: fmt.Sprintf("%s opens %s to 0.0.0.0/0", addr(n), strings.Join(bad, ", ")),
			why:     why,
			fix:     "Keep only ICMP rules from the VCN's CIDR in the default security list and move workload rules to dedicated security lists or NSGs.",
		})
	}
}

// ---------------------------------------------------------------------------
// bp_oci_unrestricted_endpoint (CIS 2.6-2.8)

// openOrEmpty: none of the allowlist values are set, or one of the
// constant ones is 0.0.0.0/0.
func openOrEmpty(values ...*model.Attr) (empty, open bool) {
	empty = true
	for _, a := range values {
		if a == nil {
			continue
		}
		empty = false
		if anyOpen(stringsOf(a.Value)) {
			open = true
		}
	}
	return empty, open
}

func checkOCIUnrestrictedEndpoint(p *pass) {
	why := "A service endpoint reachable from any address leaves only its login between the internet and your data; allowlists or a private endpoint cut off scanners and stolen-credential use from elsewhere."
	report := func(n *model.Node, attr, msg, fix string) {
		p.report(finding{node: n, attribute: attr, message: msg, why: why, fix: fix})
	}
	for _, n := range p.resources() {
		b := &n.Body
		switch n.Type {
		case "oci_database_autonomous_database":
			// CIS 2.8 covers serverless databases; dedicated ones live in a VCN.
			if getAttr(b, "autonomous_container_database_id") != nil || attrIsTrue(b, "is_dedicated") {
				continue
			}
			empty, open := openOrEmpty(getAttr(b, "whitelisted_ips"))
			switch {
			case open:
				report(n, "whitelisted_ips", fmt.Sprintf("%s allows connections from 0.0.0.0/0", addr(n)),
					"Replace 0.0.0.0/0 in whitelisted_ips with your client CIDRs or VCN OCIDs, or use a private endpoint (subnet_id).")
			case empty && getAttr(b, "subnet_id") == nil:
				report(n, "whitelisted_ips", fmt.Sprintf("%s accepts connections from any IP address", addr(n)),
					"Set whitelisted_ips to your client CIDRs or VCN OCIDs, or give the database a private endpoint with subnet_id (and nsg_ids).")
			}
		case "oci_integration_integration_instance":
			nets := blocks(b, "network_endpoint_details")
			if len(nets) == 0 {
				report(n, "network_endpoint_details", fmt.Sprintf("%s accepts requests from any IP address", addr(n)),
					"Add network_endpoint_details { network_endpoint_type = \"PUBLIC\" } with allowlisted_http_ips or allowlisted_http_vcns for your callers.")
				continue
			}
			nb := &nets[0].Body
			attrs := []*model.Attr{getAttr(nb, "allowlisted_http_ips")}
			for _, sub := range []string{"allowlisted_http_vcns", "design_time", "runtime"} {
				for _, blk := range blocks(nb, sub) {
					attrs = append(attrs, getAttr(&blk.Body, "allowlisted_http_ips"), getAttr(&blk.Body, "allowlisted_ips"))
				}
			}
			empty, open := openOrEmpty(attrs...)
			if len(blocks(nb, "allowlisted_http_vcns")) > 0 || attrNotFalse(nb, "is_integration_vcn_allowlisted") {
				empty = false
			}
			switch {
			case open:
				report(n, "network_endpoint_details", fmt.Sprintf("%s allowlists 0.0.0.0/0", addr(n)),
					"Replace 0.0.0.0/0 in the allowlist with the CIDRs or VCNs that call the instance.")
			case empty:
				report(n, "network_endpoint_details", fmt.Sprintf("%s has no allowlist", addr(n)),
					"Set allowlisted_http_ips or allowlisted_http_vcns in network_endpoint_details to the callers of the instance.")
			}
		case "oci_analytics_analytics_instance":
			nets := blocks(b, "network_endpoint_details")
			if len(nets) == 0 {
				continue // required by the provider; a validation error, not a finding
			}
			nb := &nets[0].Body
			if t, _ := attrString(nb, "network_endpoint_type"); !strings.EqualFold(t, "PUBLIC") {
				continue
			}
			attrs := []*model.Attr{getAttr(nb, "whitelisted_ips"), getAttr(nb, "whitelisted_services")}
			for _, blk := range blocks(nb, "whitelisted_vcns") {
				attrs = append(attrs, getAttr(&blk.Body, "whitelisted_ips"), getAttr(&blk.Body, "id"))
			}
			empty, open := openOrEmpty(attrs...)
			switch {
			case open:
				report(n, "network_endpoint_details", fmt.Sprintf("%s allowlists 0.0.0.0/0", addr(n)),
					"Replace 0.0.0.0/0 in whitelisted_ips with your users' CIDRs, or use network_endpoint_type = \"PRIVATE\" in a VCN.")
			case empty:
				report(n, "network_endpoint_details", fmt.Sprintf("%s has a public endpoint without an allowlist", addr(n)),
					"Set whitelisted_ips or whitelisted_vcns, or use network_endpoint_type = \"PRIVATE\" with vcn_id and subnet_id.")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Compute instances (CIS 3.1-3.3)

// nestedNotFalse: some block typ of b sets attr to anything but a literal
// false (a reference counts as intent).
func nestedNotFalse(b *model.Body, typ, attr string) bool {
	return slices.ContainsFunc(blocks(b, typ), func(blk *model.Block) bool { return attrNotFalse(&blk.Body, attr) })
}

func checkOCILegacyIMDS(p *pass) {
	for _, n := range p.ofType("oci_core_instance") {
		if nestedNotFalse(&n.Body, "instance_options", "are_legacy_imds_endpoints_disabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "instance_options",
			message: fmt.Sprintf("%s still serves the legacy (v1) metadata endpoints", addr(n)),
			why:     "IMDSv1 answers any plain GET, so a server-side request forgery in the app can read instance metadata and its credentials; IMDSv2 requires a header such requests can't add.",
			fix:     "Add instance_options { are_legacy_imds_endpoints_disabled = true } (current Oracle Linux and Windows images use IMDSv2).",
		})
	}
}

func checkOCISecureBoot(p *pass) {
	for _, n := range p.ofType("oci_core_instance") {
		if nestedNotFalse(&n.Body, "platform_config", "is_secure_boot_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "platform_config",
			message: fmt.Sprintf("%s doesn't use Secure Boot", addr(n)),
			why:     "Secure Boot only runs signed boot loaders and kernels, so a rootkit can't persist in the boot chain.",
			fix:     "Add platform_config { type = \"<the shape's platform, e.g. AMD_VM>\", is_secure_boot_enabled = true } on a shape and image that support shielded instances.",
		})
	}
}

func checkOCIInTransitEncryption(p *pass) {
	for _, n := range p.ofType("oci_core_instance") {
		if attrNotFalse(&n.Body, "is_pv_encryption_in_transit_enabled") || nestedNotFalse(&n.Body, "launch_options", "is_pv_encryption_in_transit_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "is_pv_encryption_in_transit_enabled",
			message: fmt.Sprintf("%s doesn't encrypt its volume traffic in transit", addr(n)),
			why:     "Without in-transit encryption, data moving between the instance and its boot and block volumes crosses the network in clear text.",
			fix:     "Set is_pv_encryption_in_transit_enabled = true (paravirtualized attachments).",
		})
	}
}

// ---------------------------------------------------------------------------
// Logs (CIS 4.13, 4.17)

// serviceLog is the source of an oci_logging_log service log.
type serviceLog struct {
	node     *model.Node
	category string
	resource *model.Attr
}

func (p *pass) serviceLogs(service string) []serviceLog {
	var out []serviceLog
	for _, n := range p.ofType("oci_logging_log") {
		for _, cfg := range blocks(&n.Body, "configuration") {
			for _, src := range blocks(&cfg.Body, "source") {
				s, _ := attrString(&src.Body, "service")
				if !strings.EqualFold(s, service) {
					continue
				}
				c, _ := attrString(&src.Body, "category")
				out = append(out, serviceLog{node: n, category: strings.ToLower(c), resource: getAttr(&src.Body, "resource")})
			}
		}
	}
	return out
}

// covers reports whether the log is about target: its resource refers to
// it (or to each.* while its for_each iterates target), or names the same
// constant as target's name.
func (l serviceLog) covers(target *model.Node) bool {
	if l.resource == nil {
		return false
	}
	if referencesNode(l.resource.Value, target) {
		return true
	}
	if fe := getAttr(&l.node.Body, "for_each"); fe != nil && referencesNode(fe.Value, target) {
		for _, r := range references(l.resource.Value) {
			if strings.HasPrefix(r, "each.") {
				return true
			}
		}
	}
	name, ok := attrString(&target.Body, "name")
	res, isConst := literal(l.resource.Value).(string)
	return ok && isConst && name != "" && name == res
}

func checkOCIFlowLogs(p *pass) {
	logs := p.serviceLogs("flowlogs")
	for _, subnet := range p.ofType("oci_core_subnet") {
		vcn := p.referencedNode(&subnet.Body, "vcn_id")
		if slices.ContainsFunc(logs, func(l serviceLog) bool { return l.covers(subnet) || vcn != nil && l.covers(vcn) }) {
			continue
		}
		p.report(finding{
			node:    subnet,
			message: fmt.Sprintf("%s has no VCN flow log", addr(subnet)),
			why:     "Flow logs record the connections accepted and rejected in the subnet: without them there is no trail to investigate an intrusion or a data exfiltration.",
			fix:     fmt.Sprintf("Add an oci_logging_log (log_type = \"SERVICE\") with configuration { source { source_type = \"OCISERVICE\", service = \"flowlogs\", resource = %s.id, category = \"all\" } } in a log group.", addr(subnet)),
		})
	}
}

func checkOCIBucketWriteLogs(p *pass) {
	var writes []serviceLog
	for _, l := range p.serviceLogs("objectstorage") {
		if l.category == "write" {
			writes = append(writes, l)
		}
	}
	for _, bucket := range p.ofType("oci_objectstorage_bucket") {
		if slices.ContainsFunc(writes, func(l serviceLog) bool { return l.covers(bucket) }) {
			continue
		}
		p.report(finding{
			node:    bucket,
			message: fmt.Sprintf("%s doesn't log object writes", addr(bucket)),
			why:     "Write-level service logs record who created, overwrote or deleted each object; without them changes to the data can't be traced.",
			fix:     fmt.Sprintf("Add an oci_logging_log (log_type = \"SERVICE\") with configuration { source { source_type = \"OCISERVICE\", service = \"objectstorage\", resource = %s.name, category = \"write\" } } in a log group.", addr(bucket)),
		})
	}
}

// ---------------------------------------------------------------------------
// bp_oci_key_rotation (CIS 4.16)

func checkOCIKeyRotation(p *pass) {
	for _, key := range p.ofType("oci_kms_key") {
		// Automatic rotation exists only for keys in a virtual private vault;
		// the others are rotated by hand, which the design can't show.
		vault := p.referencedNode(&key.Body, "management_endpoint")
		if vault == nil || vault.Type != "oci_kms_vault" {
			continue
		}
		if t, _ := attrString(&vault.Body, "vault_type"); !strings.EqualFold(t, "VIRTUAL_PRIVATE") {
			continue
		}
		if mode, _ := attrString(&key.Body, "protection_mode"); strings.EqualFold(mode, "EXTERNAL") {
			continue
		}
		why := "A key that never rotates protects more data for longer, so a leaked or broken key version exposes everything encrypted since it was created."
		if !attrNotFalse(&key.Body, "is_auto_rotation_enabled") {
			p.report(finding{
				node: key, attribute: "is_auto_rotation_enabled",
				message: fmt.Sprintf("%s doesn't rotate automatically", addr(key)),
				why:     why,
				fix:     "Set is_auto_rotation_enabled = true with auto_key_rotation_details { rotation_interval_in_days = 365 } (or less).",
			})
			continue
		}
		for _, d := range blocks(&key.Body, "auto_key_rotation_details") {
			if days, ok := attrNumber(&d.Body, "rotation_interval_in_days"); ok && days > 365 {
				p.report(finding{
					node: key, attribute: "auto_key_rotation_details",
					message: fmt.Sprintf("%s rotates every %d days", addr(key), int(days)),
					why:     why,
					fix:     "Set rotation_interval_in_days to 365 or less.",
				})
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Buckets and volumes (CIS 5.1-5.3)

// ociPublicIntent: the bucket is tagged Public = "true" (freeform_tags).
func ociPublicIntent(bucket *model.Node) bool {
	a := getAttr(&bucket.Body, "freeform_tags")
	if a == nil {
		return false
	}
	m, _ := literal(a.Value).(map[string]any)
	for k, v := range m {
		if strings.EqualFold(k, "public") && fmt.Sprint(v) == "true" {
			return true
		}
	}
	return false
}

func checkOCIPublicBucket(p *pass) {
	for _, n := range p.ofType("oci_objectstorage_bucket") {
		access, _ := attrString(&n.Body, "access_type")
		if access != "ObjectRead" && access != "ObjectReadWithoutList" || ociPublicIntent(n) {
			continue
		}
		p.report(finding{
			node: n, attribute: "access_type",
			message: fmt.Sprintf("%s lets anyone read its objects (access_type = %q)", addr(n), access),
			why:     "Public buckets are the most common source of data leaks; anything uploaded later becomes world-readable too.",
			fix:     "If the bucket is meant to be public, tag it Public = \"true\" in freeform_tags; otherwise set access_type = \"NoPublicAccess\" and share objects with pre-authenticated requests.",
		})
	}
}

func checkOCICustomerManagedKey(p *pass) {
	why := "Oracle-managed keys encrypt the data, but only a key in your Vault lets you control, audit, rotate and revoke access to it (crypto-shredding), as most compliance standards ask."
	report := func(n *model.Node, attr, what string) {
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s encrypts %s with an Oracle-managed key", addr(n), what),
			why:     why,
			fix:     "Set " + attr + " to an oci_kms_key (and allow the service to use it with an IAM policy).",
		})
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "oci_objectstorage_bucket":
			if kmsKeyUnset(&n.Body) {
				report(n, "kms_key_id", "its objects")
			}
		case "oci_core_volume", "oci_core_boot_volume":
			if kmsKeyUnset(&n.Body) {
				report(n, "kms_key_id", "the volume")
			}
		case "oci_file_storage_file_system":
			if kmsKeyUnset(&n.Body) {
				report(n, "kms_key_id", "the file system")
			}
		case "oci_core_instance":
			// An instance booted from an existing boot volume inherits that
			// volume's key (an oci_core_boot_volume of its own).
			for i, src := range blocks(&n.Body, "source_details") {
				if t, _ := attrString(&src.Body, "source_type"); !strings.EqualFold(t, "bootVolume") && kmsKeyUnset(&src.Body) {
					report(n, fmt.Sprintf("source_details[%d].kms_key_id", i), "its boot volume")
				}
			}
		}
	}
}

// kmsKeyUnset: kms_key_id is missing or a constant empty string.
func kmsKeyUnset(b *model.Body) bool {
	a := getAttr(b, "kms_key_id")
	if a == nil {
		return true
	}
	s, isConst := literal(a.Value).(string)
	return isConst && strings.TrimSpace(s) == ""
}

func checkOCIBucketVersioning(p *pass) {
	for _, n := range p.ofType("oci_objectstorage_bucket") {
		a := getAttr(&n.Body, "versioning")
		if a != nil {
			if v, isConst := literal(a.Value).(string); !isConst || v == "Enabled" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "versioning",
			message: fmt.Sprintf("%s doesn't keep previous object versions", addr(n)),
			why:     "Without versioning an overwrite or delete, by mistake or by ransomware, destroys the only copy of the object.",
			fix:     "Set versioning = \"Enabled\" (add a lifecycle policy to expire old versions if storage cost matters).",
		})
	}
}

// ---------------------------------------------------------------------------
// IAM policies (CIS 1.2, 1.3, 1.15)

// ociStatement is one "Allow <subject> to <verb> <resource> in <location>
// [where <conditions>]" policy statement (lower case).
type ociStatement struct {
	text        string
	subjectKind string // group | dynamic-group | any-user | any-group | service
	subject     string // "admins", "'default'/'administrators'", "a, b"
	verb        string // inspect | read | use | manage
	resource    string // "all-resources", "object-family", ...
	tenancy     bool   // location is the tenancy (not a compartment)
	where       string // the conditions without spaces and quotes
}

var ociStatementRE = regexp.MustCompile(`(?is)^\s*allow\s+(group|dynamic-group|any-user|any-group|service)\b(.*?)\s+to\s+(inspect|read|use|manage)\s+(\S+)\s+in\s+(tenancy|compartment\s.*?)(?:\s+where\s+(.*))?\s*$`)

// ociStatements reads the statements of an oci_identity_policy: a list of
// strings whose interpolations ("${var.group}") are kept as "${…}", so a
// statement naming its group or compartment by reference still parses.
func ociStatements(n *model.Node) []ociStatement {
	a := getAttr(&n.Body, "statements")
	if a == nil {
		return nil
	}
	tuple, ok := parse(a.Value).(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil
	}
	var out []ociStatement
	for _, e := range tuple.Exprs {
		text, ok := templateText(e)
		if !ok {
			continue
		}
		m := ociStatementRE.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		lower := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
		out = append(out, ociStatement{
			text:        strings.TrimSpace(text),
			subjectKind: lower(m[1]),
			subject:     lower(m[2]),
			verb:        lower(m[3]),
			resource:    lower(m[4]),
			tenancy:     lower(m[5]) == "tenancy",
			where:       strings.NewReplacer(" ", "", "\t", "", "\n", "", "'", "", `"`, "").Replace(lower(m[6])),
		})
	}
	return out
}

// templateText renders a string template with its interpolations as "${…}";
// ok is false for anything but a string.
func templateText(e hclsyntax.Expression) (string, bool) {
	switch t := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if t.Val.Type() == cty.String && t.Val.IsKnown() && !t.Val.IsNull() {
			return t.Val.AsString(), true
		}
	case *hclsyntax.TemplateExpr:
		var sb strings.Builder
		for _, part := range t.Parts {
			if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
				sb.WriteString(lit.Val.AsString())
			} else {
				sb.WriteString("${…}")
			}
		}
		return sb.String(), true
	}
	return "", false
}

// administratorsOnly: the statement is about the tenancy Administrators
// group alone (as "Administrators" or "'<domain>'/'Administrators'").
func (s ociStatement) administratorsOnly() bool {
	if s.subjectKind != "group" || strings.Contains(s.subject, ",") {
		return false
	}
	name := strings.Trim(s.subject, `'" `)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = strings.Trim(name[i+1:], `'" `)
	}
	return name == "administrators"
}

func (p *pass) reportStatements(match func(ociStatement) bool, why, fix string) {
	for _, n := range p.ofType("oci_identity_policy") {
		for _, s := range ociStatements(n) {
			if s.administratorsOnly() || !match(s) {
				continue
			}
			p.report(finding{
				node: n, attribute: "statements",
				message: fmt.Sprintf("%s: %q", addr(n), s.text),
				why:     why,
				fix:     fix,
			})
		}
	}
}

func checkOCIPolicyManageAll(p *pass) {
	p.reportStatements(func(s ociStatement) bool {
		return s.verb == "manage" && s.resource == "all-resources" && s.tenancy
	},
		"Managing all resources in the tenancy is full administrator access, including IAM itself: it should stay with the built-in Administrators group for break-glass use.",
		"Grant the group manage on the resource families it runs (e.g. manage instance-family) in its compartment instead of all-resources in tenancy.")
}

func checkOCIPolicyAdminGroup(p *pass) {
	p.reportStatements(func(s ociStatement) bool {
		if !s.tenancy || s.verb != "use" && s.verb != "manage" {
			return false
		}
		if s.resource != "users" && s.resource != "groups" && s.resource != "all-resources" {
			return false
		}
		return !strings.Contains(s.where, "target.group.name!=administrators")
	},
		"Whoever can change the membership of the Administrators group (or its users' credentials) can make themselves tenancy administrator.",
		"Add where target.group.name != 'Administrators' to the statement (keep separate inspect statements for listing users and groups).")
}

// storageDeletes are the delete permissions per storage resource type that
// CIS 1.15 keeps away from storage service admins.
var storageDeletes = map[string][]string{
	"all-resources":       {"BUCKET_DELETE", "OBJECT_DELETE", "EXPORT_SET_DELETE", "MOUNT_TARGET_DELETE", "FILE_SYSTEM_DELETE", "VOLUME_BACKUP_DELETE", "VOLUME_DELETE", "FILE_SYSTEM_DELETE_SNAPSHOT"},
	"file-family":         {"EXPORT_SET_DELETE", "MOUNT_TARGET_DELETE", "FILE_SYSTEM_DELETE", "FILE_SYSTEM_DELETE_SNAPSHOT"},
	"file-systems":        {"FILE_SYSTEM_DELETE", "FILE_SYSTEM_DELETE_SNAPSHOT"},
	"mount-targets":       {"MOUNT_TARGET_DELETE"},
	"object-family":       {"BUCKET_DELETE", "OBJECT_DELETE"},
	"buckets":             {"BUCKET_DELETE"},
	"objects":             {"OBJECT_DELETE"},
	"volume-family":       {"VOLUME_BACKUP_DELETE", "VOLUME_DELETE", "BOOT_VOLUME_BACKUP_DELETE"},
	"volumes":             {"VOLUME_DELETE"},
	"volume-backups":      {"VOLUME_BACKUP_DELETE"},
	"boot-volume-backups": {"BOOT_VOLUME_BACKUP_DELETE"},
}

func checkOCIStorageAdminDelete(p *pass) {
	p.reportStatements(func(s ociStatement) bool {
		perms, ok := storageDeletes[s.resource]
		if s.subjectKind != "group" || s.verb != "manage" || !ok {
			return false
		}
		// Compliant: every delete is excluded (the service admin) or the
		// statement grants only the deletes (the separate storage admin).
		excluded, only := true, true
		for _, perm := range perms {
			excluded = excluded && strings.Contains(s.where, "request.permission!="+strings.ToLower(perm))
			only = only && strings.Contains(s.where, "request.permission="+strings.ToLower(perm))
		}
		return !excluded && !only
	},
		"Separating who manages storage from who can delete it keeps one compromised or careless admin account from destroying the data and its backups.",
		"Add where clauses excluding the deletes, e.g. where all {request.permission != 'BUCKET_DELETE', request.permission != 'OBJECT_DELETE'} for object-family, and give deletion to a separate group.")
}
