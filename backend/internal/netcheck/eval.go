package netcheck

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// evaluator evaluates a workflow's expressions statically: variables take
// the active environment's value, else their default; locals and resource
// attributes are evaluated in dependency order; count/for_each expand into
// instances. Provider-computed attributes become reference placeholders
// (IDs) or unknown values.
type evaluator struct {
	wf        *model.Workflow
	vars      map[string]cty.Value
	locals    map[string]cty.Value
	resources map[string]map[string]cty.Value // type -> name -> value
	data      map[string]map[string]cty.Value
	funcs     map[string]function.Function
	// referenced lists, per address, the attributes other expressions read
	// from it, so its object carries them (as placeholders).
	referenced map[string]map[string]bool
	base       *hcl.EvalContext // cached context; reset after each vertex
	entities   []*entity
	notes      []string
	// exprs memoizes parsed sources: references, ordering and evaluation
	// each read every expression, and parsing dominated a design pass.
	exprs    map[string]hclsyntax.Expression
	nodes    map[string]*model.Node // by vertex
	typeObjs map[string]cty.Value   // typeObject results by type key
}

var metaAttrs = map[string]bool{"count": true, "for_each": true, "provider": true, "depends_on": true}
var metaBlocks = map[string]bool{"lifecycle": true, "provisioner": true, "connection": true, "timeouts": true}

// evaluateDesign returns the resource instances of wf as the design
// describes them.
func evaluateDesign(wf *model.Workflow) ([]*entity, []string) {
	ev := &evaluator{
		wf:         wf,
		vars:       map[string]cty.Value{},
		locals:     map[string]cty.Value{},
		resources:  map[string]map[string]cty.Value{},
		data:       map[string]map[string]cty.Value{},
		funcs:      functions(),
		referenced: map[string]map[string]bool{},
		exprs:      map[string]hclsyntax.Expression{},
		nodes:      make(map[string]*model.Node, len(wf.Nodes)),
		typeObjs:   map[string]cty.Value{},
	}
	for i := range wf.Nodes {
		if v := vertexOf(&wf.Nodes[i]); ev.nodes[v] == nil {
			ev.nodes[v] = &wf.Nodes[i] // the first, like a linear search
		}
	}
	ev.evalVariables()
	ev.collectReferences()
	for _, v := range ev.order() {
		switch {
		case strings.HasPrefix(v, "local."):
			ev.evalLocal(strings.TrimPrefix(v, "local."))
		default:
			ev.evalNode(ev.nodes[v])
		}
		ev.base = nil
	}
	return ev.entities, ev.notes
}

func (ev *evaluator) evalVariables() {
	var env *model.Environment
	for i := range ev.wf.Environments {
		if ev.wf.Environments[i].Name == ev.wf.ActiveEnvironment {
			env = &ev.wf.Environments[i]
		}
	}
	for _, v := range ev.wf.Variables {
		val := cty.DynamicVal
		if env != nil {
			if ev, ok := env.Values[v.Name]; ok && ev.Expr != "" {
				val = evalConst(ev.Expr)
			}
		}
		if val == cty.DynamicVal {
			for _, a := range v.Body.Attrs {
				if a.Name == "default" {
					val = evalConst(a.Value.Expr)
				}
			}
		}
		for _, a := range v.Body.Attrs {
			if a.Name != "type" || !val.IsWhollyKnown() {
				continue
			}
			if expr := parseExpr(a.Value.Expr); expr != nil {
				if ty, diags := typeexpr.TypeConstraint(expr); !diags.HasErrors() {
					if c, err := convert.Convert(val, ty); err == nil {
						val = c
					}
				}
			}
		}
		ev.vars[v.Name] = val
	}
}

func evalConst(src string) cty.Value {
	expr := parseExpr(src)
	if expr == nil {
		return cty.DynamicVal
	}
	v, diags := expr.Value(&hcl.EvalContext{Functions: functions()})
	if diags.HasErrors() {
		return cty.DynamicVal
	}
	return v
}

func parseExpr(src string) hclsyntax.Expression {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "expr", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	return e
}

// parse is parseExpr memoized for the evaluator's lifetime (nil = empty or
// invalid source). Parsed expressions are never mutated, only evaluated.
func (ev *evaluator) parse(src string) hclsyntax.Expression {
	e, ok := ev.exprs[src]
	if !ok {
		e = parseExpr(src)
		ev.exprs[src] = e
	}
	return e
}

// ---------------------------------------------------------------------------
// Dependency order

func vertexOf(n *model.Node) string { return hclcodec.Address(n) }

// refVertex maps a traversal to the vertex it reads ("aws_vpc.main",
// "data.aws_x.y", "local.z") and the attribute read from a resource.
func refVertex(t hcl.Traversal) (vertex, attr string) {
	names := []string{t.RootName()}
	for _, step := range t[1:] {
		switch s := step.(type) {
		case hcl.TraverseAttr:
			names = append(names, s.Name)
		case hcl.TraverseIndex, hcl.TraverseSplat:
			if len(names) < 2 || (names[0] == "data" && len(names) < 3) {
				return "", ""
			}
			continue
		}
	}
	switch names[0] {
	case "var", "path", "terraform", "count", "each", "self", "module":
		return "", ""
	case "local":
		if len(names) > 1 {
			return "local." + names[1], ""
		}
		return "", ""
	case "data":
		if len(names) > 2 {
			v := "data." + names[1] + "." + names[2]
			if len(names) > 3 {
				return v, names[3]
			}
			return v, ""
		}
		return "", ""
	}
	if len(names) > 1 {
		if len(names) > 2 {
			return names[0] + "." + names[1], names[2]
		}
		return names[0] + "." + names[1], ""
	}
	return "", ""
}

func bodyExprs(b *model.Body, fn func(src string)) {
	for _, a := range b.Attrs {
		fn(a.Value.Expr)
	}
	for i := range b.Blocks {
		bodyExprs(&b.Blocks[i].Body, fn)
	}
}

func (ev *evaluator) traversals(src string) []hcl.Traversal {
	e := ev.parse(src)
	if e == nil {
		return nil
	}
	return e.Variables()
}

func (ev *evaluator) collectReferences() {
	note := func(src string) {
		for _, t := range ev.traversals(src) {
			v, attr := refVertex(t)
			if v == "" || attr == "" || strings.HasPrefix(v, "local.") {
				continue
			}
			if ev.referenced[v] == nil {
				ev.referenced[v] = map[string]bool{}
			}
			ev.referenced[v][attr] = true
		}
	}
	for i := range ev.wf.Nodes {
		bodyExprs(&ev.wf.Nodes[i].Body, note)
	}
	for _, l := range ev.wf.Locals {
		note(l.Value.Expr)
	}
	for _, o := range ev.wf.Outputs {
		bodyExprs(&o.Body, note)
	}
}

// order returns resources, data sources and locals in dependency order
// (Kahn's algorithm; ties by name; vertices in cycles last).
func (ev *evaluator) order() []string {
	deps := map[string]map[string]bool{}
	add := func(v string) {
		if deps[v] == nil {
			deps[v] = map[string]bool{}
		}
	}
	for i := range ev.wf.Nodes {
		if ev.wf.Nodes[i].Kind != model.KindModule {
			add(vertexOf(&ev.wf.Nodes[i]))
		}
	}
	for _, l := range ev.wf.Locals {
		add("local." + l.Name)
	}
	link := func(v, src string) {
		for _, t := range ev.traversals(src) {
			if d, _ := refVertex(t); d != "" && d != v {
				if _, ok := deps[d]; ok {
					deps[v][d] = true
				}
			}
		}
	}
	for i := range ev.wf.Nodes {
		n := &ev.wf.Nodes[i]
		if n.Kind == model.KindModule {
			continue
		}
		v := vertexOf(n)
		bodyExprs(&n.Body, func(src string) { link(v, src) })
	}
	for _, l := range ev.wf.Locals {
		link("local."+l.Name, l.Value.Expr)
	}
	var out []string
	done := map[string]bool{}
	for len(done) < len(deps) {
		var ready []string
		for v, ds := range deps {
			if done[v] {
				continue
			}
			ok := true
			for d := range ds {
				if !done[d] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, v)
			}
		}
		if len(ready) == 0 { // cycle: take the rest in name order
			for v := range deps {
				if !done[v] {
					ready = append(ready, v)
				}
			}
		}
		sort.Strings(ready)
		for _, v := range ready {
			done[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Evaluation

func objectOrEmpty(m map[string]cty.Value) cty.Value {
	if len(m) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(m)
}

func (ev *evaluator) ctx() *hcl.EvalContext {
	if ev.base != nil {
		return ev.base
	}
	vars := map[string]cty.Value{
		"var":       objectOrEmpty(ev.vars),
		"local":     objectOrEmpty(ev.locals),
		"path":      cty.ObjectVal(map[string]cty.Value{"module": cty.StringVal("."), "root": cty.StringVal("."), "cwd": cty.StringVal(".")}),
		"terraform": cty.ObjectVal(map[string]cty.Value{"workspace": cty.StringVal("default")}),
		"module":    cty.DynamicVal,
	}
	for typ, byName := range ev.resources {
		vars[typ] = ev.typeObject(typ, byName)
	}
	data := map[string]cty.Value{}
	for typ, byName := range ev.data {
		data[typ] = ev.typeObject("data."+typ, byName)
	}
	vars["data"] = objectOrEmpty(data)
	ev.base = &hcl.EvalContext{Variables: vars, Functions: ev.funcs}
	return ev.base
}

// typeObject returns the object of one resource (or "data.") type's
// instances. Only the type of the vertex just evaluated changes between
// contexts, so rebuilding every type per vertex made a pass quadratic.
func (ev *evaluator) typeObject(key string, byName map[string]cty.Value) cty.Value {
	v, ok := ev.typeObjs[key]
	if !ok {
		v = objectOrEmpty(byName)
		ev.typeObjs[key] = v
	}
	return v
}

// eval evaluates src; any error (unknown reference, unsupported function,
// type mismatch) gives an unknown value.
func (ev *evaluator) eval(src string, ctx *hcl.EvalContext) cty.Value {
	e := ev.parse(src)
	if e == nil {
		return cty.DynamicVal
	}
	v, diags := e.Value(ctx)
	if diags.HasErrors() {
		return cty.DynamicVal
	}
	return v
}

func (ev *evaluator) evalLocal(name string) {
	for _, l := range ev.wf.Locals {
		if l.Name == name {
			ev.locals[name] = ev.eval(l.Value.Expr, ev.ctx())
		}
	}
}

func (ev *evaluator) evalNode(n *model.Node) {
	if n == nil || n.Kind == model.KindModule {
		return
	}
	addr := vertexOf(n)
	base := ev.ctx()
	var countA, forEachA *model.Attr
	for i := range n.Body.Attrs {
		switch n.Body.Attrs[i].Name {
		case "count":
			countA = &n.Body.Attrs[i]
		case "for_each":
			forEachA = &n.Body.Attrs[i]
		}
	}
	instance := func(ctx *hcl.EvalContext, iaddr, key string, multi bool) cty.Value {
		obj, attrs := ev.evalBody(&n.Body, ctx)
		e := &entity{node: n, typ: n.Type, name: n.Name, addr: iaddr, key: key, data: n.Kind == model.KindData, attrs: attrs, id: refPrefix + iaddr, multi: multi}
		if !e.data {
			ev.entities = append(ev.entities, e)
		}
		return ev.withComputed(addr, iaddr, obj)
	}
	var value cty.Value
	switch {
	case forEachA != nil:
		fe := ev.eval(forEachA.Value.Expr, base)
		items, ok := forEachItems(fe)
		if !ok {
			child := base.NewChild()
			child.Variables = map[string]cty.Value{"each": cty.ObjectVal(map[string]cty.Value{"key": cty.UnknownVal(cty.String), "value": cty.DynamicVal})}
			instance(child, addr+"[*]", "*", true)
			value = cty.DynamicVal
			break
		}
		objs := map[string]cty.Value{}
		for _, k := range sortedKeys(items) {
			child := base.NewChild()
			child.Variables = map[string]cty.Value{"each": cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal(k), "value": items[k]})}
			objs[k] = instance(child, fmt.Sprintf("%s[%s]", addr, strconv.Quote(k)), k, false)
		}
		value = objectOrEmpty(objs)
	case countA != nil:
		c := ev.eval(countA.Value.Expr, base)
		cnt, ok := countOf(c)
		if !ok {
			child := base.NewChild()
			child.Variables = map[string]cty.Value{"count": cty.ObjectVal(map[string]cty.Value{"index": cty.UnknownVal(cty.Number)})}
			instance(child, addr+"[*]", "*", true)
			value = cty.DynamicVal
			break
		}
		objs := make([]cty.Value, 0, cnt)
		for i := range cnt {
			child := base.NewChild()
			child.Variables = map[string]cty.Value{"count": cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(int64(i))})}
			objs = append(objs, instance(child, fmt.Sprintf("%s[%d]", addr, i), strconv.Itoa(i), false))
		}
		if len(objs) == 0 {
			value = cty.EmptyTupleVal
		} else {
			value = cty.TupleVal(objs)
		}
	default:
		value = instance(base, addr, "", false)
	}
	target, key := ev.resources, n.Type
	if n.Kind == model.KindData {
		target, key = ev.data, "data."+n.Type
	}
	if target[n.Type] == nil {
		target[n.Type] = map[string]cty.Value{}
	}
	target[n.Type][n.Name] = value
	delete(ev.typeObjs, key)
}

func forEachItems(v cty.Value) (map[string]cty.Value, bool) {
	if !v.IsWhollyKnown() || v.IsNull() {
		return nil, false
	}
	t := v.Type()
	out := map[string]cty.Value{}
	switch {
	case t.IsMapType() || t.IsObjectType():
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			out[k.AsString()] = e
		}
	case t.IsSetType() || t.IsListType() || t.IsTupleType():
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			s, err := convert.Convert(e, cty.String)
			if err != nil || s.IsNull() {
				return nil, false
			}
			out[s.AsString()] = s
		}
	default:
		return nil, false
	}
	return out, true
}

func countOf(v cty.Value) (int, bool) {
	if !v.IsKnown() || v.IsNull() {
		return 0, false
	}
	n, err := convert.Convert(v, cty.Number)
	if err != nil || n.IsNull() {
		return 0, false
	}
	i, acc := n.AsBigFloat().Int64()
	if acc != 0 && !n.AsBigFloat().IsInt() || i < 0 || i > 1000 {
		return 0, false
	}
	return int(i), true
}

// evalBody evaluates a body into a cty object (for references) and its Go
// form (for the analysis).
func (ev *evaluator) evalBody(b *model.Body, ctx *hcl.EvalContext) (cty.Value, map[string]any) {
	obj := ev.bodyValue(b, ctx)
	attrs, _ := fromCty(obj).(map[string]any)
	if attrs == nil {
		attrs = map[string]any{}
	}
	return obj, attrs
}

func (ev *evaluator) bodyValue(b *model.Body, ctx *hcl.EvalContext) cty.Value {
	vals := map[string]cty.Value{}
	for _, a := range b.Attrs {
		if metaAttrs[a.Name] {
			continue
		}
		vals[a.Name] = ev.eval(a.Value.Expr, ctx)
	}
	lists := map[string][]cty.Value{}
	unknownList := map[string]bool{}
	var order []string
	appendBlock := func(typ string, v cty.Value) {
		if _, ok := lists[typ]; !ok {
			order = append(order, typ)
		}
		lists[typ] = append(lists[typ], v)
	}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		if metaBlocks[blk.Type] {
			continue
		}
		if blk.Type == "dynamic" && len(blk.Labels) == 1 {
			typ := blk.Labels[0]
			if _, ok := lists[typ]; !ok {
				order = append(order, typ)
				lists[typ] = nil
			}
			var fe cty.Value = cty.DynamicVal
			iter := typ
			var content *model.Body
			for _, a := range blk.Body.Attrs {
				switch a.Name {
				case "for_each":
					fe = ev.eval(a.Value.Expr, ctx)
				case "iterator":
					if e := ev.parse(a.Value.Expr); e != nil {
						if tr, diags := hcl.AbsTraversalForExpr(e); !diags.HasErrors() {
							iter = tr.RootName()
						}
					}
				}
			}
			for j := range blk.Body.Blocks {
				if blk.Body.Blocks[j].Type == "content" {
					content = &blk.Body.Blocks[j].Body
				}
			}
			items, ok := forEachItems(fe)
			if !ok || content == nil {
				unknownList[typ] = true
				continue
			}
			for _, k := range sortedKeys(items) {
				child := ctx.NewChild()
				child.Variables = map[string]cty.Value{iter: cty.ObjectVal(map[string]cty.Value{"key": cty.StringVal(k), "value": items[k]})}
				appendBlock(typ, ev.bodyValue(content, child))
			}
			continue
		}
		appendBlock(blk.Type, ev.bodyValue(&blk.Body, ctx))
	}
	for _, typ := range order {
		switch {
		case unknownList[typ]:
			vals[typ] = cty.DynamicVal
		case len(lists[typ]) == 0:
			vals[typ] = cty.EmptyTupleVal
		default:
			vals[typ] = cty.TupleVal(lists[typ])
		}
	}
	return objectOrEmpty(vals)
}

// withComputed adds the provider-computed attributes other expressions read
// (id, arn, *_id, ...) to an instance object as placeholders.
func (ev *evaluator) withComputed(vertex, iaddr string, obj cty.Value) cty.Value {
	vals := map[string]cty.Value{}
	if obj.Type().IsObjectType() {
		for k, v := range obj.AsValueMap() {
			vals[k] = v
		}
	}
	set := func(name string) {
		if _, ok := vals[name]; ok {
			return
		}
		switch {
		case name == "id" || name == "arn":
			vals[name] = cty.StringVal(refPrefix + iaddr)
		case name == "name" || name == "self_link" || strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "_arn"):
			vals[name] = cty.StringVal(refPrefix + iaddr + "#" + name)
		default:
			vals[name] = cty.DynamicVal
		}
	}
	set("id")
	set("arn")
	for name := range ev.referenced[vertex] {
		set(name)
	}
	return cty.ObjectVal(vals)
}
