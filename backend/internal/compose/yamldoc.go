package compose

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Doc is one YAML file edited in place: edits splice the source lines they
// change (located through the yaml node tree) and leave every other byte as
// it was, so comments, key order, quoting and blank lines survive. After an
// edit the source is parsed again.
type Doc struct {
	Path string
	// Root is the top-level mapping (nil for an empty document).
	Root  *yaml.Node
	lines []string
	crlf  bool
	// unit is the file's indentation step; seqIn whether block sequences
	// under a key are indented ("key:\n  - a") or compact ("key:\n- a").
	unit  int
	seqIn bool
}

// SyntaxError is a YAML error located in a file.
type SyntaxError struct {
	File    string
	Line    int
	Column  int
	Message string
}

func (e *SyntaxError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Message)
}

var yamlLineRe = regexp.MustCompile(`line (\d+)(?:, column (\d+))?:?\s*`)

// ParseDoc parses a YAML file. A syntax error is a *SyntaxError.
func ParseDoc(path string, src []byte) (*Doc, error) {
	text := string(src)
	d := &Doc{Path: path, crlf: strings.Contains(text, "\r\n"), unit: 2, seqIn: true}
	if d.crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	d.lines = strings.Split(text, "\n")
	if err := d.parse(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Doc) parse() error {
	var doc yaml.Node
	err := yaml.Unmarshal([]byte(strings.Join(d.lines, "\n")), &doc)
	if err != nil {
		se := &SyntaxError{File: d.Path, Message: strings.TrimPrefix(err.Error(), "yaml: ")}
		// "while parsing X at line 1, column 3: line 3, column 3: msg": the
		// last position is the problem's.
		if all := yamlLineRe.FindAllStringSubmatchIndex(se.Message, -1); len(all) > 0 {
			m := all[len(all)-1]
			se.Line, _ = strconv.Atoi(se.Message[m[2]:m[3]])
			if m[4] >= 0 {
				se.Column, _ = strconv.Atoi(se.Message[m[4]:m[5]])
			}
			se.Message = strings.TrimSpace(se.Message[:m[0]] + se.Message[m[1]:])
			se.Message = strings.TrimSuffix(strings.ReplaceAll(se.Message, " at <unknown position>:", ":"), ":")
		}
		return se
	}
	d.Root = nil
	if len(doc.Content) > 0 {
		root := doc.Content[0]
		switch {
		case root.Kind == yaml.MappingNode:
			d.Root = root
		case root.Kind == yaml.ScalarNode && root.Tag == "!!null":
		default:
			return &SyntaxError{File: d.Path, Line: root.Line, Column: root.Column, Message: "the top level must be a mapping"}
		}
	}
	d.detectStyle()
	return nil
}

// Bytes is the current source.
func (d *Doc) Bytes() []byte {
	text := strings.Join(d.lines, "\n")
	if d.crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return []byte(text)
}

func (d *Doc) detectStyle() {
	if d.Root == nil {
		return
	}
	found := false
	var walk func(n *yaml.Node, keyCol int)
	walk = func(n *yaml.Node, keyCol int) {
		if found {
			return
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 && v.Content[0].Line > k.Line {
					if step := v.Content[0].Column - k.Column; step > 0 && !found {
						d.unit = step
					}
				}
				if v.Kind == yaml.SequenceNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 && v.Content[0].Line > k.Line {
					d.seqIn = d.dashCol(v.Content[0]) > k.Column
					found = true
					return
				}
				walk(v, k.Column)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, keyCol)
			}
		}
	}
	// The unit comes from the first nested block mapping.
	for i := 0; i+1 < len(d.Root.Content); i += 2 {
		k, v := d.Root.Content[i], d.Root.Content[i+1]
		if v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 && v.Content[0].Line > k.Line {
			d.unit = v.Content[0].Column - k.Column
			break
		}
	}
	if d.unit <= 0 || d.unit > 8 {
		d.unit = 2
	}
	unit := d.unit
	walk(d.Root, 1)
	d.unit = unit
}

// dashCol is the 1-based column of the "-" of a block sequence item.
func (d *Doc) dashCol(item *yaml.Node) int {
	line := d.lines[item.Line-1]
	for c := item.Column - 2; c >= 0; c-- {
		if c < len(line) && line[c] == '-' {
			return c + 1
		}
	}
	return item.Column
}

// ---------------------------------------------------------------------------
// Lookup

// Get returns the key and value nodes at path (mapping keys from the top),
// without following aliases (edits never change an anchor's other users).
// A numeric segment indexes a block sequence on the way ("spec.containers.0
// .image"); the last segment must be a mapping key.
func (d *Doc) Get(path ...string) (key, val *yaml.Node) {
	if d.Root == nil {
		return nil, nil
	}
	cur := d.Root
	for i, p := range path {
		if cur.Kind == yaml.SequenceNode && i < len(path)-1 {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n >= len(cur.Content) {
				return nil, nil
			}
			cur = cur.Content[n]
			continue
		}
		if cur.Kind != yaml.MappingNode {
			return nil, nil
		}
		k, v := mapGet(cur, p)
		if k == nil {
			return nil, nil
		}
		if i == len(path)-1 {
			return k, v
		}
		cur = v
	}
	return nil, cur
}

// Resolve follows an alias.
func Resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func mapGet(m *yaml.Node, key string) (k, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// mergedGet looks key up in m and then in its merge keys (<<: *x).
func mergedGet(m *yaml.Node, key string) *yaml.Node {
	m = Resolve(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	if _, v := mapGet(m, key); v != nil {
		return v
	}
	if _, v := mapGet(m, "<<"); v != nil {
		v = Resolve(v)
		srcs := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			srcs = v.Content
		}
		for _, s := range srcs {
			if r := mergedGet(s, key); r != nil {
				return r
			}
		}
	}
	return nil
}

// Line is the 1-based line of the key at path (0 when absent).
func (d *Doc) Line(path ...string) int {
	k, _ := d.Get(path...)
	if k == nil {
		return 0
	}
	return k.Line
}

// LocatePath finds the line of a dotted schema path ("services.web.ports.0"),
// walking as deep as the document goes.
func (d *Doc) LocatePath(parts []string) int {
	if d.Root == nil {
		return 1
	}
	line := 1
	cur := d.Root
	for _, p := range parts {
		cur = Resolve(cur)
		switch cur.Kind {
		case yaml.MappingNode:
			k, v := mapGet(cur, p)
			if k == nil {
				return line
			}
			line, cur = k.Line, v
		case yaml.SequenceNode:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(cur.Content) {
				return line
			}
			cur = cur.Content[i]
			line = cur.Line
		default:
			return line
		}
	}
	return line
}

// ---------------------------------------------------------------------------
// Extents

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func blank(line string) bool { return strings.TrimSpace(line) == "" }

func isComment(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }

// lastLine is the last 1-based line holding part of n.
func lastLine(n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		if l := lastLine(c); l > last {
			last = l
		}
	}
	return last
}

// span is the 0-based inclusive line range of a mapping entry or sequence
// item starting at line start (1-based) whose key or dash sits at column
// col (1-based) and whose value is v: every line up to the value's last
// node plus the more-indented lines after it (block scalars, multi-line
// flow values, commented-out lines), without trailing blank lines.
func (d *Doc) span(start, col int, v *yaml.Node) (int, int) {
	end := start - 1
	if v != nil && v.Kind != yaml.AliasNode {
		if l := lastLine(v) - 1; l > end {
			end = l
		}
	}
	for j := end + 1; j < len(d.lines); j++ {
		line := d.lines[j]
		if blank(line) || indentOf(line) > col-1 {
			end = j
			continue
		}
		break
	}
	for end > start-1 && blank(d.lines[end]) {
		end--
	}
	return start - 1, end
}

// entrySpan is the line range of the mapping entry (k, v).
func (d *Doc) entrySpan(k, v *yaml.Node) (int, int) { return d.span(k.Line, k.Column, v) }

// itemSpan is the line range of the sequence item n.
func (d *Doc) itemSpan(n *yaml.Node) (int, int) {
	return d.span(n.Line, d.dashCol(n), n)
}

// headComments extends a range up over the comment lines directly above it
// at the same indentation (they describe what is deleted).
func (d *Doc) headComments(from, col int) int {
	for from > 0 && isComment(d.lines[from-1]) && indentOf(d.lines[from-1]) == col-1 {
		from--
	}
	return from
}

func (d *Doc) replaceLines(from, to int, repl []string) error {
	lines := make([]string, 0, len(d.lines)-(to-from+1)+len(repl))
	lines = append(lines, d.lines[:from]...)
	lines = append(lines, repl...)
	lines = append(lines, d.lines[to+1:]...)
	old := d.lines
	d.lines = lines
	if err := d.parse(); err != nil {
		d.lines = old
		_ = d.parse()
		return fmt.Errorf("edit of %s produced invalid YAML: %w", d.Path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Edits

// Set writes value at path: a scalar keeps its quoting and line comment,
// anything else replaces the entry; missing parents are created.
func (d *Doc) Set(path []string, value *yaml.Node) error {
	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}
	if k, v := d.Get(path...); k != nil {
		return d.replaceValue(k, v, value)
	}
	parentPath := path[:len(path)-1]
	key := path[len(path)-1]
	var parent *yaml.Node
	if len(parentPath) == 0 {
		parent = d.Root
	} else {
		parent = d.Node(parentPath...)
	}
	if parent == nil && len(parentPath) > 0 {
		if _, err := strconv.Atoi(parentPath[len(parentPath)-1]); err == nil {
			return fmt.Errorf("%s: no item %s", d.Path, strings.Join(parentPath, "."))
		}
		// Create the parent with this entry inside.
		return d.Set(parentPath, Map(key, value))
	}
	if parent != nil && (parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0) && len(parentPath) > 0 {
		if _, err := strconv.Atoi(parentPath[len(parentPath)-1]); err == nil {
			return fmt.Errorf("%s: item %s is not a block mapping", d.Path, strings.Join(parentPath, "."))
		}
	}
	return d.insertEntry(parentPath, parent, key, value)
}

// Node is the node at path: Get's value, or a sequence item when the last
// segment indexes one.
func (d *Doc) Node(path ...string) *yaml.Node {
	if len(path) == 0 {
		return d.Root
	}
	last := path[len(path)-1]
	if n, err := strconv.Atoi(last); err == nil {
		var seq *yaml.Node
		if len(path) == 1 {
			seq = d.Root
		} else {
			_, seq = d.Get(path[:len(path)-1]...)
		}
		if seq == nil || seq.Kind != yaml.SequenceNode || n < 0 || n >= len(seq.Content) {
			return nil
		}
		return seq.Content[n]
	}
	_, v := d.Get(path...)
	return v
}

// replaceValue replaces the value of the entry (k, v).
func (d *Doc) replaceValue(k, v, value *yaml.Node) error {
	if v.Kind == yaml.ScalarNode && value.Kind == yaml.ScalarNode && v.Line == k.Line &&
		v.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 && !strings.Contains(value.Value, "\n") {
		if text, ok := d.scalarToken(v); ok {
			line := d.lines[v.Line-1]
			start := v.Column - 1
			if v.Anchor != "" || v.Tag != "" && v.Style&yaml.TaggedStyle != 0 {
				// keep "&anchor"/"!tag" prefixes: the token starts after them
				start = strings.Index(line[start:], text) + start
			}
			nv := *value
			if value.Style == 0 && v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 && value.Tag != "!!int" && value.Tag != "!!bool" {
				nv.Style = v.Style & (yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle)
			}
			repl := line[:start] + scalarText(&nv) + line[start+len(text):]
			return d.replaceLines(v.Line-1, v.Line-1, []string{repl})
		}
	}
	from, to := d.entrySpan(k, v)
	r := d.renderer()
	lines := r.entry(d.keyText(k), value, k.Column-1, d.lines[k.Line-1])
	return d.replaceLines(from, to, lines)
}

// keyText is the key as written (quotes kept).
func (d *Doc) keyText(k *yaml.Node) string {
	if t, ok := d.scalarToken(k); ok {
		return t
	}
	return scalarText(k)
}

// scalarToken is the source text of a one-line scalar.
func (d *Doc) scalarToken(n *yaml.Node) (string, bool) {
	if n.Line < 1 || n.Line > len(d.lines) {
		return "", false
	}
	line := d.lines[n.Line-1]
	start := n.Column - 1
	if start >= len(line) {
		return "", false
	}
	rest := line[start:]
	if n.Anchor != "" && strings.HasPrefix(rest, "&") {
		_, rest, _ = strings.Cut(rest, " ")
		rest = strings.TrimLeft(rest, " ")
	}
	if strings.HasPrefix(rest, "!") {
		_, rest, _ = strings.Cut(rest, " ")
		rest = strings.TrimLeft(rest, " ")
	}
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			switch rest[i] {
			case '\\':
				i++
			case '"':
				return rest[:i+1], true
			}
		}
		return "", false
	case n.Style&yaml.SingleQuotedStyle != 0:
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\'' {
				if i+1 < len(rest) && rest[i+1] == '\'' {
					i++
					continue
				}
				return rest[:i+1], true
			}
		}
		return "", false
	case n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return "", false
	}
	// Plain: a one-line plain scalar's source is its value.
	if strings.HasPrefix(rest, n.Value) && n.Value != "" {
		after := rest[len(n.Value):]
		if after == "" || strings.ContainsAny(after[:1], " \t,]}:") {
			return n.Value, true
		}
	}
	return "", false
}

// insertEntry adds key: value to the mapping parent (at parentPath).
func (d *Doc) insertEntry(parentPath []string, parent *yaml.Node, key string, value *yaml.Node) error {
	r := d.renderer()
	if d.Root == nil {
		lines := r.entry(plainKey(key), value, 0, "")
		// Keep leading comments of an empty file; append after them.
		end := len(d.lines) - 1
		for end >= 0 && blank(d.lines[end]) {
			end--
		}
		repl := append(slices.Clone(d.lines[:end+1]), lines...)
		repl = append(repl, "")
		old := d.lines
		d.lines = repl
		if err := d.parse(); err != nil {
			d.lines = old
			_ = d.parse()
			return err
		}
		return nil
	}
	if parent.Kind == yaml.AliasNode || parent.Kind == yaml.MappingNode && parent.Style&yaml.FlowStyle != 0 ||
		parent.Kind == yaml.ScalarNode {
		// Rewrite the parent in block style with the new entry: an alias
		// becomes a merge of it, a flow mapping is expanded, null is
		// replaced.
		var next *yaml.Node
		switch parent.Kind {
		case yaml.AliasNode:
			next = Map("<<", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!raw", Value: "*" + parent.Value}, key, value)
		case yaml.MappingNode:
			next = &yaml.Node{Kind: yaml.MappingNode}
			next.Content = append(slices.Clone(parent.Content), &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
		default:
			if parent.Tag != "!!null" {
				return fmt.Errorf("%s: %s is not a mapping", d.Path, strings.Join(parentPath, "."))
			}
			next = Map(key, value)
		}
		pk, pv := d.Get(parentPath...)
		from, to := d.entrySpan(pk, pv)
		head := d.lines[pk.Line-1]
		return d.replaceLines(from, to, r.entry(d.keyText(pk), next, pk.Column-1, head))
	}
	if parent.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: %s is not a mapping", d.Path, strings.Join(parentPath, "."))
	}
	var col int
	var after int // 0-based line to insert after
	separated := false
	if n := len(parent.Content); n >= 2 {
		lk, lv := parent.Content[n-2], parent.Content[n-1]
		col = parent.Content[0].Column - 1
		_, after = d.entrySpan(lk, lv)
		// Entries set apart by blank lines (services, usually) get one too.
		if n >= 4 {
			from := d.headComments(lk.Line-1, lk.Column)
			separated = from > 0 && blank(d.lines[from-1])
		}
	} else {
		pk, _ := d.Get(parentPath...)
		col = pk.Column - 1 + d.unit
		after = pk.Line - 1
	}
	repl := []string{d.lines[after]}
	if separated {
		repl = append(repl, "")
	}
	repl = append(repl, r.entry(plainKey(key), value, col, "")...)
	return d.replaceLines(after, after, repl)
}

// Delete removes the entry at path (and the comment lines right above it).
// pruneEmpty also removes parents left empty, except top-level sections,
// which become {}.
func (d *Doc) Delete(path []string, pruneEmpty bool) error {
	k, v := d.Get(path...)
	if k == nil {
		return nil
	}
	if len(path) > 1 {
		_, parent := d.Get(path[:len(path)-1]...)
		if parent.Style&yaml.FlowStyle != 0 {
			next := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
			for i := 0; i+1 < len(parent.Content); i += 2 {
				if parent.Content[i] != k {
					next.Content = append(next.Content, parent.Content[i], parent.Content[i+1])
				}
			}
			if len(next.Content) == 0 && pruneEmpty && len(path) > 2 {
				return d.Delete(path[:len(path)-1], pruneEmpty)
			}
			return d.Set(path[:len(path)-1], next)
		}
		if len(parent.Content) == 2 {
			if pruneEmpty && len(path) > 2 {
				return d.Delete(path[:len(path)-1], pruneEmpty)
			}
			return d.Set(path[:len(path)-1], &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle})
		}
	}
	from, to := d.entrySpan(k, v)
	from = d.headComments(from, k.Column)
	// Drop the blank line that separated the entry from the next one when
	// nothing separates it from the previous one (first entry, or a blank
	// line above): the remaining entries keep one separator.
	if to+1 < len(d.lines) && blank(d.lines[to+1]) && (from == 0 || blank(d.lines[from-1]) || indentOf(d.lines[from-1]) < k.Column-1) {
		to++
	}
	return d.replaceLines(from, to, nil)
}

// RenameKey renames the key at path in place.
func (d *Doc) RenameKey(path []string, name string) error {
	k, _ := d.Get(path...)
	if k == nil {
		return fmt.Errorf("%s: %s not found", d.Path, strings.Join(path, "."))
	}
	line := d.lines[k.Line-1]
	tok := d.keyText(k)
	start := k.Column - 1
	if !strings.HasPrefix(line[start:], tok) {
		return fmt.Errorf("%s:%d: can't locate key %s", d.Path, k.Line, k.Value)
	}
	nk := &yaml.Node{Kind: yaml.ScalarNode, Value: name, Style: k.Style}
	return d.replaceLines(k.Line-1, k.Line-1, []string{line[:start] + scalarText(nk) + line[start+len(tok):]})
}

// AppendItem adds an item to the sequence at path (created when missing).
func (d *Doc) AppendItem(path []string, item *yaml.Node) error {
	k, v := d.Get(path...)
	if k == nil {
		return d.Set(path, Seq(item))
	}
	if v.Kind != yaml.SequenceNode || v.Style&yaml.FlowStyle != 0 || len(v.Content) == 0 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Style: v.Style & yaml.FlowStyle, Tag: localTag(v)}
		if v.Kind == yaml.SequenceNode {
			next.Content = append(slices.Clone(v.Content), item)
		} else if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			next.Content = []*yaml.Node{item}
			next.Style = 0
		} else {
			return fmt.Errorf("%s: %s is not a list", d.Path, strings.Join(path, "."))
		}
		return d.replaceValue(k, v, next)
	}
	last := v.Content[len(v.Content)-1]
	_, after := d.itemSpan(last)
	dash := d.dashCol(v.Content[0]) - 1
	r := d.renderer()
	lines := r.item(item, dash)
	repl := append([]string{d.lines[after]}, lines...)
	return d.replaceLines(after, after, repl)
}

// DeleteItem removes item i of the sequence at path; an emptied list is
// removed when pruneEmpty, else becomes [].
func (d *Doc) DeleteItem(path []string, i int, pruneEmpty bool) error {
	k, v := d.Get(path...)
	if k == nil || v.Kind != yaml.SequenceNode || i < 0 || i >= len(v.Content) {
		return nil
	}
	if len(v.Content) == 1 {
		if pruneEmpty {
			return d.Delete(path, true)
		}
		return d.replaceValue(k, v, &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Tag: localTag(v)})
	}
	if v.Style&yaml.FlowStyle != 0 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Tag: localTag(v)}
		next.Content = slices.Delete(slices.Clone(v.Content), i, i+1)
		return d.replaceValue(k, v, next)
	}
	from, to := d.itemSpan(v.Content[i])
	return d.replaceLines(from, to, nil)
}

// SetItem replaces item i of the sequence at path (a scalar keeps its
// quoting and line comment, also inside a flow sequence).
func (d *Doc) SetItem(path []string, i int, item *yaml.Node) error {
	k, v := d.Get(path...)
	if k == nil || v.Kind != yaml.SequenceNode || i < 0 || i >= len(v.Content) {
		return fmt.Errorf("%s: no item %d in %s", d.Path, i, strings.Join(path, "."))
	}
	old := v.Content[i]
	if old.Kind == yaml.ScalarNode && item.Kind == yaml.ScalarNode && !strings.Contains(item.Value, "\n") &&
		old.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
		if text, ok := d.scalarToken(old); ok {
			line := d.lines[old.Line-1]
			start := old.Column - 1
			nv := *item
			if item.Style == 0 && old.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
				nv.Style = old.Style & (yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle)
			}
			repl := scalarText(&nv)
			if v.Style&yaml.FlowStyle != 0 && nv.Style == 0 && strings.ContainsAny(repl, ",[]{}") {
				repl = doubleQuote(nv.Value)
			}
			return d.replaceLines(old.Line-1, old.Line-1, []string{line[:start] + repl + line[start+len(text):]})
		}
	}
	if v.Style&yaml.FlowStyle != 0 {
		next := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Tag: localTag(v), Content: slices.Clone(v.Content)}
		next.Content[i] = item
		return d.replaceValue(k, v, next)
	}
	from, to := d.itemSpan(old)
	return d.replaceLines(from, to, d.renderer().item(item, d.dashCol(old)-1))
}

// ---------------------------------------------------------------------------
// Node builders

// Str is a string scalar (quoted when YAML would read it as something else).
func Str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// Int is an integer scalar.
func Int(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

// Bool is a boolean scalar.
func Bool(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// Raw is source text written as is (an item kept from the old value).
func Raw(text string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!raw", Value: text} }

// Map builds a block mapping from key, value pairs.
func Map(kv ...any) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[i].(string)}, kv[i+1].(*yaml.Node))
	}
	return m
}

// Seq builds a block sequence.
func Seq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

// StrSeq builds a block sequence of strings.
func StrSeq(items []string) *yaml.Node {
	s := &yaml.Node{Kind: yaml.SequenceNode}
	for _, it := range items {
		s.Content = append(s.Content, Str(it))
	}
	return s
}

// ---------------------------------------------------------------------------
// Rendering

type renderer struct {
	unit  int
	seqIn bool
}

func (d *Doc) renderer() renderer { return renderer{unit: d.unit, seqIn: d.seqIn} }

func pad(n int) string { return strings.Repeat(" ", n) }

// entry renders "key: value" at indent. head is the old key line: its line
// comment is kept when the value fits on that line.
func (r renderer) entry(key string, v *yaml.Node, indent int, head string) []string {
	comment := ""
	if i := strings.Index(head, " #"); i >= 0 && !strings.Contains(head[:i], "\"") && !strings.Contains(head[:i], "'") {
		comment = head[i:]
	}
	prefix := pad(indent) + key + ":"
	if localTag(v) != "" {
		prefix += " " + v.Tag // !override, !reset
	}
	switch {
	case inline(v):
		return []string{prefix + " " + r.inlineText(v) + comment}
	case v.Kind == yaml.ScalarNode: // multi-line string
		return append([]string{prefix + " |" + chompIndicator(v.Value) + comment}, blockLines(v.Value, indent+r.unit)...)
	case v.Kind == yaml.SequenceNode:
		child := indent
		if r.seqIn {
			child += r.unit
		}
		out := []string{prefix + comment}
		for _, it := range v.Content {
			out = append(out, r.item(it, child)...)
		}
		return out
	default:
		out := []string{prefix + comment}
		for i := 0; i+1 < len(v.Content); i += 2 {
			out = append(out, r.entry(plainKey(v.Content[i].Value), v.Content[i+1], indent+r.unit, "")...)
		}
		return out
	}
}

// localTag is a collection's application tag (!override, !reset; "" for
// scalars and the YAML core tags), kept when the collection is rewritten.
func localTag(v *yaml.Node) string {
	if v.Kind != yaml.ScalarNode && strings.HasPrefix(v.Tag, "!") && !strings.HasPrefix(v.Tag, "!!") {
		return v.Tag
	}
	return ""
}

// item renders "- value" with the dash at column dash (0-based).
func (r renderer) item(v *yaml.Node, dash int) []string {
	prefix := pad(dash) + "- "
	switch {
	case inline(v):
		return []string{prefix + r.inlineText(v)}
	case v.Kind == yaml.ScalarNode:
		return append([]string{prefix + "|" + chompIndicator(v.Value)}, blockLines(v.Value, dash+2+r.unit)...)
	case v.Kind == yaml.MappingNode:
		var out []string
		for i := 0; i+1 < len(v.Content); i += 2 {
			lines := r.entry(plainKey(v.Content[i].Value), v.Content[i+1], dash+2, "")
			if i == 0 {
				lines[0] = prefix + strings.TrimLeft(lines[0], " ")
			}
			out = append(out, lines...)
		}
		return out
	default:
		out := []string{strings.TrimRight(prefix, " ")}
		for _, it := range v.Content {
			out = append(out, r.item(it, dash+r.unit)...)
		}
		return out
	}
}

// inline reports whether v renders on one line.
func inline(v *yaml.Node) bool {
	switch v.Kind {
	case yaml.ScalarNode:
		return !strings.Contains(v.Value, "\n") || v.Tag == "!raw"
	case yaml.AliasNode:
		return true
	}
	return v.Style&yaml.FlowStyle != 0 || len(v.Content) == 0
}

func (r renderer) inlineText(v *yaml.Node) string {
	switch v.Kind {
	case yaml.ScalarNode:
		return scalarText(v)
	case yaml.AliasNode:
		return "*" + v.Value
	case yaml.SequenceNode:
		parts := make([]string, len(v.Content))
		for i, it := range v.Content {
			parts[i] = r.flowItem(it)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		parts := make([]string, 0, len(v.Content)/2)
		for i := 0; i+1 < len(v.Content); i += 2 {
			parts = append(parts, plainKey(v.Content[i].Value)+": "+r.flowItem(v.Content[i+1]))
		}
		if len(parts) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
}

// flowItem renders an item of a flow collection, quoting plain scalars
// holding flow indicators.
func (r renderer) flowItem(v *yaml.Node) string {
	if v.Kind == yaml.ScalarNode && v.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) == 0 && v.Tag != "!raw" &&
		v.Tag != "!!int" && v.Tag != "!!bool" && strings.ContainsAny(v.Value, ",[]{}") {
		return doubleQuote(v.Value)
	}
	return r.inlineText(v)
}

func chompIndicator(s string) string {
	switch {
	case !strings.HasSuffix(s, "\n"):
		return "-"
	case strings.HasSuffix(s, "\n\n"):
		return "+"
	}
	return ""
}

func blockLines(s string, indent int) []string {
	s = strings.TrimSuffix(s, "\n")
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, pad(indent)+l)
	}
	return out
}

func plainKey(k string) string {
	if needsQuote(k) {
		return strconv.Quote(k)
	}
	return k
}

// scalarText renders a one-line scalar in its style (plain when possible).
func scalarText(n *yaml.Node) string {
	if n.Tag == "!raw" {
		return n.Value
	}
	switch {
	case n.Style&yaml.SingleQuotedStyle != 0:
		return "'" + strings.ReplaceAll(n.Value, "'", "''") + "'"
	case n.Style&yaml.DoubleQuotedStyle != 0:
		return doubleQuote(n.Value)
	}
	switch n.Tag {
	case "!!int", "!!bool", "!!float", "!!null":
		return n.Value
	}
	if needsQuote(n.Value) {
		return doubleQuote(n.Value)
	}
	return n.Value
}

func doubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

var nonStringPlain = regexp.MustCompile(`^(?i:true|false|yes|no|on|off|y|n|null|~)$|^[-+]?(\d[\d_]*(\.\d*)?|\.\d+)([eE][-+]?\d+)?$|^0[xob][0-9a-fA-F_]+$|^[-+]?\.(inf|Inf|INF)$|^\.(nan|NaN|NAN)$`)

// needsQuote reports whether a string can't be written plain: YAML would
// read it as another type or structure. Values starting with a digit and
// holding ':' are quoted too ("8080:80" is a sexagesimal number in YAML
// 1.1, the compose docs quote ports); "nginx:1.27" stays plain.
func needsQuote(s string) bool {
	if s == "" || s != strings.TrimSpace(s) || nonStringPlain.MatchString(s) {
		return true
	}
	if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") || strings.Contains(s, " #") ||
		strings.ContainsAny(s, "\n\t\"") || strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'%@`") {
		return true
	}
	if s[0] >= '0' && s[0] <= '9' && strings.Contains(s, ":") {
		return true
	}
	return strings.Contains(s, ", ") || strings.HasSuffix(s, ",")
}
