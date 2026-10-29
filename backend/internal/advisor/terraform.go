package advisor

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// design indexes a Terraform workflow for the cloud recommenders.
type design struct {
	wf  *model.Workflow
	cat *catalog.Catalog
	// byID are the resource nodes by ID; all every node (data sources
	// too).
	byID map[string]*model.Node
	all  map[string]*model.Node
	// refBy are the IDs of the nodes that refer to a node (edge Source ->
	// Targets); refs the nodes a node refers to; into the edges whose
	// Target is a node.
	refBy map[string][]string
	refs  map[string][]string
	into  map[string][]model.Edge
	// public is set by the cloud recommenders when the design has a public
	// entry point.
	public bool
	// taken are the addresses of the design and of the blocks snippets add.
	taken map[string]bool
}

// unique returns a free name for a new resource of typ, starting at base.
func (d *design) unique(typ, base string) string {
	name := base
	for i := 2; d.taken[typ+"."+name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	d.taken[typ+"."+name] = true
	return name
}

// add appends r with its Local value computed from its additions and its
// snippet formatted like terraform fmt.
func (d *design) add(out *[]rec, priority int, r Recommendation) {
	if r.Local == "" {
		r.Local = d.local(r.Add)
	}
	if r.Snippet != "" {
		r.Snippet = string(hclwrite.Format([]byte(r.Snippet)))
	}
	*out = append(*out, rec{Recommendation: r, priority: priority})
}

func newDesign(wf *model.Workflow, cat *catalog.Catalog) *design {
	d := &design{wf: wf, cat: cat, byID: map[string]*model.Node{}, all: map[string]*model.Node{}, refBy: map[string][]string{}, refs: map[string][]string{},
		into: map[string][]model.Edge{}, taken: map[string]bool{}}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		d.all[n.ID] = n
		if n.Kind == model.KindResource {
			d.byID[n.ID] = n
			d.taken[n.Type+"."+n.Name] = true
		}
	}
	for _, v := range wf.Variables {
		d.taken["variable."+v.Name] = true
	}
	for _, e := range wf.Edges {
		d.refBy[e.Source] = append(d.refBy[e.Source], e.Target)
		d.refs[e.Target] = append(d.refs[e.Target], e.Source)
		d.into[e.Target] = append(d.into[e.Target], e)
	}
	return d
}

// referencedAt returns the nodes of types (any kind, data sources too) that
// n refers to in the attribute or nested block at path ("policy",
// "statement[0]").
func (d *design) referencedAt(n *model.Node, at string, types ...string) []*model.Node {
	var out []*model.Node
	for _, e := range d.into[n.ID] {
		if e.Attribute != at && !strings.HasPrefix(e.Attribute, at+".") && !strings.HasPrefix(e.Attribute, at+"[") {
			continue
		}
		if m := d.all[e.Source]; m != nil && (len(types) == 0 || slices.Contains(types, m.Type)) && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// ofType returns the resources of the given types in workflow order.
func (d *design) ofType(types ...string) []*model.Node {
	var out []*model.Node
	for i := range d.wf.Nodes {
		n := &d.wf.Nodes[i]
		if n.Kind == model.KindResource && slices.Contains(types, n.Type) {
			out = append(out, n)
		}
	}
	return out
}

// has reports whether the design has a resource of one of types.
func (d *design) has(types ...string) bool { return len(d.ofType(types...)) > 0 }

// referrers returns the resources of types that refer to n.
func (d *design) referrers(n *model.Node, types ...string) []*model.Node {
	return d.linked(d.refBy[n.ID], types)
}

// referenced returns the resources of types n refers to.
func (d *design) referenced(n *model.Node, types ...string) []*model.Node {
	return d.linked(d.refs[n.ID], types)
}

func (d *design) linked(ids []string, types []string) []*model.Node {
	var out []*model.Node
	for _, id := range ids {
		if m := d.byID[id]; m != nil && (len(types) == 0 || slices.Contains(types, m.Type)) && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// referredBy reports whether a resource of types refers to n.
func (d *design) referredBy(n *model.Node, types ...string) bool {
	return len(d.referrers(n, types...)) > 0
}

// local is the Local value of a set of additions: LocalRuns when every
// Terraform type deploys to the Floci emulator, else LocalExportOnly naming
// the types that don't.
func (d *design) local(adds []Addition) string {
	var unsupported []string
	for _, a := range adds {
		if !isTerraformType(a.Type) {
			continue
		}
		s, found := (*model.ResourceSchema)(nil), false
		if d.cat != nil {
			s, found = d.cat.Schema(model.KindResource, a.Type)
		}
		if (!found || !s.FlociSupported) && !slices.Contains(unsupported, a.Type) {
			unsupported = append(unsupported, a.Type)
		}
	}
	if len(unsupported) == 0 {
		return LocalRuns
	}
	return LocalExportOnly + " (" + strings.Join(unsupported, ", ") + ")"
}

// isTerraformType reports a provider resource type ("aws_lb").
func isTerraformType(s string) bool {
	_, ok := model.CloudOfType(s)
	return ok && strings.Contains(s, "_")
}

// ---------------------------------------------------------------------------
// Attribute helpers

func attr(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

func block(b *model.Body, typ string) *model.Block {
	for i := range b.Blocks {
		if b.Blocks[i].Type == typ {
			return &b.Blocks[i]
		}
	}
	return nil
}

// path follows nested blocks ("settings", "ip_configuration").
func path(b *model.Body, blocks ...string) *model.Body {
	for _, typ := range blocks {
		blk := block(b, typ)
		if blk == nil {
			return nil
		}
		b = &blk.Body
	}
	return b
}

// literal returns the constant value of an attribute (nil: unset or not a
// constant).
func literal(b *model.Body, name string) any {
	if b == nil {
		return nil
	}
	a := attr(b, name)
	if a == nil {
		return nil
	}
	raw := a.Value.Literal
	if len(raw) == 0 && a.Value.Expr != "" {
		raw, _ = hclcodec.LiteralFromExpr(a.Value.Expr)
	}
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// str returns a constant string attribute.
func str(b *model.Body, name string) (string, bool) {
	s, ok := literal(b, name).(string)
	return s, ok
}

// isTrue reports a constant true attribute.
func isTrue(b *model.Body, name string) bool {
	v, _ := literal(b, name).(bool)
	return v
}

// isFalse reports a constant false attribute.
func isFalse(b *model.Body, name string) bool {
	v, ok := literal(b, name).(bool)
	return ok && !v
}

// set reports an attribute or nested block written in b (any value).
func set(b *model.Body, name string) bool {
	return b != nil && (attr(b, name) != nil || block(b, name) != nil)
}

// number returns a constant number attribute.
func number(b *model.Body, name string) (float64, bool) {
	f, ok := literal(b, name).(float64)
	return f, ok
}

// expr returns the source of an attribute ("" when unset).
func expr(b *model.Body, name string) string {
	if b == nil {
		return ""
	}
	a := attr(b, name)
	if a == nil {
		return ""
	}
	if a.Value.Expr != "" {
		return a.Value.Expr
	}
	s, _ := hclcodec.ExprFromLiteral(a.Value.Literal)
	return s
}

func addr(n *model.Node) string { return hclcodec.Address(n) }

func addrs(nodes []*model.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = addr(n)
	}
	return out
}

// label is a Terraform name for a new block derived from n ("api_waf").
func label(n *model.Node, suffix string) string {
	if suffix == "" {
		return n.Name
	}
	return n.Name + "_" + suffix
}

// ---------------------------------------------------------------------------
// Terraform recommendations

func terraformRecs(wf *model.Workflow, cat *catalog.Catalog) ([]rec, bool) {
	d := newDesign(wf, cat)
	var out []rec
	for _, c := range wf.UsedClouds {
		switch c {
		case model.CloudAWS:
			out = append(out, awsRecs(d)...)
		case model.CloudAzure:
			out = append(out, azureRecs(d)...)
		case model.CloudGCP:
			out = append(out, gcpRecs(d)...)
		case model.CloudOCI:
			out = append(out, ociRecs(d)...)
		}
	}
	production := d.public || prodLike(wf, terraformIntent(wf)...)
	if len(d.byID) > 0 && production && len(wf.Environments) < 2 {
		out = append(out, rec{priority: prioEnvironment, Recommendation: terraformEnvironments(wf)})
	}
	return out, production
}

// terraformIntent are the names that reveal production intent: environment
// names, tags and default_tags, and the defaults of environment variables.
func terraformIntent(wf *model.Workflow) []string {
	var out []string
	for _, e := range wf.Environments {
		out = append(out, e.Name)
	}
	if wf.ActiveEnvironment != "" {
		out = append(out, wf.ActiveEnvironment)
	}
	for i := range wf.Nodes {
		if s := expr(&wf.Nodes[i].Body, "tags"); s != "" {
			out = append(out, s)
		}
	}
	for i := range wf.Providers {
		if dt := block(&wf.Providers[i].Body, "default_tags"); dt != nil {
			out = append(out, expr(&dt.Body, "tags"))
		}
	}
	for _, v := range wf.Variables {
		switch strings.ToLower(v.Name) {
		case "env", "environment", "stage":
			out = append(out, expr(&v.Body, "default"))
		}
	}
	return out
}

func terraformEnvironments(wf *model.Workflow) Recommendation {
	have := "a single environment"
	if len(wf.Environments) == 1 {
		have = fmt.Sprintf("only the %q environment", wf.Environments[0].Name)
	}
	snippet := "# With two or more environments the export writes one root and one state per environment:\n" +
		"#   modules/<service>/          the resources of this design\n" +
		"#   environments/dev/           backend.tf, main.tf (calls the module), terraform.tfvars\n" +
		"#   environments/prod/\n" +
		"# Values that differ between environments (sizes, retention, deletion protection) become variables set per environment.\n"
	if !slices.ContainsFunc(wf.Variables, func(v model.NamedBlock) bool { return v.Name == "environment" }) {
		snippet += "variable \"environment\" {\n  description = \"Environment name (dev, prod), used in names and tags\"\n  type = string\n}\n"
	}
	return Recommendation{
		ID:    "terraform_environments",
		Title: "Add separate dev and prod environments",
		Why: fmt.Sprintf("The design looks production-bound but has %s. With two or more Nimbo environments the export switches to the environments layout: "+
			"modules/<service> plus one root per environment (environments/<env>/ with its own backend and terraform.tfvars), so dev changes are planned and applied against a separate state before prod.", have),
		Pillars: []model.Pillar{model.PillarOperations, model.PillarReliability},
		Add: []Addition{
			{Type: "environment", Purpose: "dev: small sizes, short retention; deployed and tested first"},
			{Type: "environment", Purpose: "prod: production sizes, deletion protection, alarms"},
		},
		Snippet: string(hclwrite.Format([]byte(snippet))),
		Scope:   ScopeProduction,
		Local:   LocalRuns,
		Skip:    "Throwaway experiments and learning sandboxes; do not use Terraform CLI workspaces for this.",
		Rules:   []string{"bp_workspace_branching", "bp_no_remote_backend"},
		Docs:    "https://docs.aws.amazon.com/prescriptive-guidance/latest/terraform-aws-provider-best-practices/structure.html",
	}
}
