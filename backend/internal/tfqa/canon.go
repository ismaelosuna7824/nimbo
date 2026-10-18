// Package tfqa checks Terraform fidelity: it reduces the configuration of a
// module directory to a canonical form that ignores file split, top-level
// block order, attribute order, whitespace and comments, and compares two
// directories (an original and Nimbo's export of it) including the local
// modules they call, their variable files and their comments.
package tfqa

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Block is a canonical HCL block. Attribute order is irrelevant in HCL;
// nested blocks keep their order.
type Block struct {
	Type   string
	Labels []string
	Attrs  map[string]Expr
	Blocks []*Block
}

// Expr is an attribute expression: Norm is its token sequence without
// whitespace, newlines and comments; Const is set for constant strings.
type Expr struct {
	Norm  string
	Const *string
}

// Module is the canonical form of one directory's configuration.
type Module struct {
	// Blocks are the top-level blocks of the *.tf files sorted by key.
	// Every `locals` block is merged into one, as are `terraform` blocks
	// (whose nested blocks are then sorted, since their order spans files).
	Blocks []*Block
	// Overrides are override files (override.tf, *_override.tf), which
	// Terraform merges into the other blocks: kept per file name.
	Overrides map[string][]*Block
	// JSON maps *.tf.json files to their content (compared byte-wise).
	JSON map[string][]byte
	// Comments lists every comment of the *.tf files (markers included,
	// trailing whitespace trimmed), sorted.
	Comments []string
	// Problems are configuration errors found while merging (duplicate
	// locals, duplicate terraform attributes).
	Problems []string
}

// NormalizeNewlines converts CRLF line endings to LF, as Nimbo's import does.
func NormalizeNewlines(src []byte) []byte {
	return bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
}

// IsOverrideFile reports whether Terraform treats a *.tf file as an
// override file.
func IsOverrideFile(name string) bool {
	base := strings.TrimSuffix(path.Base(name), ".tf")
	return base == "override" || strings.HasSuffix(base, "_override")
}

// LoadDir canonicalizes the *.tf and *.tf.json files directly inside dir.
func LoadDir(dir string) (*Module, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	return Load(files)
}

// Load canonicalizes files (base name -> content).
func Load(files map[string][]byte) (*Module, error) {
	m := &Module{Overrides: map[string][]*Block{}, JSON: map[string][]byte{}}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	var locals, terraform *Block
	for _, name := range names {
		if strings.HasSuffix(name, ".tf.json") {
			m.JSON[name] = files[name]
			continue
		}
		src := NormalizeNewlines(files[name])
		f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", name, diags.Error())
		}
		m.Comments = append(m.Comments, comments(src)...)
		body := f.Body.(*hclsyntax.Body)
		if IsOverrideFile(name) {
			m.Overrides[name] = convertBlocks(src, body.Blocks)
			continue
		}
		for _, b := range convertBlocks(src, body.Blocks) {
			switch {
			case b.Type == "locals" && len(b.Labels) == 0:
				if locals == nil {
					locals = &Block{Type: "locals", Attrs: map[string]Expr{}}
					m.Blocks = append(m.Blocks, locals)
				}
				m.merge(locals, b)
			case b.Type == "terraform" && len(b.Labels) == 0:
				if terraform == nil {
					terraform = &Block{Type: "terraform", Attrs: map[string]Expr{}}
					m.Blocks = append(m.Blocks, terraform)
				}
				m.merge(terraform, b)
			default:
				m.Blocks = append(m.Blocks, b)
			}
		}
	}
	if terraform != nil {
		slices.SortStableFunc(terraform.Blocks, func(a, b *Block) int { return cmp.Compare(a.String(), b.String()) })
	}
	slices.SortStableFunc(m.Blocks, func(a, b *Block) int {
		return cmp.Or(cmp.Compare(a.Key(), b.Key()), cmp.Compare(a.String(), b.String()))
	})
	slices.Sort(m.Comments)
	return m, nil
}

func (m *Module) merge(into, b *Block) {
	for name, e := range b.Attrs {
		if _, dup := into.Attrs[name]; dup {
			m.Problems = append(m.Problems, fmt.Sprintf("duplicate %s attribute %q", b.Type, name))
		}
		into.Attrs[name] = e
	}
	into.Blocks = append(into.Blocks, b.Blocks...)
}

func convertBlocks(src []byte, blocks hclsyntax.Blocks) []*Block {
	out := make([]*Block, 0, len(blocks))
	for _, b := range blocks {
		cb := &Block{Type: b.Type, Labels: b.Labels, Attrs: map[string]Expr{}, Blocks: convertBlocks(src, b.Body.Blocks)}
		for name, a := range b.Body.Attributes {
			cb.Attrs[name] = newExpr(src, a.Expr)
		}
		out = append(out, cb)
	}
	return out
}

func newExpr(src []byte, e hclsyntax.Expression) Expr {
	r := e.Range()
	out := Expr{Norm: NormExpr(src[r.Start.Byte:r.End.Byte])}
	// Without an evaluation context, references and function calls fail.
	if v, diags := e.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
		s := v.AsString()
		out.Const = &s
	}
	return out
}

// NormExpr renders expression source as its tokens separated by single
// spaces, without newlines and comments.
func NormExpr(src []byte) string {
	toks, _ := hclsyntax.LexExpression(NormalizeNewlines(src), "", hcl.InitialPos)
	parts := make([]string, 0, len(toks))
	for _, t := range toks {
		switch t.Type {
		case hclsyntax.TokenNewline, hclsyntax.TokenComment, hclsyntax.TokenEOF:
			continue
		}
		parts = append(parts, string(t.Bytes))
	}
	return strings.Join(parts, " ")
}

func comments(src []byte) []string {
	toks, _ := hclsyntax.LexConfig(src, "", hcl.InitialPos)
	var out []string
	for _, t := range toks {
		if t.Type == hclsyntax.TokenComment {
			out = append(out, strings.TrimRight(string(t.Bytes), " \t\n"))
		}
	}
	return out
}

// Key identifies a top-level block: type plus quoted labels.
func (b *Block) Key() string {
	var sb strings.Builder
	sb.WriteString(b.Type)
	for _, l := range b.Labels {
		sb.WriteByte(' ')
		sb.WriteString(strconv.Quote(l))
	}
	return sb.String()
}

// String renders the block canonically (attributes sorted by name).
func (b *Block) String() string {
	var sb strings.Builder
	b.render(&sb, "")
	return sb.String()
}

func (b *Block) render(sb *strings.Builder, indent string) {
	sb.WriteString(indent + b.Key() + " {\n")
	names := make([]string, 0, len(b.Attrs))
	for n := range b.Attrs {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		sb.WriteString(indent + "  " + n + " = " + b.Attrs[n].Norm + "\n")
	}
	for _, nb := range b.Blocks {
		nb.render(sb, indent+"  ")
	}
	sb.WriteString(indent + "}\n")
}

// ---------------------------------------------------------------------------
// Comparison

// Result is the outcome of comparing an original directory with its export.
type Result struct {
	// Semantic lists configuration differences (blocks, attributes,
	// override and JSON files) in the directory and every local module.
	Semantic []string
	// Comments lists comments missing from or added to the export.
	Comments []string
	// Modules maps each original local module directory to its exported copy.
	Modules map[string]string
}

// OK reports whether nothing differs.
func (r *Result) OK() bool { return len(r.Semantic) == 0 && len(r.Comments) == 0 }

// CompareDirs compares the configuration in orig with exp. A local module
// call may point elsewhere in the export (Nimbo copies local modules to
// modules/<name>): its source is accepted when both resolve to local
// directories that compare equal.
func CompareDirs(orig, exp string) (*Result, error) {
	c := &comparer{res: &Result{Modules: map[string]string{}}, done: map[[2]string]bool{}}
	if err := c.dir(orig, exp, ""); err != nil {
		return nil, err
	}
	return c.res, nil
}

type comparer struct {
	res  *Result
	done map[[2]string]bool
}

func (c *comparer) dir(orig, exp, where string) error {
	k := [2]string{filepath.Clean(orig), filepath.Clean(exp)}
	if c.done[k] {
		return nil
	}
	c.done[k] = true
	om, err := LoadDir(orig)
	if err != nil {
		return fmt.Errorf("original %s: %w", orig, err)
	}
	em, err := LoadDir(exp)
	if err != nil {
		return fmt.Errorf("export %s: %w", exp, err)
	}
	for _, p := range om.Problems {
		c.res.Semantic = append(c.res.Semantic, where+"original: "+p)
	}
	for _, p := range em.Problems {
		c.res.Semantic = append(c.res.Semantic, where+"export: "+p)
	}

	// Map local module sources onto the export's.
	expMods := map[string]*Block{}
	for _, b := range em.Blocks {
		if b.Type == "module" {
			expMods[b.Key()] = b
		}
	}
	for _, b := range om.Blocks {
		src, ok := b.Attrs["source"]
		eb := expMods[b.Key()]
		if b.Type != "module" || !ok || src.Const == nil || !isLocal(*src.Const) || eb == nil {
			continue
		}
		esrc, ok := eb.Attrs["source"]
		if !ok || esrc.Const == nil || !isLocal(*esrc.Const) {
			continue // reported as an attribute difference
		}
		od := filepath.Join(orig, filepath.FromSlash(*src.Const))
		ed := filepath.Join(exp, filepath.FromSlash(*esrc.Const))
		c.res.Modules[filepath.Clean(od)] = filepath.Clean(ed)
		if err := c.dir(od, ed, where+b.Key()+": "); err != nil {
			return err
		}
		b.Attrs["source"] = esrc
	}

	c.res.Semantic = append(c.res.Semantic, diffBlocks(where, om.Blocks, em.Blocks)...)
	for name, ob := range om.Overrides {
		eb, ok := em.Overrides[name]
		if !ok {
			c.res.Semantic = append(c.res.Semantic, fmt.Sprintf("%soverride file %s missing", where, name))
			continue
		}
		c.res.Semantic = append(c.res.Semantic, diffBlocks(where+name+": ", ob, eb)...)
	}
	for name := range em.Overrides {
		if _, ok := om.Overrides[name]; !ok {
			c.res.Semantic = append(c.res.Semantic, fmt.Sprintf("%sunexpected override file %s", where, name))
		}
	}
	for name, data := range om.JSON {
		if !bytes.Equal(data, em.JSON[name]) {
			c.res.Semantic = append(c.res.Semantic, fmt.Sprintf("%sJSON file %s missing or changed", where, name))
		}
	}
	for name := range em.JSON {
		if _, ok := om.JSON[name]; !ok {
			c.res.Semantic = append(c.res.Semantic, fmt.Sprintf("%sunexpected JSON file %s", where, name))
		}
	}
	c.res.Comments = append(c.res.Comments, diffMultiset(where, om.Comments, em.Comments)...)
	return nil
}

func isLocal(s string) bool { return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") }

func diffBlocks(where string, a, b []*Block) []string {
	group := func(bs []*Block) map[string][]string {
		out := map[string][]string{}
		for _, x := range bs {
			out[x.Key()] = append(out[x.Key()], x.String())
		}
		for k := range out {
			slices.Sort(out[k])
		}
		return out
	}
	ga, gb := group(a), group(b)
	keys := make([]string, 0, len(ga)+len(gb))
	for k := range ga {
		keys = append(keys, k)
	}
	for k := range gb {
		if _, ok := ga[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []string
	for _, k := range keys {
		ra, rb := ga[k], gb[k]
		switch {
		case len(rb) == 0:
			out = append(out, fmt.Sprintf("%s%s: missing from export", where, k))
		case len(ra) == 0:
			out = append(out, fmt.Sprintf("%s%s: unexpected in export", where, k))
		case !slices.Equal(ra, rb):
			out = append(out, fmt.Sprintf("%s%s differs:\n%s", where, k, lineDiff(strings.Join(ra, ""), strings.Join(rb, ""))))
		}
	}
	return out
}

// lineDiff lists the lines only in a ("-") or only in b ("+").
func lineDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	count := map[string]int{}
	for _, l := range lb {
		count[l]++
	}
	var sb strings.Builder
	for _, l := range la {
		if count[l] > 0 {
			count[l]--
			continue
		}
		sb.WriteString("  - " + l + "\n")
	}
	count = map[string]int{}
	for _, l := range la {
		count[l]++
	}
	for _, l := range lb {
		if count[l] > 0 {
			count[l]--
			continue
		}
		sb.WriteString("  + " + l + "\n")
	}
	return sb.String()
}

func diffMultiset(where string, a, b []string) []string {
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	keys := make([]string, 0, len(count))
	for k, n := range count {
		if n != 0 {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if n := count[k]; n > 0 {
			out = append(out, fmt.Sprintf("%scomment lost (x%d): %s", where, n, k))
		} else {
			out = append(out, fmt.Sprintf("%scomment added (x%d): %s", where, -n, k))
		}
	}
	return out
}

// LoadVars parses a variable file into variable name -> normalized expression.
func LoadVars(name string, src []byte) (map[string]string, error) {
	src = NormalizeNewlines(src)
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", name, diags.Error())
	}
	out := map[string]string{}
	for n, a := range f.Body.(*hclsyntax.Body).Attributes {
		out[n] = newExpr(src, a.Expr).Norm
	}
	return out, nil
}
