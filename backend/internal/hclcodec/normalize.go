package hclcodec

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Normalize fills Value.Expr/Literal pairs (also in Environments), assigns
// missing node IDs (from address), derives wf.Edges and wf.Clouds, and
// returns syntax/reference diagnostics (unknown reference, duplicate address,
// invalid expression). It does not validate against provider schemas.
func Normalize(wf *model.Workflow) []model.Diagnostic {
	n := newNormalizer(wf)
	n.checkNodes()
	n.walkAll()
	wf.Edges = n.sortedEdges()
	wf.Clouds = cloudsOf(wf)
	return dedupDiags(n.diags)
}

// deriveEdges recomputes wf.Edges without reporting diagnostics.
func deriveEdges(wf *model.Workflow) {
	n := newNormalizer(wf)
	n.indexNodes()
	n.walkAll()
	wf.Edges = n.sortedEdges()
}

// providerPrefixes are resource type prefixes recognized as references even
// when no node of that type exists, so references to missing resources are
// reported.
var providerPrefixes = []string{
	"aws", "azurerm", "azuread", "azapi", "google", "oci",
	"random", "null", "archive", "tls", "local", "time", "http", "external",
}

type normalizer struct {
	wf        *model.Workflow
	diags     []model.Diagnostic
	byAddr    map[string]string // address -> node ID (first occurrence)
	resTypes  map[string]bool   // types of managed resource nodes
	prefixes  map[string]bool
	variables map[string]bool
	edges     map[string]model.Edge
}

func newNormalizer(wf *model.Workflow) *normalizer {
	n := &normalizer{
		wf:        wf,
		byAddr:    map[string]string{},
		resTypes:  map[string]bool{},
		prefixes:  map[string]bool{},
		variables: map[string]bool{},
		edges:     map[string]model.Edge{},
	}
	for _, p := range providerPrefixes {
		n.prefixes[p] = true
	}
	for _, p := range wf.Providers {
		n.prefixes[p.Name] = true
	}
	for _, v := range wf.Variables {
		n.variables[v.Name] = true
	}
	return n
}

// indexNodes assigns missing or duplicate node IDs and indexes addresses.
func (n *normalizer) indexNodes() {
	used := map[string]bool{}
	for i := range n.wf.Nodes {
		nd := &n.wf.Nodes[i]
		if nd.ID != "" && !used[nd.ID] {
			used[nd.ID] = true
			continue
		}
		nd.ID = ""
	}
	for i := range n.wf.Nodes {
		nd := &n.wf.Nodes[i]
		if nd.ID == "" {
			nd.ID = uniqueID(Address(nd), used)
			used[nd.ID] = true
		}
		addr := Address(nd)
		if _, ok := n.byAddr[addr]; !ok {
			n.byAddr[addr] = nd.ID
		}
		if nd.Kind == model.KindResource {
			n.resTypes[nd.Type] = true
		}
		if nd.Kind != model.KindModule {
			if p, _, ok := strings.Cut(nd.Type, "_"); ok {
				n.prefixes[p] = true
			}
		}
	}
}

func uniqueID(base string, used map[string]bool) string {
	id := base
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	return id
}

func (n *normalizer) checkNodes() {
	n.indexNodes()
	seen := map[string]bool{}
	for i := range n.wf.Nodes {
		nd := &n.wf.Nodes[i]
		addr := Address(nd)
		switch nd.Kind {
		case model.KindResource, model.KindData, model.KindModule:
		default:
			n.diag(model.SeverityError, "invalid_kind", fmt.Sprintf("Node %s has unknown kind %q", nd.ID, nd.Kind), "", nd.ID, "")
			continue
		}
		if !hclsyntax.ValidIdentifier(nd.Name) {
			n.diag(model.SeverityError, "invalid_name", fmt.Sprintf("Invalid name %q for %s", nd.Name, addr),
				"Names must start with a letter or underscore and contain only letters, digits, underscores and dashes.", nd.ID, "")
		}
		if nd.Kind != model.KindModule && !hclsyntax.ValidIdentifier(nd.Type) {
			n.diag(model.SeverityError, "invalid_name", fmt.Sprintf("Invalid type %q for %s", nd.Type, addr), "", nd.ID, "")
		}
		if seen[addr] {
			n.diag(model.SeverityError, "duplicate_address", fmt.Sprintf("Duplicate address %s", addr),
				"Another node already uses this kind, type and name; rename one of them.", nd.ID, "")
		}
		seen[addr] = true
	}
}

// scope describes where a value lives. Edges are only derived for node
// values (nodeID set); other values are only checked for references.
type scope struct {
	nodeID string
	owner  string // human-readable owner for messages, e.g. "aws_s3_bucket.b"
	prefix string // attribute path prefix for non-node diagnostics
	refs   bool
}

func (n *normalizer) walkAll() {
	wf := n.wf
	for i := range wf.Nodes {
		nd := &wf.Nodes[i]
		n.body(&nd.Body, scope{nodeID: nd.ID, owner: Address(nd), refs: true}, "", true)
	}
	for i := range wf.Variables {
		v := &wf.Variables[i]
		n.body(&v.Body, scope{owner: "variable " + v.Name, prefix: "variable." + v.Name + ".", refs: true}, "", true)
	}
	for i := range wf.Outputs {
		o := &wf.Outputs[i]
		n.body(&o.Body, scope{owner: "output " + o.Name, prefix: "output." + o.Name + ".", refs: true}, "", true)
	}
	if len(wf.Locals) > 0 {
		n.body(&model.Body{Attrs: wf.Locals}, scope{owner: "locals", prefix: "local.", refs: true}, "", true)
	}
	for i := range wf.Providers {
		p := &wf.Providers[i]
		n.body(&p.Body, scope{owner: "provider " + p.Name, prefix: "provider." + p.Name + ".", refs: true}, "", true)
	}
	if wf.Terraform != nil {
		n.body(wf.Terraform, scope{owner: "terraform", prefix: "terraform."}, "", true)
	}
	counts := map[string]int{}
	for i := range wf.Other {
		b := &wf.Other[i]
		prefix := fmt.Sprintf("%s[%d].", b.Type, counts[b.Type])
		counts[b.Type]++
		// moved/removed blocks name addresses that intentionally no longer exist.
		refs := b.Type != "moved" && b.Type != "removed"
		n.body(&b.Body, scope{owner: b.Type, prefix: prefix, refs: refs}, "", true)
	}
	for i := range wf.Environments {
		env := &wf.Environments[i]
		names := make([]string, 0, len(env.Values))
		for k := range env.Values {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			v := env.Values[k]
			n.value(&v, scope{owner: "environment " + env.Name, prefix: "environment." + env.Name + "."}, k)
			env.Values[k] = v
		}
	}
}

func (n *normalizer) body(b *model.Body, sc scope, prefix string, top bool) {
	for i := range b.Attrs {
		a := &b.Attrs[i]
		path := prefix + a.Name
		e := n.value(&a.Value, sc, path)
		if e == nil || !sc.refs {
			continue
		}
		if top && a.Name == "depends_on" {
			n.dependsOn(e, sc, path)
		} else {
			n.references(e, sc, path)
		}
	}
	counts := map[string]int{}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		p := fmt.Sprintf("%s%s[%d].", prefix, blk.Type, counts[blk.Type])
		counts[blk.Type]++
		n.body(&blk.Body, sc, p, false)
	}
}

// value makes Expr authoritative, derives the missing form and returns the
// parsed expression (nil when invalid).
func (n *normalizer) value(v *model.Value, sc scope, path string) hclsyntax.Expression {
	attr := sc.prefix + path
	v.Expr = strings.TrimSpace(v.Expr)
	if v.Expr == "" && len(v.Literal) > 0 {
		expr, err := ExprFromLiteral(v.Literal)
		if err != nil {
			n.diag(model.SeverityError, "invalid_literal", fmt.Sprintf("Invalid value for %s in %s", path, sc.owner), err.Error(), sc.nodeID, attr)
			return nil
		}
		v.Expr = expr
	}
	e, err := parseExpr(v.Expr)
	if err != nil {
		v.Literal = nil
		n.diag(model.SeverityError, "invalid_expression", fmt.Sprintf("Invalid expression for %s in %s", path, sc.owner), err.Error(), sc.nodeID, attr)
		return nil
	}
	v.Literal, _ = literalOf(e)
	return e
}

func (n *normalizer) dependsOn(e hclsyntax.Expression, sc scope, path string) {
	tuple, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		n.references(e, sc, path)
		return
	}
	for _, item := range tuple.Exprs {
		t, ok := item.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			n.references(item, sc, path)
			continue
		}
		addr, _, isRef := n.resolve(t.Traversal)
		if !isRef {
			n.references(item, sc, path)
			continue
		}
		n.link(addr, "", model.EdgeDependsOn, sc, path)
	}
}

func (n *normalizer) references(e hclsyntax.Expression, sc scope, path string) {
	w := &refWalker{fn: func(t *hclsyntax.ScopeTraversalExpr, ancestors []hclsyntax.Node) {
		tr := t.Traversal
		if tr.RootName() == "var" {
			if len(tr) > 1 {
				if a, ok := tr[1].(hcl.TraverseAttr); ok && !n.variables[a.Name] {
					n.diag(model.SeverityWarning, "unknown_variable", fmt.Sprintf("Reference to undeclared variable var.%s in %s", a.Name, sc.owner),
						"Declare the variable or fix the reference.", sc.nodeID, sc.prefix+path)
				}
			}
			return
		}
		addr, rest, ok := n.resolve(tr)
		if !ok {
			return
		}
		srcAttr := firstAttr(rest)
		if srcAttr == "" {
			srcAttr = attrFromAncestors(t, ancestors)
		}
		n.link(addr, srcAttr, model.EdgeReference, sc, path)
	}}
	hclsyntax.Walk(e, w)
}

func (n *normalizer) link(addr, srcAttr string, kind model.EdgeKind, sc scope, path string) {
	src, ok := n.byAddr[addr]
	if !ok {
		n.diag(model.SeverityWarning, "unknown_reference", fmt.Sprintf("Reference to undeclared %s in %s", addr, sc.owner),
			"No node with this address exists in the workflow.", sc.nodeID, sc.prefix+path)
		return
	}
	if sc.nodeID == "" || src == sc.nodeID {
		return
	}
	e := model.Edge{
		ID:         fmt.Sprintf("e:%s>%s:%s:%s", src, sc.nodeID, path, srcAttr),
		Source:     src,
		Target:     sc.nodeID,
		Kind:       kind,
		Attribute:  path,
		SourceAttr: srcAttr,
	}
	n.edges[e.ID] = e
}

// resolve maps a traversal to the node address it references. rest holds the
// steps after the address.
func (n *normalizer) resolve(tr hcl.Traversal) (addr string, rest hcl.Traversal, ok bool) {
	if len(tr) < 2 {
		return "", nil, false
	}
	root := tr.RootName()
	first, isAttr := tr[1].(hcl.TraverseAttr)
	if !isAttr {
		return "", nil, false
	}
	switch root {
	case "data":
		if len(tr) < 3 {
			return "", nil, false
		}
		name, isAttr := tr[2].(hcl.TraverseAttr)
		if !isAttr {
			return "", nil, false
		}
		return "data." + first.Name + "." + name.Name, tr[3:], true
	case "module":
		return "module." + first.Name, tr[2:], true
	case "var", "local", "path", "each", "count", "self", "terraform":
		return "", nil, false
	}
	if !n.resTypes[root] {
		p, _, hasUnderscore := strings.Cut(root, "_")
		if !hasUnderscore || !n.prefixes[p] {
			return "", nil, false
		}
	}
	return root + "." + first.Name, tr[2:], true
}

func firstAttr(tr hcl.Traversal) string {
	for _, s := range tr {
		if a, ok := s.(hcl.TraverseAttr); ok {
			return a.Name
		}
	}
	return ""
}

// attrFromAncestors finds the attribute read from a reference whose address
// is followed by a dynamic index or a splat, e.g. aws_x.y[each.key].id or
// aws_x.y[*].arn.
func attrFromAncestors(t hclsyntax.Node, ancestors []hclsyntax.Node) string {
	cur := t
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch p := ancestors[i].(type) {
		case *hclsyntax.IndexExpr:
			if p.Collection != cur {
				return ""
			}
		case *hclsyntax.RelativeTraversalExpr:
			if p.Source != cur {
				return ""
			}
			if a := firstAttr(p.Traversal); a != "" {
				return a
			}
		case *hclsyntax.SplatExpr:
			if p.Source != cur {
				return ""
			}
			if rt, ok := p.Each.(*hclsyntax.RelativeTraversalExpr); ok {
				return firstAttr(rt.Traversal)
			}
			return ""
		default:
			return ""
		}
		cur = ancestors[i]
	}
	return ""
}

type refWalker struct {
	stack []hclsyntax.Node
	fn    func(*hclsyntax.ScopeTraversalExpr, []hclsyntax.Node)
}

func (w *refWalker) Enter(node hclsyntax.Node) hcl.Diagnostics {
	if t, ok := node.(*hclsyntax.ScopeTraversalExpr); ok {
		w.fn(t, w.stack)
	}
	w.stack = append(w.stack, node)
	return nil
}

func (w *refWalker) Exit(hclsyntax.Node) hcl.Diagnostics {
	w.stack = w.stack[:len(w.stack)-1]
	return nil
}

func (n *normalizer) sortedEdges() []model.Edge {
	edges := make([]model.Edge, 0, len(n.edges))
	for _, e := range n.edges {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Attribute != b.Attribute {
			return a.Attribute < b.Attribute
		}
		return a.SourceAttr < b.SourceAttr
	})
	return edges
}

func (n *normalizer) diag(sev model.Severity, code, msg, detail, nodeID, attr string) {
	n.diags = append(n.diags, model.Diagnostic{Severity: sev, Code: code, Message: msg, Detail: detail, NodeID: nodeID, Attribute: attr})
}

func dedupDiags(diags []model.Diagnostic) []model.Diagnostic {
	out := make([]model.Diagnostic, 0, len(diags))
	seen := map[model.Diagnostic]bool{}
	for _, d := range diags {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// cloudOfType maps a resource type or provider name to its cloud.
func cloudOfType(s string) (model.Cloud, bool) {
	p, _, _ := strings.Cut(s, "_")
	switch p {
	case "aws":
		return model.CloudAWS, true
	case "azurerm", "azuread", "azapi":
		return model.CloudAzure, true
	case "google", "google-beta":
		return model.CloudGCP, true
	case "oci":
		return model.CloudOCI, true
	}
	return "", false
}

func cloudsOf(wf *model.Workflow) []model.Cloud {
	found := map[model.Cloud]bool{}
	for _, nd := range wf.Nodes {
		if nd.Kind == model.KindModule {
			continue
		}
		if c, ok := cloudOfType(nd.Type); ok {
			found[c] = true
		}
	}
	for _, p := range wf.Providers {
		if c, ok := cloudOfType(p.Name); ok {
			found[c] = true
		}
	}
	clouds := make([]model.Cloud, 0, len(found))
	for _, c := range model.AllClouds {
		if found[c] {
			clouds = append(clouds, c)
		}
	}
	return slices.Clip(clouds)
}
