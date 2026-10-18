package mcp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// pathSegment is one step of an attribute path such as
// "versioning.enabled" or "attribute[1].name".
type pathSegment struct {
	name  string
	index int
}

func parsePath(path string) ([]pathSegment, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty attribute name")
	}
	var segs []pathSegment
	for _, part := range strings.Split(path, ".") {
		seg := pathSegment{name: part}
		if open := strings.IndexByte(part, '['); open >= 0 {
			if !strings.HasSuffix(part, "]") {
				return nil, fmt.Errorf("invalid attribute path %q", path)
			}
			idx, err := strconv.Atoi(part[open+1 : len(part)-1])
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid index in attribute path %q", path)
			}
			seg = pathSegment{name: part[:open], index: idx}
		}
		if seg.name == "" {
			return nil, fmt.Errorf("invalid attribute path %q", path)
		}
		segs = append(segs, seg)
	}
	if segs[len(segs)-1].index != 0 || strings.HasSuffix(path, "]") {
		return nil, fmt.Errorf("attribute path %q must end with an attribute name", path)
	}
	return segs, nil
}

// blockAt returns the index-th nested block of type name, creating it when
// create is set and index equals the current count.
func blockAt(body *model.Body, seg pathSegment, create bool) (*model.Body, error) {
	for _, a := range body.Attrs {
		if a.Name == seg.name {
			return nil, fmt.Errorf("%q is an attribute, not a block; set its whole expression instead", seg.name)
		}
	}
	n := 0
	for i := range body.Blocks {
		if body.Blocks[i].Type != seg.name {
			continue
		}
		if n == seg.index {
			return &body.Blocks[i].Body, nil
		}
		n++
	}
	if !create || seg.index != n {
		return nil, fmt.Errorf("block %s[%d] does not exist (%d present)", seg.name, seg.index, n)
	}
	body.Blocks = append(body.Blocks, model.Block{Type: seg.name, Body: model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}})
	return &body.Blocks[len(body.Blocks)-1].Body, nil
}

// setAttr sets path to the HCL expression expr, creating intermediate blocks.
func setAttr(body *model.Body, path, expr string) error {
	segs, err := parsePath(path)
	if err != nil {
		return err
	}
	cur := body
	for _, seg := range segs[:len(segs)-1] {
		if cur, err = blockAt(cur, seg, true); err != nil {
			return err
		}
	}
	name := segs[len(segs)-1].name
	for _, b := range cur.Blocks {
		if b.Type == name {
			return fmt.Errorf("%q is a nested block; set its attributes with %q", name, name+".<attribute>")
		}
	}
	for i := range cur.Attrs {
		if cur.Attrs[i].Name == name {
			cur.Attrs[i].Value = model.Value{Expr: expr}
			return nil
		}
	}
	cur.Attrs = append(cur.Attrs, model.Attr{Name: name, Value: model.Value{Expr: expr}})
	return nil
}

// unsetAttr removes an attribute or, when path names a block, every block
// of that type (or only block[i] with an index).
func unsetAttr(body *model.Body, path string) error {
	parts := strings.Split(path, ".")
	last := parts[len(parts)-1]
	cur := body
	if len(parts) > 1 {
		segs, err := parsePath(strings.Join(parts[:len(parts)-1], ".") + ".x")
		if err != nil {
			return err
		}
		for _, seg := range segs[:len(segs)-1] {
			if cur, err = blockAt(cur, seg, false); err != nil {
				return err
			}
		}
	}
	for i := range cur.Attrs {
		if cur.Attrs[i].Name == last {
			cur.Attrs = append(cur.Attrs[:i], cur.Attrs[i+1:]...)
			return nil
		}
	}
	name, index := last, -1
	if open := strings.IndexByte(last, '['); open >= 0 && strings.HasSuffix(last, "]") {
		idx, err := strconv.Atoi(last[open+1 : len(last)-1])
		if err != nil {
			return fmt.Errorf("invalid index in %q", path)
		}
		name, index = last[:open], idx
	}
	kept := cur.Blocks[:0]
	removed, n := false, 0
	for _, b := range cur.Blocks {
		if b.Type == name && (index < 0 || index == n) {
			removed = true
			n++
			continue
		}
		if b.Type == name {
			n++
		}
		kept = append(kept, b)
	}
	cur.Blocks = kept
	if !removed {
		return fmt.Errorf("%q is not set", path)
	}
	return nil
}

// setNamedAttr sets a top-level attribute of a variable/output block.
func setNamedAttr(body *model.Body, name string, v model.Value) {
	for i := range body.Attrs {
		if body.Attrs[i].Name == name {
			body.Attrs[i].Value = v
			return
		}
	}
	body.Attrs = append(body.Attrs, model.Attr{Name: name, Value: v})
}
