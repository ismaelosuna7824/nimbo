package hclcodec

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// MergeImported carries app-only data from prev into next (a fresh Import of
// edited code): node IDs, positions, labels, parents matched by address;
// environments, active environment, export layout and service name,
// workflow ID/name/description/icon/createdAt and the archived flag.
// Every block stays in the file it was
// written in. Blocks that had no recorded file in prev and are still in the
// file the recommended layout gave them stay unrecorded, and so do new
// blocks written where that layout puts them, unless the layout of next
// would then move them (or other blocks of their file keep a recorded
// file); every other block records its file. resourceFile is the one given
// to Export.
func MergeImported(prev, next *model.Workflow, resourceFile func(string) string) {
	if prev == nil || next == nil {
		return
	}
	next.ID = prev.ID
	next.Name = prev.Name
	next.Description = prev.Description
	next.CreatedAt = prev.CreatedAt
	next.Archived = prev.Archived
	next.Icon = prev.Icon
	next.Challenge = prev.Challenge

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
	next.Layout, next.Service = prev.Layout, prev.Service
	keepAutoFiles(prev, next, resourceFile)
	deriveEdges(next)
}

// MergeEnvironmentsTree completes MergeImported(prev, next) when next is an
// ImportEnvironments of code edited in the environments layout (service
// module and environment roots without their variable files): prev's
// environments stay, with the values the roots' module calls set directly
// (fromTree, next's environments before the merge) on top; environment
// roots prev does not have are ignored (environments are managed in the
// app). The defaults export removed from environment-specific inputs are
// restored, in place.
func MergeEnvironmentsTree(prev, next *model.Workflow, fromTree []model.Environment) {
	envs := make([]model.Environment, len(prev.Environments))
	for i, e := range prev.Environments {
		e.Values = maps.Clone(e.Values)
		if e.Values == nil {
			e.Values = map[string]model.Value{}
		}
		if j := envIndex(fromTree, e.Name); j >= 0 {
			maps.Copy(e.Values, fromTree[j].Values)
		}
		envs[i] = e
	}
	next.Environments = envs
	l, err := newEnvLayout(prev)
	if err != nil {
		return
	}
	for _, v := range l.vars {
		if !v.specific || v.def == "" {
			continue
		}
		for i := range next.Variables {
			nv := &next.Variables[i]
			if nv.Name != v.block.Name || attrExpr(&nv.Body, "default") != "" {
				continue
			}
			at := len(nv.Body.Attrs)
			for j, a := range v.block.Body.Attrs {
				if a.Name == "default" {
					at = min(j, at)
					nv.Body.Attrs = slices.Insert(nv.Body.Attrs, at, model.Attr{Name: "default", Value: a.Value, Comments: a.Comments})
					break
				}
			}
		}
	}
}

// keepAutoFiles clears the recorded files of next that only repeat prev's
// recommended layout (and of new blocks written where that layout puts
// them), so the layout keeps adapting to later changes, as long as every
// block still lands in the file it was written in: a block the layout would
// put elsewhere (e.g. next to the blocks of its class written to another
// file) keeps its file, and so does every block of a file that keeps
// recorded blocks, so that file keeps its source order.
func keepAutoFiles(prev, next *model.Workflow, resourceFile func(string) string) {
	type filePos struct {
		file  string
		order int
	}
	written := map[string]filePos{}
	for _, p := range layout(next, resourceFile) {
		if !p.merged {
			written[p.key] = filePos{*p.file, *p.order}
		}
	}
	before := map[string]*placed{}
	for _, p := range layout(prev, resourceFile) {
		before[p.key] = p
	}
	// A locals or terraform block prev laid out itself, still alone in that
	// file, stays automatic.
	if len(prev.LocalsBlocks) == 0 && before["locals"] != nil && len(next.LocalsBlocks) == 1 && next.LocalsBlocks[0].File == before["locals"].place {
		unpinBlocks(&next.LocalsBlocks, &next.LocalsComments, next.Locals, nil)
	}
	if prev.Terraform != nil && next.Terraform != nil && len(prev.TerraformBlocks) == 0 && before["terraform"] != nil &&
		len(next.TerraformBlocks) == 1 && next.TerraformBlocks[0].File == before["terraform"].place {
		unpinBlocks(&next.TerraformBlocks, &next.Terraform.Comments, next.Terraform.Attrs, next.Terraform.Blocks)
	}
	for _, p := range layout(next, resourceFile) {
		if old, ok := before[p.key]; !p.merged && (!ok || *old.file == "" && old.place == *p.file) {
			*p.file, *p.order = "", 0
		}
	}
	// Record the written file again where the layout disagrees, until it
	// agrees everywhere: every record changes the layout of the rest.
	for changed := true; changed; {
		changed = false
		placed := layout(next, resourceFile)
		recorded := map[string]bool{}
		for _, p := range placed {
			if *p.file != "" {
				recorded[*p.file] = true
			}
		}
		for _, p := range placed {
			w, ok := written[p.key]
			if p.merged || !ok || w.file == "" || *p.file != "" || p.place == w.file && !recorded[w.file] {
				continue
			}
			*p.file, *p.order = w.file, w.order
			changed = true
		}
	}
}

// RenameNode sets the name of a node and rewrites every reference to its old
// address (node bodies, depends_on, variables, outputs, locals, providers,
// `moved` destinations and other top-level blocks; `moved`/`removed` sources
// keep naming historical addresses). A `moved` block left moving an address
// to itself (renaming back to its source) is dropped. It adds no `moved`
// block for the rename itself (see AddMoved) and does not normalize wf.
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
	other := wf.Other[:0]
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
			if to := compactExpr(getAttrValue(&b.Body, "to")); to != "" && to == compactExpr(getAttrValue(&b.Body, "from")) {
				continue
			}
		default:
			rw(&b.Body)
		}
		other = append(other, *b)
	}
	wf.Other = other
	node.Name = newName
	return nil
}

// AddMoved records that the resource or module call at m.From now lives at
// m.To (a `moved` block), e.g. after renaming a deployed node. A block in
// the opposite direction is dropped instead (renaming back), an identical
// one is kept once, and other blocks already moving something to m.To are
// pointed at m.From so they chain (old -> from -> to) instead of declaring
// two sources for one address.
func AddMoved(wf *model.Workflow, m model.MovedAddress) {
	for i := range wf.Other {
		b := &wf.Other[i]
		if b.Type != "moved" || compactExpr(getAttrValue(&b.Body, "to")) != m.To || compactExpr(getAttrValue(&b.Body, "from")) == m.From {
			continue
		}
		for j := range b.Body.Attrs {
			if a := &b.Body.Attrs[j]; a.Name == "to" {
				a.Value = model.Value{Expr: m.From}
			}
		}
	}
	addMoved(wf, m)
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
