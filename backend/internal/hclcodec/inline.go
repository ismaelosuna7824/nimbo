package hclcodec

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Inlining is the result of InlineModule.
type Inlining struct {
	// Dir is the module directory (slash path relative to the workflow
	// root) whose Terraform files are no longer used.
	Dir string
	// Moved are the root `moved` blocks added.
	Moved []model.MovedAddress
	// AssetMoves are the module's files its blocks read through
	// "${path.module}/<path>" that move back to the root
	// (modules/<name>/services/<svc> -> services/<svc>), the reverse of
	// ExtractModule's.
	AssetMoves []AssetMove
}

// InlineModule replaces the call of a local module (nodeRef: ID or address)
// by the module's blocks, the reverse of ExtractModule: input variables
// become the call's argument expressions (or their defaults), root
// references to module outputs become the output expressions,
// depends_on = [module.<name>] lists the inlined blocks, the call's
// depends_on goes to every inlined block, provider mappings are applied, and
// a root `moved` block moves every resource and nested module call out of
// the module (moved blocks in the opposite direction are dropped). files
// are the module directory's files (name -> content; only *.tf are read).
// Files the blocks read through "${path.module}/<path>" move back to the
// root (AssetMoves) unless rootHas reports that path taken (nil: nothing
// is) or the root reads it; those stay in the module folder, read through
// ${path.module}/modules/<name>/<path>.
//
// Only modules called once and without count/for_each can be inlined. wf is
// changed only on success; call Normalize afterwards.
func InlineModule(wf *model.Workflow, nodeRef string, files map[string][]byte, rootHas func(rel string) bool) (*Inlining, error) {
	work, err := cloneWorkflow(wf)
	if err != nil {
		return nil, err
	}
	call := findNodeRef(work, nodeRef)
	if call == nil {
		return nil, fmt.Errorf("node %q not found", nodeRef)
	}
	if call.Kind != model.KindModule {
		return nil, fmt.Errorf("%s is not a module call", Address(call))
	}
	callAddr := Address(call)
	dir, ok := LocalModuleDir(call)
	if !ok {
		return nil, fmt.Errorf("%s is not a local module inside the workflow files (its source is downloaded at deploy time); only local modules can be inlined", callAddr)
	}
	for _, meta := range []string{"count", "for_each"} {
		if getAttrValue(&call.Body, meta) != "" {
			return nil, fmt.Errorf("%s uses %s: it creates one copy of the module per instance, which cannot become a single set of root blocks; remove %s first", callAddr, meta, meta)
		}
	}
	for i := range work.Nodes {
		n := &work.Nodes[i]
		if n != call && n.Kind == model.KindModule {
			if d, ok := LocalModuleDir(n); ok && d == dir {
				return nil, fmt.Errorf("%s is called more than once (also by %s); inline works on modules called once", dir, Address(n))
			}
		}
	}

	tf := map[string][]byte{}
	for name, data := range files {
		if strings.HasSuffix(name, ".tf") && !strings.Contains(name, "/") {
			tf[name] = data
		}
	}
	mod, diags, err := Import(callAddr, tf)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			return nil, fmt.Errorf("%s has errors: %s", dir, d.Message)
		}
	}
	if len(mod.Providers) > 0 {
		return nil, fmt.Errorf("%s configures its own providers (provider blocks inside the module); move them to the root first", dir)
	}
	for _, b := range mod.Other {
		if b.Type != "check" {
			return nil, fmt.Errorf("%s contains a %s block, which inline does not translate; remove it first", dir, b.Type)
		}
	}

	in := &inliner{root: work, mod: mod, call: call, callAddr: callAddr, dir: dir, args: map[string]string{}, modAddr: map[string]bool{},
		rootHas: rootHas, rootUnits: rootPathUnits(work, func(n *model.Node) bool { return n == call }), assetUnits: map[string]bool{}}
	if err := in.prepare(); err != nil {
		return nil, err
	}
	if err := in.substituteModule(); err != nil {
		return nil, err
	}
	if err := in.rewriteRoot(); err != nil {
		return nil, err
	}
	return in.assemble(wf)
}

type inliner struct {
	root     *model.Workflow
	mod      *model.Workflow
	call     *model.Node
	callAddr string
	dir      string

	args      map[string]string // variable name -> expression replacing var.<name>
	providers map[string]string // module provider configuration -> root configuration
	modAddr   map[string]bool   // addresses of the module's nodes
	dependsOn []string          // the call's depends_on elements

	rootHas    func(rel string) bool
	rootUnits  []string        // asset units the root reads
	assetUnits map[string]bool // asset units moving back to the root
}

// movable reports whether the module's asset unit can move to the root.
func (in *inliner) movable(unit string) bool {
	return unit != "" && !overlapsAny(unit, in.rootUnits) && (in.rootHas == nil || !in.rootHas(unit))
}

// prepare resolves variable values and provider mappings and checks for
// address and local name clashes.
func (in *inliner) prepare() error {
	meta := map[string]bool{"source": true, "version": true, "providers": true, "depends_on": true}
	callArgs := map[string]string{}
	for _, a := range in.call.Body.Attrs {
		if !meta[a.Name] {
			callArgs[a.Name] = a.Value.Expr
		}
	}
	for _, v := range in.mod.Variables {
		typ := getAttrValue(&v.Body, "type")
		arg, ok := callArgs[v.Name]
		delete(callArgs, v.Name)
		switch {
		case ok && strings.Contains(typ, "optional("):
			return fmt.Errorf("variable %s has optional() object attributes whose defaults the module fills in; inlining would change the value", v.Name)
		case ok:
			in.args[v.Name] = arg
		case getAttrValue(&v.Body, "default") != "":
			in.args[v.Name] = getAttrValue(&v.Body, "default")
		default:
			return fmt.Errorf("variable %s of %s has no value: the call does not set it and it has no default", v.Name, in.dir)
		}
	}
	for name := range callArgs {
		return fmt.Errorf("%s sets %s, which the module does not declare", in.callAddr, name)
	}

	if expr := getAttrValue(&in.call.Body, "providers"); expr != "" {
		in.providers = map[string]string{}
		e, err := parseExpr(expr)
		if err != nil {
			return err
		}
		obj, ok := e.(*hclsyntax.ObjectConsExpr)
		if !ok {
			return errors.New("providers must be a map")
		}
		for _, it := range obj.Items {
			k, v := it.KeyExpr.Range(), it.ValueExpr.Range()
			in.providers[compactExpr(expr[k.Start.Byte:k.End.Byte])] = compactExpr(expr[v.Start.Byte:v.End.Byte])
		}
	}
	if expr := getAttrValue(&in.call.Body, "depends_on"); expr != "" {
		e, err := parseExpr(expr)
		if err != nil {
			return err
		}
		if tup, ok := e.(*hclsyntax.TupleConsExpr); ok {
			for _, el := range tup.Exprs {
				in.dependsOn = append(in.dependsOn, strings.TrimSpace(expr[el.Range().Start.Byte:el.Range().End.Byte]))
			}
		}
	}

	rootAddr := map[string]bool{}
	for i := range in.root.Nodes {
		if n := &in.root.Nodes[i]; n != in.call {
			rootAddr[Address(n)] = true
		}
	}
	for i := range in.mod.Nodes {
		addr := Address(&in.mod.Nodes[i])
		if rootAddr[addr] {
			return fmt.Errorf("%s exists both in the root and in %s; rename one of them first", addr, in.dir)
		}
		in.modAddr[addr] = true
	}
	locals := map[string]bool{}
	for _, l := range in.root.Locals {
		locals[l.Name] = true
	}
	for _, l := range in.mod.Locals {
		if locals[l.Name] {
			return fmt.Errorf("local.%s exists both in the root and in %s; rename one of them first", l.Name, in.dir)
		}
	}
	return nil
}

// substituteModule rewrites the module's blocks for the root: variables,
// path.module, nested local module sources and provider mappings.
func (in *inliner) substituteModule() error {
	for i := range in.mod.Nodes {
		n := &in.mod.Nodes[i]
		if err := in.substBody(&n.Body); err != nil {
			return fmt.Errorf("%s: %w", Address(n), err)
		}
		if err := in.mapProvider(n); err != nil {
			return err
		}
		if n.Kind == model.KindModule {
			for j := range n.Body.Attrs {
				a := &n.Body.Attrs[j]
				if a.Name != "source" {
					continue
				}
				if src, ok := constStringExpr(a.Value.Expr); ok && IsLocalModuleSource(src) {
					target := ResolveLocalSource(in.dir, src)
					if target == ".." || strings.HasPrefix(target, "../") {
						return fmt.Errorf("%s: source %s leaves the workflow files", Address(n), src)
					}
					a.Value = stringValue("./" + target)
				}
			}
		}
	}
	for i := range in.mod.Locals {
		if err := in.substAttr(&in.mod.Locals[i]); err != nil {
			return fmt.Errorf("local.%s: %w", in.mod.Locals[i].Name, err)
		}
	}
	for i := range in.mod.Outputs {
		if err := in.substBody(&in.mod.Outputs[i].Body); err != nil {
			return fmt.Errorf("output %s: %w", in.mod.Outputs[i].Name, err)
		}
	}
	for i := range in.mod.Other {
		if err := in.substBody(&in.mod.Other[i].Body); err != nil {
			return fmt.Errorf("%s block: %w", in.mod.Other[i].Type, err)
		}
	}
	return nil
}

func (in *inliner) substBody(b *model.Body) error {
	for i := range b.Attrs {
		if err := in.substAttr(&b.Attrs[i]); err != nil {
			return err
		}
	}
	for i := range b.Blocks {
		if err := in.substBody(&b.Blocks[i].Body); err != nil {
			return err
		}
	}
	return nil
}

func (in *inliner) substAttr(a *model.Attr) error {
	expr := a.Value.Expr
	if expr == "" {
		return nil
	}
	e, err := parseExpr(expr)
	if err != nil {
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	var spans []span
	back := map[int]bool{}
	for _, p := range pathRefs(e) {
		if p.root == "module" && in.movable(p.unit) {
			back[p.start] = true
			in.assetUnits[p.unit] = true
		}
	}
	for _, r := range references(e, func(string) bool { return false }) {
		switch {
		case r.addr == "path.module":
			if !back[r.start] {
				spans = append(spans, pathModuleSpan(expr, r, in.dir))
			}
		case strings.HasPrefix(r.addr, "var."):
			name := strings.TrimPrefix(r.addr, "var.")
			arg, ok := in.args[name]
			if !ok {
				return fmt.Errorf("%s: var.%s is not declared in the module", a.Name, name)
			}
			spans = append(spans, span{r.start, r.end, substitution(arg, r.steps > 0)})
		}
	}
	if len(spans) > 0 {
		a.Value = model.Value{Expr: applySpans(expr, spans)}
	}
	return nil
}

// pathModuleSpan replaces path.module (the module directory) by the same
// directory seen from the root: inside a template interpolation
// "${path.module}/x" becomes "${path.module}/modules/m/x".
func pathModuleSpan(expr string, r reference, dir string) span {
	before := strings.TrimRight(expr[:r.start], " ")
	after := strings.TrimLeft(expr[r.end:], " ")
	if strings.HasSuffix(before, "${") && strings.HasPrefix(after, "}") && r.steps == 0 {
		start := len(before) - 2
		end := len(expr) - len(after) + 1
		return span{start, end, "${path.module}/" + dir}
	}
	return span{r.start, r.end, `"${path.module}/` + dir + `"`}
}

// substitution is expr ready to replace a reference; it gets parentheses
// unless it is a primary expression (and, when more steps follow, unless it
// is a traversal or function call).
func substitution(expr string, followed bool) string {
	expr = strings.TrimSpace(expr)
	if endsWithHeredoc(expr) {
		return "(\n" + expr + "\n)"
	}
	e, err := parseExpr(expr)
	if err != nil {
		return "(" + expr + ")"
	}
	switch e.(type) {
	case *hclsyntax.ScopeTraversalExpr, *hclsyntax.FunctionCallExpr, *hclsyntax.ParenthesesExpr,
		*hclsyntax.RelativeTraversalExpr, *hclsyntax.IndexExpr, *hclsyntax.SplatExpr:
		return expr
	case *hclsyntax.LiteralValueExpr, *hclsyntax.TemplateExpr, *hclsyntax.TemplateWrapExpr,
		*hclsyntax.TupleConsExpr, *hclsyntax.ObjectConsExpr:
		if !followed {
			return expr
		}
	}
	return "(" + expr + ")"
}

// mapProvider applies the call's providers map to a module node.
func (in *inliner) mapProvider(n *model.Node) error {
	if in.providers == nil {
		return nil
	}
	if n.Kind == model.KindModule {
		for child, parent := range in.providers {
			if child != parent {
				return fmt.Errorf("%s is a nested module call and %s maps provider %s to %s; inline does not remap nested modules", Address(n), in.callAddr, child, parent)
			}
		}
		return nil
	}
	implied := providerOfType(n.Type)
	cfg := implied
	idx := -1
	for i, a := range n.Body.Attrs {
		if a.Name == "provider" {
			cfg, idx = compactExpr(a.Value.Expr), i
		}
	}
	parent, ok := in.providers[cfg]
	if !ok || parent == cfg {
		return nil
	}
	switch {
	case parent == implied && idx >= 0:
		n.Body.Attrs = slices.Delete(n.Body.Attrs, idx, idx+1)
	case idx >= 0:
		n.Body.Attrs[idx].Value = model.Value{Expr: parent}
	default:
		n.Body.Attrs = append([]model.Attr{{Name: "provider", Value: model.Value{Expr: parent}}}, n.Body.Attrs...)
	}
	return nil
}

// rewriteRoot replaces references to the module call in the root.
func (in *inliner) rewriteRoot() error {
	outputs := map[string]string{}
	for _, o := range in.mod.Outputs {
		outputs[o.Name] = getAttrValue(&o.Body, "value")
	}
	var modAddrs []string
	for i := range in.mod.Nodes {
		modAddrs = append(modAddrs, Address(&in.mod.Nodes[i]))
	}
	known := func(addr string) bool { return addr == in.callAddr }

	var rewrite func(b *model.Body, top bool) error
	rewriteValue := func(a *model.Attr) error {
		expr := a.Value.Expr
		if expr == "" {
			return nil
		}
		e, err := parseExpr(expr)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		var spans []span
		for _, r := range references(e, known) {
			if r.addr != in.callAddr {
				continue
			}
			if r.attr == "" {
				return fmt.Errorf("%s uses the whole %s object; reference its outputs instead before inlining", a.Name, in.callAddr)
			}
			val, ok := outputs[r.attr]
			if !ok {
				return fmt.Errorf("%s reads %s.%s, which the module does not output", a.Name, in.callAddr, r.attr)
			}
			if val == "" {
				return fmt.Errorf("output %s of %s has no value", r.attr, in.dir)
			}
			spans = append(spans, span{r.start, r.end, substitution(val, r.steps > 0)})
		}
		if len(spans) > 0 {
			a.Value = model.Value{Expr: applySpans(expr, spans)}
		}
		return nil
	}
	rewrite = func(b *model.Body, top bool) error {
		for i := range b.Attrs {
			a := &b.Attrs[i]
			if top && a.Name == "depends_on" {
				expr, err := expandDependsOn(a.Value.Expr, in.callAddr, modAddrs)
				if err != nil {
					return err
				}
				a.Value = model.Value{Expr: expr}
				continue
			}
			if err := rewriteValue(a); err != nil {
				return err
			}
		}
		for i := range b.Blocks {
			if err := rewrite(&b.Blocks[i].Body, false); err != nil {
				return err
			}
		}
		return nil
	}

	for i := range in.root.Nodes {
		n := &in.root.Nodes[i]
		if n == in.call {
			continue
		}
		if err := rewrite(&n.Body, true); err != nil {
			return fmt.Errorf("%s: %w", Address(n), err)
		}
	}
	for i := range in.root.Variables {
		if err := rewrite(&in.root.Variables[i].Body, false); err != nil {
			return fmt.Errorf("variable %s: %w", in.root.Variables[i].Name, err)
		}
	}
	for i := range in.root.Outputs {
		if err := rewrite(&in.root.Outputs[i].Body, true); err != nil {
			return fmt.Errorf("output %s: %w", in.root.Outputs[i].Name, err)
		}
	}
	if err := rewrite(&model.Body{Attrs: in.root.Locals}, false); err != nil {
		return fmt.Errorf("locals: %w", err)
	}
	for i := range in.root.Providers {
		if err := rewrite(&in.root.Providers[i].Body, false); err != nil {
			return fmt.Errorf("provider %s: %w", in.root.Providers[i].Name, err)
		}
	}
	prefix := in.callAddr + "."
	for i := range in.root.Other {
		b := &in.root.Other[i]
		switch b.Type {
		case "moved", "removed":
		case "import":
			for j := range b.Body.Attrs {
				a := &b.Body.Attrs[j]
				if a.Name == "to" {
					if strings.HasPrefix(strings.TrimSpace(a.Value.Expr), prefix) {
						a.Value = model.Value{Expr: strings.TrimPrefix(strings.TrimSpace(a.Value.Expr), prefix)}
					}
					continue
				}
				if err := rewriteValue(a); err != nil {
					return fmt.Errorf("import: %w", err)
				}
			}
		default:
			if err := rewrite(&b.Body, false); err != nil {
				return fmt.Errorf("%s block: %w", b.Type, err)
			}
		}
	}
	return nil
}

// expandDependsOn replaces the module call in a depends_on list by the
// addresses of its blocks.
func expandDependsOn(expr, callAddr string, addrs []string) (string, error) {
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
		if t, ok := el.(*hclsyntax.ScopeTraversalExpr); ok && len(t.Traversal) == 2 && t.Traversal.RootName() == "module" {
			if a, ok := t.Traversal[1].(hcl.TraverseAttr); ok && "module."+a.Name == callAddr {
				changed = true
				for _, addr := range addrs {
					if !slices.Contains(items, addr) {
						items = append(items, addr)
					}
				}
				continue
			}
		}
		if !slices.Contains(items, src) {
			items = append(items, src)
		}
	}
	if !changed {
		return expr, nil
	}
	return "[" + strings.Join(items, ", ") + "]", nil
}

// assemble puts the module blocks in place of the call and adds moved blocks.
func (in *inliner) assemble(wf *model.Workflow) (*Inlining, error) {
	res := &Inlining{Dir: in.dir, Moved: []model.MovedAddress{}, AssetMoves: assetMoves(in.assetUnits, in.dir, "")}
	used := map[string]bool{}
	callIdx := 0
	for i := range in.root.Nodes {
		if &in.root.Nodes[i] == in.call {
			callIdx = i
			continue
		}
		used[in.root.Nodes[i].ID] = true
	}
	const cols, dx, dy = 3, 288, 128
	cx := in.call.Position.X - float64(min(len(in.mod.Nodes), cols)-1)*dx/2
	var inlined []model.Node
	for i, n := range in.mod.Nodes {
		addr := Address(&n)
		n.ID = uniqueID(addr, used)
		used[n.ID] = true
		n.File, n.Order, n.Parent = "", 0, in.call.Parent
		n.Position = model.Position{X: cx + float64(i%cols)*dx, Y: in.call.Position.Y + float64(i/cols)*dy}
		n.Positioned = true
		if len(in.dependsOn) > 0 {
			addDependsOn(&n.Body, in.dependsOn)
		}
		if n.Kind != model.KindData {
			res.Moved = append(res.Moved, model.MovedAddress{From: in.callAddr + "." + addr, To: addr})
		}
		inlined = append(inlined, n)
	}
	callID := in.call.ID
	nodes := append([]model.Node{}, in.root.Nodes[:callIdx]...)
	nodes = append(nodes, inlined...)
	nodes = append(nodes, in.root.Nodes[callIdx+1:]...)
	for i := range nodes {
		if nodes[i].Parent == callID {
			nodes[i].Parent = ""
		}
	}
	in.root.Nodes = nodes
	// Module content joins the root's recommended layout, not the module's files.
	for _, l := range in.mod.Locals {
		l.File, l.Order = "", 0
		in.root.Locals = append(in.root.Locals, l)
	}
	for _, b := range in.mod.Other {
		b.File, b.Order = "", 0
		in.root.Other = append(in.root.Other, b)
	}
	in.mergeRequiredProviders()
	for _, m := range res.Moved {
		addMoved(in.root, m)
	}
	in.root.Edges, in.root.Diagnostics = nil, nil
	*wf = *in.root
	return res, nil
}

// addDependsOn appends addresses to a node's depends_on.
func addDependsOn(b *model.Body, addrs []string) {
	for i := range b.Attrs {
		a := &b.Attrs[i]
		if a.Name != "depends_on" {
			continue
		}
		expr := strings.TrimSpace(a.Value.Expr)
		if e, err := parseExpr(expr); err == nil {
			if tup, ok := e.(*hclsyntax.TupleConsExpr); ok {
				var items []string
				for _, el := range tup.Exprs {
					items = append(items, strings.TrimSpace(expr[el.Range().Start.Byte:el.Range().End.Byte]))
				}
				for _, d := range addrs {
					if !slices.Contains(items, d) {
						items = append(items, d)
					}
				}
				a.Value = model.Value{Expr: "[" + strings.Join(items, ", ") + "]"}
			}
		}
		return
	}
	b.Attrs = append(b.Attrs, model.Attr{Name: "depends_on", Value: model.Value{Expr: "[" + strings.Join(addrs, ", ") + "]"}})
}

// mergeRequiredProviders adds the module's required_providers entries the
// root lacks (without configuration_aliases, which only modules declare).
func (in *inliner) mergeRequiredProviders() {
	if in.mod.Terraform == nil || in.root.Terraform == nil {
		return
	}
	var rootRP *model.Block
	for i := range in.root.Terraform.Blocks {
		if in.root.Terraform.Blocks[i].Type == "required_providers" {
			rootRP = &in.root.Terraform.Blocks[i]
			break
		}
	}
	for _, b := range in.mod.Terraform.Blocks {
		if b.Type != "required_providers" {
			continue
		}
		for _, a := range b.Body.Attrs {
			if rootRP != nil && getAttrValue(&rootRP.Body, a.Name) != "" {
				continue
			}
			source, version := requiredProvider(&model.Workflow{Terraform: &model.Body{Blocks: []model.Block{b}}}, a.Name)
			if source == "" {
				continue
			}
			expr := "{\nsource = " + source + "\n"
			if version != "" {
				expr += "version = " + version + "\n"
			}
			expr += "}"
			if rootRP == nil {
				in.root.Terraform.Blocks = append(in.root.Terraform.Blocks, model.Block{Type: "required_providers", Body: model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}})
				rootRP = &in.root.Terraform.Blocks[len(in.root.Terraform.Blocks)-1]
			}
			rootRP.Body.Attrs = append(rootRP.Body.Attrs, model.Attr{Name: a.Name, Value: model.Value{Expr: expr}})
		}
	}
}
