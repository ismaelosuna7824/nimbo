package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

// JSON configs are edited through the hujson syntax tree so comments
// (JSONC: VS Code, Gemini CLI), member order and formatting of everything
// except the nimbo entry survive an install or uninstall.

// parseJSONC parses data (empty = "{}") and returns its top-level object.
func parseJSONC(data []byte) (hujson.Value, *hujson.Object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return hujson.Value{}, nil, fmt.Errorf("not valid JSON: %w", err)
	}
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return hujson.Value{}, nil, errors.New("top level is not a JSON object")
	}
	return v, root, nil
}

func memberIndex(obj *hujson.Object, name string) int {
	for i, m := range obj.Members {
		if lit, ok := m.Name.Value.(hujson.Literal); ok && lit.String() == name {
			return i
		}
	}
	return -1
}

// jsonHasServer reports whether data has <key>.<name>.
func jsonHasServer(data []byte, key, name string) bool {
	_, root, err := parseJSONC(data)
	if err != nil {
		return false
	}
	i := memberIndex(root, key)
	if i < 0 {
		return false
	}
	servers, ok := root.Members[i].Value.Value.(*hujson.Object)
	return ok && memberIndex(servers, name) >= 0
}

// jsonSetServer sets <key>.<name> = entry in data, creating key if needed.
func jsonSetServer(data []byte, key, name string, entry any) ([]byte, error) {
	v, root, err := parseJSONC(data)
	if err != nil {
		return nil, err
	}
	unit := detectIndent(data)
	i := memberIndex(root, key)
	if i < 0 {
		val, err := jsonValue(map[string]any{name: entry}, unit, unit)
		if err != nil {
			return nil, err
		}
		appendMember(root, key, val, unit, "")
	} else {
		servers, ok := root.Members[i].Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("%q is not a JSON object", key)
		}
		val, err := jsonValue(entry, unit+unit, unit)
		if err != nil {
			return nil, err
		}
		if j := memberIndex(servers, name); j >= 0 {
			val.BeforeExtra = servers.Members[j].Value.BeforeExtra
			val.AfterExtra = servers.Members[j].Value.AfterExtra
			servers.Members[j].Value = val
		} else {
			appendMember(servers, name, val, unit+unit, unit)
		}
	}
	return finish(v), nil
}

// jsonRemoveServer deletes <key>.<name>; changed is false when it is absent.
func jsonRemoveServer(data []byte, key, name string) (out []byte, changed bool, err error) {
	v, root, err := parseJSONC(data)
	if err != nil {
		return nil, false, err
	}
	i := memberIndex(root, key)
	if i < 0 {
		return data, false, nil
	}
	servers, ok := root.Members[i].Value.Value.(*hujson.Object)
	if !ok {
		return data, false, nil
	}
	j := memberIndex(servers, name)
	if j < 0 {
		return data, false, nil
	}
	last := j == len(servers.Members)-1
	trailingComma := servers.Members[j].Value.AfterExtra != nil
	servers.Members = append(servers.Members[:j], servers.Members[j+1:]...)
	if n := len(servers.Members); n == 0 {
		if isSpace(servers.AfterExtra) {
			servers.AfterExtra = nil
		}
	} else if last {
		// Keep the trailing-comma style of the removed last member.
		if trailingComma {
			servers.Members[n-1].Value.AfterExtra = []byte{}
		} else {
			servers.Members[n-1].Value.AfterExtra = nil
		}
	}
	return finish(v), true, nil
}

// appendMember adds name: val as the last member of obj; memberIndent and
// closeIndent are used when obj has no member to copy the layout from.
func appendMember(obj *hujson.Object, name string, val hujson.Value, memberIndent, closeIndent string) {
	nameVal := hujson.Value{Value: hujson.String(name)}
	before := []byte("\n" + memberIndent)
	if n := len(obj.Members); n > 0 {
		prev := obj.Members[n-1].Name.BeforeExtra
		if k := bytes.LastIndexByte(prev, '\n'); k >= 0 && isSpace(prev[k:]) {
			before = bytes.Clone(prev[k:])
		} else if !bytes.ContainsRune(prev, '\n') && isSpace(prev) {
			before = bytes.Clone(prev) // single-line object
		}
		last := &obj.Members[n-1].Value
		if last.AfterExtra != nil { // trailing comma: keep it on the new last member
			val.AfterExtra = []byte{}
		}
	} else if isSpace(obj.AfterExtra) {
		obj.AfterExtra = []byte("\n" + closeIndent)
	}
	nameVal.BeforeExtra = before
	val.BeforeExtra = []byte(" ")
	obj.Members = append(obj.Members, hujson.ObjectMember{Name: nameVal, Value: val})
}

// jsonValue renders v as an indented hujson value whose continuation lines
// start with prefix.
func jsonValue(v any, prefix, unit string) (hujson.Value, error) {
	b, err := json.MarshalIndent(v, prefix, unit)
	if err != nil {
		return hujson.Value{}, err
	}
	return hujson.Parse(b)
}

func finish(v hujson.Value) []byte {
	out := v.Pack()
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

// detectIndent returns the indentation unit of a JSON document: the leading
// whitespace of its first indented line, or two spaces.
func detectIndent(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

func isSpace(b []byte) bool { return len(bytes.TrimSpace(b)) == 0 }
