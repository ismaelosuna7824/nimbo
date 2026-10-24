package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Mirrors of the Terraform inspector affordances the `set` gesture accepts
// (apps/desktop/src/features/inspector): the Link to… picker's options
// (references.ts), list values built with Link to… (body.ts appendToList)
// and the object-list editor of list(object) attributes (object-list.ts,
// object-list-field.tsx). Keep both sides in sync.

// refOption is one entry of the Link to… picker: a path after the node
// address (or a whole var./local. expression) and its type.
type refOption struct {
	path string
	typ  json.RawMessage
}

// pickerTop mirrors references.ts linkableAttributes: well-known outputs
// first, then every computed attribute (sorted).
func pickerTop(b model.SchemaBlock) []refOption {
	var out []refOption
	for _, p := range pickerAttrs {
		for _, a := range b.Attributes {
			if a.Name == p {
				out = append(out, refOption{a.Name, a.Type})
			}
		}
	}
	var computed []refOption
	for _, a := range b.Attributes {
		if a.Computed && !slices.Contains(pickerAttrs, a.Name) {
			computed = append(computed, refOption{a.Name, a.Type})
		}
	}
	sort.Slice(computed, func(i, j int) bool { return computed[i].path < computed[j].path })
	return append(out, computed...)
}

// ctyObjectAttrs returns the fields of an object type (["object", {…}]).
func ctyObjectAttrs(t json.RawMessage) map[string]json.RawMessage {
	var v []json.RawMessage
	if json.Unmarshal(t, &v) != nil || len(v) < 2 || string(v[0]) != `"object"` {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(v[1], &fields) != nil {
		return nil
	}
	return fields
}

// ctyCollection splits ["list"|"set"|"map", T] into its kind and element.
func ctyCollection(t json.RawMessage) (string, json.RawMessage) {
	var v []json.RawMessage
	if json.Unmarshal(t, &v) != nil || len(v) != 2 {
		return "", nil
	}
	var kind string
	if json.Unmarshal(v[0], &kind) != nil {
		return "", nil
	}
	return kind, v[1]
}

// pickerListElements mirrors references.ts LIST_ELEMENTS.
const pickerListElements = 3

// pickerNested mirrors references.ts nestedReferences (the "More…" level):
// computed attributes of list/single/group blocks two levels deep, the
// fields of element [0] of listed list(object) attributes and elements
// [0]…[2] of listed lists of primitives.
func pickerNested(b model.SchemaBlock) []refOption {
	var out []refOption
	var walk func(b model.SchemaBlock, prefix string, depth int)
	walk = func(b model.SchemaBlock, prefix string, depth int) {
		blocks := slices.Clone(b.Blocks)
		sort.Slice(blocks, func(i, j int) bool { return blocks[i].Name < blocks[j].Name })
		for _, nb := range blocks {
			if nb.Nesting != "list" && nb.Nesting != "single" && nb.Nesting != "group" {
				continue
			}
			at := prefix + nb.Name
			if nb.Nesting == "list" {
				at += "[0]"
			}
			attrs := slices.Clone(nb.Block.Attributes)
			sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name < attrs[j].Name })
			for _, a := range attrs {
				if a.Computed {
					out = append(out, refOption{at + "." + a.Name, a.Type})
				}
			}
			if depth < 2 {
				walk(nb.Block, at+".", depth+1)
			}
		}
	}
	walk(b, "", 1)
	for _, top := range pickerTop(b) {
		kind, elem := ctyCollection(top.typ)
		if kind != "list" {
			continue
		}
		if fields := ctyObjectAttrs(elem); fields != nil {
			for _, name := range slices.Sorted(maps.Keys(fields)) {
				out = append(out, refOption{top.path + "[0]." + name, fields[name]})
			}
		} else if k := ctyKind(elem); k == "string" || k == "number" || k == "bool" {
			for i := range pickerListElements {
				out = append(out, refOption{fmt.Sprintf("%s[%d]", top.path, i), elem})
			}
		}
	}
	return out
}

var typeCallRE = regexp.MustCompile(`^(list|set|map)\((.+)\)$`)

// parseTypeExpr mirrors references.ts parseTypeExpr: nil for types the
// picker can't match.
func parseTypeExpr(src string) json.RawMessage {
	s := strings.Join(strings.Fields(src), "")
	switch s {
	case "", "any":
		return json.RawMessage(`"dynamic"`)
	case "string", "number", "bool":
		return json.RawMessage(`"` + s + `"`)
	}
	m := typeCallRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	inner := parseTypeExpr(m[2])
	if inner == nil {
		return nil
	}
	return json.RawMessage(`["` + m[1] + `",` + string(inner) + `]`)
}

// localType mirrors references.ts localType.
func localType(v model.Value) json.RawMessage {
	var lit any
	if len(v.Literal) == 0 || json.Unmarshal(v.Literal, &lit) != nil {
		return json.RawMessage(`"dynamic"`)
	}
	switch x := lit.(type) {
	case string:
		return json.RawMessage(`"string"`)
	case float64:
		return json.RawMessage(`"number"`)
	case bool:
		return json.RawMessage(`"bool"`)
	case []any:
		if !slices.ContainsFunc(x, func(e any) bool { _, s := e.(string); return !s }) {
			return json.RawMessage(`["list","string"]`)
		}
	case map[string]any:
		for _, e := range x {
			if _, s := e.(string); !s {
				return json.RawMessage(`"dynamic"`)
			}
		}
		return json.RawMessage(`["map","string"]`)
	}
	return json.RawMessage(`"dynamic"`)
}

// scopeRefs mirrors references.ts scopeReferences: var.<name> (typed by
// its `type`) and local.<name>.
func scopeRefs(wf *model.Workflow) []refOption {
	var out []refOption
	for _, v := range wf.Variables {
		src := ""
		if a := bodyAttr(&v.Body, "type"); a != nil {
			src = a.Value.Expr
		}
		if t := parseTypeExpr(src); t != nil {
			out = append(out, refOption{"var." + v.Name, t})
		}
	}
	for _, l := range wf.Locals {
		out = append(out, refOption{"local." + l.Name, localType(l.Value)})
	}
	return out
}

// refFit mirrors references.ts refFit: "replace", "append" (a new element
// of a list of strings) or "" (doesn't fit without an expression).
func refFit(to, from json.RawMessage) string {
	if string(to) == `"dynamic"` || string(from) == `"dynamic"` {
		return "replace"
	}
	if linkFits(to, from) {
		return "replace"
	}
	switch fk := ctyKind(from); {
	case ctyKind(to) == "string-list" && (fk == "string" || fk == "number" || fk == "bool"):
		return "append"
	}
	return ""
}

// linkRE splits a picked reference into the node address (data.<type>.<name>
// or <type>.<name>), its instance key ([0], ["key"]) and the path after it.
var linkRE = regexp.MustCompile(`^((?:data\.)?[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)(\[[^\]]*\])?\.(.+)$`)

func linkTarget(link string) (addr, instance, path string, ok bool) {
	m := linkRE.FindStringSubmatch(link)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// countInstances mirrors references.ts COUNT_INSTANCES.
const countInstances = 3

// forEachKeys mirrors references.ts forEachKeys: the keys of a written-down
// for_each value (literal, toset([…]), a local or a variable default).
func forEachKeys(v *model.Value, wf *model.Workflow, depth int) ([]string, bool) {
	if v == nil || depth > 3 {
		return nil, false
	}
	if len(v.Literal) > 0 {
		var list []string
		if json.Unmarshal(v.Literal, &list) == nil {
			return list, true
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v.Literal, &obj) == nil {
			// Object key order: as written in the expression when there is one.
			if v.Expr != "" {
				if items, ok := hclObjectConstructor(v.Expr); ok {
					return objectKeys(items), true
				}
			}
			return slices.Sorted(maps.Keys(obj)), true
		}
		return nil, false
	}
	e := strings.TrimSpace(v.Expr)
	if m := regexp.MustCompile(`^toset\(([\s\S]+)\)$`).FindStringSubmatch(e); m != nil {
		return forEachKeys(&model.Value{Expr: m[1]}, wf, depth+1)
	}
	if m := regexp.MustCompile(`^local\.([\w-]+)$`).FindStringSubmatch(e); m != nil {
		for _, l := range wf.Locals {
			if l.Name == m[1] {
				return forEachKeys(&l.Value, wf, depth+1)
			}
		}
		return nil, false
	}
	if m := regexp.MustCompile(`^var\.([\w-]+)$`).FindStringSubmatch(e); m != nil {
		for _, x := range wf.Variables {
			if x.Name == m[1] {
				if a := bodyAttr(&x.Body, "default"); a != nil {
					return forEachKeys(&a.Value, wf, depth+1)
				}
			}
		}
		return nil, false
	}
	if items, ok := hclObjectConstructor(e); ok {
		return objectKeys(items), true
	}
	items, ok := listItems(&model.Value{Expr: e})
	if !ok {
		return nil, false
	}
	var out []string
	for _, it := range items {
		s, isText := hclStringLiteral(it)
		if !isText {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func objectKeys(items []hclObjectItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.key)
	}
	return out
}

// instanceKeys mirrors references.ts instanceKeys: nil for a single
// instance, [0]… for count, ["key"] for written-down for_each keys (an
// empty, non-nil list when they aren't known).
func instanceKeys(n *model.Node, wf *model.Workflow) []string {
	if c := bodyAttr(&n.Body, "count"); c != nil {
		count := countInstances
		var num float64
		if len(c.Value.Literal) > 0 && json.Unmarshal(c.Value.Literal, &num) == nil {
			count = max(min(int(num), countInstances), 0)
		}
		out := []string{}
		for i := range count {
			out = append(out, fmt.Sprintf("[%d]", i))
		}
		return out
	}
	if f := bodyAttr(&n.Body, "for_each"); f != nil {
		keys, _ := forEachKeys(&f.Value, wf, 0)
		out := []string{}
		for _, k := range keys {
			data, _ := json.Marshal(k)
			out = append(out, "["+string(data)+"]")
		}
		return out
	}
	return nil
}

// hclObjectItem is one `key = value` of an object constructor.
type hclObjectItem struct {
	key, src string
}

var hclItemRE = regexp.MustCompile(`^("(?:[^"\\]|\\.)*"|[A-Za-z_][\w-]*)\s*[=:]\s*([\s\S]+)$`)

// hclObjectConstructor mirrors body.ts objectConstructor.
func hclObjectConstructor(src string) ([]hclObjectItem, bool) {
	s := strings.TrimSpace(src)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, false
	}
	pieces, ok := hclSplitTopLevel(s[1:len(s)-1], true)
	if !ok || len(pieces) > 0 && forRE.MatchString(pieces[0]) {
		return nil, false
	}
	var out []hclObjectItem
	for _, p := range pieces {
		m := hclItemRE.FindStringSubmatch(p)
		if m == nil || strings.HasPrefix(m[2], "=") {
			return nil, false
		}
		key := m[1]
		if strings.HasPrefix(key, `"`) {
			if json.Unmarshal([]byte(key), &key) != nil {
				return nil, false
			}
		}
		out = append(out, hclObjectItem{key, strings.TrimSpace(m[2])})
	}
	return out, true
}

// --- list values (body.ts) ---

// hclSkipString mirrors body.ts skipString.
func hclSkipString(src string, i int) int {
	i++
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\\':
			i += 2
		case c == '"':
			return i + 1
		case (c == '$' || c == '%') && i+2 < len(src) && src[i+1] == c && src[i+2] == '{':
			i += 3
		case (c == '$' || c == '%') && i+1 < len(src) && src[i+1] == '{':
			depth := 1
			i += 2
			for i < len(src) && depth > 0 {
				if src[i] == '"' {
					if i = hclSkipString(src, i); i < 0 {
						return -1
					}
					continue
				}
				if src[i] == '{' {
					depth++
				} else if src[i] == '}' {
					depth--
				}
				i++
			}
		default:
			i++
		}
	}
	return -1
}

// hclSplitTopLevel mirrors body.ts splitTopLevel.
func hclSplitTopLevel(src string, newlines bool) ([]string, bool) {
	var out []string
	depth, start, i := 0, 0, 0
	for i < len(src) {
		c := src[i]
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch {
		case c == '"':
			if i = hclSkipString(src, i); i < 0 {
				return nil, false
			}
			continue
		case c == '#' || c == '/' && (next == '/' || next == '*') || c == '<' && next == '<':
			return nil, false
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth--; depth < 0 {
				return nil, false
			}
		case depth == 0 && (c == ',' || newlines && c == '\n'):
			out = append(out, src[start:i])
			start = i + 1
		}
		i++
	}
	if depth != 0 {
		return nil, false
	}
	out = append(out, src[start:])
	var items []string
	for _, s := range out {
		if s = strings.TrimSpace(s); s != "" {
			items = append(items, s)
		}
	}
	return items, true
}

// hclStringLiteral mirrors body.ts stringLiteral.
func hclStringLiteral(src string) (string, bool) {
	if !strings.HasPrefix(src, `"`) || strings.Contains(src, "${") || strings.Contains(src, "%{") {
		return "", false
	}
	var s string
	return s, json.Unmarshal([]byte(src), &s) == nil
}

var forRE = regexp.MustCompile(`^for\s`)

// listItems mirrors body.ts listItems: the HCL source of each element of a
// literal array or a `[a, b]` constructor; false for other expressions.
func listItems(v *model.Value) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	if v.Expr == "" {
		var lit []any
		if json.Unmarshal(v.Literal, &lit) != nil {
			return nil, false
		}
		var out []string
		for _, x := range lit {
			data, _ := json.Marshal(x)
			out = append(out, string(data))
		}
		return out, true
	}
	s := strings.TrimSpace(v.Expr)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, false
	}
	items, ok := hclSplitTopLevel(s[1:len(s)-1], false)
	if !ok || len(items) > 0 && forRE.MatchString(items[0]) {
		return nil, false
	}
	return items, true
}

// listValue mirrors body.ts listValue: a literal when every element is a
// quoted string, nil when empty.
func listValue(items []string) *model.Value {
	if len(items) == 0 {
		return nil
	}
	var strs []string
	for _, it := range items {
		s, ok := hclStringLiteral(it)
		if !ok {
			return &model.Value{Expr: "[" + strings.Join(items, ", ") + "]"}
		}
		strs = append(strs, s)
	}
	data, _ := json.Marshal(strs)
	return &model.Value{Literal: data}
}

// appendToList mirrors body.ts appendToList.
func appendToList(v *model.Value, ref string) *model.Value {
	items, _ := listItems(v)
	if v != nil && slices.Contains(items, ref) {
		return v
	}
	return listValue(append(slices.Clone(items), ref))
}

// --- object lists (object-list.ts, object-list-field.tsx) ---

// objectListFields mirrors object-list.ts objectListFields: the object
// fields of a list(object)/set(object) attribute, nil otherwise.
func objectListFields(t json.RawMessage) map[string]json.RawMessage {
	kind, elem := ctyCollection(t)
	if kind != "list" && kind != "set" {
		return nil
	}
	return ctyObjectAttrs(elem)
}

// setObjectList is the object-list editor: the cards of a list(object)
// attribute written as blocks (attributes-as-blocks syntax, what the
// starters use and the editor's Add writes). The value lists every card:
// existing ones are restated. Each card field takes what its field widget
// writes; a list field of texts and {link} items is the tag input plus
// Link to… appends.
func setObjectList(body *model.Body, sa model.SchemaAttribute, v formValue) error {
	fields := objectListFields(sa.Type)
	if bodyAttr(body, sa.Name) != nil {
		return fmt.Errorf("%s has a value (not blocks): the audit edits the cards of attributes-as-blocks only", sa.Name)
	}
	var blocks []model.Block
	for i, item := range v.seq.Content {
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("card %d: write a map of field: value", i+1)
		}
		card := emptyNocodeBody()
		for j := 0; j < len(item.Content); j += 2 {
			name, vn := item.Content[j].Value, item.Content[j+1]
			typ, ok := fields[name]
			if !ok {
				return fmt.Errorf("card %d: %s is not a field of %s (%s)", i+1, name, sa.Name, strings.Join(slices.Sorted(maps.Keys(fields)), ", "))
			}
			fv, err := v.field(vn)
			if err != nil {
				return fmt.Errorf("card %d: %s: %w", i+1, name, err)
			}
			if fv.seq != nil {
				val, err := listParts(fv, typ)
				if err != nil {
					return fmt.Errorf("card %d: %s: %w", i+1, name, err)
				}
				setBodyAttr(&card, name, val)
				continue
			}
			field := model.SchemaBlock{Attributes: []model.SchemaAttribute{{Name: name, Type: typ, Optional: true}}}
			if err := setFormAttr(&card, field, name, fv, 1); err != nil {
				return fmt.Errorf("card %d: %s: %w", i+1, name, err)
			}
		}
		blocks = append(blocks, model.Block{Type: sa.Name, Body: card})
	}
	setBodyBlocks(body, sa.Name, blocks)
	return nil
}

// listParts is a list field of texts (tag input) and {link: …} items
// (Link to… appending; the chips' input adds texts after them).
func listParts(v formValue, typ json.RawMessage) (*model.Value, error) {
	if ctyKind(typ) != "string-list" {
		return nil, fmt.Errorf("a list of texts and links only goes into a list of strings, not %s", typ)
	}
	var items []string
	for _, el := range v.seq.Content {
		if el.Kind == yaml.ScalarNode {
			data, _ := json.Marshal(el.Value)
			items = append(items, string(data))
			continue
		}
		fv, err := v.field(el)
		if err != nil {
			return nil, err
		}
		if fv.link == "" || refFit(typ, fv.linkType) != "append" {
			return nil, fmt.Errorf("list items are texts or {link: …} of a single value (Link to… appends it)")
		}
		if !slices.Contains(items, fv.link) {
			items = append(items, fv.link)
		}
	}
	return listValue(items), nil
}

// removeChip is the X of one chip of a list field showing references
// (attr-field.tsx ListChips, body.ts removeListItem): the element whose
// source (a reference) or text is item. Constant lists show a tag input
// instead (write the remaining tags as a literal).
func removeChip(body *model.Body, sa model.SchemaAttribute, item string) error {
	a := bodyAttr(body, sa.Name)
	if ctyKind(sa.Type) != "string-list" || a == nil || len(a.Value.Literal) > 0 {
		return fmt.Errorf("%s doesn't show chips (only a list of strings holding references does)", sa.Name)
	}
	items, ok := listItems(&a.Value)
	if !ok {
		return fmt.Errorf("%s is an expression, not a list of chips", sa.Name)
	}
	i := slices.IndexFunc(items, func(s string) bool {
		text, isText := hclStringLiteral(s)
		return s == item || isText && text == item
	})
	if i < 0 {
		return fmt.Errorf("%s has no chip %q (chips: %s)", sa.Name, item, strings.Join(items, ", "))
	}
	setBodyAttr(body, sa.Name, listValue(slices.Delete(items, i, i+1)))
	return nil
}

// --- map values (body.ts mapEntries/mapValue, map-field.tsx) ---

// mapEntries mirrors body.ts mapEntries: the entries of a literal object or
// a `{ k = v }` constructor as HCL sources; false for other expressions.
func mapEntries(v *model.Value) ([]hclObjectItem, bool) {
	if v == nil {
		return nil, true
	}
	if v.Expr != "" {
		return hclObjectConstructor(v.Expr)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(v.Literal, &obj) != nil {
		return nil, false
	}
	var out []hclObjectItem
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		out = append(out, hclObjectItem{k, string(obj[k])})
	}
	return out, true
}

var hclIdentRE = regexp.MustCompile(`^[A-Za-z_][\w-]*$`)

// hclConstant mirrors body.ts sourceValue: a JSON source without
// interpolation is a literal.
func hclConstant(src string) bool {
	return json.Valid([]byte(src)) && !strings.Contains(src, "${") && !strings.Contains(src, "%{")
}

// mapValueOf mirrors body.ts mapValue: a literal object when every value is
// constant, a `{ k = v }` constructor otherwise, nil when empty.
func mapValueOf(items []hclObjectItem) *model.Value {
	if len(items) == 0 {
		return nil
	}
	if !slices.ContainsFunc(items, func(it hclObjectItem) bool { return !hclConstant(it.src) }) {
		var b strings.Builder
		b.WriteString("{")
		for i, it := range items {
			if i > 0 {
				b.WriteString(",")
			}
			key, _ := json.Marshal(it.key)
			b.Write(key)
			b.WriteString(":")
			b.WriteString(it.src)
		}
		b.WriteString("}")
		return &model.Value{Literal: json.RawMessage(b.String())}
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		key := it.key
		if !hclIdentRE.MatchString(key) {
			data, _ := json.Marshal(key)
			key = string(data)
		}
		lines = append(lines, "  "+key+" = "+it.src)
	}
	return &model.Value{Expr: "{\n" + strings.Join(lines, "\n") + "\n}"}
}

// setMapRows is the map editor of a map(string) attribute (map-field.tsx):
// the final rows, each a typed text, {keep: true} (a row holding an
// expression left untouched), or a Link to… pick ({link: X}; {link: X,
// base64: true} on a user_data row).
func setMapRows(body *model.Body, sa model.SchemaAttribute, v formValue) error {
	if ctyKind(sa.Type) != "string-map" {
		return fmt.Errorf("rows with links only go into a map of strings, not %s", sa.Type)
	}
	var current []hclObjectItem
	if a := bodyAttr(body, sa.Name); a != nil {
		items, ok := mapEntries(&a.Value)
		if !ok {
			return fmt.Errorf("%s is an expression (%s): the inspector shows an expression box, not rows", sa.Name, a.Value.Expr)
		}
		current = items
	}
	var rows []hclObjectItem
	for i := 0; i < len(v.mapNode.Content); i += 2 {
		key, vn := v.mapNode.Content[i].Value, v.mapNode.Content[i+1]
		if vn.Kind == yaml.ScalarNode {
			data, _ := json.Marshal(vn.Value)
			rows = append(rows, hclObjectItem{key, string(data)})
			continue
		}
		fv, err := v.field(vn)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		switch {
		case fv.keep:
			j := slices.IndexFunc(current, func(it hclObjectItem) bool { return it.key == key })
			if j < 0 {
				return fmt.Errorf("%s: no such row to keep", key)
			}
			rows = append(rows, current[j])
		case fv.link != "" && refFit(json.RawMessage(`"string"`), fv.linkType) != "replace":
			return fmt.Errorf("%s: Link to… on a map value lists text values only, not %s (%s)", key, fv.link, fv.linkType)
		case fv.link != "" && fv.base64:
			if key != "user_data" {
				return fmt.Errorf("%s: Base64-encode is offered on user_data rows only", key)
			}
			rows = append(rows, hclObjectItem{key, "base64encode(" + fv.link + ")"})
		case fv.link != "" && !fv.appendLink:
			rows = append(rows, hclObjectItem{key, fv.link})
		default:
			return fmt.Errorf("%s: a row is a text, {link: …} or {keep: true}", key)
		}
	}
	setBodyAttr(body, sa.Name, mapValueOf(rows))
	return nil
}

// setBase64Link is Link to… with Base64-encode on a user_data field
// (reference-picker.tsx base64): base64encode(<pick>).
func setBase64Link(body *model.Body, sa model.SchemaAttribute, v formValue) error {
	if sa.Name != "user_data" || ctyKind(sa.Type) != "string" {
		return fmt.Errorf("Base64-encode is offered on user_data fields only")
	}
	if refFit(json.RawMessage(`"string"`), v.linkType) != "replace" {
		return fmt.Errorf("Base64-encode lists text values only, not %s (%s)", v.link, v.linkType)
	}
	setBodyAttr(body, sa.Name, &model.Value{Expr: "base64encode(" + v.link + ")"})
	return nil
}

// --- provider configurations (providers.ts, providers-section.tsx) ---

// providerRegion mirrors providers.ts PROVIDER_REGION.
var providerRegion = map[string]string{"aws": "region", "google": "region", "oci": "region", "azurerm": ""}

func providerAliasOf(p model.NamedBlock) string {
	var alias string
	if a := bodyAttr(&p.Body, "alias"); a != nil {
		_ = json.Unmarshal(a.Value.Literal, &alias)
	}
	return alias
}

// providerUsers mirrors providers.ts providerUsers.
func providerUsers(wf *model.Workflow, p model.NamedBlock) []string {
	alias := providerAliasOf(p)
	var out []string
	for _, n := range wf.Nodes {
		if n.Kind == model.KindModule {
			continue
		}
		set := ""
		if a := bodyAttr(&n.Body, "provider"); a != nil {
			set = strings.TrimSpace(a.Value.Expr)
		}
		if alias != "" && set == p.Name+"."+alias || alias == "" && set == "" && strings.HasPrefix(n.Type, p.Name+"_") {
			out = append(out, hclcodec.Address(&n))
		}
	}
	return out
}

// provider: the Providers section of the workflow overview (inspector with
// nothing selected): add a configuration with an alias and a region (typed,
// or a string variable), or remove one no resource uses.
func (r *nocodeRun) tfProvider(n *yaml.Node) error {
	var a struct {
		Name   string    `yaml:"name"`
		Alias  string    `yaml:"alias"`
		Region yaml.Node `yaml:"region"`
		Remove bool      `yaml:"remove"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	regionAttr, known := providerRegion[a.Name]
	if !known {
		return fmt.Errorf("the section offers the providers %s", strings.Join(slices.Sorted(maps.Keys(providerRegion)), ", "))
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(wf.Providers, func(p model.NamedBlock) bool { return p.Name == a.Name && providerAliasOf(p) == a.Alias })
	if a.Remove {
		if i < 0 {
			return fmt.Errorf("no provider %s alias %q", a.Name, a.Alias)
		}
		if users := providerUsers(wf, wf.Providers[i]); len(users) > 0 {
			return fmt.Errorf("used by %s: the remove button is disabled", strings.Join(users, ", "))
		}
		wf.Providers = slices.Delete(wf.Providers, i, i+1)
		return r.call("PUT", r.wfPath(""), wf, nil)
	}
	if a.Alias != "" && !tfNameRE.MatchString(a.Alias) {
		return fmt.Errorf("alias %q: letters, digits, _ or -, starting with a letter or _", a.Alias)
	}
	if i >= 0 {
		return fmt.Errorf("provider %s alias %q already exists", a.Name, a.Alias)
	}
	body := emptyNocodeBody()
	if a.Alias != "" {
		if err := setLiteral(&body, "alias", a.Alias); err != nil {
			return err
		}
	}
	switch {
	case a.Region.Kind == 0:
	case regionAttr == "":
		return fmt.Errorf("%s has no region field", a.Name)
	case a.Region.Kind == yaml.ScalarNode:
		if err := setLiteral(&body, regionAttr, a.Region.Value); err != nil {
			return err
		}
	case a.Region.Kind == yaml.MappingNode && len(a.Region.Content) == 2 && a.Region.Content[0].Value == "link":
		ref := a.Region.Content[1].Value
		ok := false
		for _, o := range scopeRefs(wf) {
			ok = ok || strings.HasPrefix(o.path, "var.") && o.path == ref && (string(o.typ) == `"string"` || string(o.typ) == `"dynamic"`)
		}
		if !ok {
			return fmt.Errorf("region: the select lists the string variables, not %s", ref)
		}
		setBodyAttr(&body, regionAttr, &model.Value{Expr: ref})
	default:
		return fmt.Errorf("region: a text or {link: var.<name>}")
	}
	if a.Name == "azurerm" {
		body.Blocks = []model.Block{{Type: "features", Body: emptyNocodeBody()}}
	}
	wf.Providers = append(wf.Providers, model.NamedBlock{Name: a.Name, Body: body})
	return r.call("PUT", r.wfPath(""), wf, nil)
}
