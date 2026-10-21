package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const (
	maxFileResources   = 15
	moduleWorkflowMin  = 12 // workflows smaller than this don't need modules
	moduleFamilyMin    = 5  // related resources that make a module worth it
	duplicateMinNodes  = 3
	duplicateMinLength = 6
)

func checkLargeFile(p *pass) {
	files := p.wf.NodeFiles
	if files == nil {
		files = hclcodec.NodeFiles(p.wf, p.cat.ResourceFile)
	}
	byFile := map[string][]*model.Node{}
	var order []string
	for _, n := range p.resources() {
		f := files[n.ID]
		if f == "" {
			f = "main.tf"
		}
		if byFile[f] == nil {
			order = append(order, f)
		}
		byFile[f] = append(byFile[f], n)
	}
	for _, f := range order {
		nodes := byFile[f]
		if len(nodes) <= maxFileResources {
			continue
		}
		p.report(finding{
			node:    nodes[0],
			message: fmt.Sprintf("%s holds %d resources", f, len(nodes)),
			why:     "Long files are hard to review and merge; grouping resources by concern (network.tf, iam.tf, api.tf, …) or into modules shows the architecture at a glance.",
			fix:     fmt.Sprintf("Move groups of related resources to their own files (clear the pinned file in the inspector to use Nimbo's layout) or extract them into a module with extract_module. Keep at most %d resources per file.", maxFileResources),
		})
	}
}

// families groups resource types into the logical groups that usually form
// one module (AWS Prescriptive Guidance: the network foundation, the data
// tier, an application's API or container service, a cluster). IAM and
// security groups are left out on purpose: they belong with the service
// they protect.
var families = []struct {
	name   string
	module string
	match  *regexp.Regexp
}{
	{"network foundation", "network", regexp.MustCompile(`^(aws_(vpc|subnet|route|route_table|route_table_association|internet_gateway|egress_only_internet_gateway|nat_gateway|eip|network_acl|network_acl_rule|vpc_endpoint|default_route_table|default_network_acl|default_security_group|flow_log)$|aws_vpc_(endpoint|ipv4_|ipv6_|dhcp_options|peering_connection|ipam|block_public_access)|azurerm_(virtual_network|subnet|route_table|route|nat_gateway|public_ip|network_security_group|network_security_rule|subnet_.*)$|google_compute_(network|subnetwork|router|router_nat|route|firewall|global_address)$|oci_core_(vcn|subnet|internet_gateway|nat_gateway|route_table|security_list|service_gateway)$)`)},
	{"data tier", "data", regexp.MustCompile(`^(aws_(db_|rds_|dynamodb_|elasticache_|docdb_|redshift_|opensearch_|memorydb_)|azurerm_(mssql_|postgresql_|mysql_|cosmosdb_|redis_)|google_(sql_|redis_|spanner_|bigtable_|alloydb_)|oci_(database_|mysql_))`)},
	{"API", "api", regexp.MustCompile(`^aws_(api_gateway_|apigatewayv2_)`)},
	{"container service", "service", regexp.MustCompile(`^aws_(ecs_|lb$|lb_|alb|ecr_|appautoscaling_)`)},
	{"Kubernetes cluster", "cluster", regexp.MustCompile(`^(aws_eks_|azurerm_kubernetes_|google_container_)`)},
}

func checkModuleCandidate(p *pass) {
	res := p.resources()
	if len(res) < moduleWorkflowMin {
		return
	}
	for _, fam := range families {
		var members []*model.Node
		for _, n := range res {
			if fam.match.MatchString(n.Type) {
				members = append(members, n)
			}
		}
		if len(members) < moduleFamilyMin {
			continue
		}
		var addrs []string
		for _, n := range members {
			addrs = append(addrs, addr(n))
		}
		p.report(finding{
			node:    members[0],
			message: fmt.Sprintf("%d %s resources could be a module: %s", len(members), fam.name, list(members)),
			why:     "A module gives a logical group (network foundation, data tier, security controls, an application) one interface of inputs and outputs, so it can be reviewed, tested and reused per environment or region as a unit (AWS Prescriptive Guidance). Group by concern, not by resource type, and don't wrap single resources.",
			fix:     fmt.Sprintf("Run extract_module with nodes %s and name %q; Nimbo writes the module with typed, described variables and outputs and adds moved blocks so nothing is recreated.", strings.Join(addrs, ", "), fam.module),
		})
	}
}

// Values that legitimately repeat: wildcard CIDRs, IAM versions, …
var duplicateExempt = map[string]bool{
	"0.0.0.0/0": true, "::/0": true, "2012-10-17": true, "sts:AssumeRole": true,
}

func checkDuplicateLiteral(p *pass) {
	type use struct {
		nodes []*model.Node
		attrs []string
	}
	uses := map[string]*use{}
	var order []string
	for _, n := range p.resources() {
		schema, _ := p.cat.Schema(n.Kind, n.Type)
		enumAttrs := map[string]bool{}
		if schema != nil {
			for _, a := range schema.Block.Attributes {
				if len(a.Enum) > 0 {
					enumAttrs[a.Name] = true
				}
			}
		}
		seen := map[string]bool{}
		walkAttrs(&n.Body, "", func(path string, a *model.Attr) {
			if enumAttrs[path] {
				return
			}
			s, ok := literal(a.Value).(string)
			if !ok || !duplicateCandidate(s) || seen[s] {
				return
			}
			seen[s] = true
			u := uses[s]
			if u == nil {
				u = &use{}
				uses[s] = u
				order = append(order, s)
			}
			u.nodes = append(u.nodes, n)
			u.attrs = append(u.attrs, lastSegment(path))
		})
	}
	sort.SliceStable(order, func(i, j int) bool { return len(uses[order[i]].nodes) > len(uses[order[j]].nodes) })
	for _, s := range order {
		u := uses[s]
		if len(u.nodes) < duplicateMinNodes {
			continue
		}
		p.report(finding{
			node: u.nodes[0], attribute: u.attrs[0],
			message: fmt.Sprintf("The value %q is repeated in %d resources: %s", s, len(u.nodes), list(u.nodes)),
			why:     "A value copied into several places drifts when one copy is changed; a local (or variable) changes them all at once and names what the value means.",
			fix:     fmt.Sprintf("Add locals { %s = %q } and use local.%s in each resource (or a variable if it differs per environment).", localName(u.attrs[0]), s, localName(u.attrs[0])),
		})
	}
}

var (
	plainWord = regexp.MustCompile(`^[A-Za-z]+$`)
	upperEnum = regexp.MustCompile(`^[A-Z0-9_]+$`)
	mimeType  = regexp.MustCompile(`^(application|audio|font|image|text|video|multipart)/[a-z0-9.+-]+$`)
)

// duplicateCandidate keeps configuration-like values (CIDRs, ARNs, IDs,
// hosts, images, …) and drops words, enums, MIME types and long free text.
func duplicateCandidate(s string) bool {
	if len(s) < duplicateMinLength || len(s) > 200 || duplicateExempt[s] {
		return false
	}
	if plainWord.MatchString(s) || upperEnum.MatchString(s) || mimeType.MatchString(s) || strings.ContainsAny(s, " \n\t{") {
		return false
	}
	return strings.ContainsAny(s, "0123456789/:.")
}

func localName(attr string) string {
	if attr == "" || attr == "name" {
		return "shared_value"
	}
	return attr
}

// sgAttachmentTypes are the standalone security group rule resources.
var sgAttachmentTypes = map[string]bool{
	"aws_security_group_rule":             true,
	"aws_vpc_security_group_ingress_rule": true,
	"aws_vpc_security_group_egress_rule":  true,
}

// checkInlineSGRules flags inline ingress/egress in aws_security_group only
// where the same root (or local module) also uses standalone rule resources:
// mixing both styles is what breaks (both manage the group's rules and
// overwrite each other when they target the same group). Inline-only code is
// left alone: it is consistent, and flagging every inline rule would be noise
// on most published examples.
func checkInlineSGRules(p *pass) {
	scope := func(wf *model.Workflow, where string, report func(sg *model.Node, msg string, conflict bool)) {
		byAddr := map[string]*model.Node{}
		var attachments []*model.Node
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if n.Kind != model.KindResource {
				continue
			}
			byAddr[addr(n)] = n
			if sgAttachmentTypes[n.Type] {
				attachments = append(attachments, n)
			}
		}
		if len(attachments) == 0 {
			return
		}
		for i := range wf.Nodes {
			sg := &wf.Nodes[i]
			if sg.Kind != model.KindResource || sg.Type != "aws_security_group" || !hasInlineRules(&sg.Body) {
				continue
			}
			conflict := false
			for _, a := range attachments {
				if at := getAttr(&a.Body, "security_group_id"); at != nil && referencesNode(at.Value, sg) {
					conflict = true
					break
				}
			}
			msg := fmt.Sprintf("%s%s has inline ingress/egress rules while %s uses standalone rules", where, addr(sg), list(attachments))
			if conflict {
				msg = fmt.Sprintf("%s%s has inline ingress/egress rules and standalone rules (%s) for the same group", where, addr(sg), list(attachments))
			}
			report(sg, msg, conflict)
		}
	}
	const why = "AWS Prescriptive Guidance prefers attachment resources (aws_vpc_security_group_ingress_rule / _egress_rule) over inline blocks: each rule is its own resource with its own description and diff, and modules can add rules to a group they don't own. Inline rules and standalone rules for the same group both claim its rules, so every apply removes what the other added."
	const fix = "Move each inline ingress/egress block to an aws_vpc_security_group_ingress_rule / aws_vpc_security_group_egress_rule resource (security_group_id = the group's id, one CIDR or source group per rule) and remove the inline blocks."
	sevOf := func(conflict bool) model.Severity {
		if conflict {
			return model.SeverityWarning
		}
		return model.SeverityInfo
	}
	scope(p.wf, "", func(sg *model.Node, msg string, conflict bool) {
		p.report(finding{severity: sevOf(conflict), node: sg, message: msg, why: why, fix: fix})
	})
	for _, c := range p.localModules() {
		scope(c.mod, moduleName(c)+": ", func(_ *model.Node, msg string, conflict bool) {
			p.report(finding{severity: sevOf(conflict), node: c.root, message: msg, why: why, fix: fix})
		})
	}
}

// hasInlineRules: ingress/egress blocks, or non-empty ingress/egress lists.
func hasInlineRules(b *model.Body) bool {
	if len(blocks(b, "ingress")) > 0 || len(blocks(b, "egress")) > 0 {
		return true
	}
	for _, name := range []string{"ingress", "egress"} {
		if a := getAttr(b, name); a != nil {
			if l, ok := literal(a.Value).([]any); !ok || len(l) > 0 {
				return true
			}
		}
	}
	return false
}
