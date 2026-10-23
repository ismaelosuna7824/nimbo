package kube

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// editFields are the fields the round-trip check edits on every object
// that has them.
func editFields(o *Object) []string {
	var out []string
	if _, ok := toInt(dig(o.Obj, "spec", "replicas")); ok {
		out = append(out, "spec.replicas")
	}
	if spec, _ := podSpec(o); spec != nil {
		base := PodSpecPath(o)
		for _, c := range list(spec["containers"]) {
			name := str(c, "name")
			if name == "" {
				continue
			}
			out = append(out, base+".containers[name="+name+"].image")
			if ports := list(dig(c, "ports")); len(ports) > 0 {
				out = append(out, base+".containers[name="+name+"].ports[0].containerPort")
			}
		}
	}
	if o.Kind == "Service" && len(list(dig(o.Obj, "spec", "ports"))) > 0 {
		out = append(out, "spec.ports[0].port")
	}
	return out
}

// mutated is a different value of the same type.
func mutated(v any) any {
	if n, ok := toInt(v); ok {
		return n + 1
	}
	s := fmt.Sprint(v)
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[i:], "/") {
		return s[:i] + ":nimbo-edit"
	}
	return s + "-nimbo"
}

// roundTrip edits one field of o through the editor, renders again and
// checks the object shows the new value, then writes the old value back
// and checks every file is byte-identical. Not editable fields are
// skipped (ok=false).
func roundTrip(dir string, wf *model.Workflow, env string, o *Object, fieldPath string) (ok bool, err error) {
	ctx := context.Background()
	p := Open(ctx, dir, wf, Options{Environment: env})
	o = p.Object(o.ID)
	segs, _ := ParsePath(fieldPath)
	src := p.fieldSources(o, [][]seg{segs})[0]
	if !src.Editable || !isScalar(src.Value) || src.Value == nil {
		return false, nil
	}
	old := src.Value
	next := mutated(old)
	if src.pattern != nil && src.Format != "" {
		// Only the values parts can change: change the last one.
		cur := slices.Clone(src.cur)
		cur[len(cur)-1] = fmt.Sprint(mutated(cur[len(cur)-1]))
		next = src.pattern.render(func() map[string]string {
			m := map[string]string{}
			for i, k := range src.pattern.keys {
				m[k] = cur[i]
			}
			return m
		}())
		if n, err := strconv.Atoi(fmt.Sprint(next)); err == nil {
			next = n
		}
	}
	e := p.Edit()
	if err := e.Set(o, fieldPath, next); err != nil {
		return false, fmt.Errorf("set %v: %w", next, err)
	}
	content := e.Files()
	originals := map[string][]byte{}
	for f := range content {
		data, err := p.Read(f)
		if err == nil {
			originals[f] = data
		}
	}
	p2 := Open(ctx, dir, wf, Options{Environment: env, Content: content})
	o2 := p2.Object(o.ID)
	if o2 == nil {
		return false, fmt.Errorf("%s gone after the edit (%v)", o.ID, p2.Diagnostics)
	}
	keys, _, found := concrete(o2.Obj, segs)
	got, _ := at(o2.Obj, keys)
	if !found || fmt.Sprint(got) != fmt.Sprint(next) {
		return false, fmt.Errorf("after setting %v (via %s %s:%d) the object renders %v", next, src.Via, src.File, src.Line, got)
	}
	e2 := p2.Edit()
	if err := e2.Set(o2, fieldPath, old); err != nil {
		return false, fmt.Errorf("revert: %w", err)
	}
	for f, data := range e2.Files() {
		if string(originals[f]) != string(data) {
			return false, fmt.Errorf("%s not restored:\n%s", f, firstDiff(string(originals[f]), string(data)))
		}
	}
	return true, nil
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n- %s\n+ %s", i+1, x, y)
		}
	}
	return ""
}
