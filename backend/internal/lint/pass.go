package lint

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// pass is the state shared by the rules of one Check.
type pass struct {
	wf   *model.Workflow
	cat  *catalog.Catalog
	mods *model.ModuleTree // local modules; nil = none read
	rule *rule
	out  []model.Diagnostic

	byAddr map[string]*model.Node
}

func newPass(wf *model.Workflow, cat *catalog.Catalog, mods *model.ModuleTree) *pass {
	p := &pass{wf: wf, cat: cat, mods: mods, byAddr: make(map[string]*model.Node, len(wf.Nodes))}
	for i := range wf.Nodes {
		p.byAddr[hclcodec.Address(&wf.Nodes[i])] = &wf.Nodes[i]
	}
	return p
}

// finding is one report of the current rule.
type finding struct {
	severity  model.Severity // "" = the rule's severity
	node      *model.Node
	attribute string
	message   string
	why       string
	fix       string
}

func (p *pass) report(f finding) {
	sev := f.severity
	if sev == "" {
		sev = p.rule.Severity
	}
	d := model.Diagnostic{Severity: sev, Code: p.rule.Code, Message: f.message, Detail: f.why, Fix: f.fix, Attribute: f.attribute}
	if f.node != nil {
		d.NodeID = f.node.ID
	}
	p.out = append(p.out, d)
}

// resources returns the managed resource nodes.
func (p *pass) resources() []*model.Node {
	var out []*model.Node
	for i := range p.wf.Nodes {
		if n := &p.wf.Nodes[i]; n.Kind == model.KindResource {
			out = append(out, n)
		}
	}
	return out
}

func (p *pass) ofType(types ...string) []*model.Node {
	var out []*model.Node
	for _, n := range p.resources() {
		for _, t := range types {
			if n.Type == t {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func (p *pass) hasLocal(name string) bool {
	for _, l := range p.wf.Locals {
		if l.Name == name {
			return true
		}
	}
	return false
}

func addr(n *model.Node) string { return hclcodec.Address(n) }

// ---------------------------------------------------------------------------
// Values

func getAttr(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

func blocks(b *model.Body, typ string) []*model.Block {
	var out []*model.Block
	for i := range b.Blocks {
		if b.Blocks[i].Type == typ {
			out = append(out, &b.Blocks[i])
		}
	}
	return out
}

func source(v model.Value) string {
	if v.Expr != "" {
		return v.Expr
	}
	if len(v.Literal) > 0 {
		if s, err := hclcodec.ExprFromLiteral(v.Literal); err == nil {
			return s
		}
	}
	return ""
}

// literal returns v's constant JSON value (nil when it is not constant).
func literal(v model.Value) any {
	raw := v.Literal
	if len(raw) == 0 && v.Expr != "" {
		raw, _ = hclcodec.LiteralFromExpr(v.Expr)
	}
	if len(raw) == 0 {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func attrString(b *model.Body, name string) (string, bool) {
	a := getAttr(b, name)
	if a == nil {
		return "", false
	}
	s, ok := literal(a.Value).(string)
	return s, ok
}

func attrBool(b *model.Body, name string) (value, set bool) {
	a := getAttr(b, name)
	if a == nil {
		return false, false
	}
	switch v := literal(a.Value).(type) {
	case bool:
		return v, true
	case string:
		return v == "true", v == "true" || v == "false"
	}
	return false, false
}

// attrIsTrue: set to literal true.
func attrIsTrue(b *model.Body, name string) bool {
	v, set := attrBool(b, name)
	return set && v
}

// attrNotFalse: set to anything but a literal false (a reference counts as
// intent).
func attrNotFalse(b *model.Body, name string) bool {
	a := getAttr(b, name)
	if a == nil {
		return false
	}
	v, set := attrBool(b, name)
	return !set || v
}

func attrNumber(b *model.Body, name string) (float64, bool) {
	a := getAttr(b, name)
	if a == nil {
		return 0, false
	}
	switch v := literal(a.Value).(type) {
	case float64:
		return v, true
	case string:
		var f float64
		_, err := fmt.Sscan(v, &f)
		return f, err == nil
	}
	return 0, false
}

// strings returns the constant strings of v: a string, or the string items
// of a constant list.
func stringsOf(v model.Value) []string {
	switch t := literal(v).(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func attrStrings(b *model.Body, name string) []string {
	a := getAttr(b, name)
	if a == nil {
		return nil
	}
	return stringsOf(a.Value)
}

// parse parses v's expression (nil when invalid).
func parse(v model.Value) hclsyntax.Expression {
	src := source(v)
	if src == "" {
		return nil
	}
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "expr", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	return e
}

// references returns the traversals of v as dotted names
// ("aws_s3_bucket.b.id", "var.x", "local.name_prefix").
func references(v model.Value) []string {
	e := parse(v)
	if e == nil {
		return nil
	}
	var out []string
	for _, tr := range e.Variables() {
		parts := []string{tr.RootName()}
		for _, step := range tr[1:] {
			if a, ok := step.(hcl.TraverseAttr); ok {
				parts = append(parts, a.Name)
			} else {
				break
			}
		}
		out = append(out, strings.Join(parts, "."))
	}
	return out
}

// referencesNode reports whether v refers to node n (address prefix).
func referencesNode(v model.Value, n *model.Node) bool {
	a := addr(n)
	for _, r := range references(v) {
		if r == a || strings.HasPrefix(r, a+".") {
			return true
		}
	}
	return false
}

// referencedNode returns the node attribute name of body refers to first.
func (p *pass) referencedNode(b *model.Body, name string) *model.Node {
	a := getAttr(b, name)
	if a == nil {
		return nil
	}
	for _, r := range references(a.Value) {
		parts := strings.Split(r, ".")
		for k := len(parts); k >= 2; k-- {
			if n := p.byAddr[strings.Join(parts[:k], ".")]; n != nil {
				return n
			}
		}
	}
	return nil
}

// walkAttrs visits every attribute of body and its nested blocks with its
// dotted path ("ingress[0].cidr_blocks"); lifecycle/provisioner/connection
// blocks are skipped.
func walkAttrs(b *model.Body, prefix string, fn func(path string, a *model.Attr)) {
	for i := range b.Attrs {
		fn(prefix+b.Attrs[i].Name, &b.Attrs[i])
	}
	count := map[string]int{}
	for i := range b.Blocks {
		blk := &b.Blocks[i]
		idx := count[blk.Type]
		count[blk.Type]++
		switch blk.Type {
		case "lifecycle", "provisioner", "connection", "precondition", "postcondition":
			continue
		}
		walkAttrs(&blk.Body, fmt.Sprintf("%s%s[%d].", prefix, blk.Type, idx), fn)
	}
}

// providerKey is the provider configuration a resource uses: "aws" or
// "aws.west".
func providerKey(n *model.Node) string {
	if a := getAttr(&n.Body, "provider"); a != nil {
		if s := strings.TrimSpace(source(a.Value)); s != "" {
			return s
		}
	}
	name, _, _ := strings.Cut(n.Type, "_")
	return name
}

// providerBlock returns the provider configuration for key ("aws",
// "aws.west").
func (p *pass) providerBlock(key string) *model.NamedBlock {
	name, alias, _ := strings.Cut(key, ".")
	for i := range p.wf.Providers {
		pb := &p.wf.Providers[i]
		if pb.Name != name {
			continue
		}
		a, _ := attrString(&pb.Body, "alias")
		if a == alias {
			return pb
		}
	}
	return nil
}

func (p *pass) schemaHas(n *model.Node, attr string) bool {
	s, ok := p.cat.Schema(n.Kind, n.Type)
	if !ok {
		return false
	}
	for _, a := range s.Block.Attributes {
		if a.Name == attr {
			return a.Required || a.Optional
		}
	}
	return false
}

// list renders up to three addresses: "a, b and 2 more".
func list(nodes []*model.Node) string {
	var names []string
	for i, n := range nodes {
		if i == 3 {
			names = append(names, fmt.Sprintf("%d more", len(nodes)-3))
			break
		}
		names = append(names, addr(n))
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
