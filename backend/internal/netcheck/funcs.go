package netcheck

import (
	"fmt"
	"math/big"
	"net/netip"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// functions is the curated Terraform function set static evaluation
// understands: collection/string/number helpers that network code uses to
// derive CIDRs, names and maps, plus the CIDR functions. Anything else
// (file, templatefile, timestamp, ...) is absent, so expressions calling it
// evaluate to unknown.
func functions() map[string]function.Function {
	return map[string]function.Function{
		"abs": stdlib.AbsoluteFunc, "ceil": stdlib.CeilFunc, "floor": stdlib.FloorFunc,
		"max": stdlib.MaxFunc, "min": stdlib.MinFunc, "pow": stdlib.PowFunc,
		"parseint": stdlib.ParseIntFunc, "signum": stdlib.SignumFunc, "log": stdlib.LogFunc,
		"chomp": stdlib.ChompFunc, "format": stdlib.FormatFunc, "formatlist": stdlib.FormatListFunc,
		"indent": stdlib.IndentFunc, "join": stdlib.JoinFunc, "lower": stdlib.LowerFunc,
		"upper": stdlib.UpperFunc, "regex": stdlib.RegexFunc, "regexall": stdlib.RegexAllFunc,
		"replace": stdlib.ReplaceFunc, "split": stdlib.SplitFunc, "strrev": stdlib.ReverseFunc,
		"substr": stdlib.SubstrFunc, "title": stdlib.TitleFunc, "trim": stdlib.TrimFunc,
		"trimprefix": stdlib.TrimPrefixFunc, "trimsuffix": stdlib.TrimSuffixFunc,
		"trimspace": stdlib.TrimSpaceFunc, "length": stdlib.LengthFunc,
		"chunklist": stdlib.ChunklistFunc, "coalesce": stdlib.CoalesceFunc,
		"coalescelist": stdlib.CoalesceListFunc, "compact": stdlib.CompactFunc,
		"concat": stdlib.ConcatFunc, "contains": stdlib.ContainsFunc, "distinct": stdlib.DistinctFunc,
		"element": stdlib.ElementFunc, "flatten": stdlib.FlattenFunc, "index": stdlib.IndexFunc,
		"keys": stdlib.KeysFunc, "lookup": stdlib.LookupFunc, "merge": stdlib.MergeFunc,
		"range": stdlib.RangeFunc, "reverse": stdlib.ReverseListFunc, "setintersection": stdlib.SetIntersectionFunc,
		"setproduct": stdlib.SetProductFunc, "setsubtract": stdlib.SetSubtractFunc,
		"setunion": stdlib.SetUnionFunc, "slice": stdlib.SliceFunc, "sort": stdlib.SortFunc,
		"values": stdlib.ValuesFunc, "zipmap": stdlib.ZipmapFunc, "jsonencode": stdlib.JSONEncodeFunc,
		"jsondecode": stdlib.JSONDecodeFunc,
		"tostring":   convertFunc(cty.String), "tonumber": convertFunc(cty.Number),
		"tobool": convertFunc(cty.Bool),
		"tolist": convertFunc(cty.List(cty.DynamicPseudoType)),
		"toset":  convertFunc(cty.Set(cty.DynamicPseudoType)),
		"tomap":  convertFunc(cty.Map(cty.DynamicPseudoType)),
		"try":    tryfunc.TryFunc, "can": tryfunc.CanFunc,
		"one": oneFunc, "alltrue": boolsFunc(true), "anytrue": boolsFunc(false),
		"startswith": strPredicate(strings.HasPrefix), "endswith": strPredicate(strings.HasSuffix),
		"strcontains": strPredicate(strings.Contains),
		"cidrsubnet":  cidrSubnetFunc, "cidrhost": cidrHostFunc, "cidrnetmask": cidrNetmaskFunc,
		"cidrsubnets": cidrSubnetsFunc,
	}
}

func convertFunc(to cty.Type) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "v", Type: cty.DynamicPseudoType, AllowNull: true, AllowUnknown: true, AllowDynamicType: true}},
		Type: func(args []cty.Value) (cty.Type, error) {
			if !args[0].IsKnown() {
				return cty.DynamicPseudoType, nil
			}
			v, err := convert.Convert(args[0], to)
			if err != nil {
				return cty.NilType, err
			}
			return v.Type(), nil
		},
		Impl: func(args []cty.Value, ret cty.Type) (cty.Value, error) {
			if !args[0].IsKnown() {
				return cty.UnknownVal(ret), nil
			}
			return convert.Convert(args[0], to)
		},
	})
}

var oneFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
	Type:   function.StaticReturnType(cty.DynamicPseudoType),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		v := args[0]
		if !v.CanIterateElements() {
			return cty.NilVal, fmt.Errorf("one: need a list or set")
		}
		switch v.LengthInt() {
		case 0:
			return cty.NullVal(cty.DynamicPseudoType), nil
		case 1:
			it := v.ElementIterator()
			it.Next()
			_, e := it.Element()
			return e, nil
		}
		return cty.NilVal, fmt.Errorf("one: more than one element")
	},
})

func boolsFunc(all bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "list", Type: cty.DynamicPseudoType}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			v := args[0]
			if !v.CanIterateElements() {
				return cty.NilVal, fmt.Errorf("need a list")
			}
			for it := v.ElementIterator(); it.Next(); {
				_, e := it.Element()
				b, err := convert.Convert(e, cty.Bool)
				if err != nil || b.IsNull() {
					return cty.NilVal, fmt.Errorf("need booleans")
				}
				if b.True() != all {
					return cty.BoolVal(!all), nil
				}
			}
			return cty.BoolVal(all), nil
		},
	})
}

func strPredicate(fn func(s, sub string) bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "s", Type: cty.String}, {Name: "sub", Type: cty.String}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			return cty.BoolVal(fn(args[0].AsString(), args[1].AsString())), nil
		},
	})
}

// ---------------------------------------------------------------------------
// CIDR functions (Terraform semantics)

var cidrSubnetFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "prefix", Type: cty.String}, {Name: "newbits", Type: cty.Number}, {Name: "netnum", Type: cty.Number}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		p, err := netip.ParsePrefix(args[0].AsString())
		if err != nil {
			return cty.NilVal, err
		}
		newbits, _ := args[1].AsBigFloat().Int64()
		netnum, _ := args[2].AsBigFloat().Int(nil)
		s, err := cidrSubnet(p, int(newbits), netnum)
		if err != nil {
			return cty.NilVal, err
		}
		return cty.StringVal(s.String()), nil
	},
})

var cidrSubnetsFunc = function.New(&function.Spec{
	Params:   []function.Parameter{{Name: "prefix", Type: cty.String}},
	VarParam: &function.Parameter{Name: "newbits", Type: cty.Number},
	Type:     function.StaticReturnType(cty.List(cty.String)),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		p, err := netip.ParsePrefix(args[0].AsString())
		if err != nil {
			return cty.NilVal, err
		}
		p = p.Masked()
		bits := p.Addr().BitLen()
		cur := new(big.Int).Set(addrInt(p.Addr()))
		var out []cty.Value
		for _, a := range args[1:] {
			nb, _ := a.AsBigFloat().Int64()
			plen := p.Bits() + int(nb)
			if nb < 1 || plen > bits {
				return cty.NilVal, fmt.Errorf("cidrsubnets: invalid newbits %d", nb)
			}
			size := new(big.Int).Lsh(big.NewInt(1), uint(bits-plen))
			// align the cursor up to the block size
			rem := new(big.Int).Mod(cur, size)
			if rem.Sign() != 0 {
				cur.Add(cur, new(big.Int).Sub(size, rem))
			}
			addr, ok := intAddr(cur, bits)
			if !ok || !p.Contains(addr) {
				return cty.NilVal, fmt.Errorf("cidrsubnets: not enough space in %s", p)
			}
			out = append(out, cty.StringVal(netip.PrefixFrom(addr, plen).String()))
			cur.Add(cur, size)
		}
		if len(out) == 0 {
			return cty.ListValEmpty(cty.String), nil
		}
		return cty.ListVal(out), nil
	},
})

var cidrHostFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "prefix", Type: cty.String}, {Name: "hostnum", Type: cty.Number}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		p, err := netip.ParsePrefix(args[0].AsString())
		if err != nil {
			return cty.NilVal, err
		}
		p = p.Masked()
		hn, _ := args[1].AsBigFloat().Int(nil)
		bits := p.Addr().BitLen()
		size := new(big.Int).Lsh(big.NewInt(1), uint(bits-p.Bits()))
		if hn.Sign() < 0 {
			hn.Add(hn, size)
		}
		if hn.Sign() < 0 || hn.Cmp(size) >= 0 {
			return cty.NilVal, fmt.Errorf("cidrhost: host number out of range for %s", p)
		}
		addr, ok := intAddr(new(big.Int).Add(addrInt(p.Addr()), hn), bits)
		if !ok {
			return cty.NilVal, fmt.Errorf("cidrhost: overflow")
		}
		return cty.StringVal(addr.String()), nil
	},
})

var cidrNetmaskFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "prefix", Type: cty.String}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		p, err := netip.ParsePrefix(args[0].AsString())
		if err != nil {
			return cty.NilVal, err
		}
		if !p.Addr().Is4() {
			return cty.NilVal, fmt.Errorf("cidrnetmask: IPv4 only")
		}
		m := uint32(0xffffffff) << (32 - p.Bits())
		if p.Bits() == 0 {
			m = 0
		}
		return cty.StringVal(netip.AddrFrom4([4]byte{byte(m >> 24), byte(m >> 16), byte(m >> 8), byte(m)}).String()), nil
	},
})

// subnet is Terraform's cidrsubnet.
func cidrSubnet(p netip.Prefix, newbits int, netnum *big.Int) (netip.Prefix, error) {
	p = p.Masked()
	bits := p.Addr().BitLen()
	plen := p.Bits() + newbits
	if newbits < 0 || plen > bits {
		return netip.Prefix{}, fmt.Errorf("cidrsubnet: %d bits won't fit in %s", newbits, p)
	}
	if netnum.Sign() < 0 || netnum.BitLen() > newbits {
		return netip.Prefix{}, fmt.Errorf("cidrsubnet: network number %s doesn't fit in %d bits", netnum, newbits)
	}
	v := new(big.Int).Lsh(netnum, uint(bits-plen))
	addr, ok := intAddr(v.Or(v, addrInt(p.Addr())), bits)
	if !ok {
		return netip.Prefix{}, fmt.Errorf("cidrsubnet: overflow")
	}
	return netip.PrefixFrom(addr, plen), nil
}

func addrInt(a netip.Addr) *big.Int {
	b := a.AsSlice()
	return new(big.Int).SetBytes(b)
}

func intAddr(v *big.Int, bits int) (netip.Addr, bool) {
	n := bits / 8
	if v.Sign() < 0 || v.BitLen() > bits {
		return netip.Addr{}, false
	}
	buf := make([]byte, n)
	v.FillBytes(buf)
	return netip.AddrFromSlice(buf)
}
