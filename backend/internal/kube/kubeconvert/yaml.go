package kubeconvert

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Objects are built once as yaml node trees and written in the three
// formats. Fields Helm takes from values go through a valuer: plain
// manifests and Kustomize write the literal, Helm records the literal in
// values.yaml and writes a template expression (as a marker the encoder
// leaves alone, replaced after encoding).

// str, num and boolean are scalar nodes with explicit tags so the
// encoder quotes "8080" and "true" when they are strings.
func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func num(n int64) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(n, 10)}
}

func boolean(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

// mapping builds an ordered mapping from key/value pairs. Values may be
// *yaml.Node, string, int, int64, bool or nil (pair skipped); a nil
// *yaml.Node is skipped too.
func mapping(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(kv); i += 2 {
		if v := node(kv[i+1]); v != nil {
			n.Content = append(n.Content, str(kv[i].(string)), v)
		}
	}
	return n
}

func node(v any) *yaml.Node {
	switch v := v.(type) {
	case nil:
		return nil
	case *yaml.Node:
		return v
	case string:
		return str(v)
	case int:
		return num(int64(v))
	case int64:
		return num(v)
	case bool:
		return boolean(v)
	}
	panic(fmt.Sprintf("kubeconvert: unsupported node value %T", v))
}

// set appends (or replaces) key in mapping m.
func set(m *yaml.Node, key string, v any) {
	val := node(v)
	if val == nil {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, str(key), val)
}

// get returns the value of key in mapping m (nil when absent).
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func seq(items ...*yaml.Node) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, it := range items {
		if it != nil {
			n.Content = append(n.Content, it)
		}
	}
	return n
}

func strSeq(items ...string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, it := range items {
		n.Content = append(n.Content, str(it))
	}
	return n
}

// clone deep-copies a node tree.
func clone(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = clone(ch)
	}
	return &c
}

// encode writes documents as one multi-document YAML stream.
func encode(docs ...*yaml.Node) []byte {
	var b bytes.Buffer
	for i, d := range docs {
		if i > 0 {
			b.WriteString("---\n")
		}
		b.Write(encodeOne(d))
	}
	return b.Bytes()
}

func encodeOne(d *yaml.Node) []byte {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(d); err != nil {
		panic(fmt.Sprintf("kubeconvert: encode: %v", err)) // nodes are built here: a failure is a bug
	}
	_ = e.Close()
	return b.Bytes()
}

// valuer decides how a field values can override is written.
type valuer interface {
	// scalar is a field Helm reads from .Values.<path> (literal v).
	scalar(path string, v *yaml.Node) *yaml.Node
	// block is a mapping/sequence Helm writes with toYaml .Values.<path>.
	block(path string, v *yaml.Node) *yaml.Node
	// expr is a field with a fixed Helm expression (release name, chart
	// labels): literal elsewhere.
	expr(literal, helm string) *yaml.Node
	// rangeMap writes the entries of the map .Values.<path> (values quoted)
	// as a mapping: env of a ConfigMap, stringData of a Secret.
	rangeMap(path string, v *yaml.Node) *yaml.Node
	helm() bool
}

// literal is the valuer of plain manifests and Kustomize.
type literal struct{}

func (literal) scalar(_ string, v *yaml.Node) *yaml.Node   { return v }
func (literal) block(_ string, v *yaml.Node) *yaml.Node    { return v }
func (literal) expr(lit, _ string) *yaml.Node              { return str(lit) }
func (literal) rangeMap(_ string, v *yaml.Node) *yaml.Node { return v }
func (literal) helm() bool                                 { return false }

// helmValuer records values and returns markers.
type helmValuer struct {
	values  *yaml.Node // values.yaml root mapping
	markers []marker
}

type marker struct {
	block bool   // replaces the whole value: written on the lines below the key
	text  string // inline expression, or block template (%d: indent width)
	// lines: text is a multi-line template whose lines start with %[1]s
	// (the indent string).
	lines bool
}

const markerPrefix = "__NIMBO_HELM_"

var markerRe = regexp.MustCompile(`(?m)^( *)(- )?([^\n]*?)` + markerPrefix + `(\d+)__\n`)

func (h *helmValuer) mark(m marker) *yaml.Node {
	h.markers = append(h.markers, m)
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprintf("%s%d__", markerPrefix, len(h.markers)-1)}
}

// put stores v at the dotted path of values.yaml.
func (h *helmValuer) put(path string, v *yaml.Node) {
	m := h.values
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next := get(m, p)
		if next == nil {
			next = mapping()
			set(m, p, next)
		}
		m = next
	}
	set(m, parts[len(parts)-1], clone(v))
}

func (h *helmValuer) scalar(path string, v *yaml.Node) *yaml.Node {
	h.put(path, v)
	pipe := ""
	if v.Tag == "!!str" {
		pipe = " | quote"
	}
	return h.mark(marker{text: "{{ .Values." + path + pipe + " }}"})
}

func (h *helmValuer) block(path string, v *yaml.Node) *yaml.Node {
	h.put(path, v)
	return h.mark(marker{block: true, text: "{{- toYaml .Values." + path + " | nindent %d }}"})
}

func (h *helmValuer) expr(_, helm string) *yaml.Node { return h.mark(marker{text: helm}) }

func (h *helmValuer) rangeMap(path string, v *yaml.Node) *yaml.Node {
	h.put(path, v)
	return h.mark(marker{block: true, lines: true, text: "%[1]s{{- range $k, $v := .Values." + path + " }}\n%[1]s{{ $k }}: {{ $v | toString | quote }}\n%[1]s{{- end }}"})
}

func (h *helmValuer) helm() bool { return true }

// expand replaces the markers of an encoded Helm template.
func (h *helmValuer) expand(src []byte) []byte {
	return markerRe.ReplaceAllFunc(src, func(line []byte) []byte {
		sm := markerRe.FindSubmatch(line)
		indent, dash, before := string(sm[1]), string(sm[2]), string(sm[3])
		i, _ := strconv.Atoi(string(sm[4]))
		m := h.markers[i]
		if !m.block {
			return []byte(indent + dash + before + m.text + "\n")
		}
		// "key: MARKER": the block goes on the following lines, two spaces
		// deeper than the key.
		keyIndent := len(indent)
		if dash != "" {
			keyIndent += 2
		}
		if m.lines {
			return []byte(indent + dash + strings.TrimRight(before, " ") + "\n" + fmt.Sprintf(m.text, strings.Repeat(" ", keyIndent+2)) + "\n")
		}
		return []byte(indent + dash + strings.TrimRight(before, " ") + "\n" + strings.Repeat(" ", keyIndent+2) + fmt.Sprintf(m.text, keyIndent+2) + "\n")
	})
}
