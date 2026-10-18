package hclcodec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// comment is one comment token of a source file.
type comment struct {
	text           string // markers included, trailing whitespace trimmed
	start          int    // byte offset
	line, lastLine int
}

// converter turns one parsed file into model bodies, attaching its comments
// to the attribute or block they belong to:
//   - comment lines directly above an item are its Lead (blank lines between
//     comment groups, and between the comments and the item, are kept as "");
//   - a comment on the same line after an attribute value is its Line;
//     after a block's "{" the block's Line, after its "}" the block's End;
//   - comment lines after the last item of a block are the block's Tail;
//     after the last top-level block of a file, that block's After.
//
// Comments inside an expression are part of the expression source.
type converter struct {
	src      []byte
	comments []comment
}

func newConverter(src []byte) *converter {
	c := &converter{src: src}
	toks, _ := hclsyntax.LexConfig(src, "", hcl.InitialPos)
	for _, t := range toks {
		if t.Type != hclsyntax.TokenComment {
			continue
		}
		text := strings.TrimRight(string(t.Bytes), " \t\r\n")
		c.comments = append(c.comments, comment{
			text:     text,
			start:    t.Range.Start.Byte,
			line:     t.Range.Start.Line,
			lastLine: t.Range.Start.Line + strings.Count(text, "\n"),
		})
	}
	return c
}

// slot is a body item comments can attach to.
type slot struct {
	start, end         int // byte range of the whole item
	startLine, endLine int
	comments           func() *model.Comments
	block              bool
}

func attrSlot(a *hclsyntax.Attribute, dst *model.Attr) slot {
	return slot{
		start: a.SrcRange.Start.Byte, end: a.SrcRange.End.Byte,
		startLine: a.SrcRange.Start.Line, endLine: a.SrcRange.End.Line,
		comments: func() *model.Comments {
			if dst.Comments == nil {
				dst.Comments = &model.Comments{}
			}
			return dst.Comments
		},
	}
}

func blockSlot(b *hclsyntax.Block, dst *model.Body) slot {
	return slot{
		start: b.TypeRange.Start.Byte, end: b.CloseBraceRange.End.Byte,
		startLine: b.TypeRange.Start.Line, endLine: b.CloseBraceRange.End.Line,
		comments: func() *model.Comments { return bodyComments(dst) },
		block:    true,
	}
}

func bodyComments(b *model.Body) *model.Comments {
	if b.Comments == nil {
		b.Comments = &model.Comments{}
	}
	return b.Comments
}

func (c *converter) exprSource(e hclsyntax.Expression) string {
	r := e.Range()
	return string(c.src[r.Start.Byte:r.End.Byte])
}

// body converts a block body; attributes keep their source order
// (hclsyntax stores them in a map).
func (c *converter) body(blk *hclsyntax.Block) model.Body {
	b := blk.Body
	attrs := make([]*hclsyntax.Attribute, 0, len(b.Attributes))
	for _, a := range b.Attributes {
		attrs = append(attrs, a)
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].SrcRange.Start.Byte < attrs[j].SrcRange.Start.Byte })
	out := model.Body{Attrs: make([]model.Attr, 0, len(attrs)), Blocks: make([]model.Block, 0, len(b.Blocks))}
	for _, a := range attrs {
		out.Attrs = append(out.Attrs, model.Attr{Name: a.Name, Value: model.Value{Expr: c.exprSource(a.Expr)}})
	}
	for _, nb := range b.Blocks {
		out.Blocks = append(out.Blocks, model.Block{Type: nb.Type, Labels: labelsOrNil(nb.Labels), Body: c.body(nb)})
	}
	slots := make([]slot, 0, len(attrs)+len(b.Blocks))
	for i, a := range attrs {
		slots = append(slots, attrSlot(a, &out.Attrs[i]))
	}
	for i, nb := range b.Blocks {
		slots = append(slots, blockSlot(nb, &out.Blocks[i].Body))
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].start < slots[j].start })
	line, tail := c.attach(slots, blk.OpenBraceRange.End.Byte, blk.CloseBraceRange.Start.Byte, blk.OpenBraceRange.Start.Line)
	if line != "" {
		bodyComments(&out).Line = line
	}
	if len(tail) > 0 {
		bodyComments(&out).Tail = tail
	}
	// Export writes attributes first, without blank lines between them: the
	// first one has no blank line to keep above its comments.
	if len(out.Attrs) > 0 && out.Attrs[0].Comments != nil {
		out.Attrs[0].Comments.Lead = normalizeLead(out.Attrs[0].Comments.Lead)
	}
	return out
}

// attach distributes the comments in [from, to) that are not inside one of
// slots (sorted by position). It returns the comment on openLine before the
// first slot and the comment lines left after the last slot.
func (c *converter) attach(slots []slot, from, to, openLine int) (line string, rest []string) {
	k := sort.Search(len(c.comments), func(i int) bool { return c.comments[i].start >= from })
	var pending []string
	pendingLast := 0
	var prev *slot
	place := func(cm comment) {
		switch {
		case prev != nil && cm.line == prev.endLine:
			cms := prev.comments()
			if prev.block {
				cms.End = joinLine(cms.End, cm.text)
			} else {
				cms.Line = joinLine(cms.Line, cm.text)
			}
			return
		case prev == nil && cm.line == openLine:
			line = joinLine(line, cm.text)
			return
		}
		if len(pending) == 0 {
			if prev != nil && cm.line > prev.endLine+1 {
				pending = append(pending, "")
			}
		} else if cm.line > pendingLast+1 {
			pending = append(pending, "")
		}
		pending = append(pending, cm.text)
		pendingLast = cm.lastLine
	}
	for i := range slots {
		s := &slots[i]
		for ; k < len(c.comments) && c.comments[k].start < s.start; k++ {
			place(c.comments[k])
		}
		if len(pending) > 0 {
			if pending[0] == "" && s.block {
				pending = pending[1:] // export separates blocks with a blank line itself
			}
			if s.startLine > pendingLast+1 {
				pending = append(pending, "")
			}
			cms := s.comments()
			cms.Lead = append(cms.Lead, pending...)
			pending = nil
		}
		for ; k < len(c.comments) && c.comments[k].start < s.end; k++ {
			// Inside the item: nested bodies attach their own comments;
			// comments inside an expression are part of its source.
		}
		prev = s
	}
	for ; k < len(c.comments) && c.comments[k].start < to; k++ {
		place(c.comments[k])
	}
	return line, pending
}

func joinLine(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

// gatherAfter moves the comments that follow top-level blocks at the end of
// a file (model.Comments.After) to the last block of the file Export writes
// them to, which is where importing the export finds them. Export itself
// writes every After at the end of its file.
func gatherAfter(wf *model.Workflow) {
	for _, slots := range topLevelComments(wf) {
		var after []string
		for _, s := range slots {
			if *s != nil && len((*s).After) > 0 {
				after = joinAfter(after, (*s).After)
				(*s).After = nil
			}
		}
		if len(after) > 0 {
			last := slots[len(slots)-1]
			if *last == nil {
				*last = &model.Comments{}
			}
			(*last).After = after
		}
	}
}

// joinAfter appends end-of-file comment lines without repeating blank lines.
func joinAfter(a, b []string) []string {
	for _, l := range b {
		if l == "" && len(a) > 0 && a[len(a)-1] == "" {
			continue
		}
		a = append(a, l)
	}
	return a
}

// topLevelComments lists the comment slots of the top-level blocks of each
// file Export writes, in Export's order.
func topLevelComments(wf *model.Workflow) [][]**model.Comments {
	var versions, providers, variables, main, outputs []**model.Comments
	if wf.Terraform != nil {
		versions = append(versions, &wf.Terraform.Comments)
	}
	for i := range wf.Providers {
		providers = append(providers, &wf.Providers[i].Body.Comments)
	}
	for i := range wf.Variables {
		variables = append(variables, &wf.Variables[i].Body.Comments)
	}
	if len(wf.Locals) > 0 || !emptyComments(wf.LocalsComments) {
		main = append(main, &wf.LocalsComments)
	}
	for _, kind := range []model.NodeKind{model.KindData, model.KindResource, model.KindModule} {
		for i := range wf.Nodes {
			if wf.Nodes[i].Kind == kind {
				main = append(main, &wf.Nodes[i].Body.Comments)
			}
		}
	}
	for i := range wf.Other {
		main = append(main, &wf.Other[i].Body.Comments)
	}
	for i := range wf.Outputs {
		outputs = append(outputs, &wf.Outputs[i].Body.Comments)
	}
	return [][]**model.Comments{versions, providers, variables, main, outputs}
}

// topBlock is a converted top-level block.
type topBlock struct {
	hb   *hclsyntax.Block
	body model.Body
}

// file converts the top-level blocks of a file. carry are comment lines
// left over from previous comment-only files; they lead the first block.
// When the file has no blocks, every comment is returned as rest.
func (c *converter) file(body *hclsyntax.Body, carry []string) (blocks []topBlock, rest []string) {
	blocks = make([]topBlock, len(body.Blocks))
	slots := make([]slot, len(body.Blocks))
	for i, b := range body.Blocks {
		blocks[i] = topBlock{hb: b, body: c.body(b)}
		slots[i] = blockSlot(b, &blocks[i].body)
	}
	_, rest = c.attach(slots, 0, len(c.src), 0)
	if len(blocks) == 0 {
		return nil, append(carry, rest...)
	}
	if len(carry) > 0 {
		cms := bodyComments(&blocks[0].body)
		cms.Lead = normalizeLead(append(append([]string(nil), carry...), cms.Lead...))
	}
	if len(rest) > 0 {
		bodyComments(&blocks[len(blocks)-1].body).After = rest
	}
	return blocks, nil
}

// mergeBlockComments folds the comments of a block merged into an earlier
// one (a second `terraform` or `locals` block): the lines above it join the
// merged block's Lead (file headers stay outside the block); the comment
// after its "{" and the lines carried from earlier merged blocks lead its
// first item, or are carried further when it has none. It returns the
// lines still to carry: the comments after its content.
func mergeBlockComments(merged func() *model.Comments, cms *model.Comments, carry []string, first func() *model.Comments) []string {
	if cms != nil {
		if len(cms.Lead) > 0 {
			m := merged()
			if n := len(m.Lead); n > 0 && m.Lead[n-1] != "" {
				m.Lead = append(m.Lead, "")
			}
			m.Lead = normalizeLead(append(m.Lead, cms.Lead...))
		}
		if cms.Line != "" {
			carry = append(carry, cms.Line)
		}
	}
	if len(carry) > 0 {
		if dst := first(); dst != nil {
			dst.Lead = normalizeLead(append(carry, dst.Lead...))
			carry = nil
		}
	}
	return appendTrailing(carry, cms)
}

// normalizeLead drops leading and repeated blank lines, as Import reads
// comment lines back.
func normalizeLead(lines []string) []string {
	out := lines[:0]
	for _, l := range lines {
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// appendTrailing adds the comments after a block's content (Tail, End,
// After) to lines.
func appendTrailing(lines []string, cms *model.Comments) []string {
	if cms == nil {
		return lines
	}
	lines = append(lines, cms.Tail...)
	if cms.End != "" {
		lines = append(lines, cms.End)
	}
	return append(lines, cms.After...)
}

// firstItemComments returns the Comments of the first attribute or nested
// block of b, or nil when b is empty.
func firstItemComments(b *model.Body) *model.Comments {
	switch {
	case len(b.Attrs) > 0:
		if b.Attrs[0].Comments == nil {
			b.Attrs[0].Comments = &model.Comments{}
		}
		return b.Attrs[0].Comments
	case len(b.Blocks) > 0:
		return bodyComments(&b.Blocks[0].Body)
	}
	return nil
}

// emptyComments reports whether c holds nothing.
func emptyComments(c *model.Comments) bool {
	return c == nil || (len(c.Lead) == 0 && c.Line == "" && len(c.Tail) == 0 && c.End == "" && len(c.After) == 0)
}

// validComment reports whether s is exactly one HCL comment. A line
// comment (Line, End) must not span lines unless it is a /* */ comment.
func validComment(s string) error {
	toks, _ := hclsyntax.LexConfig([]byte(s+"\n"), "", hcl.InitialPos)
	n := 0
	for _, t := range toks {
		switch t.Type {
		case hclsyntax.TokenComment:
			n++
		case hclsyntax.TokenNewline, hclsyntax.TokenEOF:
		default:
			return fmt.Errorf("invalid comment %q: not a # or // line or a /* */ block", s)
		}
	}
	if n != 1 {
		return fmt.Errorf("invalid comment %q: expected exactly one comment", s)
	}
	return nil
}
