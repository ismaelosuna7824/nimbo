package kubecatalog

import (
	"bytes"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v4"
)

// Small builders for the YAML documents the palette writes: key order is
// the order given, strings that YAML would read as numbers or booleans are
// quoted, and nodes may carry comments.

// m builds a mapping from alternating keys and values; nil values are
// skipped.
func m(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(kv); i += 2 {
		v := node(kv[i+1])
		if v == nil {
			continue
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[i].(string)}, v)
	}
	return n
}

// seq builds a sequence; nil items are skipped.
func seq(items ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, it := range items {
		if v := node(it); v != nil {
			n.Content = append(n.Content, v)
		}
	}
	return n
}

// flow renders a node in flow style ([a, b], {k: v}).
func flow(n *yaml.Node) *yaml.Node {
	n.Style = yaml.FlowStyle
	return n
}

// strs is a sequence of strings.
func strs(s ...string) *yaml.Node {
	items := make([]any, len(s))
	for i, v := range s {
		items[i] = v
	}
	return seq(items...)
}

// comment sets the head comment of n.
func comment(n *yaml.Node, text string) *yaml.Node {
	n.HeadComment = text
	return n
}

func node(v any) *yaml.Node {
	switch x := v.(type) {
	case nil:
		return nil
	case *yaml.Node:
		if x == nil {
			return nil
		}
		return x
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	}
	panic(fmt.Sprintf("kubecatalog: unsupported YAML value %T", v))
}

// docs encodes documents as a multi-document YAML stream.
func docs(nodes ...*yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, n := range nodes {
		if err := enc.Encode(n); err != nil {
			panic(err)
		}
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	return buf.String()
}

// meta is an object's metadata (namespace omitted when empty).
func meta(name, namespace string, labels *yaml.Node) *yaml.Node {
	md := m("name", name)
	if namespace != "" {
		md = m("name", name, "namespace", namespace)
	}
	if labels != nil {
		md.Content = append(md.Content, node("labels"), labels)
	}
	return md
}

func obj(apiVersion, kind string, md *yaml.Node, rest ...any) *yaml.Node {
	return m(append([]any{"apiVersion", apiVersion, "kind", kind, "metadata", md}, rest...)...)
}
