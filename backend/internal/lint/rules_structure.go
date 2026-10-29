package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/zclconf/go-cty/cty"
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

// inlineCodeSites are the nested blocks that embed function code: the
// source blocks of archive_file (the zip every cloud's functions deploy:
// Lambda, Azure zip deploy, Cloud Functions sources) and the file blocks of
// azurerm_function_app_function.
var inlineCodeSites = []struct {
	kind        model.NodeKind
	typ, block  string
	fileAttr    string
	packageHint string
}{
	{model.KindData, "archive_file", "source", "filename",
		"remove the source blocks and set source_dir = \"${path.module}/services/%[1]s/src\""},
	{model.KindResource, "azurerm_function_app_function", "file", "name",
		"remove the file blocks, zip the folder with a data \"archive_file\" (source_dir = \"${path.module}/services/%[1]s/src\") and deploy it with zip_deploy_file on the function app"},
}

// checkInlineFunctionCode flags function code embedded in Terraform (root
// or local module): archive_file source blocks and Azure function file
// blocks whose content is program code. Application code belongs in source
// files of the workflow (services/<svc>/src/...) that an archive_file zips
// with source_dir. Short or configuration-like content (a JSON line, a YAML
// file) and content Nimbo can't read (file(), references) are fine.
func checkInlineFunctionCode(p *pass) {
	const why = "Code inside a Terraform string can't be linted, tested, formatted or built with its language's tools, every change to it shows up as an infrastructure diff, and it can't grow into a real service (modules, dependencies, tests). Terraform should only package and deploy it."
	scope := func(wf *model.Workflow, report func(n *model.Node, attr string, f finding)) {
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			for _, site := range inlineCodeSites {
				if n.Kind != site.kind || n.Type != site.typ {
					continue
				}
				for j, b := range blocks(&n.Body, site.block) {
					a := getAttr(&b.Body, "content")
					if a == nil {
						continue
					}
					if text, ok := inlineText(a.Value); ok && looksLikeCode(text) {
						file, _ := attrString(&b.Body, site.fileAttr)
						report(n, fmt.Sprintf("%s[%d].content", site.block, j), finding{
							message: fmt.Sprintf("%s embeds program code%s", addr(n), orEmpty(file, " (%s)")),
							why:     why,
							fix:     fmt.Sprintf("Move the code to services/%[1]s/src/ in the workflow files (e.g. services/%[1]s/src/index.mjs) and "+site.packageHint+". Code that needs a build (TypeScript, Go, Java, .NET) gets a services/%[1]s/Dockerfile whose last stage is `FROM scratch AS package`, and source_dir points at services/%[1]s/dist.", n.Name),
						})
						break
					}
				}
			}
		}
	}
	scope(p.wf, func(n *model.Node, attr string, f finding) {
		f.node, f.attribute = n, attr
		p.report(f)
	})
	for _, c := range p.localModules() {
		scope(c.mod, func(_ *model.Node, _ string, f finding) {
			f.node, f.message = c.root, moduleName(c)+": "+f.message
			p.report(f)
		})
	}
}

func orEmpty(s, format string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf(format, s)
}

// inlineText returns the text of v: a constant string, or the literal parts
// of a template (interpolations left out). ok is false for any other
// expression (file(), templatefile(), references): its text is unknown here.
func inlineText(v model.Value) (string, bool) {
	if s, ok := literal(v).(string); ok {
		return s, true
	}
	tmpl, ok := parse(v).(*hclsyntax.TemplateExpr)
	if !ok {
		return "", false
	}
	var sb strings.Builder
	for _, part := range tmpl.Parts {
		if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
			sb.WriteString(lit.Val.AsString())
		}
	}
	return sb.String(), true
}

// codeLine matches a line only program code has: imports and exports,
// function, class and variable definitions, a shebang.
var codeLine = regexp.MustCompile(`(?m)^\s*(?:import\s|export\s|from\s+[\w.]+\s+import\s|(?:async\s+)?def\s+\w+\s*\(|func\s+[\w(]|package\s+[\w.]+\s*;?\s*$|(?:public\s+|internal\s+)?(?:static\s+)?class\s+\w+|(?:async\s+)?function\b|(?:const|let|var)\s+\w+\s*=|module\.exports\b|exports\.\w+\s*=|using\s+[\w.]+\s*;|#!/)`)

// looksLikeCode: two or more non-blank lines, one of them a code line.
func looksLikeCode(text string) bool {
	lines := 0
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	return lines >= 2 && codeLine.MatchString(text)
}
