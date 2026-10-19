package hclcodec

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AttrInfo is what a provider schema says about one attribute.
type AttrInfo struct {
	Type        string // Terraform type constraint: "string", "list(string)", ...
	Description string
	Sensitive   bool
}

// SchemaLookup answers provider schema questions while extracting a module.
// A nil SchemaLookup types every input as `any`.
type SchemaLookup interface {
	// Attribute describes a top-level attribute of a resource or data source type.
	Attribute(kind model.NodeKind, typ, attr string) (AttrInfo, bool)
	// HasSensitive reports whether any attribute of the type is sensitive.
	HasSensitive(kind model.NodeKind, typ string) bool
}

// TypeConstraint converts a provider schema type (cty type JSON such as
// "string" or ["list","string"]) into a Terraform type constraint; "any"
// when it cannot be read.
func TypeConstraint(raw json.RawMessage) string {
	ty, err := ctyjson.UnmarshalType(raw)
	if err != nil {
		return "any"
	}
	return typeexpr.TypeString(ty)
}

var moduleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidModuleName reports whether name can name an extracted module
// (snake_case: it is both the module call name and its directory).
func ValidModuleName(name string) error {
	if !moduleNameRe.MatchString(name) {
		return fmt.Errorf("invalid module name %q: use snake_case (lowercase letters, digits and underscores, starting with a letter)", name)
	}
	return nil
}

// reservedVariableNames cannot name a module input variable.
var reservedVariableNames = map[string]bool{
	"source": true, "version": true, "providers": true, "count": true, "for_each": true,
	"lifecycle": true, "depends_on": true, "locals": true,
}

// Extraction is the result of ExtractModule.
type Extraction struct {
	model.ModuleExtraction
	// Contents are the module files, by slash path relative to the workflow
	// root ("modules/<name>/main.tf").
	Contents map[string][]byte
	// NodeID is the ID of the new module call node.
	NodeID string
}

// ExtractOptions are the catalog facts ExtractModule uses.
type ExtractOptions struct {
	// Schema types inputs and describes inputs/outputs (nil: `any`).
	Schema SchemaLookup
	// ResourceFile is ExportOptions.ResourceFile for the module files.
	ResourceFile func(resourceType string) string
}

// ExtractModule moves the nodes refs (IDs or addresses) of wf into a new
// local module "modules/<name>" and calls it from the root in their place:
//
//   - references from the moved blocks to the rest of the root (nodes,
//     variables, locals) become typed, described input variables that the
//     module call passes;
//   - references from the root to the moved blocks become described module
//     outputs, read as module.<name>.<output>; depends_on on a moved block
//     becomes depends_on = [module.<name>];
//   - depends_on from a moved block to the root moves to the module call;
//   - non-default provider aliases are passed with `providers = {}` and
//     declared as configuration_aliases;
//   - every moved resource and module call gets a root `moved` block (the
//     whole resource, so count/for_each instances move too), so applying the
//     result plans no destroy/recreate;
//   - path.module in moved blocks becomes path.root (files stay in the root).
//
// The module files follow the recommended layout (Export of the module with
// opts.ResourceFile, plus README.md). The call node is placed at the
// centroid of the moved nodes. wf is changed only on success; call
// Normalize afterwards.
func ExtractModule(wf *model.Workflow, refs []string, name string, opts ExtractOptions) (*Extraction, error) {
	if err := ValidModuleName(name); err != nil {
		return nil, err
	}
	work, err := cloneWorkflow(wf)
	if err != nil {
		return nil, err
	}
	deriveEdges(work)
	x := &extractor{
		wf: work, name: name, schema: opts.Schema, resourceFile: opts.ResourceFile,
		byAddr: map[string]*model.Node{}, moved: map[string]bool{},
		inputs: map[string]*ioEntry{}, outputs: map[string]*ioEntry{},
		inputNames: map[string]bool{}, outputNames: map[string]bool{},
		configs: map[string]bool{}, callDeps: map[string]bool{}, dependents: map[string]bool{},
	}
	for i := range work.Nodes {
		x.byAddr[Address(&work.Nodes[i])] = &work.Nodes[i]
	}
	if _, taken := x.byAddr["module."+name]; taken {
		return nil, fmt.Errorf("module.%s already exists; pick another name", name)
	}
	for _, r := range refs {
		n := findNodeRef(work, r)
		if n == nil {
			return nil, fmt.Errorf("node %q not found", r)
		}
		x.moved[Address(n)] = true
	}
	if len(x.moved) == 0 {
		return nil, errors.New("select at least one node to move into the module")
	}

	for i := range work.Nodes {
		if n := &work.Nodes[i]; x.moved[Address(n)] {
			if err := x.inbound(n, &n.Body, true); err != nil {
				return nil, fmt.Errorf("%s: %w", Address(n), err)
			}
		}
	}
	if err := x.outbound(); err != nil {
		return nil, err
	}
	if err := x.checkCycles(); err != nil {
		return nil, err
	}
	return x.assemble(wf)
}

// ioEntry is one module input or output being built.
type ioEntry struct {
	io     model.ModuleIO
	blocks []model.Block // validation blocks copied from a root variable
}

type extractor struct {
	wf           *model.Workflow
	name         string
	schema       SchemaLookup
	resourceFile func(string) string
	byAddr       map[string]*model.Node
	moved        map[string]bool // addresses moving into the module

	inputs, outputs         map[string]*ioEntry // by reference key
	inputOrder, outputOrder []string
	inputNames, outputNames map[string]bool

	configs       map[string]bool // provider configurations used by moved blocks ("aws", "aws.east")
	nestedInherit bool            // a moved module call inherits default providers
	callDeps      map[string]bool // root addresses moved blocks depend on
	dependents    map[string]bool // root node addresses with depends_on on a moved block
}

// reference is one reference found in an expression.
type reference struct {
	addr       string // "var.x", "local.y", "aws_x.y", "data.t.n", "module.m"
	attr       string // attribute read right after a node address ("" = whole object)
	start, end int    // byte span of addr (+ ".attr") in the expression
	steps      int    // traversal steps after the address and attribute
}

// key identifies the value a reference reads.
func (r reference) key() string {
	if r.attr == "" {
		return r.addr
	}
	return r.addr + "." + r.attr
}

// references lists the root-level references of e. Node references are
// kept only when known reports the address.
func references(e hclsyntax.Expression, known func(addr string) bool) []reference {
	var out []reference
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		t, ok := n.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			return nil
		}
		if r, ok := referenceOf(t.Traversal, known); ok {
			out = append(out, r)
		}
		return nil
	})
	return out
}

func referenceOf(tr hcl.Traversal, known func(addr string) bool) (reference, bool) {
	if len(tr) < 2 {
		return reference{}, false
	}
	a1, ok := tr[1].(hcl.TraverseAttr)
	if !ok {
		return reference{}, false
	}
	var addr string
	steps := 2
	node := true
	switch root := tr.RootName(); root {
	case "var", "local":
		addr, node = root+"."+a1.Name, false
	case "path":
		if a1.Name != "module" {
			return reference{}, false
		}
		addr, node = "path.module", false
	case "terraform", "count", "each", "self":
		return reference{}, false
	case "data":
		a2, ok := attrStep(tr, 2)
		if !ok {
			return reference{}, false
		}
		addr, steps = "data."+a1.Name+"."+a2, 3
	default:
		addr = root + "." + a1.Name
	}
	if node && !known(addr) {
		return reference{}, false
	}
	r := reference{addr: addr, start: tr[0].SourceRange().Start.Byte, end: tr[steps-1].SourceRange().End.Byte}
	if node {
		if a, ok := attrStep(tr, steps); ok {
			r.attr = a
			r.end = tr[steps].SourceRange().End.Byte
			steps++
		}
	}
	r.steps = len(tr) - steps
	return r, true
}

func attrStep(tr hcl.Traversal, i int) (string, bool) {
	if i >= len(tr) {
		return "", false
	}
	a, ok := tr[i].(hcl.TraverseAttr)
	return a.Name, ok
}

// span replaces expression bytes [start, end) with text.
type span struct {
	start, end int
	text       string
}

func applySpans(expr string, spans []span) string {
	if len(spans) == 0 {
		return expr
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	out := expr
	for _, s := range spans {
		out = out[:s.start] + s.text + out[s.end:]
	}
	return out
}

func (x *extractor) known(addr string) bool {
	_, ok := x.byAddr[addr]
	return ok
}

// inbound rewrites the body of a moved node (top: the node body itself).
func (x *extractor) inbound(n *model.Node, b *model.Body, top bool) error {
	attrs := b.Attrs[:0]
	for i := range b.Attrs {
		a := b.Attrs[i]
		switch {
		case top && a.Name == "depends_on":
			expr, err := x.splitDependsOn(a.Value.Expr, func(addr string) string {
				if x.moved[addr] {
					return addr
				}
				x.callDeps[addr] = true
				return ""
			})
			if err != nil {
				return err
			}
			if expr == "" {
				continue
			}
			a.Value = model.Value{Expr: expr}
		case top && n.Kind != model.KindModule && a.Name == "provider":
			x.configs[compactExpr(a.Value.Expr)] = true
		case top && n.Kind == model.KindModule && a.Name == "providers":
			if err := x.providerMapConfigs(a.Value.Expr); err != nil {
				return err
			}
		case top && n.Kind == model.KindModule && a.Name == "source":
			src, ok := constStringExpr(a.Value.Expr)
			if ok && IsLocalModuleSource(src) {
				rel, err := relativeSource("modules/"+x.name, ResolveLocalSource(".", src))
				if err != nil {
					return err
				}
				a.Value = stringValue(rel)
			}
		default:
			if err := x.inboundValue(n, &a, top); err != nil {
				return err
			}
		}
		attrs = append(attrs, a)
	}
	b.Attrs = attrs
	if top {
		if n.Kind == model.KindModule {
			if getAttrValue(b, "providers") == "" {
				x.nestedInherit = true
			}
		} else if getAttrValue(b, "provider") == "" {
			x.configs[providerOfType(n.Type)] = true
		}
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if top && blk.Type == "lifecycle" {
			if err := x.inboundLifecycle(blk); err != nil {
				return err
			}
			continue
		}
		if err := x.inbound(n, &blk.Body, false); err != nil {
			return err
		}
	}
	return nil
}

// inboundLifecycle rejects replace_triggered_by on root nodes (a module
// cannot reference resources of its caller); ignore_changes names
// attributes, not references.
func (x *extractor) inboundLifecycle(blk *model.Block) error {
	for _, a := range blk.Body.Attrs {
		if a.Name != "replace_triggered_by" {
			continue
		}
		e, err := parseExpr(a.Value.Expr)
		if err != nil {
			return err
		}
		for _, r := range references(e, x.known) {
			if x.known(r.addr) && !x.moved[r.addr] {
				return fmt.Errorf("lifecycle.replace_triggered_by references %s, which stays in the root; a module cannot reference its caller's resources (move %s too, or remove the trigger)", r.addr, r.addr)
			}
		}
	}
	return nil
}

func (x *extractor) inboundValue(n *model.Node, a *model.Attr, top bool) error {
	if a.Value.Expr == "" {
		return nil
	}
	e, err := parseExpr(a.Value.Expr)
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	refs := references(e, x.known)
	whole := strings.TrimSpace(a.Value.Expr)
	var spans []span
	for _, r := range refs {
		switch {
		case r.addr == "path.module":
			spans = append(spans, span{r.start, r.end, "path.root"})
			continue
		case x.moved[r.addr]:
			continue
		}
		var consumer *AttrInfo
		if top && n.Kind != model.KindModule && x.schema != nil && whole == a.Value.Expr[r.start:r.end] {
			if info, ok := x.schema.Attribute(n.Kind, n.Type, a.Name); ok {
				consumer = &info
			}
		}
		name := x.input(r, a.Value.Expr[r.start:r.end], consumer)
		spans = append(spans, span{r.start, r.end, "var." + name})
	}
	if len(spans) > 0 {
		a.Value = model.Value{Expr: applySpans(a.Value.Expr, spans)}
	}
	return nil
}

// input returns the module variable carrying the value of reference r
// (source text refText), creating it on first use.
func (x *extractor) input(r reference, refText string, consumer *AttrInfo) string {
	key := r.key()
	if in, ok := x.inputs[key]; ok {
		if in.io.Type == "any" && consumer != nil && consumer.Type != "" {
			in.io.Type = consumer.Type
		}
		return in.io.Name
	}
	in := &ioEntry{io: model.ModuleIO{Type: "any", Value: refText}}
	base := ""
	switch {
	case strings.HasPrefix(r.addr, "var."):
		base = strings.TrimPrefix(r.addr, "var.")
		in.io.Description = "Passed through from var." + base + " of the root module."
		if v := x.variable(base); v != nil {
			if t := getAttrValue(&v.Body, "type"); t != "" {
				in.io.Type = t
			}
			if d, ok := constStringExpr(getAttrValue(&v.Body, "description")); ok && d != "" {
				in.io.Description = d
			}
			in.io.Sensitive = getAttrValue(&v.Body, "sensitive") == "true"
			for _, blk := range v.Body.Blocks {
				if blk.Type == "validation" {
					in.blocks = append(in.blocks, blk)
				}
			}
		}
	case strings.HasPrefix(r.addr, "local."):
		base = strings.TrimPrefix(r.addr, "local.")
		in.io.Description = "Value of local." + base + " in the root module."
		if r.attr == "" {
			in.io.Type = x.localType(base)
		}
	default:
		nd := x.byAddr[r.addr]
		base = ioName(nd.Name, r.attr)
		in.io.Description = "The whole " + r.addr + " object from the root module."
		if r.attr != "" {
			in.io.Description = "Value of " + key + " from the root module."
			if x.schema != nil && nd.Kind != model.KindModule {
				if info, ok := x.schema.Attribute(nd.Kind, nd.Type, r.attr); ok {
					if info.Type != "" {
						in.io.Type = info.Type
					}
					if d := firstSentence(info.Description); d != "" {
						in.io.Description = d + " (" + key + " in the root module)."
					}
				}
			}
		}
	}
	if in.io.Type == "any" && consumer != nil && consumer.Type != "" {
		in.io.Type = consumer.Type
	}
	name := base
	if reservedVariableNames[name] {
		name += "_value"
	}
	name = uniqueName(name, x.inputNames)
	if name != base {
		in.blocks = nil // validation conditions name var.<base>
	}
	in.io.Name = name
	x.inputs[key] = in
	x.inputOrder = append(x.inputOrder, key)
	return name
}

func (x *extractor) variable(name string) *model.NamedBlock {
	for i := range x.wf.Variables {
		if x.wf.Variables[i].Name == name {
			return &x.wf.Variables[i]
		}
	}
	return nil
}

// localType is the type of a root local whose value is a string template or
// a literal ("${var.project}-${var.environment}" -> "string"), else "any".
func (x *extractor) localType(name string) string {
	for _, l := range x.wf.Locals {
		if l.Name != name {
			continue
		}
		e, err := parseExpr(l.Value.Expr)
		if err != nil {
			return "any"
		}
		switch e := e.(type) {
		case *hclsyntax.TemplateExpr:
			return "string"
		case *hclsyntax.LiteralValueExpr:
			switch t := e.Val.Type(); {
			case t == cty.String:
				return "string"
			case t == cty.Number:
				return "number"
			case t == cty.Bool:
				return "bool"
			}
		}
		return "any"
	}
	return "any"
}

// output returns the module output exposing reference r, creating it on first use.
func (x *extractor) output(r reference, refText string) string {
	key := r.key()
	if o, ok := x.outputs[key]; ok {
		return o.io.Name
	}
	nd := x.byAddr[r.addr]
	o := &ioEntry{io: model.ModuleIO{Value: refText}}
	if r.attr == "" {
		o.io.Description = "The whole " + r.addr + " object."
		if x.schema != nil && nd.Kind != model.KindModule {
			o.io.Sensitive = x.schema.HasSensitive(nd.Kind, nd.Type)
		}
	} else {
		o.io.Description = "Value of " + key + "."
		if x.schema != nil && nd.Kind != model.KindModule {
			if info, ok := x.schema.Attribute(nd.Kind, nd.Type, r.attr); ok {
				if d := firstSentence(info.Description); d != "" {
					o.io.Description = d + " (" + key + ")."
				}
				o.io.Sensitive = info.Sensitive
			}
		}
	}
	o.io.Name = uniqueName(ioName(nd.Name, r.attr), x.outputNames)
	x.outputs[key] = o
	x.outputOrder = append(x.outputOrder, key)
	return o.io.Name
}

// outbound rewrites every root reference to a moved block.
func (x *extractor) outbound() error {
	for i := range x.wf.Nodes {
		n := &x.wf.Nodes[i]
		addr := Address(n)
		if x.moved[addr] {
			continue
		}
		if err := x.outboundBody(addr, &n.Body, true); err != nil {
			return fmt.Errorf("%s: %w", addr, err)
		}
	}
	for i := range x.wf.Variables {
		if err := x.outboundBody("", &x.wf.Variables[i].Body, false); err != nil {
			return fmt.Errorf("variable %s: %w", x.wf.Variables[i].Name, err)
		}
	}
	for i := range x.wf.Outputs {
		if err := x.outboundBody("", &x.wf.Outputs[i].Body, true); err != nil {
			return fmt.Errorf("output %s: %w", x.wf.Outputs[i].Name, err)
		}
	}
	if err := x.outboundBody("", &model.Body{Attrs: x.wf.Locals}, false); err != nil {
		return fmt.Errorf("locals: %w", err)
	}
	for i := range x.wf.Providers {
		p := &x.wf.Providers[i]
		before := fmt.Sprint(p.Body)
		if err := x.outboundBody("", &p.Body, false); err != nil {
			return fmt.Errorf("provider %s: %w", p.Name, err)
		}
		if fmt.Sprint(p.Body) != before {
			cfg := p.Name
			if alias, ok := constStringExpr(getAttrValue(&p.Body, "alias")); ok && alias != "" {
				cfg += "." + alias
			}
			if x.configs[cfg] {
				return fmt.Errorf("provider %s is configured from blocks being moved and is also used by them; the module would depend on itself", cfg)
			}
		}
	}
	for i := range x.wf.Other {
		b := &x.wf.Other[i]
		switch b.Type {
		case "moved", "removed":
			// Historical addresses; a chain old -> moved -> module.<name>.moved works.
		case "import":
			for j := range b.Body.Attrs {
				a := &b.Body.Attrs[j]
				if a.Name != "to" {
					if err := x.outboundValue(a); err != nil {
						return fmt.Errorf("import: %w", err)
					}
					continue
				}
				e, err := parseExpr(a.Value.Expr)
				if err != nil {
					return fmt.Errorf("import: %w", err)
				}
				var spans []span
				for _, r := range references(e, x.known) {
					if x.moved[r.addr] {
						spans = append(spans, span{r.start, r.start, "module." + x.name + "."})
					}
				}
				if len(spans) > 0 {
					a.Value = model.Value{Expr: applySpans(a.Value.Expr, spans)}
				}
			}
		default:
			if err := x.outboundBody("", &b.Body, false); err != nil {
				return fmt.Errorf("%s block: %w", b.Type, err)
			}
		}
	}
	return nil
}

// outboundBody rewrites references to moved blocks in a root body; owner is
// the node address for node bodies.
func (x *extractor) outboundBody(owner string, b *model.Body, top bool) error {
	for i := range b.Attrs {
		a := &b.Attrs[i]
		if top && a.Name == "depends_on" {
			call := "module." + x.name
			expr, err := x.splitDependsOn(a.Value.Expr, func(addr string) string {
				if !x.moved[addr] {
					return addr
				}
				if owner != "" {
					x.dependents[owner] = true
				}
				return call
			})
			if err != nil {
				return err
			}
			if expr != "" {
				a.Value = model.Value{Expr: expr}
			}
			continue
		}
		if err := x.outboundValue(a); err != nil {
			return err
		}
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if top && blk.Type == "lifecycle" {
			for _, a := range blk.Body.Attrs {
				if a.Name != "replace_triggered_by" {
					continue
				}
				e, err := parseExpr(a.Value.Expr)
				if err != nil {
					return err
				}
				for _, r := range references(e, x.known) {
					if x.moved[r.addr] {
						return fmt.Errorf("lifecycle.replace_triggered_by references %s; a resource cannot be replaced by a trigger inside a module (move this resource too, or remove the trigger)", r.addr)
					}
				}
			}
			continue
		}
		if err := x.outboundBody(owner, &blk.Body, false); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) outboundValue(a *model.Attr) error {
	if a.Value.Expr == "" {
		return nil
	}
	e, err := parseExpr(a.Value.Expr)
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	var spans []span
	for _, r := range references(e, x.known) {
		if !x.moved[r.addr] {
			continue
		}
		name := x.output(r, a.Value.Expr[r.start:r.end])
		spans = append(spans, span{r.start, r.end, "module." + x.name + "." + name})
	}
	if len(spans) > 0 {
		a.Value = model.Value{Expr: applySpans(a.Value.Expr, spans)}
	}
	return nil
}

// splitDependsOn rewrites a depends_on list: keep maps each element address
// to its replacement ("" drops it). Duplicates are removed. It returns ""
// when nothing is left.
func (x *extractor) splitDependsOn(expr string, keep func(addr string) string) (string, error) {
	e, err := parseExpr(expr)
	if err != nil {
		return "", err
	}
	tup, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return expr, nil
	}
	var items []string
	changed := false
	for _, el := range tup.Exprs {
		src := strings.TrimSpace(expr[el.Range().Start.Byte:el.Range().End.Byte])
		t, ok := el.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			items = append(items, src)
			continue
		}
		r, ok := referenceOf(t.Traversal, x.known)
		if !ok || r.attr != "" || r.steps != 0 || !x.known(r.addr) {
			items = append(items, src)
			continue
		}
		repl := keep(r.addr)
		if repl != r.addr {
			changed = true
		}
		if repl != "" && !slices.Contains(items, repl) {
			items = append(items, repl)
		}
	}
	if !changed {
		return expr, nil
	}
	if len(items) == 0 {
		return "", nil
	}
	return "[" + strings.Join(items, ", ") + "]", nil
}

// providerMapConfigs records the root provider configurations a moved
// module call passes through its `providers` map.
func (x *extractor) providerMapConfigs(expr string) error {
	e, err := parseExpr(expr)
	if err != nil {
		return err
	}
	obj, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	for _, it := range obj.Items {
		r := it.ValueExpr.Range()
		x.configs[compactExpr(expr[r.Start.Byte:r.End.Byte])] = true
	}
	return nil
}

// checkCycles rejects extractions where depends_on on the whole module (or
// of the whole module) would create a dependency cycle.
func (x *extractor) checkCycles() error {
	addrOf := map[string]string{}
	for i := range x.wf.Nodes {
		addrOf[x.wf.Nodes[i].ID] = Address(&x.wf.Nodes[i])
	}
	deps := map[string][]string{} // holder -> referenced
	for _, e := range x.wf.Edges {
		t, s := addrOf[e.Target], addrOf[e.Source]
		if t != "" && s != "" {
			deps[t] = append(deps[t], s)
		}
	}
	reaches := func(from, to string) bool {
		seen := map[string]bool{from: true}
		stack := []string{from}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, d := range deps[cur] {
				if d == to {
					return true
				}
				if !seen[d] {
					seen[d] = true
					stack = append(stack, d)
				}
			}
		}
		return false
	}
	movedAddrs := sortedKeys(x.moved)
	for _, d := range sortedKeys(x.dependents) {
		for _, m := range movedAddrs {
			if reaches(m, d) {
				return fmt.Errorf("%s has depends_on on a block being moved, but %s uses %s: depends_on = [module.%s] would form a cycle; move %s too or drop its depends_on", d, m, d, x.name, d)
			}
		}
	}
	for _, d := range sortedKeys(x.callDeps) {
		for _, m := range movedAddrs {
			if reaches(d, m) {
				return fmt.Errorf("a moved block has depends_on on %s, which uses %s: the module call would depend on %s and form a cycle; move %s too or drop the depends_on", d, m, d, d)
			}
		}
	}
	return nil
}

// assemble builds the module files, the call node and the moved blocks, and
// stores the result in wf.
func (x *extractor) assemble(wf *model.Workflow) (*Extraction, error) {
	work := x.wf
	source := "./modules/" + x.name
	res := &Extraction{ModuleExtraction: model.ModuleExtraction{
		Name: x.name, Source: source,
		Inputs: []model.ModuleIO{}, Outputs: []model.ModuleIO{}, Moved: []model.MovedAddress{},
		Providers: []string{}, DependsOn: sortedKeys(x.callDeps), Files: []string{},
	}}

	mod := &model.Workflow{
		Name:        x.name,
		Description: "Module extracted by Nimbo from the root configuration.",
		Variables:   []model.NamedBlock{}, Outputs: []model.NamedBlock{}, Locals: []model.Attr{},
		Providers: []model.NamedBlock{}, Other: []model.Block{},
	}
	var kept []model.Node
	var movedIDs = map[string]bool{}
	var sumX, sumY float64
	parent, sameParent := "", true
	insertAt := -1
	for i := range work.Nodes {
		n := work.Nodes[i]
		addr := Address(&n)
		if !x.moved[addr] {
			kept = append(kept, n)
			continue
		}
		if insertAt < 0 {
			insertAt = len(kept)
		}
		movedIDs[n.ID] = true
		sumX += n.Position.X
		sumY += n.Position.Y
		if len(mod.Nodes) == 0 {
			parent = n.Parent
		} else if n.Parent != parent {
			sameParent = false
		}
		switch n.Kind {
		case model.KindResource, model.KindModule:
			to := "module." + x.name + "." + addr
			res.Moved = append(res.Moved, model.MovedAddress{From: addr, To: to})
		}
		n.File, n.Order, n.Parent, n.Position, n.Positioned, n.Label = "", 0, "", model.Position{}, false, ""
		mod.Nodes = append(mod.Nodes, n)
	}
	if !sameParent || movedIDs[parent] {
		parent = ""
	}

	for _, k := range x.inputOrder {
		in := x.inputs[k]
		res.Inputs = append(res.Inputs, in.io)
		body := model.Body{Attrs: []model.Attr{
			{Name: "description", Value: stringValue(in.io.Description)},
			{Name: "type", Value: model.Value{Expr: in.io.Type}},
		}, Blocks: []model.Block{}}
		if in.io.Sensitive {
			body.Attrs = append(body.Attrs, model.Attr{Name: "sensitive", Value: model.Value{Expr: "true"}})
		}
		body.Blocks = append(body.Blocks, in.blocks...)
		mod.Variables = append(mod.Variables, model.NamedBlock{Name: in.io.Name, Body: body})
	}
	for _, k := range x.outputOrder {
		o := x.outputs[k]
		res.Outputs = append(res.Outputs, o.io)
		body := model.Body{Attrs: []model.Attr{
			{Name: "description", Value: stringValue(o.io.Description)},
			{Name: "value", Value: model.Value{Expr: o.io.Value}},
		}, Blocks: []model.Block{}}
		if o.io.Sensitive {
			body.Attrs = append(body.Attrs, model.Attr{Name: "sensitive", Value: model.Value{Expr: "true"}})
		}
		mod.Outputs = append(mod.Outputs, model.NamedBlock{Name: o.io.Name, Body: body})
	}

	// Providers: defaults are inherited unless an alias forces a
	// `providers` map, which then has to list every configuration.
	hasAlias := false
	for c := range x.configs {
		if strings.Contains(c, ".") {
			hasAlias = true
		}
	}
	configs := map[string]bool{}
	for c := range x.configs {
		configs[c] = true
	}
	if hasAlias && x.nestedInherit {
		for _, n := range requiredProviderNames(work) {
			configs[n] = true
		}
		for _, p := range work.Providers {
			if getAttrValue(&p.Body, "alias") == "" {
				configs[p.Name] = true
			}
		}
	}
	mod.Terraform = x.moduleTerraform(work, configs)

	call := model.Node{
		Kind: model.KindModule, Type: "module", Name: x.name,
		Parent: parent, Positioned: true,
		Position: model.Position{X: roundGrid(sumX / float64(len(mod.Nodes))), Y: roundGrid(sumY / float64(len(mod.Nodes)))},
		Body:     model.Body{Attrs: []model.Attr{{Name: "source", Value: stringValue(source)}}, Blocks: []model.Block{}},
	}
	if hasAlias {
		names := sortedKeys(configs)
		sort.SliceStable(names, func(i, j int) bool {
			return !strings.Contains(names[i], ".") && strings.Contains(names[j], ".")
		})
		var sb strings.Builder
		sb.WriteString("{\n")
		for _, c := range names {
			sb.WriteString(c + " = " + c + "\n")
		}
		sb.WriteString("}")
		call.Body.Attrs = append(call.Body.Attrs, model.Attr{Name: "providers", Value: model.Value{Expr: sb.String()}})
		res.Providers = names
	}
	for _, in := range res.Inputs {
		call.Body.Attrs = append(call.Body.Attrs, model.Attr{Name: in.Name, Value: model.Value{Expr: in.Value}})
	}
	if len(res.DependsOn) > 0 {
		call.Body.Attrs = append(call.Body.Attrs, model.Attr{Name: "depends_on", Value: model.Value{Expr: "[" + strings.Join(res.DependsOn, ", ") + "]"}})
	}
	used := map[string]bool{}
	for _, n := range kept {
		used[n.ID] = true
	}
	call.ID = uniqueID("module."+x.name, used)
	for i := range kept {
		if movedIDs[kept[i].Parent] {
			kept[i].Parent = ""
		}
	}
	nodes := append([]model.Node{}, kept[:insertAt]...)
	nodes = append(nodes, call)
	nodes = append(nodes, kept[insertAt:]...)
	work.Nodes = nodes

	for _, m := range res.Moved {
		addMoved(work, m)
	}

	files, err := Export(mod, ExportOptions{ResourceFile: x.resourceFile})
	if err != nil {
		return nil, fmt.Errorf("render module: %w", err)
	}
	files["README.md"] = Readme(mod, ReadmeOptions{ResourceFile: x.resourceFile, ModuleSource: source})
	dir := "modules/" + x.name
	res.Contents = map[string][]byte{}
	for name, data := range files {
		p := dir + "/" + name
		res.Contents[p] = data
		res.Files = append(res.Files, p)
	}
	sort.Strings(res.Files)
	res.NodeID = call.ID
	work.Edges, work.Diagnostics = nil, nil
	*wf = *work
	return res, nil
}

// moduleTerraform is the module's terraform block: required_version and the
// required_providers entries of the configurations it uses, with
// configuration_aliases for aliases.
func (x *extractor) moduleTerraform(root *model.Workflow, configs map[string]bool) *model.Body {
	aliases := map[string][]string{}
	for c := range configs {
		local, _, _ := strings.Cut(c, ".")
		if _, ok := aliases[local]; !ok {
			aliases[local] = nil
		}
		if strings.Contains(c, ".") {
			aliases[local] = append(aliases[local], c)
		}
	}
	tf := &model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}
	if root.Terraform != nil {
		if v := getAttrValue(root.Terraform, "required_version"); v != "" {
			tf.Attrs = append(tf.Attrs, model.Attr{Name: "required_version", Value: model.Value{Expr: v}})
		}
	}
	if len(aliases) == 0 {
		return tf
	}
	rp := model.Block{Type: "required_providers", Body: model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}}
	for _, local := range sortedKeys(aliases) {
		source, version := requiredProvider(root, local)
		if source == "" {
			source = `"hashicorp/` + local + `"`
		}
		var sb strings.Builder
		sb.WriteString("{\nsource = " + source + "\n")
		if version != "" {
			sb.WriteString("version = " + version + "\n")
		}
		if as := aliases[local]; len(as) > 0 {
			sort.Strings(as)
			sb.WriteString("configuration_aliases = [" + strings.Join(as, ", ") + "]\n")
		}
		sb.WriteString("}")
		rp.Body.Attrs = append(rp.Body.Attrs, model.Attr{Name: local, Value: model.Value{Expr: sb.String()}})
	}
	tf.Blocks = append(tf.Blocks, rp)
	return tf
}

// requiredProvider returns the source and version expressions of a root
// required_providers entry ("" when absent).
func requiredProvider(wf *model.Workflow, local string) (source, version string) {
	if wf.Terraform == nil {
		return "", ""
	}
	for _, b := range wf.Terraform.Blocks {
		if b.Type != "required_providers" {
			continue
		}
		expr := getAttrValue(&b.Body, local)
		if expr == "" {
			continue
		}
		e, err := parseExpr(expr)
		if err != nil {
			continue
		}
		obj, ok := e.(*hclsyntax.ObjectConsExpr)
		if !ok {
			// Legacy form: aws = "~> 5.0" (a version constraint).
			return "", strings.TrimSpace(expr)
		}
		for _, it := range obj.Items {
			r := it.ValueExpr.Range()
			val := strings.TrimSpace(expr[r.Start.Byte:r.End.Byte])
			switch hcl.ExprAsKeyword(it.KeyExpr) {
			case "source":
				source = val
			case "version":
				version = val
			}
		}
		return source, version
	}
	return "", ""
}

func requiredProviderNames(wf *model.Workflow) []string {
	var out []string
	if wf.Terraform == nil {
		return nil
	}
	for _, b := range wf.Terraform.Blocks {
		if b.Type == "required_providers" {
			for _, a := range b.Body.Attrs {
				out = append(out, a.Name)
			}
		}
	}
	return out
}

// addMoved appends a moved block unless it exists, dropping an existing
// block that moves the other way (the pair would form a cycle).
func addMoved(wf *model.Workflow, m model.MovedAddress) {
	other := wf.Other[:0]
	exists := false
	for _, b := range wf.Other {
		if b.Type == "moved" {
			from, to := compactExpr(getAttrValue(&b.Body, "from")), compactExpr(getAttrValue(&b.Body, "to"))
			if from == m.To && to == m.From {
				continue
			}
			if from == m.From && to == m.To {
				exists = true
			}
		}
		other = append(other, b)
	}
	wf.Other = other
	if exists {
		return
	}
	wf.Other = append(wf.Other, model.Block{Type: "moved", Body: model.Body{
		Attrs: []model.Attr{
			{Name: "from", Value: model.Value{Expr: m.From}},
			{Name: "to", Value: model.Value{Expr: m.To}},
		},
		Blocks: []model.Block{},
	}})
}

// relativeSource is the local module source that reaches target (a slash
// path relative to the workflow root) from module directory dir.
func relativeSource(dir, target string) (string, error) {
	rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(target))
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel, nil
}

// ioName names a module input/output for attribute attr of node name:
// "queue" + "arn" -> "queue_arn"; "vpc" + "vpc_id" -> "vpc_id".
func ioName(name, attr string) string {
	switch {
	case attr == "":
		return name
	case attr == name || strings.HasPrefix(attr, name+"_"):
		return attr
	}
	return name + "_" + attr
}

func uniqueName(base string, used map[string]bool) string {
	name := base
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[name] = true
	return name
}

// firstSentence returns the first sentence of a schema description, as
// plain text (at most 160 characters).
func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "`", ""))
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if len(s) > 160 {
		s = strings.TrimSpace(s[:157]) + "..."
	}
	if s != "" {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

func roundGrid(v float64) float64 {
	const grid = 16
	if v < 0 {
		return -roundGrid(-v)
	}
	return float64(int(v/grid+0.5)) * grid
}

func compactExpr(s string) string { return strings.Join(strings.Fields(s), "") }

func stringValue(s string) model.Value {
	lit, _ := json.Marshal(s)
	expr, _ := ExprFromLiteral(lit)
	return model.Value{Expr: expr, Literal: lit}
}

// getAttrValue returns the expression of a body attribute ("" when unset).
func getAttrValue(b *model.Body, name string) string {
	for _, a := range b.Attrs {
		if a.Name == name {
			return strings.TrimSpace(a.Value.Expr)
		}
	}
	return ""
}

// constStringExpr evaluates expression source as a constant string.
func constStringExpr(expr string) (string, bool) {
	if expr == "" {
		return "", false
	}
	return constTokens([]byte(expr))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// findNodeRef resolves ref as a node ID or address.
func findNodeRef(wf *model.Workflow, ref string) *model.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == ref {
			return &wf.Nodes[i]
		}
	}
	for i := range wf.Nodes {
		if Address(&wf.Nodes[i]) == ref {
			return &wf.Nodes[i]
		}
	}
	return nil
}

// cloneWorkflow deep-copies wf.
func cloneWorkflow(wf *model.Workflow) (*model.Workflow, error) {
	data, err := json.Marshal(wf)
	if err != nil {
		return nil, err
	}
	out := &model.Workflow{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

// LocalModuleDir returns the directory (slash path relative to the workflow
// root, e.g. "modules/network") of a module call with a constant local
// source.
func LocalModuleDir(n *model.Node) (string, bool) {
	if n.Kind != model.KindModule {
		return "", false
	}
	src, ok := constStringExpr(getAttrValue(&n.Body, "source"))
	if !ok || !IsLocalModuleSource(src) {
		return "", false
	}
	dir := ResolveLocalSource(".", src)
	if dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
		return "", false
	}
	return path.Clean(dir), true
}
