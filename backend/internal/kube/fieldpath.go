package kube

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Field paths address a field of an object: keys separated by dots, list
// items by name ("containers[name=web]") or index ("[0]"), and keys holding
// dots quoted ('metadata.labels["app.kubernetes.io/name"]').

// seg is one path segment: a key, an index (Index >= 0) or a name match
// (Name != "").
type seg struct {
	Key   string
	Index int
	Name  string
}

func (s seg) isKey() bool { return s.Index < 0 && s.Name == "" }

// ParsePath parses a field path.
func ParsePath(p string) ([]seg, error) {
	var out []seg
	i := 0
	for i < len(p) {
		switch p[i] {
		case '.':
			i++
		case '[':
			end := strings.IndexByte(p[i:], ']')
			if strings.HasPrefix(p[i:], `["`) {
				q := strings.Index(p[i+2:], `"]`)
				if q < 0 {
					return nil, fmt.Errorf("unterminated key in %q", p)
				}
				out = append(out, seg{Key: p[i+2 : i+2+q], Index: -1})
				i += q + 4
				continue
			}
			if end < 0 {
				return nil, fmt.Errorf("unterminated [ in %q", p)
			}
			in := p[i+1 : i+end]
			if name, ok := strings.CutPrefix(in, "name="); ok {
				out = append(out, seg{Name: name, Index: -1})
			} else if n, err := strconv.Atoi(in); err == nil && n >= 0 {
				out = append(out, seg{Index: n})
			} else {
				return nil, fmt.Errorf("invalid item %q in %q: use [<index>] or [name=<name>]", in, p)
			}
			i += end + 1
		default:
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			out = append(out, seg{Key: p[i:j], Index: -1})
			i = j
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty field path")
	}
	return out, nil
}

// FormatPath writes segments back as a path.
func FormatPath(segs []seg) string {
	var b strings.Builder
	for i, s := range segs {
		switch {
		case s.Name != "":
			b.WriteString("[name=" + s.Name + "]")
		case s.Index >= 0:
			b.WriteString("[" + strconv.Itoa(s.Index) + "]")
		case strings.ContainsAny(s.Key, ".[]") || s.Key == "":
			b.WriteString(`["` + s.Key + `"]`)
		default:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.Key)
		}
	}
	return b.String()
}

// concrete resolves name matches against v (an object tree): the
// segments as keys and indexes, ok=false when the path does not exist.
// A missing last key is fine (missing reports it).
func concrete(v any, segs []seg) (out []string, missing bool, ok bool) {
	cur := v
	for i, s := range segs {
		last := i == len(segs)-1
		switch {
		case s.isKey():
			m, isMap := cur.(map[string]any)
			if !isMap {
				return nil, false, false
			}
			out = append(out, s.Key)
			next, has := m[s.Key]
			if !has {
				if last {
					return out, true, true
				}
				return out, true, false
			}
			cur = next
		case s.Name != "":
			l, isList := cur.([]any)
			if !isList {
				return nil, false, false
			}
			idx := slices.IndexFunc(l, func(it any) bool { return str(it, "name") == s.Name })
			if idx < 0 {
				return out, true, false
			}
			out = append(out, strconv.Itoa(idx))
			cur = l[idx]
		default:
			l, isList := cur.([]any)
			if !isList || s.Index >= len(l) {
				return out, true, false
			}
			out = append(out, strconv.Itoa(s.Index))
			cur = l[s.Index]
		}
	}
	return out, false, true
}

// at returns the value at concrete keys/indexes.
func at(v any, keys []string) (any, bool) {
	cur := v
	for _, k := range keys {
		switch t := cur.(type) {
		case map[string]any:
			n, ok := t[k]
			if !ok {
				return nil, false
			}
			cur = n
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// leaf is one scalar field of an object.
type leaf struct {
	segs  []seg    // canonical (lists of named maps by name)
	keys  []string // concrete keys/indexes
	value any
}

// leaves lists the scalar fields of an object (status left out); lists
// whose items are maps with unique names are addressed by name.
func leaves(obj map[string]any) []leaf {
	var out []leaf
	var walk func(v any, segs []seg, keys []string)
	walk = func(v any, segs []seg, keys []string) {
		switch t := v.(type) {
		case map[string]any:
			if len(t) == 0 {
				out = append(out, leaf{segs: slices.Clone(segs), keys: slices.Clone(keys), value: t})
				return
			}
			for _, k := range slices.Sorted(maps.Keys(t)) {
				if len(segs) == 0 && k == "status" {
					continue
				}
				walk(t[k], append(segs, seg{Key: k, Index: -1}), append(keys, k))
			}
		case []any:
			if len(t) == 0 {
				out = append(out, leaf{segs: slices.Clone(segs), keys: slices.Clone(keys), value: t})
				return
			}
			named := uniqueNames(t)
			for i, it := range t {
				s := seg{Index: i}
				if named {
					s = seg{Name: str(it, "name"), Index: -1}
				}
				walk(it, append(segs, s), append(keys, strconv.Itoa(i)))
			}
		default:
			out = append(out, leaf{segs: slices.Clone(segs), keys: slices.Clone(keys), value: v})
		}
	}
	walk(obj, nil, nil)
	return out
}

func uniqueNames(l []any) bool {
	seen := map[string]bool{}
	for _, it := range l {
		n := str(it, "name")
		if _, isMap := it.(map[string]any); !isMap || n == "" || seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}
