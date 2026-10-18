package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// lit is a constant value with both forms filled.
func lit(v any) model.Value {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("catalog: literal %v: %v", v, err))
	}
	return litRaw(raw)
}

func litRaw(raw json.RawMessage) model.Value {
	return model.Value{Expr: hclLiteral(raw), Literal: raw}
}

// expr is an HCL expression (references, functions); Normalize fills Literal
// when it turns out constant.
func expr(src string) model.Value { return model.Value{Expr: src} }

// hclLiteral renders a JSON value as HCL source.
func hclLiteral(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "null"
	}
	var sb strings.Builder
	writeHCL(&sb, v)
	return sb.String()
}

func writeHCL(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		sb.WriteString(strconv.FormatBool(t))
	case json.Number:
		sb.WriteString(t.String())
	case string:
		sb.WriteString(hclString(t))
	case []any:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeHCL(sb, e)
		}
		sb.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if len(keys) == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{ ")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(hclKey(k))
			sb.WriteString(" = ")
			writeHCL(sb, t[k])
		}
		sb.WriteString(" }")
	}
}

// hclString quotes s as an HCL string literal (no interpolation).
func hclString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := range len(s) {
		ch := s[i]
		switch {
		case ch == '\\':
			sb.WriteString(`\\`)
		case ch == '"':
			sb.WriteString(`\"`)
		case ch == '\n':
			sb.WriteString(`\n`)
		case ch == '\r':
			sb.WriteString(`\r`)
		case ch == '\t':
			sb.WriteString(`\t`)
		case (ch == '$' || ch == '%') && i+1 < len(s) && s[i+1] == '{':
			sb.WriteByte(ch)
			sb.WriteByte(ch)
		case ch < 0x20:
			fmt.Fprintf(&sb, `\u%04x`, ch)
		default:
			sb.WriteByte(ch)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func hclKey(k string) string {
	if identRe.MatchString(k) {
		return k
	}
	return hclString(k)
}

// address is the Terraform address of a node.
func address(n *model.Node) string {
	switch n.Kind {
	case model.KindData:
		return "data." + n.Type + "." + n.Name
	case model.KindModule:
		return "module." + n.Name
	}
	return n.Type + "." + n.Name
}

func ref(n *model.Node, attr string) string { return address(n) + "." + attr }

func getAttr(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

func setAttr(b *model.Body, name string, v model.Value) {
	if a := getAttr(b, name); a != nil {
		a.Value = v
		return
	}
	b.Attrs = append(b.Attrs, model.Attr{Name: name, Value: v})
}

// setDefault sets name only when it is not set yet.
func setDefault(b *model.Body, name string, v model.Value) {
	if getAttr(b, name) == nil {
		setAttr(b, name, v)
	}
}

func getBlock(b *model.Body, typ string) *model.Block {
	for i := range b.Blocks {
		if b.Blocks[i].Type == typ {
			return &b.Blocks[i]
		}
	}
	return nil
}

// ensureBlock returns the first block of typ, appending an empty one if absent.
func ensureBlock(b *model.Body, typ string) *model.Block {
	if blk := getBlock(b, typ); blk != nil {
		return blk
	}
	b.Blocks = append(b.Blocks, model.Block{Type: typ, Body: emptyBody()})
	return &b.Blocks[len(b.Blocks)-1]
}

func emptyBody() model.Body { return model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}} }

// valueSource returns the HCL source of v (Expr, or Literal rendered).
func valueSource(v model.Value) string {
	if v.Expr != "" {
		return v.Expr
	}
	if len(v.Literal) > 0 {
		return hclLiteral(v.Literal)
	}
	return ""
}

var traversalRe = regexp.MustCompile(`^(data\.)?([A-Za-z_][A-Za-z0-9_-]*)\.([A-Za-z_][A-Za-z0-9_-]*)\.([A-Za-z_][A-Za-z0-9_]*)$`)

// resolve maps a simple reference expression ("aws_iam_role.r.arn") to the
// referenced node (existing or pending) and attribute.
func (b *builder) resolve(v model.Value) (*model.Node, string) {
	m := traversalRe.FindStringSubmatch(strings.TrimSpace(v.Expr))
	if m == nil {
		return nil, ""
	}
	kind := model.KindResource
	if m[1] != "" {
		kind = model.KindData
	}
	for _, n := range b.all() {
		if n.Kind == kind && n.Type == m[2] && n.Name == m[3] {
			return n, m[4]
		}
	}
	return nil, ""
}

// referencesNode reports whether src (an HCL expression) mentions addr as a
// traversal root ("aws_sqs_queue.q" in "aws_sqs_queue.q.arn").
func referencesNode(src, addr string) bool {
	for i := 0; ; {
		j := strings.Index(src[i:], addr)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(addr)
		before := j == 0 || !isIdentByte(src[j-1]) && src[j-1] != '.'
		after := end == len(src) || !isIdentByte(src[end])
		if before && after {
			return true
		}
		i = end
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '-' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// objectItem is one key = value pair of an object constructor, as source.
type objectItem struct{ key, value string }

// mergeObject sets key = valueSrc inside an object-valued attribute,
// preserving the other entries. Non-constructor expressions are wrapped in
// merge().
func mergeObject(v *model.Value, key, valueSrc string) model.Value {
	var items []objectItem
	if v != nil {
		src := valueSource(*v)
		parsed, ok := parseObject(src)
		if !ok {
			return expr(fmt.Sprintf("merge(%s, {\n  %s = %s\n})", src, hclKey(key), valueSrc))
		}
		items = parsed
	}
	replaced := false
	for i := range items {
		if strings.Trim(items[i].key, `"`) == key {
			items[i].value = valueSrc
			replaced = true
		}
	}
	if !replaced {
		items = append(items, objectItem{hclKey(key), valueSrc})
	}
	var sb strings.Builder
	sb.WriteString("{\n")
	for _, it := range items {
		fmt.Fprintf(&sb, "  %s = %s\n", it.key, it.value)
	}
	sb.WriteString("}")
	return expr(sb.String())
}

func parseObject(src string) ([]objectItem, bool) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, true
	}
	e, diags := hclsyntax.ParseExpression([]byte(src), "value.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	obj, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, false
	}
	items := make([]objectItem, 0, len(obj.Items))
	for _, it := range obj.Items {
		items = append(items, objectItem{
			key:   strings.TrimSpace(string(it.KeyExpr.Range().SliceBytes([]byte(src)))),
			value: strings.TrimSpace(string(it.ValueExpr.Range().SliceBytes([]byte(src)))),
		})
	}
	return items, true
}

// appendList adds itemSrc to a list-valued attribute unless already present;
// non-constructor expressions are wrapped in concat().
func appendList(v *model.Value, itemSrc string) model.Value {
	var items []string
	if v != nil {
		src := strings.TrimSpace(valueSource(*v))
		e, diags := hclsyntax.ParseExpression([]byte(src), "value.hcl", hcl.InitialPos)
		tup, ok := e.(*hclsyntax.TupleConsExpr)
		if diags.HasErrors() || !ok {
			return expr(fmt.Sprintf("concat(%s, [%s])", src, itemSrc))
		}
		for _, it := range tup.Exprs {
			items = append(items, strings.TrimSpace(string(it.Range().SliceBytes([]byte(src)))))
		}
	}
	if !slices.Contains(items, itemSrc) {
		items = append(items, itemSrc)
	}
	return expr("[" + strings.Join(items, ", ") + "]")
}
