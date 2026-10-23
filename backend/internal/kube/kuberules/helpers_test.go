package kuberules

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// parseObjects reads the documents of manifest files into objects located
// by their own node lines (like plain manifests).
func parseObjects(t testing.TB, files map[string]string) []*object {
	t.Helper()
	var out []*object
	for _, file := range sortedKeys(files) {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(files[file])))
		for {
			var doc yaml.Node
			if err := dec.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("%s: %v", file, err)
			}
			n := resolve(&doc)
			if n == nil || n.Kind != yaml.MappingNode {
				continue
			}
			for _, d := range flatten(n) {
				out = append(out, testObject(file, d))
			}
		}
	}
	return out
}

// flatten expands kind: List documents.
func flatten(n *yaml.Node) []*yaml.Node {
	if strings.HasSuffix(str(n, "kind"), "List") && get(n, "items") != nil {
		return items(n, "items")
	}
	return []*yaml.Node{n}
}

func testObject(file string, n *yaml.Node) *object {
	ns := str(n, "metadata", "namespace")
	o := &object{apiVersion: str(n, "apiVersion"), kind: str(n, "kind"), name: str(n, "metadata", "name"), node: n, explicitNS: ns != ""}
	if ns == "" {
		ns = "default"
	}
	o.namespace = ns
	if loadSchema().namespaced[o.apiVersion+"/"+o.kind] {
		o.id = model.KubeNodeID(o.kind, ns, o.name)
	} else {
		o.namespace = ns
		o.id = model.KubeNodeID(o.kind, "", o.name)
	}
	o.locate = func(path string) (string, int) { return file, nodeLine(n, path) }
	return o
}

// nodeLine is the line of the deepest existing key (or list item) of a
// field path.
func nodeLine(n *yaml.Node, path string) int {
	best := n.Line
	cur := n
	for _, part := range splitPath(path) {
		if i, ok := strings.CutPrefix(part, "["); ok {
			var k int
			fmt.Sscanf(i, "%d]", &k)
			if cur = resolve(cur); cur == nil || cur.Kind != yaml.SequenceNode || k >= len(cur.Content) {
				break
			}
			cur = cur.Content[k]
			best = cur.Line
			continue
		}
		kn := keyNode(cur, part)
		if kn == nil {
			break
		}
		best, cur = kn.Line, key(cur, part)
	}
	return best
}

// splitPath splits "a.b[0][\"x.y\"]" into keys and "[i]" indexes.
func splitPath(path string) []string {
	var out []string
	for path != "" {
		switch {
		case strings.HasPrefix(path, `["`):
			end := strings.Index(path, `"]`)
			out = append(out, path[2:end])
			path = path[end+2:]
		case strings.HasPrefix(path, "["):
			end := strings.Index(path, "]")
			out = append(out, path[:end+1])
			path = path[end+1:]
		case strings.HasPrefix(path, "."):
			path = path[1:]
		default:
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			out = append(out, path[:end])
			path = path[end:]
		}
	}
	return out
}

func checkFiles(t testing.TB, env string, files map[string]string) []model.Diagnostic {
	t.Helper()
	return run(input{objects: parseObjects(t, files), env: env, localImages: map[string]bool{"api": true}, namespaces: map[string]bool{}})
}

// codes lists findings as "severity:code@file:line".
func codes(diags []model.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s:%s@%s:%d", d.Severity, d.Code, d.File, d.Line))
	}
	sort.Strings(out)
	return out
}

func hasCode(diags []model.Diagnostic, code string) bool {
	return slices.ContainsFunc(diags, func(d model.Diagnostic) bool { return d.Code == code })
}
