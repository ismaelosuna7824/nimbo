package hclcodec

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AssetMove is a workflow file or folder (slash paths relative to the
// workflow root) that moves with blocks between the root and a local
// module, so the blocks keep reading it through ${path.module}. The caller
// moves it when it exists in the workflow's assets.
type AssetMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// pathRef is a path.module or path.root traversal in an expression and the
// file or folder it reads ("" when unknown).
type pathRef struct {
	root       string // "module" or "root"
	start, end int    // byte span of the traversal
	unit       string
}

// pathRefs lists the path.module / path.root traversals of e that start a
// string template ("${path.module}/services/api/src"), with the asset unit
// the template reads: the file or folder named by the literal that follows
// (up to the last complete segment before another interpolation), or
// services/<svc> for anything under a service folder, so a service moves as
// a whole. Other uses (path.module alone, as a function argument) are not
// listed: the file they read is unknown.
func pathRefs(e hclsyntax.Expression) []pathRef {
	var out []pathRef
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		tmpl, ok := n.(*hclsyntax.TemplateExpr)
		if !ok {
			return nil
		}
		for i, part := range tmpl.Parts {
			t, ok := part.(*hclsyntax.ScopeTraversalExpr)
			if !ok || len(t.Traversal) != 2 || t.Traversal.RootName() != "path" || i+1 >= len(tmpl.Parts) {
				continue
			}
			step, ok := t.Traversal[1].(hcl.TraverseAttr)
			if !ok || step.Name != "module" && step.Name != "root" {
				continue
			}
			lit, ok := tmpl.Parts[i+1].(*hclsyntax.LiteralValueExpr)
			if !ok || lit.Val.Type() != cty.String || lit.Val.IsNull() {
				continue
			}
			r := t.Traversal.SourceRange()
			out = append(out, pathRef{root: step.Name, start: r.Start.Byte, end: r.End.Byte,
				unit: assetUnit(lit.Val.AsString(), i+2 == len(tmpl.Parts))})
		}
		return nil
	})
	return out
}

// assetUnit is the workflow file or folder a path template reads after
// ${path.module}: lit is the literal that follows, complete when the
// template ends with it. "" when it can't be told or names a module folder.
func assetUnit(lit string, complete bool) string {
	rest, ok := strings.CutPrefix(lit, "/")
	if !ok {
		return ""
	}
	if !complete {
		i := strings.LastIndex(rest, "/")
		if i < 0 {
			return ""
		}
		rest = rest[:i]
	}
	if rest == "" {
		return ""
	}
	clean := path.Clean(rest)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) ||
		clean == ModulesDir || strings.HasPrefix(clean, ModulesDir+"/") {
		return ""
	}
	if parts := strings.SplitN(clean, "/", 3); len(parts) >= 2 && parts[0] == "services" {
		return parts[0] + "/" + parts[1]
	}
	return clean
}

// overlaps reports whether one of two slash paths contains the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func overlapsAny(unit string, units []string) bool {
	return slices.ContainsFunc(units, func(u string) bool { return overlaps(unit, u) })
}

// rootPathUnits lists the asset units the root reads through path.module
// or path.root, leaving out the nodes skip reports (those that move).
func rootPathUnits(wf *model.Workflow, skip func(n *model.Node) bool) []string {
	var out []string
	visit := func(v *model.Value) {
		if v.Expr == "" {
			return
		}
		e, err := parseExpr(v.Expr)
		if err != nil {
			return
		}
		for _, r := range pathRefs(e) {
			if r.unit != "" {
				out = append(out, r.unit)
			}
		}
	}
	var body func(b *model.Body)
	body = func(b *model.Body) {
		for i := range b.Attrs {
			visit(&b.Attrs[i].Value)
		}
		for i := range b.Blocks {
			body(&b.Blocks[i].Body)
		}
	}
	for i := range wf.Nodes {
		if !skip(&wf.Nodes[i]) {
			body(&wf.Nodes[i].Body)
		}
	}
	for i := range wf.Variables {
		body(&wf.Variables[i].Body)
	}
	for i := range wf.Outputs {
		body(&wf.Outputs[i].Body)
	}
	for i := range wf.Locals {
		visit(&wf.Locals[i].Value)
	}
	for i := range wf.Providers {
		body(&wf.Providers[i].Body)
	}
	for i := range wf.Other {
		body(&wf.Other[i].Body)
	}
	return out
}

// assetMoves renders the units (deduplicated, nested ones folded into the
// unit containing them) as moves from fromDir to toDir ("" = the root).
func assetMoves(units map[string]bool, fromDir, toDir string) []AssetMove {
	keys := slices.Sorted(maps.Keys(units))
	var out []AssetMove
	var kept []string
	for _, u := range keys {
		if slices.ContainsFunc(kept, func(k string) bool { return strings.HasPrefix(u, k+"/") }) {
			continue
		}
		kept = append(kept, u)
		out = append(out, AssetMove{From: path.Join(fromDir, u), To: path.Join(toDir, u)})
	}
	return out
}
