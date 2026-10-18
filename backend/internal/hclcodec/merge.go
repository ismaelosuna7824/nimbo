package hclcodec

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// MergeImported carries app-only data from prev into next (a fresh Import of
// edited code): node IDs, positions, labels, parents matched by address;
// environments, active environment, workflow ID/name/description/icon/
// createdAt and the archived flag.
func MergeImported(prev, next *model.Workflow) {
	if prev == nil || next == nil {
		return
	}
	next.ID = prev.ID
	next.Name = prev.Name
	next.Description = prev.Description
	next.CreatedAt = prev.CreatedAt
	next.Archived = prev.Archived
	next.Icon = prev.Icon

	prevByAddr := map[string]*model.Node{}
	for i := range prev.Nodes {
		n := &prev.Nodes[i]
		if _, ok := prevByAddr[Address(n)]; !ok {
			prevByAddr[Address(n)] = n
		}
	}
	used := map[string]bool{}
	matched := make([]bool, len(next.Nodes))
	for i := range next.Nodes {
		n := &next.Nodes[i]
		p, ok := prevByAddr[Address(n)]
		if !ok || used[p.ID] {
			continue
		}
		n.ID = p.ID
		n.Position = p.Position
		n.Positioned = p.Positioned
		n.Label = p.Label
		n.Parent = p.Parent
		used[n.ID] = true
		matched[i] = true
	}
	for i := range next.Nodes {
		if matched[i] {
			continue
		}
		n := &next.Nodes[i]
		n.ID = uniqueID(Address(n), used)
		used[n.ID] = true
	}
	for i := range next.Nodes {
		if p := next.Nodes[i].Parent; p != "" && !used[p] {
			next.Nodes[i].Parent = ""
		}
	}

	envs := append([]model.Environment(nil), prev.Environments...)
	for _, e := range next.Environments {
		if envIndex(envs, e.Name) < 0 {
			envs = append(envs, e)
		}
	}
	if envs == nil {
		envs = []model.Environment{}
	}
	next.Environments = envs
	if prev.ActiveEnvironment != "" {
		next.ActiveEnvironment = prev.ActiveEnvironment
	}
	deriveEdges(next)
}

// RenameNode sets the name of a node and rewrites every reference to its old
// address (node bodies, depends_on, variables, outputs, locals, providers,
// `moved` destinations and other top-level blocks; `moved`/`removed` sources
// keep naming historical addresses). It does not normalize wf.
func RenameNode(wf *model.Workflow, nodeID, newName string) error {
	var node *model.Node
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == nodeID {
			node = &wf.Nodes[i]
			break
		}
	}
	if node == nil {
		return fmt.Errorf("node %q not found", nodeID)
	}
	if !hclsyntax.ValidIdentifier(newName) {
		return fmt.Errorf("invalid name %q: use letters, digits, underscores and dashes, starting with a letter or underscore", newName)
	}
	if newName == node.Name {
		return nil
	}
	renamed := *node
	renamed.Name = newName
	newAddr := Address(&renamed)
	for i := range wf.Nodes {
		if Address(&wf.Nodes[i]) == newAddr {
			return fmt.Errorf("address %s is already used", newAddr)
		}
	}

	rw := func(b *model.Body) { renameInBody(b, node, newName) }
	for i := range wf.Nodes {
		rw(&wf.Nodes[i].Body)
	}
	for i := range wf.Variables {
		rw(&wf.Variables[i].Body)
	}
	for i := range wf.Outputs {
		rw(&wf.Outputs[i].Body)
	}
	for i := range wf.Providers {
		rw(&wf.Providers[i].Body)
	}
	rw(&model.Body{Attrs: wf.Locals})
	for i := range wf.Other {
		b := &wf.Other[i]
		switch b.Type {
		case "removed":
		case "moved":
			for j := range b.Body.Attrs {
				if a := &b.Body.Attrs[j]; a.Name == "to" {
					a.Value.Expr = renameInExpr(a.Value.Expr, node, newName)
				}
			}
		default:
			rw(&b.Body)
		}
	}
	node.Name = newName
	return nil
}

func renameInBody(b *model.Body, target *model.Node, newName string) {
	for i := range b.Attrs {
		v := &b.Attrs[i].Value
		if v.Expr != "" {
			v.Expr = renameInExpr(v.Expr, target, newName)
		}
	}
	for i := range b.Blocks {
		renameInBody(&b.Blocks[i].Body, target, newName)
	}
}

// renameInExpr replaces the name step of every traversal addressing target.
// Unparseable expressions are returned unchanged.
func renameInExpr(expr string, target *model.Node, newName string) string {
	e, err := parseExpr(expr)
	if err != nil {
		return expr
	}
	var spans []hcl.Range
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		t, ok := n.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			return nil
		}
		if r, ok := nameStep(t.Traversal, target); ok {
			spans = append(spans, r)
		}
		return nil
	})
	if len(spans) == 0 {
		return expr
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Byte > spans[j].Start.Byte })
	out := []byte(expr)
	for _, r := range spans {
		// The step range covers ".name".
		if r.End.Byte > len(out) || string(out[r.Start.Byte:r.End.Byte]) != "."+target.Name {
			continue
		}
		out = append(out[:r.Start.Byte+1], append([]byte(newName), out[r.End.Byte:]...)...)
	}
	return string(out)
}

// nameStep returns the source range of the step holding target's name when
// tr addresses target.
func nameStep(tr hcl.Traversal, target *model.Node) (hcl.Range, bool) {
	attrAt := func(i int) (hcl.TraverseAttr, bool) {
		if i >= len(tr) {
			return hcl.TraverseAttr{}, false
		}
		a, ok := tr[i].(hcl.TraverseAttr)
		return a, ok
	}
	root := tr.RootName()
	switch target.Kind {
	case model.KindData:
		typ, ok1 := attrAt(1)
		name, ok2 := attrAt(2)
		if root == "data" && ok1 && ok2 && typ.Name == target.Type && name.Name == target.Name {
			return name.SrcRange, true
		}
	case model.KindModule:
		if name, ok := attrAt(1); root == "module" && ok && name.Name == target.Name {
			return name.SrcRange, true
		}
	default:
		if name, ok := attrAt(1); root == target.Type && ok && name.Name == target.Name {
			return name.SrcRange, true
		}
	}
	return hcl.Range{}, false
}
