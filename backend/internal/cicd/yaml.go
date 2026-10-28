package cicd

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"
)

// The pipelines are built as yaml node trees: the encoder quotes and
// escapes, so names, paths and scripts taken from the workflow cannot
// break the document, and key order stays the order written here.

func str(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

// mapping builds an ordered mapping from key/value pairs. Values may be
// *yaml.Node, string, []string, bool or nil (pair skipped); a nil
// *yaml.Node, an empty string and an empty []string are skipped too.
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
		if v == "" {
			return nil
		}
		return str(v)
	case []string:
		if len(v) == 0 {
			return nil
		}
		return strSeq(v...)
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
	}
	panic(fmt.Sprintf("cicd: unsupported node value %T", v))
}

// set appends key to mapping m (skipped like mapping's nil values).
func set(m *yaml.Node, key string, v any) {
	if val := node(v); val != nil {
		m.Content = append(m.Content, str(key), val)
	}
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
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, it := range items {
		n.Content = append(n.Content, str(it))
	}
	return n
}

// encode writes the document after header, a block of comment lines.
func encode(header []string, doc *yaml.Node) []byte {
	var b bytes.Buffer
	for _, l := range header {
		if l == "" {
			b.WriteString("#\n")
		} else {
			b.WriteString("# " + l + "\n")
		}
	}
	b.WriteString("\n")
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(doc); err != nil {
		panic(fmt.Sprintf("cicd: encode: %v", err)) // nodes are built here: a failure is a bug
	}
	_ = e.Close()
	return b.Bytes()
}
