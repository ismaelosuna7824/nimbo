package challenge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// expr is a Terraform expression kept as source text (never evaluated).
type expr string

// segment is one step of a where path.
type segment struct {
	key   string
	index int // -1: not an index
	any   bool
}

// parsePath splits a where path: dots, [N] / [*] / .N / .*, and
// "quoted.keys".
func parsePath(p string) ([]segment, error) {
	var out []segment
	i := 0
	for i < len(p) {
		switch c := p[i]; {
		case c == '.':
			i++
			continue
		case c == '"':
			j := strings.IndexByte(p[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("path %q: unterminated quote", p)
			}
			out = append(out, segment{key: p[i+1 : i+1+j], index: -1})
			i += j + 2
		case c == '[':
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("path %q: unterminated [", p)
			}
			s, err := token(p[i+1 : i+j])
			if err != nil {
				return nil, fmt.Errorf("path %q: %v", p, err)
			}
			out = append(out, s)
			i += j + 1
		default:
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			s, err := token(p[i:j])
			if err != nil {
				return nil, fmt.Errorf("path %q: %v", p, err)
			}
			out = append(out, s)
			i = j
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	return out, nil
}

func token(t string) (segment, error) {
	switch {
	case t == "":
		return segment{}, fmt.Errorf("empty segment")
	case t == "*":
		return segment{any: true, index: -1}, nil
	}
	if n, err := strconv.Atoi(t); err == nil && n >= 0 {
		return segment{key: t, index: n}, nil
	}
	return segment{key: t, index: -1}, nil
}

// resolve returns every value path reaches from v. Lists are walked by
// index, "*", the element named (name/key) like the segment, or else
// every element (Terraform blocks: any instance).
func resolve(v any, segs []segment) []any {
	if len(segs) == 0 {
		if v == nil {
			return nil
		}
		return []any{v}
	}
	s, rest := segs[0], segs[1:]
	var out []any
	switch x := v.(type) {
	case map[string]any:
		if s.any {
			for _, e := range x {
				out = append(out, resolve(e, rest)...)
			}
			return out
		}
		if e, ok := x[s.key]; ok {
			if e == nil && len(rest) == 0 {
				// A key set to null (compose `networks: [front]`) exists.
				return []any{null{}}
			}
			return resolve(e, rest)
		}
	case expr:
		// An object or tuple expression with some non-literal values
		// (app_settings = { A = var.a, B = "x" }) is walked key by key.
		if g, ok := exprStructure(string(x)); ok {
			return resolve(g, segs)
		}
	case []any:
		switch {
		case s.any:
			for _, e := range x {
				out = append(out, resolve(e, rest)...)
			}
		case s.index >= 0:
			if s.index < len(x) {
				out = resolve(x[s.index], rest)
			}
		default:
			named := false
			for _, e := range x {
				if m, ok := e.(map[string]any); ok && (m["name"] == s.key || m["key"] == s.key) {
					named = true
					out = append(out, resolve(e, rest)...)
				}
			}
			if !named {
				for _, e := range x {
					out = append(out, resolve(e, segs)...)
				}
			}
		}
	}
	return out
}

// exprStructure decodes an object or tuple constructor expression into
// maps and lists: constant elements as their values, the others as
// expressions (source text). ok is false for any other expression.
func exprStructure(src string) (any, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(src), "", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	switch e.(type) {
	case *hclsyntax.ObjectConsExpr, *hclsyntax.TupleConsExpr:
		return exprGeneric(e, []byte(src)), true
	}
	return nil, false
}

func exprGeneric(e hclsyntax.Expression, src []byte) any {
	switch x := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		out := make(map[string]any, len(x.Items))
		for _, item := range x.Items {
			if k, ok := objectKey(item.KeyExpr); ok {
				out[k] = exprGeneric(item.ValueExpr, src)
			}
		}
		return out
	case *hclsyntax.TupleConsExpr:
		out := make([]any, len(x.Exprs))
		for i, el := range x.Exprs {
			out[i] = exprGeneric(el, src)
		}
		return out
	}
	if v, diags := e.Value(nil); !diags.HasErrors() && v.IsWhollyKnown() {
		if data, err := ctyjson.Marshal(v, v.Type()); err == nil {
			var out any
			if json.Unmarshal(data, &out) == nil {
				return out
			}
		}
	}
	return expr(e.Range().SliceBytes(src))
}

// objectKey is an object item's key: a bare identifier or a constant
// string.
func objectKey(e hclsyntax.Expression) (string, bool) {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		if kw := hcl.ExprAsKeyword(k.Wrapped); kw != "" {
			return kw, true
		}
		e = k.Wrapped
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsKnown() || v.IsNull() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

var matcherKeys = []string{"exists", "in", "notIn", "min", "max", "contains", "ref"}

// isMatcher reports whether want is a matcher object.
func isMatcher(want any) (map[string]any, bool) {
	m, ok := want.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, false
	}
	for k := range m {
		if !slices.Contains(matcherKeys, k) {
			return nil, false
		}
	}
	return m, true
}

// validWhere checks a where clause's paths and matchers.
func validWhere(w model.Where) error {
	for p, want := range w {
		if _, err := parsePath(p); err != nil {
			return err
		}
		m, ok := isMatcher(want)
		if !ok {
			if obj, isObj := want.(map[string]any); isObj {
				for k := range obj {
					if !slices.Contains(matcherKeys, k) {
						return fmt.Errorf("where %s: unknown matcher %q (use %s, or a scalar)", p, k, strings.Join(matcherKeys, ", "))
					}
				}
			}
			continue
		}
		for k, v := range m {
			switch k {
			case "exists":
				if _, ok := v.(bool); !ok {
					return fmt.Errorf("where %s: exists takes true or false", p)
				}
			case "in", "notIn":
				if _, ok := v.([]any); !ok {
					return fmt.Errorf("where %s: %s takes a list", p, k)
				}
			case "min", "max":
				if _, ok := number(v); !ok {
					return fmt.Errorf("where %s: %s takes a number", p, k)
				}
			case "contains", "ref":
				if _, ok := v.(string); !ok && k == "ref" {
					return fmt.Errorf("where %s: ref takes a string", p)
				}
			}
		}
	}
	return nil
}

// matchWhere reports whether obj satisfies every clause, and the first
// clause that failed.
func matchWhere(obj any, w model.Where) (bool, string) {
	for _, p := range sortedKeys(w) {
		segs, err := parsePath(p)
		if err != nil {
			return false, err.Error()
		}
		if !matchValues(resolve(obj, segs), w[p]) {
			return false, p
		}
	}
	return true, ""
}

func matchValues(vals []any, want any) bool {
	m, ok := isMatcher(want)
	if !ok {
		for _, v := range vals {
			if equal(v, want) {
				return true
			}
		}
		return false
	}
	if e, ok := m["exists"].(bool); ok && e != (len(vals) > 0) {
		return false
	}
	rest := false
	for k := range m {
		rest = rest || k != "exists"
	}
	if !rest {
		return true
	}
	for _, v := range vals {
		if matchOne(v, m) {
			return true
		}
	}
	return false
}

// matchOne applies every matcher but exists to one value.
func matchOne(v any, m map[string]any) bool {
	for k, arg := range m {
		switch k {
		case "in":
			if !slices.ContainsFunc(arg.([]any), func(a any) bool { return equal(v, a) }) {
				return false
			}
		case "notIn":
			if slices.ContainsFunc(arg.([]any), func(a any) bool { return equal(v, a) }) {
				return false
			}
		case "min", "max":
			n, ok := number(v)
			lim, _ := number(arg)
			if !ok || (k == "min" && n < lim) || (k == "max" && n > lim) {
				return false
			}
		case "contains":
			if !contains(v, arg) {
				return false
			}
		case "ref":
			e, ok := v.(expr)
			if !ok || !references(string(e), arg.(string)) {
				return false
			}
		}
	}
	return true
}

// references reports whether a Terraform expression refers to target (a
// type, an address or a prefix such as "var.").
func references(e, target string) bool {
	for i := 0; ; {
		j := strings.Index(e[i:], target)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(target)
		before := start == 0 || !identChar(e[start-1])
		after := end == len(e) || !identChar(e[end]) || strings.HasSuffix(target, ".")
		if before && after {
			return true
		}
		i = start + 1
	}
}

func identChar(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func contains(v, arg any) bool {
	switch x := v.(type) {
	case string:
		s, ok := arg.(string)
		return ok && strings.Contains(x, s)
	case expr:
		s, ok := arg.(string)
		return ok && strings.Contains(string(x), s)
	case []any:
		// An element equal to arg, or a string (or expression) element
		// containing it (interpolated list entries).
		return slices.ContainsFunc(x, func(e any) bool {
			if equal(e, arg) {
				return true
			}
			switch e.(type) {
			case string, expr:
				return contains(e, arg)
			}
			return false
		})
	case map[string]any:
		s, ok := arg.(string)
		_, has := x[s]
		return ok && has
	}
	return false
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return n, err == nil
	}
	return 0, false
}

// equal compares a found value with an expected one: numbers numerically,
// booleans with their spelling, expressions by source text, the rest by
// JSON value.
// null is a key present with a null value.
type null struct{}

func equal(v, want any) bool {
	if _, ok := v.(null); ok {
		return want == nil
	}
	if e, ok := v.(expr); ok {
		return strings.TrimSpace(string(e)) == fmt.Sprint(want)
	}
	if a, ok := number(v); ok {
		if b, ok := number(want); ok {
			return a == b
		}
	}
	if b, ok := v.(bool); ok {
		return fmt.Sprint(b) == fmt.Sprint(want)
	}
	if s, ok := v.(string); ok {
		if b, ok := want.(bool); ok {
			return s == strconv.FormatBool(b)
		}
		return s == fmt.Sprint(want)
	}
	return reflect.DeepEqual(normalize(v), normalize(want))
}

func normalize(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

// toGeneric turns a value into maps, lists and scalars by JSON.
func toGeneric(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// bodyValue is a Terraform body as a where object: literal arguments
// decoded, expressions as source text, nested blocks as lists of bodies
// by block type.
func bodyValue(b model.Body) map[string]any {
	out := map[string]any{}
	for _, a := range b.Attrs {
		switch {
		case len(a.Value.Literal) > 0:
			var v any
			if json.Unmarshal(a.Value.Literal, &v) == nil {
				out[a.Name] = v
			}
		case a.Value.Expr != "":
			out[a.Name] = expr(a.Value.Expr)
		}
	}
	for _, blk := range b.Blocks {
		typ := blk.Type
		if typ == "dynamic" && len(blk.Labels) > 0 {
			typ = blk.Labels[0]
		}
		list, _ := out[typ].([]any)
		out[typ] = append(list, bodyValue(blk.Body))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
