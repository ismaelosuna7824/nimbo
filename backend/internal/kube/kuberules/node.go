package kuberules

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Helpers over the yaml.v4 node of a rendered object: rules read fields by
// key and keep the nodes so findings point at the line that set them.

// resolve follows aliases and unwraps a document node.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil {
		switch n.Kind {
		case yaml.AliasNode:
			n = n.Alias
		case yaml.DocumentNode:
			if len(n.Content) == 0 {
				return nil
			}
			n = n.Content[0]
		default:
			return n
		}
	}
	return nil
}

// isNull reports whether n is absent or an explicit null (which Kubernetes
// treats as an omitted field).
func isNull(n *yaml.Node) bool {
	n = resolve(n)
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// key returns the value node of key k in mapping n (nil when absent or n
// is not a mapping); merge keys (<<) are followed.
func key(n *yaml.Node, k string) *yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var merged *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		if kn.Value == k {
			if isNull(n.Content[i+1]) {
				return nil
			}
			return resolve(n.Content[i+1])
		}
		if kn.Value == "<<" && kn.ShortTag() == "!!merge" && merged == nil {
			merged = key(n.Content[i+1], k)
		}
	}
	return merged
}

// keyNode returns the key node of k in mapping n (for the line of a field).
func keyNode(n *yaml.Node, k string) *yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == k {
			return n.Content[i]
		}
	}
	return nil
}

// get walks a dotted path of mapping keys.
func get(n *yaml.Node, path ...string) *yaml.Node {
	for _, k := range path {
		n = key(n, k)
		if n == nil {
			return nil
		}
	}
	return n
}

// str is the scalar value at path ("" when absent or not a scalar).
func str(n *yaml.Node, path ...string) string {
	v := get(n, path...)
	if v == nil || v.Kind != yaml.ScalarNode || v.ShortTag() == "!!null" {
		return ""
	}
	return v.Value
}

// intAt is the integer at path and whether it is set.
func intAt(n *yaml.Node, path ...string) (int, bool) {
	s := str(n, path...)
	if s == "" {
		return 0, false
	}
	i, err := strconv.Atoi(s)
	return i, err == nil
}

// boolAt is the boolean at path (nil when absent or not a boolean).
func boolAt(n *yaml.Node, path ...string) *bool {
	v := get(n, path...)
	if v == nil || v.Kind != yaml.ScalarNode {
		return nil
	}
	switch strings.ToLower(v.Value) {
	case "true":
		b := true
		return &b
	case "false":
		b := false
		return &b
	}
	return nil
}

// items are the elements of the sequence at path.
func items(n *yaml.Node, path ...string) []*yaml.Node {
	v := get(n, path...)
	if v == nil || v.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(v.Content))
	for _, c := range v.Content {
		if c := resolve(c); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// pairs lists the scalar key/values of the mapping at path, in order.
func pairs(n *yaml.Node, path ...string) [][2]string {
	v := get(n, path...)
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	var out [][2]string
	for i := 0; i+1 < len(v.Content); i += 2 {
		val := resolve(v.Content[i+1])
		s := ""
		if val != nil && val.Kind == yaml.ScalarNode && val.ShortTag() != "!!null" {
			s = val.Value
		}
		out = append(out, [2]string{v.Content[i].Value, s})
	}
	return out
}

// strMap is the mapping at path as a Go map of scalars.
func strMap(n *yaml.Node, path ...string) map[string]string {
	p := pairs(n, path...)
	if p == nil {
		return nil
	}
	m := make(map[string]string, len(p))
	for _, kv := range p {
		m[kv[0]] = kv[1]
	}
	return m
}

// field joins a field path ("spec.template" + "containers[0]"); a key with
// dots is written ["app.kubernetes.io/name"] (the path syntax of
// kube.Locate and the editor).
func field(base string, parts ...string) string {
	out := base
	for _, p := range parts {
		switch {
		case strings.Contains(p, ".") && !strings.Contains(p, "["):
			out += `["` + p + `"]`
		case out == "":
			out = p
		case strings.HasPrefix(p, "["):
			out += p
		default:
			out += "." + p
		}
	}
	return out
}

// idx is "[i]" for a field path.
func idx(i int) string { return "[" + strconv.Itoa(i) + "]" }
