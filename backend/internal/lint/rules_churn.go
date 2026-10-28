package lint

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// checkCountOverList flags resources and module calls that create one
// instance per element of a list by position: count = length(L) with L
// indexed by count.index in the body (L[count.index], element(L,
// count.index)). count keys the instances by index, so removing or
// reordering an element shifts every later one and Terraform destroys and
// recreates them; for_each keys them by the element instead.
//
// L must be a variable, a local or a literal list: lists read from
// resources, data sources or module outputs may be unknown at plan time,
// where for_each can't be used. Toggles (count = var.create ? 1 : 0),
// constant counts, lists only counted (cidrsubnet(…, count.index)) and data
// sources (reading them destroys nothing) are not flagged. Local modules
// are checked too and reported on the root call they are reached through.
func checkCountOverList(p *pass) {
	scan := func(wf *model.Workflow, prefix string, at *model.Node) {
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if n.Kind != model.KindResource && n.Kind != model.KindModule {
				continue
			}
			list, text, coll := countedList(&n.Body)
			if coll == nil || !indexedByCount(&n.Body, list) {
				continue
			}
			node := at
			if node == nil {
				node = n
			}
			p.report(countOverListFinding(node, prefix, addr(n), list, listElements(wf, coll, text)))
		}
	}
	scan(p.wf, "", nil)
	for _, c := range p.localModules() {
		scan(c.mod, moduleName(c)+": ", c.root)
	}
}

func countOverListFinding(node *model.Node, prefix, address, list string, elems []string) finding {
	f := finding{
		node:      node,
		attribute: "count",
		message:   fmt.Sprintf("%s%s creates one instance per element of %s by position (count = length(%s))", prefix, address, list, list),
		why: fmt.Sprintf("count keys the instances by index (%s[0], %s[1], …): removing or reordering an element of %s shifts every later element to another index, so Terraform destroys and recreates those instances even though their element didn't change. for_each keys each instance by its element, so the others stay put.",
			address, address, list),
	}
	fix := fmt.Sprintf("Use for_each = toset(%s) and each.value instead of %s[count.index] (for a list of objects, a map keyed by a stable id: for_each = { for x in %s : x.name => x }). Move other count.index uses (cidrsubnet netnums, names) into the values of a map keyed by the element so they stay with it. References change too: %s[0] becomes %s[\"<element>\"] and %s[*] becomes values(%s)[*].",
		list, list, list, address, address, address, address)
	if prefix != "" {
		fix += " The moved blocks go in the module's code."
	}
	if len(elems) == 0 {
		f.fix = fix + fmt.Sprintf(" For instances already deployed, add one moved block per element so Terraform moves them instead of recreating them: moved { from = %s[<index>] to = %s[\"<element at that index>\"] }.", address, address)
		return f
	}
	var sb strings.Builder
	for i, e := range elems {
		fmt.Fprintf(&sb, "\nmoved {\n  from = %s[%d]\n  to   = %s[%s]\n}", address, i, address, hclString(e))
	}
	f.fix = fix + " For instances already deployed, add these moved blocks so Terraform moves them instead of recreating them:" + sb.String()
	return f
}

// countedList returns the list count = length(list) counts: as a
// whitespace-free key (see exprKey), its source text and its expression
// (nil when count is something else). Only variables, locals and literal
// lists count.
func countedList(b *model.Body) (key, text string, coll hclsyntax.Expression) {
	a := getAttr(b, "count")
	if a == nil || !strings.Contains(a.Value.Expr, "length") {
		return "", "", nil
	}
	call, isCall := parse(a.Value).(*hclsyntax.FunctionCallExpr)
	if !isCall || call.Name != "length" || len(call.Args) != 1 || call.ExpandFinal {
		return "", "", nil
	}
	arg := call.Args[0]
	switch e := arg.(type) {
	case *hclsyntax.TupleConsExpr:
	case *hclsyntax.ScopeTraversalExpr:
		if root := e.Traversal.RootName(); len(e.Traversal) < 2 || root != "var" && root != "local" {
			return "", "", nil
		}
	default:
		return "", "", nil
	}
	text = exprText(source(a.Value), arg.Range())
	if key = exprKey(text); key == "" {
		return "", "", nil
	}
	return key, text, arg
}

// indexedByCount reports whether an attribute of b (nested blocks included)
// indexes the list keyed list with count.index: list[count.index] or
// element(list, count.index).
func indexedByCount(b *model.Body, list string) bool {
	found := false
	walkAttrs(b, "", func(_ string, a *model.Attr) {
		if found || !strings.Contains(a.Value.Expr, "count.index") {
			return
		}
		e := parse(a.Value)
		if e == nil {
			return
		}
		src := source(a.Value)
		hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
			switch x := n.(type) {
			case *hclsyntax.IndexExpr:
				found = found || isCountIndex(x.Key) && exprKey(exprText(src, x.Collection.Range())) == list
			case *hclsyntax.FunctionCallExpr:
				found = found || x.Name == "element" && len(x.Args) == 2 && isCountIndex(x.Args[1]) && exprKey(exprText(src, x.Args[0].Range())) == list
			}
			return nil
		})
	})
	return found
}

func isCountIndex(e hclsyntax.Expression) bool {
	t, ok := e.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(t.Traversal) != 2 || t.Traversal.RootName() != "count" {
		return false
	}
	a, ok := t.Traversal[1].(hcl.TraverseAttr)
	return ok && a.Name == "index"
}

// exprText is the source of rng in src ("" when out of range).
func exprText(src string, rng hcl.Range) string {
	if rng.Start.Byte < 0 || rng.End.Byte > len(src) || rng.Start.Byte >= rng.End.Byte {
		return ""
	}
	return src[rng.Start.Byte:rng.End.Byte]
}

// exprKey is an expression's source without whitespace, so the same
// expression matches however it is spaced.
func exprKey(text string) string { return strings.Join(strings.Fields(text), "") }

// listElements returns the constant elements of the counted list (its
// expression coll and source text): a literal list, var.<name>
// with a literal default or local.<name> with a literal value. nil unless
// they are distinct strings (toset would merge duplicates; other values
// need a map).
func listElements(wf *model.Workflow, coll hclsyntax.Expression, text string) []string {
	var v model.Value
	switch e := coll.(type) {
	case *hclsyntax.TupleConsExpr:
		v = model.Value{Expr: text}
	case *hclsyntax.ScopeTraversalExpr:
		step, ok := e.Traversal[len(e.Traversal)-1].(hcl.TraverseAttr)
		if len(e.Traversal) != 2 || !ok {
			return nil
		}
		name := step.Name
		if e.Traversal.RootName() == "var" {
			for i := range wf.Variables {
				if wf.Variables[i].Name == name {
					if d := getAttr(&wf.Variables[i].Body, "default"); d != nil {
						v = d.Value
					}
				}
			}
		} else {
			for i := range wf.Locals {
				if wf.Locals[i].Name == name {
					v = wf.Locals[i].Value
				}
			}
		}
	}
	items, ok := literal(v).([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		s, ok := it.(string)
		if !ok || seen[s] {
			return nil
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// hclString renders s as an HCL string literal (quotes and template
// sequences escaped).
func hclString(s string) string {
	return string(hclwrite.TokensForValue(cty.StringVal(s)).Bytes())
}
