package hclcodec

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Layout: which root .tf file every top-level block is written to.
//
// A block with a recorded file (Node.File, NamedBlock.File, Block.File, and
// for locals and terraform blocks one BlockPart per source block, named by
// the File/Order of its items) stays there: Import records the source file
// of every block, so an imported project keeps its own layout. A block
// without one gets Nimbo's recommended layout, recomputed on every export:
//
//   - terraform -> versions.tf, provider -> providers.tf,
//     variable -> variables.tf, locals -> locals.tf, output -> outputs.tf;
//   - data sources -> data.tf, resources -> the file of their catalog
//     category (network.tf, compute.tf, storage.tf, ...; the resourceFile
//     callback), module calls and other blocks (moved, import, check, ...)
//     -> main.tf;
//   - with at most smallProject resources, data sources and resources all go
//     to main.tf: small projects are not split.
//
// Next to recorded files (an imported project, or blocks the user moved), a
// new block joins the file that already holds most recorded blocks of its
// class (resources of the same category, variables, data sources, ...);
// ties go to the first file name. New locals and terraform items join the
// last recorded block of the file holding most of them.
//
// Inside a file, blocks with a source position (Order, recorded by Import)
// come first in source order, whatever their kind. The others (new blocks,
// blocks moved into the file) follow by kind (terraform, provider,
// variable, locals, data/resource/module, other, output), in workflow
// order except data sources, resources and module calls: dependency order,
// then address.

// smallProject is the largest number of resources kept in a single main.tf.
const smallProject = 5

// Recommended file names.
const (
	versionsFile  = "versions.tf"
	providersFile = "providers.tf"
	variablesFile = "variables.tf"
	localsFile    = "locals.tf"
	outputsFile   = "outputs.tf"
	dataFile      = "data.tf"
	mainFile      = "main.tf"
)

// Block kinds in file order.
const (
	rankTerraform = iota
	rankProvider
	rankVariable
	rankLocals
	rankNode
	rankOther
	rankOutput
)

// placed is one top-level block of a workflow and the file it goes to.
type placed struct {
	rank  int
	index int    // index in its workflow slice
	class string // blocks of one class gather in the same file
	key   string // identity across re-imports (MergeImported)
	file  *string
	order *int   // source position in *file (0 = none)
	auto  string // recommended file when *file is ""
	place string // resulting file
	cms   **model.Comments
	node  *model.Node // nodes only
	// merged marks a locals or terraform block (one per BlockPart, plus one
	// for the items without a part); pin records a file and position.
	merged bool
	pin    func(file string, order int)

	// What Export writes.
	owner, typ string
	labels     []string
	body       *model.Body
}

// layout places every top-level block of wf. resourceFile names the file
// of a resource type's category ("network.tf"); nil or "" means main.tf.
func layout(wf *model.Workflow, resourceFile func(string) string) []*placed {
	resources := 0
	for i := range wf.Nodes {
		if wf.Nodes[i].Kind == model.KindResource {
			resources++
		}
	}
	small := resources <= smallProject

	var out []*placed
	add := func(p *placed) {
		if p.pin == nil {
			p.pin = func(file string, order int) { *p.file, *p.order = file, order }
		}
		out = append(out, p)
	}
	if wf.Terraform != nil {
		for _, p := range splitBlocks(rankTerraform, "terraform", versionsFile, &wf.TerraformBlocks, &wf.Terraform.Comments, wf.Terraform.Attrs, wf.Terraform.Blocks, true) {
			add(p)
		}
	}
	providerSeen := map[string]int{}
	for i := range wf.Providers {
		p := &wf.Providers[i]
		providerSeen[p.Name]++
		add(&placed{rank: rankProvider, index: i, class: "provider", key: fmt.Sprintf("provider.%s#%d", p.Name, providerSeen[p.Name]),
			file: &p.File, order: &p.Order, auto: providersFile, cms: &p.Body.Comments,
			owner: "provider " + p.Name, typ: "provider", labels: []string{p.Name}, body: &p.Body})
	}
	for i := range wf.Variables {
		v := &wf.Variables[i]
		add(&placed{rank: rankVariable, index: i, class: "variable", key: "var." + v.Name, file: &v.File, order: &v.Order, auto: variablesFile,
			cms: &v.Body.Comments, owner: "variable " + v.Name, typ: "variable", labels: []string{v.Name}, body: &v.Body})
	}
	for _, p := range splitBlocks(rankLocals, "locals", localsFile, &wf.LocalsBlocks, &wf.LocalsComments, wf.Locals, nil, false) {
		add(p)
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		p := &placed{rank: rankNode, index: i, key: Address(n), file: &n.File, order: &n.Order, cms: &n.Body.Comments, node: n,
			owner: Address(n), typ: string(n.Kind), labels: []string{n.Type, n.Name}, body: &n.Body}
		switch n.Kind {
		case model.KindData:
			p.class, p.auto = "data", dataFile
		case model.KindModule:
			p.class, p.auto, p.typ, p.labels = "module", mainFile, "module", []string{n.Name}
		default:
			f := ""
			if resourceFile != nil {
				f = resourceFile(n.Type)
			}
			if f == "" {
				f = mainFile
			}
			p.class, p.auto = "resource:"+f, f
		}
		if small && n.Kind != model.KindModule {
			p.auto = mainFile
		}
		add(p)
	}
	otherSeen := map[string]int{}
	for i := range wf.Other {
		b := &wf.Other[i]
		id := strings.Join(append([]string{b.Type}, b.Labels...), ".")
		otherSeen[id]++
		add(&placed{rank: rankOther, index: i, class: "other:" + b.Type, key: fmt.Sprintf("%s#%d", id, otherSeen[id]),
			file: &b.File, order: &b.Order, auto: mainFile, cms: &b.Body.Comments, owner: b.Type, typ: b.Type, labels: b.Labels, body: &b.Body})
	}
	for i := range wf.Outputs {
		o := &wf.Outputs[i]
		add(&placed{rank: rankOutput, index: i, class: "output", key: "output." + o.Name, file: &o.File, order: &o.Order, auto: outputsFile,
			cms: &o.Body.Comments, owner: "output " + o.Name, typ: "output", labels: []string{o.Name}, body: &o.Body})
	}

	// Recorded files by class, for new blocks next to them.
	counts := map[string]map[string]int{}
	for _, p := range out {
		if *p.file == "" {
			continue
		}
		if counts[p.class] == nil {
			counts[p.class] = map[string]int{}
		}
		counts[p.class][*p.file]++
	}
	for _, p := range out {
		switch {
		case *p.file != "":
			p.place = *p.file
		case len(counts[p.class]) > 0:
			p.place = majority(counts[p.class])
		default:
			p.place = p.auto
		}
	}
	return out
}

// splitBlocks places the locals or terraform blocks of a workflow: one per
// recorded part (BlockPart) with the items naming it, in list order. Items
// without a part join the last part of the file holding most parts, or get
// a block of their own (autoCms are its comments) in the recommended file
// when there is no part or those comments must be kept. always writes the
// block even when empty (a terraform block).
func splitBlocks(rank int, kind, auto string, parts *[]model.BlockPart, autoCms **model.Comments, attrs []model.Attr, blocks []model.Block, always bool) []*placed {
	key := func(file string, order int) string { return fmt.Sprintf("%s#%d", file, order) }
	partOf := map[string]int{}
	for j, p := range *parts {
		partOf[key(p.File, p.Order)] = j
	}
	type items struct{ attrs, blocks []int }
	byPart := make([]items, len(*parts))
	var free items
	for i := range attrs {
		if j, ok := partOf[key(attrs[i].File, attrs[i].Order)]; ok && attrs[i].File != "" {
			byPart[j].attrs = append(byPart[j].attrs, i)
		} else {
			free.attrs = append(free.attrs, i)
		}
	}
	for i := range blocks {
		if j, ok := partOf[key(blocks[i].File, blocks[i].Order)]; ok && blocks[i].File != "" {
			byPart[j].blocks = append(byPart[j].blocks, i)
		} else {
			free.blocks = append(free.blocks, i)
		}
	}
	hasFree := len(free.attrs) > 0 || len(free.blocks) > 0
	ownBlock := len(*parts) == 0 || !emptyComments(*autoCms)
	if hasFree && !ownBlock {
		// Join the last part of the file holding most parts.
		counts := map[string]int{}
		for _, p := range *parts {
			counts[p.File]++
		}
		file, target := majority(counts), -1
		for j, p := range *parts {
			if p.File == file && (target < 0 || p.Order >= (*parts)[target].Order) {
				target = j
			}
		}
		byPart[target].attrs = append(byPart[target].attrs, free.attrs...)
		byPart[target].blocks = append(byPart[target].blocks, free.blocks...)
		free = items{}
	}
	entry := func(it items, cms **model.Comments) (*model.Body, func(file string, order int)) {
		body := &model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}, Comments: *cms}
		for _, i := range it.attrs {
			body.Attrs = append(body.Attrs, attrs[i])
		}
		for _, i := range it.blocks {
			body.Blocks = append(body.Blocks, blocks[i])
		}
		return body, func(file string, order int) {
			for _, i := range it.attrs {
				attrs[i].File, attrs[i].Order = file, order
			}
			for _, i := range it.blocks {
				blocks[i].File, blocks[i].Order = file, order
			}
		}
	}
	var out []*placed
	for j := range *parts {
		p := &(*parts)[j]
		it := byPart[j]
		if len(it.attrs) == 0 && len(it.blocks) == 0 && emptyComments(p.Comments) {
			continue
		}
		body, pinItems := entry(it, &p.Comments)
		out = append(out, &placed{rank: rank, index: j, class: kind, key: kind + "@" + key(p.File, p.Order), merged: true,
			file: &p.File, order: &p.Order, auto: auto, cms: &p.Comments, owner: kind, typ: kind, body: body,
			pin: func(file string, order int) {
				pinItems(file, order)
				(*parts)[j].File, (*parts)[j].Order = file, order
			}})
	}
	if hasFree && !ownBlock || !(hasFree || !emptyComments(*autoCms) || always && len(*parts) == 0) {
		return out
	}
	body, pinItems := entry(free, autoCms)
	file, order := "", 0
	return append(out, &placed{rank: rank, index: len(*parts), class: kind, key: kind, merged: true,
		file: &file, order: &order, auto: auto, cms: autoCms, owner: kind, typ: kind, body: body,
		pin: func(f string, o int) {
			pinItems(f, o)
			*parts = append(*parts, model.BlockPart{File: f, Order: o, Comments: *autoCms})
			*autoCms = nil
		}})
}

// unpinBlocks forgets the parts of the locals or terraform blocks: their
// items and comments become one block in the recommended layout.
func unpinBlocks(parts *[]model.BlockPart, autoCms **model.Comments, attrs []model.Attr, blocks []model.Block) {
	for i := range attrs {
		attrs[i].File, attrs[i].Order = "", 0
	}
	for i := range blocks {
		blocks[i].File, blocks[i].Order = "", 0
	}
	var merged *model.Comments
	foldComments(&merged, *autoCms)
	for _, p := range *parts {
		foldComments(&merged, p.Comments)
	}
	*autoCms = merged
	*parts = nil
}

// majority returns the key with the highest count; ties go to the smallest key.
func majority(m map[string]int) string {
	best, n := "", -1
	for k, c := range m {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

// groupFiles groups placed blocks by file, each file in writing order.
func groupFiles(wf *model.Workflow, blocks []*placed) map[string][]*placed {
	byFile := map[string][]*placed{}
	for _, p := range blocks {
		byFile[p.place] = append(byFile[p.place], p)
	}
	for name, list := range byFile {
		byFile[name] = fileOrder(wf, list)
	}
	return byFile
}

// fileOrder sorts the blocks of one file: first the blocks with a source
// position in it (Order), in source order; then the others (new blocks and
// blocks moved into the file) by kind, in workflow order, except data
// sources, resources and module calls: dependency order, then address.
func fileOrder(wf *model.Workflow, list []*placed) []*placed {
	sourced := func(p *placed) bool { return *p.file != "" && *p.order > 0 }
	slices.SortStableFunc(list, func(a, b *placed) int {
		if sa, sb := sourced(a), sourced(b); sa != sb {
			if sa {
				return -1
			}
			return 1
		} else if sa && *a.order != *b.order {
			return *a.order - *b.order
		}
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return a.index - b.index
	})
	start, end := -1, -1
	for i, p := range list {
		if p.rank == rankNode && !sourced(p) {
			if start < 0 {
				start = i
			}
			end = i + 1
		}
	}
	if start >= 0 {
		dependencyOrder(wf, list[start:end])
	}
	return list
}

// dependencyOrder sorts nodes so that a node comes after the nodes it
// references (wf.Edges); among the ready ones, data sources first, then by
// address. Cycles are broken by the same order.
func dependencyOrder(wf *model.Workflow, list []*placed) {
	byID := make(map[string]*placed, len(list))
	for _, p := range list {
		byID[p.node.ID] = p
	}
	deps := map[*placed]map[*placed]bool{}
	users := map[*placed][]*placed{}
	for _, e := range wf.Edges {
		src, dst := byID[e.Source], byID[e.Target]
		if src == nil || dst == nil || src == dst || deps[dst][src] {
			continue
		}
		if deps[dst] == nil {
			deps[dst] = map[*placed]bool{}
		}
		deps[dst][src] = true
		users[src] = append(users[src], dst)
	}
	less := func(a, b *placed) int {
		ka, kb := nodeKindRank(a.node.Kind), nodeKindRank(b.node.Kind)
		if ka != kb {
			return ka - kb
		}
		return cmp.Compare(a.owner, b.owner)
	}
	remaining := slices.Clone(list)
	slices.SortFunc(remaining, less)
	pending := map[*placed]int{}
	for _, p := range remaining {
		pending[p] = len(deps[p])
	}
	out := list[:0]
	done := map[*placed]bool{}
	for len(out) < len(list) {
		next := -1
		for i, p := range remaining {
			if !done[p] && pending[p] == 0 {
				next = i
				break
			}
		}
		if next < 0 { // cycle: take the first remaining block
			for i, p := range remaining {
				if !done[p] {
					next = i
					break
				}
			}
		}
		p := remaining[next]
		done[p] = true
		out = append(out, p)
		for _, u := range users[p] {
			pending[u]--
		}
	}
}

func nodeKindRank(k model.NodeKind) int {
	switch k {
	case model.KindData:
		return 0
	case model.KindResource:
		return 1
	}
	return 2
}

// Placement is the file every top-level block of a workflow is written to.
// The slices are index-aligned with the workflow's; Terraform and Locals are
// "" when the workflow has no such block.
type Placement struct {
	Terraform string
	Locals    string
	Providers []string
	Variables []string
	Outputs   []string
	Nodes     []string
	Other     []string
}

// Layout returns where Export writes every top-level block of wf. Pass the
// same resourceFile as ExportOptions.ResourceFile.
func Layout(wf *model.Workflow, resourceFile func(resourceType string) string) Placement {
	pl := Placement{
		Providers: make([]string, len(wf.Providers)),
		Variables: make([]string, len(wf.Variables)),
		Outputs:   make([]string, len(wf.Outputs)),
		Nodes:     make([]string, len(wf.Nodes)),
		Other:     make([]string, len(wf.Other)),
	}
	for _, p := range layout(wf, resourceFile) {
		switch p.rank {
		case rankTerraform:
			pl.Terraform = p.place
		case rankProvider:
			pl.Providers[p.index] = p.place
		case rankVariable:
			pl.Variables[p.index] = p.place
		case rankLocals:
			pl.Locals = p.place
		case rankNode:
			pl.Nodes[p.index] = p.place
		case rankOther:
			pl.Other[p.index] = p.place
		case rankOutput:
			pl.Outputs[p.index] = p.place
		}
	}
	return pl
}

// NodeFiles maps node ID to the file Export writes the node to.
func NodeFiles(wf *model.Workflow, resourceFile func(resourceType string) string) map[string]string {
	out := make(map[string]string, len(wf.Nodes))
	for i, f := range Layout(wf, resourceFile).Nodes {
		out[wf.Nodes[i].ID] = f
	}
	return out
}

// PinFiles records where Export currently writes every block (file and
// position), as an import of the export would: later changes to the
// workflow no longer move them.
func PinFiles(wf *model.Workflow, resourceFile func(resourceType string) string) {
	byFile := groupFiles(wf, layout(wf, resourceFile))
	for name, blocks := range byFile {
		for i, p := range blocks {
			p.pin(name, i+1)
		}
	}
}

// UnpinFiles forgets every recorded file, so the workflow gets the
// recommended layout (templates and starters): locals and terraform blocks
// become one each. Nodes are reordered as Export writes them, and
// end-of-file comments move to the last block of their new file, so
// importing the export gives back the same workflow.
func UnpinFiles(wf *model.Workflow, resourceFile func(resourceType string) string) {
	for _, p := range layout(wf, resourceFile) {
		if !p.merged {
			*p.file, *p.order = "", 0
		}
	}
	unpinBlocks(&wf.LocalsBlocks, &wf.LocalsComments, wf.Locals, nil)
	if wf.Terraform != nil {
		unpinBlocks(&wf.TerraformBlocks, &wf.Terraform.Comments, wf.Terraform.Attrs, wf.Terraform.Blocks)
	}
	byFile := groupFiles(wf, layout(wf, resourceFile))
	names := keysOf(byFile)
	nodes := make([]model.Node, 0, len(wf.Nodes))
	for _, name := range names {
		for _, p := range byFile[name] {
			if p.node != nil {
				nodes = append(nodes, *p.node)
			}
		}
	}
	wf.Nodes = nodes
	gatherAfter(wf, resourceFile)
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ValidFileName reports whether name can hold top-level blocks: a root-level
// *.tf file name that Terraform loads as a normal configuration file (not
// hidden, not an override file).
func ValidFileName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("file name is empty")
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("file %q must be a file name in the project root, without folders", name)
	case !strings.HasSuffix(name, ".tf") || name == ".tf":
		return fmt.Errorf("file %q must end in .tf", name)
	case strings.HasPrefix(name, ".") || strings.HasPrefix(name, "#") || strings.HasSuffix(name, "~"):
		return fmt.Errorf("terraform ignores file %q", name)
	case IsOverrideFile(name):
		return fmt.Errorf("file %q is an override file; Terraform merges it into other blocks", name)
	case strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) }) >= 0:
		return fmt.Errorf("file %q contains control characters", name)
	case path.Clean(name) != name:
		return fmt.Errorf("file %q is not a clean file name", name)
	}
	return nil
}

// fileDiags reports recorded files that cannot be written.
func fileDiags(wf *model.Workflow) []model.Diagnostic {
	var out []model.Diagnostic
	for _, p := range layout(wf, nil) {
		if *p.file == "" {
			continue
		}
		if err := ValidFileName(*p.file); err != nil {
			d := model.Diagnostic{Severity: model.SeverityError, Code: "invalid_file", Message: err.Error(), Detail: p.owner}
			if p.node != nil {
				d.NodeID = p.node.ID
			}
			out = append(out, d)
		}
	}
	return out
}
