package netcheck

import (
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// unknownValue marks a value that is not known before deploy (computed by
// the provider, a module output, an unset variable, an unsupported
// function, ...). Checks skip it instead of guessing.
type unknownValue struct{}

var unknown = unknownValue{}

func isUnknown(v any) bool { _, ok := v.(unknownValue); return ok }

// refPrefix starts the placeholder strings design evaluation gives to
// provider-computed IDs: "nimbo-ref:aws_vpc.main" (the resource's id/arn)
// or "nimbo-ref:aws_vpc.main#default_route_table_id" (another attribute).
// The deployed basis uses the real IDs from the state instead.
const refPrefix = "nimbo-ref:"

// entity is one resource instance with its attribute values. Nested blocks
// are lists of maps, as in `terraform show -json`.
type entity struct {
	node  *model.Node // nil when the state has no node for it
	typ   string
	name  string
	addr  string // instance address: aws_subnet.public["a"]
	key   string // for_each key / count index ("" without either)
	data  bool
	attrs map[string]any
	id    string // real ID (deployed) or refPrefix+addr (design); "" unknown
	// multi marks a resource whose count/for_each is unknown: the entity
	// stands for an unknown number of instances.
	multi bool
}

func (e *entity) nodeID() string {
	if e == nil || e.node == nil {
		return ""
	}
	return e.node.ID
}

// label is the short display name of an entity: its Name tag when known,
// otherwise its address.
func (e *entity) label() string {
	if e == nil {
		return ""
	}
	return e.addr
}

// tagName returns the Name tag (or name attribute) when known.
func (e *entity) tagName() string {
	if tags, ok := e.attrs["tags"].(map[string]any); ok {
		if s, ok := tags["Name"].(string); ok {
			return s
		}
	}
	if s, ok := e.attrs["name"].(string); ok && !strings.HasPrefix(s, refPrefix) {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// Accessors. Each returns known=false for unknown values; absent/null
// attributes are known zero values.

func get(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			if isUnknown(cur) {
				return unknown
			}
			return nil
		}
		cur = mm[part]
	}
	return cur
}

func (e *entity) raw(path string) any { return get(e.attrs, path) }

func (e *entity) str(path string) (string, bool) { return asString(e.raw(path)) }

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		if strings.HasPrefix(x, refPrefix) && strings.Contains(x, "#") {
			// a provider-computed attribute that is not an ID reference
			return x, true
		}
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

func (e *entity) boolean(path string) (val, set, known bool) {
	switch x := e.raw(path).(type) {
	case nil:
		return false, false, true
	case bool:
		return x, true, true
	case string:
		b, err := strconv.ParseBool(x)
		return b, err == nil, err == nil
	}
	return false, true, false
}

func (e *entity) number(path string) (n float64, set, known bool) {
	switch x := e.raw(path).(type) {
	case nil:
		return 0, false, true
	case float64:
		return x, true, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil, err == nil
	}
	return 0, true, false
}

// strs returns the strings of a list/set attribute (a single string counts
// as a list of one). known is false when the list or an item is unknown.
func (e *entity) strs(path string) ([]string, bool) { return asStrings(e.raw(path)) }

func asStrings(v any) ([]string, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case string:
		return []string{x}, true
	case []any:
		out := make([]string, 0, len(x))
		known := true
		for _, it := range x {
			s, ok := it.(string)
			if !ok {
				if it == nil {
					continue
				}
				known = false
				continue
			}
			out = append(out, s)
		}
		return out, known
	}
	return nil, false
}

// blocks returns the nested blocks (or list-of-object attribute) at path.
// known is false when the block list itself is unknown (dynamic block with
// an unknown for_each).
func (e *entity) blocks(path string) ([]map[string]any, bool) { return asBlocks(e.raw(path)) }

func asBlocks(v any) ([]map[string]any, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case map[string]any:
		return []map[string]any{x}, true
	case []any:
		out := make([]map[string]any, 0, len(x))
		known := true
		for _, it := range x {
			m, ok := it.(map[string]any)
			if !ok {
				known = false
				continue
			}
			out = append(out, m)
		}
		return out, known
	}
	return nil, false
}

// mapStr reads a string from a block map.
func mapStr(m map[string]any, k string) (string, bool) { return asString(m[k]) }

func mapNum(m map[string]any, k string) (float64, bool, bool) {
	switch x := m[k].(type) {
	case nil:
		return 0, false, true
	case float64:
		return x, true, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil, err == nil
	}
	return 0, true, false
}

func mapBool(m map[string]any, k string) (val, set, known bool) {
	switch x := m[k].(type) {
	case nil:
		return false, false, true
	case bool:
		return x, true, true
	case string:
		b, err := strconv.ParseBool(x)
		return b, err == nil, err == nil
	}
	return false, true, false
}

// prefix parses a CIDR; ok is false when the value is unknown or invalid.
func prefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

// ---------------------------------------------------------------------------
// cty -> Go

func fromCty(v cty.Value) any {
	if !v.IsKnown() {
		return unknown
	}
	if v.IsNull() {
		return nil
	}
	t := v.Type()
	switch {
	case t == cty.String:
		return v.AsString()
	case t == cty.Number:
		f, _ := v.AsBigFloat().Float64()
		return f
	case t == cty.Bool:
		return v.True()
	case t.IsListType() || t.IsSetType() || t.IsTupleType():
		out := make([]any, 0, v.LengthInt())
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			out = append(out, fromCty(e))
		}
		return out
	case t.IsMapType() || t.IsObjectType():
		out := map[string]any{}
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			out[k.AsString()] = fromCty(e)
		}
		return out
	case t == cty.DynamicPseudoType:
		return unknown
	}
	return unknown
}

// toCty converts a Go value from the state (JSON) into cty.
func toCty(v any) cty.Value {
	switch x := v.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType)
	case unknownValue:
		return cty.DynamicVal
	case string:
		return cty.StringVal(x)
	case float64:
		return cty.NumberVal(big.NewFloat(x))
	case bool:
		return cty.BoolVal(x)
	case []any:
		if len(x) == 0 {
			return cty.EmptyTupleVal
		}
		vals := make([]cty.Value, len(x))
		for i, it := range x {
			vals[i] = toCty(it)
		}
		return cty.TupleVal(vals)
	case map[string]any:
		if len(x) == 0 {
			return cty.EmptyObjectVal
		}
		vals := make(map[string]cty.Value, len(x))
		for k, it := range x {
			vals[k] = toCty(it)
		}
		return cty.ObjectVal(vals)
	}
	return cty.DynamicVal
}

// ---------------------------------------------------------------------------
// Ports and protocols

// portRange is an inclusive port range; all ports = 0-65535.
type portRange struct{ from, to int }

var allPorts = portRange{0, 65535}
var ephemeral = portRange{1024, 65535}

func (r portRange) String() string {
	switch {
	case r.from <= 0 && r.to >= 65535:
		return "all"
	case r.from == r.to:
		return strconv.Itoa(r.from)
	}
	return fmt.Sprintf("%d-%d", r.from, r.to)
}

func (r portRange) contains(o portRange) bool { return r.from <= o.from && o.to <= r.to }

// normProto maps protocol spellings ("-1", "all", "6", "TCP", "*") to
// tcp | udp | icmp | icmpv6 | all, or the number for other protocols.
func normProto(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "-1", "all", "*", "any", "":
		return "all"
	case "6", "tcp":
		return "tcp"
	case "17", "udp":
		return "udp"
	case "1", "icmp":
		return "icmp"
	case "58", "icmpv6":
		return "icmpv6"
	}
	return strings.ToLower(p)
}

func protoMatches(rule, want string) bool { return rule == "all" || rule == want }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// kindName is the display name of a route target / gateway kind.
func kindName(kind string) string {
	switch kind {
	case "nat-gateway":
		return "NAT gateway"
	case "vpc-endpoint":
		return "VPC endpoint"
	case "vpn-gateway":
		return "VPN gateway"
	}
	return strings.ReplaceAll(kind, "-", " ")
}
