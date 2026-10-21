// Package codeintel backs the code editor's Terraform language features
// that need the engine: problems of a file while it is edited (syntax
// errors from the HCL parser, schema problems from the catalog, references
// to undeclared names and attributes).
package codeintel

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Problem codes of Check besides the catalog's (catalog.CodeUnknownType,
// CodeUnknownAttribute, ...).
const (
	CodeSyntax            = "syntax"
	CodeUnknownVariable   = "unknown_variable"
	CodeUnknownLocal      = "unknown_local"
	CodeUnknownReference  = "unknown_reference"
	CodeUnknownOutput     = "unknown_output"
	CodeUnsupportedAttrib = "unsupported_attribute"
)

// Check reports the problems of the file at file (a workflow path such as
// "main.tf" or "modules/network/main.tf") while it is edited. files holds
// every .tf file of the workflow by workflow path, unsaved edits applied.
// Syntax errors are errors and end the check. Otherwise the file's
// resource, data and provider blocks are checked against their schemas,
// and its references against what the file's directory declares (local
// modules' outputs included): warnings, since the code may be half written.
// Reference checks are skipped while another file of the directory does not
// parse (its declarations are unknown).
func Check(cat *catalog.Catalog, files map[string][]byte, file string) []model.CodeProblem {
	src := files[file]
	f, diags := hclsyntax.ParseConfig(src, path.Base(file), hcl.InitialPos)
	if diags.HasErrors() {
		var out []model.CodeProblem
		for _, d := range diags {
			if d.Severity != hcl.DiagError {
				continue
			}
			rng := hcl.Range{Start: hcl.InitialPos, End: hcl.InitialPos}
			if d.Subject != nil {
				rng = *d.Subject
			}
			out = append(out, problem(model.SeverityError, CodeSyntax, d.Summary, d.Detail, rng))
		}
		return out
	}
	body := f.Body.(*hclsyntax.Body)
	out := schemaProblems(cat, body, path.Base(file), src)
	if decl, ok := declarations(files, file, body); ok {
		out = append(out, referenceProblems(cat, body, decl)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Column < out[j].Column
	})
	return out
}

func problem(sev model.Severity, code, msg, detail string, r hcl.Range) model.CodeProblem {
	return model.CodeProblem{
		Severity: sev, Code: code, Message: msg, Detail: detail,
		Line: r.Start.Line, Column: r.Start.Column, EndLine: r.End.Line, EndColumn: r.End.Column,
	}
}

// ---------------------------------------------------------------------------
// Schema problems: catalog.Validate on the file's blocks, located in source.

func schemaProblems(cat *catalog.Catalog, body *hclsyntax.Body, name string, src []byte) []model.CodeProblem {
	wf, _, err := hclcodec.Import("check", map[string][]byte{name: src})
	if err != nil {
		return nil
	}
	for i := range wf.Nodes {
		wf.Nodes[i].ID = hclcodec.Address(&wf.Nodes[i])
	}
	var out []model.CodeProblem
	for _, d := range cat.Validate(wf) {
		if d.Code == catalog.CodeFlociUnsupported {
			continue
		}
		if blk := findNodeBlock(body, d.NodeID); blk != nil {
			out = append(out, problem(model.SeverityWarning, d.Code, d.Message, d.Detail, locate(blk, d.Code, d.Attribute)))
		}
	}
	// Provider blocks pair with wf.Providers in source order per name.
	seen := map[string]int{}
	for i := range wf.Providers {
		p := &wf.Providers[i]
		blk := nthBlock(body, "provider", p.Name, seen[p.Name])
		seen[p.Name]++
		if blk == nil {
			continue
		}
		for _, d := range cat.ValidateProvider(p.Name, &p.Body) {
			out = append(out, problem(model.SeverityWarning, d.Code, d.Message, d.Detail, locate(blk, d.Code, d.Attribute)))
		}
	}
	return out
}

// findNodeBlock finds the resource/data/module block of an address.
func findNodeBlock(body *hclsyntax.Body, addr string) *hclsyntax.Block {
	kind, rest := "resource", addr
	if r, ok := strings.CutPrefix(addr, "data."); ok {
		kind, rest = "data", r
	}
	typ, name, ok := strings.Cut(rest, ".")
	if !ok {
		return nil
	}
	for _, b := range body.Blocks {
		if b.Type == kind && len(b.Labels) == 2 && b.Labels[0] == typ && b.Labels[1] == name {
			return b
		}
	}
	return nil
}

func nthBlock(body *hclsyntax.Body, typ, label string, n int) *hclsyntax.Block {
	for _, b := range body.Blocks {
		if b.Type == typ && len(b.Labels) > 0 && b.Labels[0] == label {
			if n == 0 {
				return b
			}
			n--
		}
	}
	return nil
}

// locate maps a Validate attribute path ("a", "b[1].c", "d.e") inside blk
// to the source range to underline: the attribute's name, the block's type
// for unknown blocks, the enclosing block's header for missing ones.
func locate(blk *hclsyntax.Block, code, attrPath string) hcl.Range {
	header := hcl.RangeBetween(blk.TypeRange, blk.OpenBraceRange)
	if code == catalog.CodeUnknownType && len(blk.Labels) > 0 {
		return blk.LabelRanges[0]
	}
	if attrPath == "" {
		return header
	}
	segs := strings.Split(attrPath, ".")
	cur := blk
	for i, seg := range segs {
		name, idx := seg, 0
		if open := strings.IndexByte(seg, '['); open >= 0 && strings.HasSuffix(seg, "]") {
			name = seg[:open]
			idx, _ = strconv.Atoi(seg[open+1 : len(seg)-1])
		}
		last := i == len(segs)-1
		if last {
			if a, ok := cur.Body.Attributes[name]; ok {
				return a.NameRange
			}
			if code == catalog.CodeUnknownBlock {
				for _, b := range cur.Body.Blocks {
					if b.Type == name || b.Type == "dynamic" && len(b.Labels) > 0 && b.Labels[0] == name {
						return b.TypeRange
					}
				}
			}
			if code == catalog.CodeTooManyBlocks {
				n := 0
				for _, b := range cur.Body.Blocks {
					if b.Type == name {
						n++
						if n > 1 {
							return b.TypeRange
						}
					}
				}
			}
			return hcl.RangeBetween(cur.TypeRange, cur.OpenBraceRange)
		}
		var next *hclsyntax.Block
		n := 0
		for _, b := range cur.Body.Blocks {
			if b.Type == name {
				if n == idx {
					next = b
					break
				}
				n++
			}
		}
		if next == nil {
			return hcl.RangeBetween(cur.TypeRange, cur.OpenBraceRange)
		}
		cur = next
	}
	return header
}

// ---------------------------------------------------------------------------
// Reference problems.

// decls are the names a directory declares.
type decls struct {
	vars, locals map[string]bool
	resources    map[string]string // "aws_s3_bucket.b" / "data.aws_x.y" -> type
	modules      map[string]string // call name -> constant source ("" when not constant)
	dir          string            // workflow directory ("" for the root)
	files        map[string][]byte // every workflow .tf file
}

// declarations collects what file's directory declares; false when another
// file there does not parse.
func declarations(files map[string][]byte, file string, own *hclsyntax.Body) (*decls, bool) {
	dir := dirOf(file)
	d := &decls{
		vars: map[string]bool{}, locals: map[string]bool{},
		resources: map[string]string{}, modules: map[string]string{},
		dir: dir, files: files,
	}
	for p, src := range files {
		if dirOf(p) != dir || !strings.HasSuffix(p, ".tf") {
			continue
		}
		body := own
		if p != file {
			f, diags := hclsyntax.ParseConfig(src, p, hcl.InitialPos)
			if diags.HasErrors() {
				return nil, false
			}
			body = f.Body.(*hclsyntax.Body)
		}
		d.add(body)
	}
	return d, true
}

func (d *decls) add(body *hclsyntax.Body) {
	for _, b := range body.Blocks {
		switch {
		case b.Type == "variable" && len(b.Labels) == 1:
			d.vars[b.Labels[0]] = true
		case b.Type == "locals":
			for name := range b.Body.Attributes {
				d.locals[name] = true
			}
		case b.Type == "resource" && len(b.Labels) == 2:
			d.resources[b.Labels[0]+"."+b.Labels[1]] = b.Labels[0]
		case b.Type == "data" && len(b.Labels) == 2:
			d.resources["data."+b.Labels[0]+"."+b.Labels[1]] = b.Labels[0]
		case b.Type == "module" && len(b.Labels) == 1:
			src := ""
			if a, ok := b.Body.Attributes["source"]; ok {
				if v, diags := a.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
					src = v.AsString()
				}
			}
			d.modules[b.Labels[0]] = src
		}
	}
}

// moduleOutputs lists the outputs of a local module called with source
// from d's directory; false when the module is not a local module inside
// the workflow files.
func (d *decls) moduleOutputs(source string) (map[string]bool, bool) {
	if !hclcodec.IsLocalModuleSource(source) {
		return nil, false
	}
	target := path.Clean(path.Join(d.dir, strings.ReplaceAll(source, `\`, "/")))
	if target == "." || strings.HasPrefix(target, "../") || target == ".." {
		return nil, false
	}
	out, found := map[string]bool{}, false
	for p, src := range d.files {
		if dirOf(p) != target || !strings.HasSuffix(p, ".tf") {
			continue
		}
		found = true
		f, diags := hclsyntax.ParseConfig(src, p, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, false
		}
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			if b.Type == "output" && len(b.Labels) == 1 {
				out[b.Labels[0]] = true
			}
		}
	}
	return out, found
}

func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// skipRefs are blocks whose expressions name addresses or types rather than
// read values.
var skipRefs = map[string]bool{"moved": true, "removed": true, "import": true, "terraform": true}

// builtinRoots are reference roots that are always valid.
var builtinRoots = map[string]bool{"each": true, "count": true, "path": true, "terraform": true, "self": true}

func referenceProblems(cat *catalog.Catalog, body *hclsyntax.Body, d *decls) []model.CodeProblem {
	c := refChecker{cat: cat, d: d, schemas: map[string]*model.SchemaBlock{}, outputs: map[string]map[string]bool{}}
	for _, b := range body.Blocks {
		if skipRefs[b.Type] {
			continue
		}
		c.body(b.Type, b.Body)
	}
	return c.out
}

type refChecker struct {
	cat     *catalog.Catalog
	d       *decls
	schemas map[string]*model.SchemaBlock // resource/data address type key -> schema (nil: unknown)
	outputs map[string]map[string]bool    // module name -> outputs (nil: unknown)
	out     []model.CodeProblem
}

func (c *refChecker) body(blockType string, b *hclsyntax.Body) {
	for name, a := range b.Attributes {
		// Type constraints and ignore_changes name types and attributes.
		if blockType == "variable" && name == "type" || blockType == "lifecycle" && name == "ignore_changes" {
			continue
		}
		for _, tr := range a.Expr.Variables() {
			c.traversal(tr)
		}
	}
	for _, nb := range b.Blocks {
		c.body(nb.Type, nb.Body)
	}
}

func (c *refChecker) warn(code, msg, detail string, r hcl.Range) {
	c.out = append(c.out, problem(model.SeverityWarning, code, msg, detail, r))
}

func attrStep(tr hcl.Traversal, i int) (string, bool) {
	if i >= len(tr) {
		return "", false
	}
	a, ok := tr[i].(hcl.TraverseAttr)
	return a.Name, ok
}

func spanTo(tr hcl.Traversal, i int) hcl.Range {
	return hcl.RangeBetween(tr[0].SourceRange(), tr[i].SourceRange())
}

func (c *refChecker) traversal(tr hcl.Traversal) {
	root := tr.RootName()
	switch {
	case builtinRoots[root]:
	case root == "var":
		if name, ok := attrStep(tr, 1); ok && !c.d.vars[name] {
			c.warn(CodeUnknownVariable, "Reference to undeclared input variable var."+name,
				"Declare it with a variable block in this module.", spanTo(tr, 1))
		}
	case root == "local":
		if name, ok := attrStep(tr, 1); ok && !c.d.locals[name] {
			c.warn(CodeUnknownLocal, "Reference to undeclared local value local."+name,
				"Declare it in a locals block in this module.", spanTo(tr, 1))
		}
	case root == "module":
		name, ok := attrStep(tr, 1)
		if !ok {
			return
		}
		source, declared := c.d.modules[name]
		if !declared {
			c.warn(CodeUnknownReference, "Reference to undeclared module module."+name, "No module block with this name in this module.", spanTo(tr, 1))
			return
		}
		out, ok := c.moduleOutputs(name, source)
		if !ok {
			return
		}
		if o, isAttr := attrStep(tr, 2); isAttr && !out[o] {
			c.warn(CodeUnknownOutput, "Module "+name+" has no output "+strconv.Quote(o), "The module at "+source+" does not declare this output.", spanTo(tr, 2))
		}
	case root == "data":
		typ, ok1 := attrStep(tr, 1)
		name, ok2 := attrStep(tr, 2)
		if !ok1 || !ok2 {
			return
		}
		c.resource(tr, model.KindData, "data."+typ+"."+name, typ, 3)
	default:
		// TYPE.NAME: only types the catalog knows, so local names that
		// merely look like references (iterators, provider aliases) pass.
		name, ok := attrStep(tr, 1)
		if !ok {
			return
		}
		addr := root + "." + name
		if _, declared := c.d.resources[addr]; !declared && !c.cat.HasType(model.KindResource, root) {
			return
		}
		c.resource(tr, model.KindResource, addr, root, 2)
	}
}

func (c *refChecker) moduleOutputs(name, source string) (map[string]bool, bool) {
	if out, cached := c.outputs[name]; cached {
		return out, out != nil
	}
	out, ok := c.d.moduleOutputs(source)
	if !ok {
		out = nil
	}
	c.outputs[name] = out
	return out, ok
}

// resource checks a resource/data reference: declared, and its first
// attribute (after an optional instance key) exists in the schema.
func (c *refChecker) resource(tr hcl.Traversal, kind model.NodeKind, addr, typ string, next int) {
	if _, declared := c.d.resources[addr]; !declared {
		what := "resource"
		if kind == model.KindData {
			what = "data source"
		}
		c.warn(CodeUnknownReference, "Reference to undeclared "+what+" "+addr, "No "+string(kind)+" block with this type and name in this module.", spanTo(tr, next-1))
		return
	}
	if next < len(tr) {
		if _, isIndex := tr[next].(hcl.TraverseIndex); isIndex {
			next++
		} else if _, isSplat := tr[next].(hcl.TraverseSplat); isSplat {
			next++
		}
	}
	attr, ok := attrStep(tr, next)
	if !ok {
		return
	}
	key := string(kind) + "/" + typ
	sb, cached := c.schemas[key]
	if !cached {
		if s, found := c.cat.Schema(kind, typ); found {
			sb = &s.Block
		}
		c.schemas[key] = sb
	}
	if sb == nil || attr == "id" {
		return
	}
	for _, a := range sb.Attributes {
		if a.Name == attr {
			return
		}
	}
	for _, b := range sb.Blocks {
		if b.Name == attr {
			return
		}
	}
	// The step's range starts at its dot.
	r := tr[next].SourceRange()
	r.Start.Column++
	r.Start.Byte++
	c.warn(CodeUnsupportedAttrib, "Unsupported attribute "+strconv.Quote(attr)+" of "+addr,
		"The "+typ+" schema has no attribute or block named "+attr+".", r)
}
