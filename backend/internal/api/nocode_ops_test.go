package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The gestures of the no-code scripts (see nocode_test.go). Each one sends
// what the desktop app sends for that gesture, and refuses what the app's
// forms can't express (that needs code).

var nocodeOps = map[string]func(*nocodeRun, *yaml.Node) error{
	// Terraform
	"add":         (*nocodeRun).tfAdd,
	"rename":      (*nocodeRun).tfRename,
	"delete":      (*nocodeRun).tfDelete,
	"set":         (*nocodeRun).tfSet,
	"variable":    (*nocodeRun).tfVariable,
	"provider":    (*nocodeRun).tfProvider, // nocode_tf_test.go
	"connect":     (*nocodeRun).tfConnect,
	"accessAWS":   (*nocodeRun).accessAWS,
	"accessAzure": (*nocodeRun).accessAzure,
	"accessGCP":   (*nocodeRun).accessGCP,
	"accessOCI":   (*nocodeRun).accessOCI,
	// Docker
	"composeAddService":    (*nocodeRun).composeAddService,
	"composePatch":         (*nocodeRun).composePatch,
	"composeDeleteService": (*nocodeRun).composeDeleteService,
	"composeAddTop":        (*nocodeRun).composeAddTop,
	"composePatchTop":      (*nocodeRun).composePatchTop,
	"composeDeleteTop":     (*nocodeRun).composeDeleteTop,
	"composeConnect":       (*nocodeRun).composeConnect,
	"envCreate":            (*nocodeRun).envCreate,
	"envSettings":          (*nocodeRun).envSettings,
	"envVariable":          (*nocodeRun).envVariable,
	"envActivate":          (*nocodeRun).envActivate, // nocode_docker_test.go
	"composeEnv":           (*nocodeRun).composeEnv,
	"composeStage":         (*nocodeRun).composeStage,
	// Kubernetes
	"kubeAdd":     (*nocodeRun).kubeAdd,
	"kubePatch":   (*nocodeRun).kubePatch,
	"kubeConnect": (*nocodeRun).kubeConnect,
	"kubeAccess":  (*nocodeRun).kubeAccess,
	"kubeDelete":  (*nocodeRun).kubeDelete,
}

// ---------------------------------------------------------------------------
// Terraform: canvas and inspector

// tfNameRE is the inspector's Terraform name rule (inspector.tsx NAME_RE).
var tfNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

func (r *nocodeRun) tfNode(wf *model.Workflow, ref string) (*model.Node, error) {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == ref || hclcodec.Address(&wf.Nodes[i]) == ref {
			return &wf.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("no node %q on the canvas", ref)
}

func (r *nocodeRun) tfNodeID(ref string) (string, error) {
	wf, err := r.workflow()
	if err != nil {
		return "", err
	}
	n, err := r.tfNode(wf, ref)
	if err != nil {
		return "", err
	}
	return n.ID, nil
}

// add: drop a palette entry (canvas/actions.ts addResource), then type its
// Terraform name in the inspector.
func (r *nocodeRun) tfAdd(n *yaml.Node) error {
	var a struct {
		Type string         `yaml:"type"`
		Kind model.NodeKind `yaml:"kind"`
		Name string         `yaml:"name"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	if a.Kind == "" {
		a.Kind = model.KindResource
	}
	if a.Kind != model.KindResource && a.Kind != model.KindData {
		return fmt.Errorf("the palette adds resources and data sources, not %q", a.Kind)
	}
	var res struct {
		NodeID string `json:"nodeId"`
	}
	if err := r.call("POST", r.wfPath("/nodes"), map[string]any{"kind": a.Kind, "type": a.Type, "position": model.Position{}}, &res); err != nil {
		return err
	}
	if a.Name == "" {
		return nil
	}
	return r.patchName(res.NodeID, a.Name)
}

func (r *nocodeRun) patchName(nodeID, name string) error {
	if !tfNameRE.MatchString(name) {
		return fmt.Errorf("the inspector refuses Terraform name %q", name)
	}
	return r.call("PATCH", r.wfPath("/nodes/"+nodeID), map[string]string{"name": name}, nil)
}

func (r *nocodeRun) tfRename(n *yaml.Node) error {
	var a struct{ Node, Name string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, err := r.tfNodeID(a.Node)
	if err != nil {
		return err
	}
	return r.patchName(id, a.Name)
}

func (r *nocodeRun) tfDelete(n *yaml.Node) error {
	var a struct{ Node string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, err := r.tfNodeID(a.Node)
	if err != nil {
		return err
	}
	return r.call("DELETE", r.wfPath("/nodes/"+id), nil, nil)
}

func (r *nocodeRun) schema(kind model.NodeKind, typ string) (*model.ResourceSchema, error) {
	key := string(kind) + "/" + typ
	if s, ok := r.schemas[key]; ok {
		return s, nil
	}
	var s model.ResourceSchema
	if err := r.call("GET", "/catalog/resources/"+string(kind)+"/"+typ, nil, &s); err != nil {
		return nil, err
	}
	r.schemas[key] = &s
	return &s, nil
}

// set: inspector form edits of one node (block-form.tsx, attr-field.tsx,
// meta-arguments.tsx), saved like the workflow store (PUT the workflow).
func (r *nocodeRun) tfSet(n *yaml.Node) error {
	var a struct {
		Node  string    `yaml:"node"`
		Attrs yaml.Node `yaml:"attrs"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	if a.Attrs.Kind != yaml.MappingNode || len(a.Attrs.Content) == 0 {
		return fmt.Errorf("attrs is a map of path: value")
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	node, err := r.tfNode(wf, a.Node)
	if err != nil {
		return err
	}
	if node.Kind != model.KindResource && node.Kind != model.KindData {
		return fmt.Errorf("%s: module inputs are expressions (code)", a.Node)
	}
	sch, err := r.schema(node.Kind, node.Type)
	if err != nil {
		return err
	}
	for i := 0; i < len(a.Attrs.Content); i += 2 {
		path := a.Attrs.Content[i].Value
		v, err := r.formValue(wf, node, a.Attrs.Content[i+1])
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := r.setPath(wf, node, sch, path, v); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return r.call("PUT", r.wfPath(""), wf, nil)
}

// formValue is what a field widget writes: a literal, a Link to… pick
// (appending to a list field with append: true, wrapped in base64encode()
// with base64: true), nothing (unset), a list holding maps (the
// object-list editor's cards, or a card's list field of texts and links),
// or a map holding maps (the map editor's rows with {link: …} or
// {keep: true} values; see nocode_tf_test.go). Any other expression is
// code.
type formValue struct {
	unset      bool
	link       string          // "<address>.<path>", "var.<name>" or "local.<name>"
	linkType   json.RawMessage // the linked attribute's type
	appendLink bool            // Link to… appends to the list
	base64     bool            // Link to… with Base64-encode (user data)
	keep       bool            // a map row left as it is
	literal    any
	seq        *yaml.Node                          // a sequence holding maps
	mapNode    *yaml.Node                          // a map holding maps
	field      func(*yaml.Node) (formValue, error) // decodes the values inside seq / mapNode
	removeItem string                              // the X of a list chip: the element's reference or text
}

func (r *nocodeRun) formValue(wf *model.Workflow, self *model.Node, n *yaml.Node) (formValue, error) {
	field := func(c *yaml.Node) (formValue, error) { return r.formValue(wf, self, c) }
	if n.Kind == yaml.MappingNode && len(n.Content) == 2 {
		switch k, v := n.Content[0].Value, n.Content[1]; k {
		case "unset":
			if v.Value != "true" {
				return formValue{}, fmt.Errorf("write {unset: true}")
			}
			return formValue{unset: true}, nil
		case "keep":
			if v.Value != "true" {
				return formValue{}, fmt.Errorf("write {keep: true}")
			}
			return formValue{keep: true}, nil
		case "link":
			t, err := r.checkLink(wf, self, v.Value)
			return formValue{link: v.Value, linkType: t}, err
		case "remove":
			return formValue{removeItem: v.Value}, nil
		}
	}
	if n.Kind == yaml.MappingNode && len(n.Content) == 4 && n.Content[0].Value == "link" && (n.Content[2].Value == "append" || n.Content[2].Value == "base64") {
		if n.Content[3].Value != "true" {
			return formValue{}, fmt.Errorf("write {link: …, %s: true}", n.Content[2].Value)
		}
		t, err := r.checkLink(wf, self, n.Content[1].Value)
		return formValue{link: n.Content[1].Value, linkType: t, appendLink: n.Content[2].Value == "append", base64: n.Content[2].Value == "base64"}, err
	}
	if n.Kind == yaml.SequenceNode && slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind == yaml.MappingNode }) {
		return formValue{seq: n, field: field}, nil
	}
	if n.Kind == yaml.MappingNode && slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind == yaml.MappingNode }) {
		return formValue{mapNode: n, field: field}, nil
	}
	var lit any
	if err := n.Load(&lit); err != nil {
		return formValue{}, err
	}
	return formValue{literal: lit}, nil
}

// pickerAttrs are the attributes the Link to… picker lists first
// (reference-picker.tsx PREFERRED); every computed attribute follows.
var pickerAttrs = []string{"arn", "id", "name", "url", "invoke_arn", "qualified_arn", "endpoint", "function_name", "bucket", "queue_url", "execution_arn", "api_endpoint", "self_link", "primary_connection_string"}

// checkLink accepts what the Link to… picker offers (reference-picker.tsx,
// references.ts): another resource or data source (an instance of it for
// count/for_each nodes) and an attribute it lists (top level, or nested
// behind "More…"), an input variable or a local. It returns that
// attribute's type.
func (r *nocodeRun) checkLink(wf *model.Workflow, self *model.Node, link string) (json.RawMessage, error) {
	if strings.HasPrefix(link, "var.") || strings.HasPrefix(link, "local.") {
		for _, o := range scopeRefs(wf) {
			if o.path == link {
				return o.typ, nil
			}
		}
		return nil, fmt.Errorf("link %q: the picker lists the workflow's variables (of a type it knows) and locals", link)
	}
	addr, instance, path, ok := linkTarget(link)
	if !ok {
		return nil, fmt.Errorf("link %q is not <address>.<attribute>", link)
	}
	target, err := r.tfNode(wf, addr)
	if err != nil || target.ID == self.ID || target.Kind == model.KindModule || hclcodec.Address(target) != addr {
		return nil, fmt.Errorf("link %q: the picker lists the other resources and data sources by address", link)
	}
	switch keys := instanceKeys(target, wf); {
	case keys == nil && instance != "":
		return nil, fmt.Errorf("link %q: %s is a single instance (no count/for_each)", link, addr)
	case keys != nil && !slices.Contains(keys, instance):
		return nil, fmt.Errorf("link %q: %s has count/for_each; the picker lists its instances %v", link, addr, keys)
	}
	sch, err := r.schema(target.Kind, target.Type)
	if err != nil {
		return nil, err
	}
	for _, o := range slices.Concat(pickerTop(sch.Block), pickerNested(sch.Block)) {
		if o.path == path {
			return o.typ, nil
		}
	}
	return nil, fmt.Errorf("link %q: the picker doesn't list %s for %s (top-level %s and computed attributes; More…: computed attributes of list/single blocks, list(object) elements [0])", link, path, target.Type, strings.Join(pickerAttrs, "/"))
}

// linkFits tells whether a value of type from can be assigned to an
// attribute of type to without an expression around the reference
// (Terraform converts primitives to strings, and lists to sets).
func linkFits(to, from json.RawMessage) bool {
	tk, fk := ctyKind(to), ctyKind(from)
	switch {
	case string(to) == string(from):
		return true
	case tk == "string":
		return fk == "string" || fk == "number" || fk == "bool"
	case tk == "string-list":
		return fk == "string-list"
	case tk == "number" || tk == "bool":
		return fk == tk || fk == "string"
	}
	return false
}

// segRE is one path segment: a name, or a block with its instance index.
var segRE = regexp.MustCompile(`^([a-z0-9_]+)(?:\[(\d+)\])?$`)

type pathSeg struct {
	name  string
	index int // -1: no index
}

func parsePath(path string) ([]pathSeg, error) {
	var out []pathSeg
	for _, s := range strings.Split(path, ".") {
		m := segRE.FindStringSubmatch(s)
		if m == nil {
			return nil, fmt.Errorf("bad path segment %q (name or block[index])", s)
		}
		seg := pathSeg{name: m[1], index: -1}
		if m[2] != "" {
			seg.index, _ = strconv.Atoi(m[2])
		}
		out = append(out, seg)
	}
	return out, nil
}

func (r *nocodeRun) setPath(wf *model.Workflow, node *model.Node, sch *model.ResourceSchema, path string, v formValue) error {
	segs, err := parsePath(path)
	if err != nil {
		return err
	}
	if done, err := r.setMeta(wf, node, segs, v); done || err != nil {
		return err
	}
	return setInBlock(&node.Body, sch.Block, segs, v, 0)
}

// setMeta handles the Meta-arguments section (pro mode): count, depends_on
// (a picker of other nodes), provider (an alias of a provider block) and
// the lifecycle switches.
func (r *nocodeRun) setMeta(wf *model.Workflow, node *model.Node, segs []pathSeg, v formValue) (bool, error) {
	body := &node.Body
	switch {
	case len(segs) == 1 && segs[0] == pathSeg{"count", -1}:
		if v.unset {
			setBodyAttr(body, "count", nil)
			return true, nil
		}
		if _, ok := v.literal.(int); !ok {
			return true, fmt.Errorf("count takes a whole number")
		}
		return true, setLiteral(body, "count", v.literal)
	case len(segs) == 1 && segs[0] == pathSeg{"depends_on", -1}:
		if v.unset {
			setBodyAttr(body, "depends_on", nil)
			return true, nil
		}
		list, ok := v.literal.([]any)
		if !ok || len(list) == 0 {
			return true, fmt.Errorf("depends_on takes the addresses of other nodes (the picker)")
		}
		var addrs []string
		for _, x := range list {
			a, _ := x.(string)
			t, err := r.tfNode(wf, a)
			if err != nil || t.ID == node.ID || hclcodec.Address(t) != a {
				return true, fmt.Errorf("depends_on: the picker lists other nodes by address, not %v", x)
			}
			addrs = append(addrs, a)
		}
		setBodyAttr(body, "depends_on", &model.Value{Expr: "[" + strings.Join(addrs, ", ") + "]"})
		return true, nil
	case len(segs) == 1 && segs[0] == pathSeg{"provider", -1}:
		if v.unset {
			setBodyAttr(body, "provider", nil)
			return true, nil
		}
		alias, _ := v.literal.(string)
		for _, p := range wf.Providers {
			if a := bodyAttr(&p.Body, "alias"); a != nil && string(a.Value.Literal) == strconv.Quote(strings.TrimPrefix(alias, p.Name+".")) && strings.HasPrefix(node.Type, p.Name+"_") {
				setBodyAttr(body, "provider", &model.Value{Expr: alias})
				return true, nil
			}
		}
		return true, fmt.Errorf("provider %q is not an alias of a provider block", alias)
	case len(segs) == 2 && segs[0] == pathSeg{"lifecycle", -1} && node.Kind == model.KindResource:
		if segs[1].name != "create_before_destroy" && segs[1].name != "prevent_destroy" || segs[1].index >= 0 {
			return true, fmt.Errorf("lifecycle.%s is an expression (code); the switches are create_before_destroy and prevent_destroy", segs[1].name)
		}
		var lc model.Body
		if i := slices.IndexFunc(body.Blocks, func(b model.Block) bool { return b.Type == "lifecycle" }); i >= 0 {
			lc = body.Blocks[i].Body
		} else {
			lc = emptyNocodeBody()
		}
		if v.unset {
			setBodyAttr(&lc, segs[1].name, nil)
		} else if _, ok := v.literal.(bool); !ok {
			return true, fmt.Errorf("lifecycle.%s is a switch (true/false)", segs[1].name)
		} else if err := setLiteral(&lc, segs[1].name, v.literal); err != nil {
			return true, err
		}
		var items []model.Block
		if len(lc.Attrs) > 0 || len(lc.Blocks) > 0 {
			items = []model.Block{{Type: "lifecycle", Body: lc}}
		}
		setBodyBlocks(body, "lifecycle", items)
		return true, nil
	}
	return false, nil
}

// setInBlock walks nested blocks (adding one when the index is the
// count, like the form's Add button) and edits the last segment; depth is
// the block nesting (0: the resource block).
func setInBlock(body *model.Body, sch model.SchemaBlock, segs []pathSeg, v formValue, depth int) error {
	seg := segs[0]
	if len(segs) == 1 && seg.index < 0 {
		return setFormAttr(body, sch, seg.name, v, depth)
	}
	if seg.index < 0 {
		return fmt.Errorf("%s is not a block instance: write %s[index]", seg.name, seg.name)
	}
	i := slices.IndexFunc(sch.Blocks, func(b model.SchemaNestedBlock) bool { return b.Name == seg.name })
	if i < 0 {
		return fmt.Errorf("%s is not a block of the provider schema (the form doesn't show it)", seg.name)
	}
	nb := sch.Blocks[i]
	if nb.Nesting == "map" {
		return fmt.Errorf("%s is a map of blocks: not supported by the no-code audit yet", seg.name)
	}
	var items []model.Block
	for _, b := range body.Blocks {
		if b.Type == seg.name {
			items = append(items, b)
		}
	}
	maxItems := nb.MaxItems
	if nb.Nesting == "single" || nb.Nesting == "group" {
		maxItems = 1
	}
	if len(segs) == 1 {
		if !v.unset {
			return fmt.Errorf("%s[%d] is a block: set its attributes, or {unset: true} to remove it", seg.name, seg.index)
		}
		if seg.index >= len(items) {
			return fmt.Errorf("%s has %d blocks", seg.name, len(items))
		}
		if len(items) <= nb.MinItems {
			return fmt.Errorf("%s needs at least %d blocks: the form can't remove it", seg.name, nb.MinItems)
		}
		setBodyBlocks(body, seg.name, slices.Delete(items, seg.index, seg.index+1))
		return nil
	}
	switch {
	case seg.index == len(items):
		if maxItems > 0 && len(items) >= maxItems {
			return fmt.Errorf("%s takes at most %d blocks", seg.name, maxItems)
		}
		items = append(items, model.Block{Type: seg.name, Body: emptyNocodeBody()})
	case seg.index > len(items):
		return fmt.Errorf("%s has %d blocks: add them in order", seg.name, len(items))
	}
	if err := setInBlock(&items[seg.index].Body, nb.Block, segs[1:], v, depth+1); err != nil {
		return err
	}
	setBodyBlocks(body, seg.name, items)
	return nil
}

// setFormAttr edits an attribute the form shows (block-form.tsx editable:
// id/tags_all are hidden on the resource block only) with its widget
// (attr-field.tsx LiteralWidget, Link to…, Clear; object-list-field.tsx).
func setFormAttr(body *model.Body, sch model.SchemaBlock, name string, v formValue, depth int) error {
	i := slices.IndexFunc(sch.Attributes, func(a model.SchemaAttribute) bool { return a.Name == name })
	if i < 0 {
		return fmt.Errorf("not an attribute of the provider schema (the form doesn't show it)")
	}
	sa := sch.Attributes[i]
	asBlocks := objectListFields(sa.Type) != nil && slices.ContainsFunc(body.Blocks, func(b model.Block) bool { return b.Type == name })
	if bodyAttr(body, name) == nil && !asBlocks && (sa.Computed && !sa.Optional && !sa.Required || depth == 0 && (name == "id" || name == "tags_all") && !sa.Required || sa.Deprecated) {
		return fmt.Errorf("the form hides it (computed, deprecated or provider bookkeeping)")
	}
	if objectListFields(sa.Type) != nil && !v.unset && v.link == "" && v.removeItem == "" && v.mapNode == nil {
		if l, ok := v.literal.([]any); v.seq == nil && (!ok || len(l) > 0) {
			return fmt.Errorf("the object-list editor: write the cards as a list of {field: value} maps")
		}
		if v.seq == nil {
			v.seq = &yaml.Node{Kind: yaml.SequenceNode}
		}
		return setObjectList(body, sa, v)
	}
	if v.seq != nil {
		return fmt.Errorf("a list of maps only goes into an object-list attribute (cards); a list field takes texts, then {link: …, append: true}")
	}
	if v.mapNode != nil {
		return setMapRows(body, sa, v)
	}
	if v.keep {
		return fmt.Errorf("{keep: true} is a map row left as it is (inside a map value)")
	}
	kind := ctyKind(sa.Type)
	switch {
	case v.removeItem != "":
		return removeChip(body, sa, v.removeItem)
	case v.unset:
		if sa.Required {
			return fmt.Errorf("required: the form can't clear it")
		}
		setBodyAttr(body, name, nil)
		return nil
	case v.link != "":
		if kind == "bool" {
			return fmt.Errorf("a switch can't link to another resource")
		}
		if v.base64 {
			return setBase64Link(body, sa, v)
		}
		switch fit := refFit(sa.Type, v.linkType); {
		case fit == "":
			return fmt.Errorf("Link to… doesn't list %s (%s) for an attribute of type %s: wrapping it is an expression (code)", v.link, v.linkType, sa.Type)
		case fit == "append" && !v.appendLink:
			return fmt.Errorf("Link to… appends %s to the list %s: write {link: …, append: true}", v.link, name)
		case fit == "replace" && v.appendLink:
			return fmt.Errorf("Link to… replaces %s with %s (it is the whole value, not an element): drop append", name, v.link)
		case fit == "append":
			var cur *model.Value
			if a := bodyAttr(body, name); a != nil {
				cur = &a.Value
			}
			setBodyAttr(body, name, appendToList(cur, v.link))
		default:
			setBodyAttr(body, name, &model.Value{Expr: v.link})
		}
		return nil
	}
	lit := v.literal
	switch kind {
	case "bool":
		if _, ok := lit.(bool); !ok {
			return fmt.Errorf("a switch: write true or false")
		}
	case "number":
		switch lit.(type) {
		case int, float64:
		default:
			return fmt.Errorf("a number field: write a number")
		}
	case "string":
		s, ok := lit.(string)
		if !ok {
			return fmt.Errorf("a text field: write a string (quote numbers and booleans)")
		}
		if len(sa.Enum) > 0 && !slices.Contains(sa.Enum, s) {
			return fmt.Errorf("the select offers %s", strings.Join(sa.Enum, ", "))
		}
	case "string-list":
		list, ok := lit.([]any)
		if !ok || slices.ContainsFunc(list, func(x any) bool { _, s := x.(string); return !s }) {
			return fmt.Errorf("a tag input: write a list of strings")
		}
		if len(list) == 0 {
			setBodyAttr(body, name, nil)
			return nil
		}
	case "string-map":
		m, ok := lit.(map[string]any)
		if !ok {
			return fmt.Errorf("a key/value editor: write a map of strings")
		}
		for k, x := range m {
			if _, s := x.(string); !s {
				return fmt.Errorf("a key/value editor: %s must be a string", k)
			}
		}
		if len(m) == 0 {
			setBodyAttr(body, name, nil)
			return nil
		}
	default:
		return fmt.Errorf("type %s: the inspector only takes an expression here (code)", sa.Type)
	}
	return setLiteral(body, name, lit)
}

// ctyKind mirrors inspector/body.ts ctyKind.
func ctyKind(t json.RawMessage) string {
	var v any
	if json.Unmarshal(t, &v) != nil {
		return "complex"
	}
	switch x := v.(type) {
	case string:
		if x == "string" || x == "number" || x == "bool" {
			return x
		}
	case []any:
		if len(x) == 2 && x[1] == "string" {
			switch x[0] {
			case "list", "set":
				return "string-list"
			case "map":
				return "string-map"
			}
		}
	}
	return "complex"
}

func emptyNocodeBody() model.Body { return model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}} }

func bodyAttr(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

// setBodyAttr mirrors inspector/body.ts setAttr (nil removes; comments stay).
func setBodyAttr(b *model.Body, name string, v *model.Value) {
	i := slices.IndexFunc(b.Attrs, func(a model.Attr) bool { return a.Name == name })
	switch {
	case v == nil && i >= 0:
		b.Attrs = slices.Delete(b.Attrs, i, i+1)
	case v == nil:
	case i < 0:
		b.Attrs = append(b.Attrs, model.Attr{Name: name, Value: *v})
	default:
		b.Attrs[i].Value = *v
	}
}

func setLiteral(b *model.Body, name string, lit any) error {
	data, err := json.Marshal(lit)
	if err != nil {
		return err
	}
	setBodyAttr(b, name, &model.Value{Literal: data})
	return nil
}

// setBodyBlocks mirrors inspector/body.ts setBlocks: every block of typ is
// replaced by items, at the position of the first one.
func setBodyBlocks(b *model.Body, typ string, items []model.Block) {
	var rest []model.Block
	at := -1
	for _, blk := range b.Blocks {
		if blk.Type == typ {
			if at < 0 {
				at = len(rest)
			}
			continue
		}
		rest = append(rest, blk)
	}
	if at < 0 {
		at = len(rest)
	}
	b.Blocks = slices.Concat(rest[:at], items, rest[at:])
	if b.Blocks == nil {
		b.Blocks = []model.Block{}
	}
}

// variableTypes are the type suggestions of the Variables panel
// (blocks-editors.tsx TYPE_SUGGESTIONS); anything else is typed code.
var variableTypes = []string{"string", "number", "bool", "list(string)", "set(string)", "map(string)", "list(number)", "any"}

// variable: the Variables panel (add a row, name, type, default or "Remove
// default", description, sensitive), saved like the store. A row that
// exists is edited.
func (r *nocodeRun) tfVariable(n *yaml.Node) error {
	var a struct {
		Name          string `yaml:"name"`
		Type          string `yaml:"type"`
		Default       any    `yaml:"default"`
		RemoveDefault bool   `yaml:"removeDefault"`
		Description   string `yaml:"description"`
		Sensitive     bool   `yaml:"sensitive"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	if !tfNameRE.MatchString(a.Name) {
		return fmt.Errorf("the panel refuses variable name %q", a.Name)
	}
	if a.Type != "" && !slices.Contains(variableTypes, a.Type) {
		return fmt.Errorf("type %q: the panel suggests %s; other types are code", a.Type, strings.Join(variableTypes, ", "))
	}
	if a.RemoveDefault && a.Default != nil {
		return fmt.Errorf("default and removeDefault are exclusive")
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(wf.Variables, func(v model.NamedBlock) bool { return v.Name == a.Name })
	if i < 0 {
		// A new row has type string.
		wf.Variables = append(wf.Variables, model.NamedBlock{Name: a.Name, Body: emptyNocodeBody()})
		i = len(wf.Variables) - 1
		setBodyAttr(&wf.Variables[i].Body, "type", &model.Value{Expr: "string"})
	}
	body := &wf.Variables[i].Body
	if a.Type != "" {
		setBodyAttr(body, "type", &model.Value{Expr: a.Type})
	}
	typ := "any"
	if t := bodyAttr(body, "type"); t != nil {
		typ = strings.ReplaceAll(t.Value.Expr, " ", "")
	}
	if a.RemoveDefault {
		setBodyAttr(body, "default", nil)
	}
	if a.Default != nil {
		// The default's typed control: string, number or bool (variableKind);
		// any other type takes an expression.
		ok := false
		switch a.Default.(type) {
		case string:
			ok = typ == "string" || typ == "any"
		case int, float64:
			ok = typ == "number" || typ == "any"
		case bool:
			ok = typ == "bool" || typ == "any"
		}
		if !ok {
			return fmt.Errorf("default %v for type %s: the panel only types string, number and bool defaults (others are expressions)", a.Default, typ)
		}
		if err := setLiteral(body, "default", a.Default); err != nil {
			return err
		}
	}
	if a.Description != "" {
		if err := setLiteral(body, "description", a.Description); err != nil {
			return err
		}
	}
	if a.Sensitive {
		if err := setLiteral(body, "sensitive", true); err != nil {
			return err
		}
	}
	return r.call("PUT", r.wfPath(""), wf, nil)
}

// connect: drag source onto target and pick one of the listed recipes
// (canvas.tsx onConnect / applyRecipe).
func (r *nocodeRun) tfConnect(n *yaml.Node) error {
	var a struct{ Source, Target, Recipe string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	src, err := r.tfNodeID(a.Source)
	if err != nil {
		return err
	}
	dst, err := r.tfNodeID(a.Target)
	if err != nil {
		return err
	}
	var recipes []model.ConnectionRecipe
	if err := r.call("GET", r.wfPath("/connections?source="+url.QueryEscape(src)+"&target="+url.QueryEscape(dst)), nil, &recipes); err != nil {
		return err
	}
	if !slices.ContainsFunc(recipes, func(rc model.ConnectionRecipe) bool { return rc.ID == a.Recipe }) {
		ids := make([]string, len(recipes))
		for i, rc := range recipes {
			ids[i] = rc.ID
		}
		return fmt.Errorf("recipe %q is not offered for %s → %s (offered: %s)", a.Recipe, a.Source, a.Target, strings.Join(ids, ", "))
	}
	return r.call("POST", r.wfPath("/connections"), map[string]string{"source": src, "target": dst, "recipeId": a.Recipe}, nil)
}

// ---------------------------------------------------------------------------
// Terraform: access editors (features/access): read the view, change it
// like the editor does, write it back.

func (r *nocodeRun) accessView(node string) (string, *model.AccessView, error) {
	id, err := r.tfNodeID(node)
	if err != nil {
		return "", nil, err
	}
	var v model.AccessView
	if err := r.call("GET", r.wfPath("/access?node="+url.QueryEscape(id)), nil, &v); err != nil {
		return "", nil, err
	}
	return id, &v, nil
}

func (r *nocodeRun) putAccess(edit model.AccessEdit) error {
	return r.call("PUT", r.wfPath("/access"), edit, nil)
}

// awsStatementEdit is a statement written in the AWS policy editor.
type awsStatementEdit struct {
	Sid        string               `yaml:"sid"`
	Effect     string               `yaml:"effect"`
	Actions    []string             `yaml:"actions"`
	Resources  []string             `yaml:"resources"`
	Principals []model.AWSPrincipal `yaml:"principals"`
}

func (r *nocodeRun) accessAWS(n *yaml.Node) error {
	var a struct {
		Node        string `yaml:"node"`
		Path        string `yaml:"path"`
		RemoveWhere *struct {
			Sid       string   `yaml:"sid"`
			Actions   []string `yaml:"actions"`
			Resources []string `yaml:"resources"`
		} `yaml:"removeWhere"`
		Add    []awsStatementEdit `yaml:"add"`
		Update []struct {
			Index            int `yaml:"index"`
			awsStatementEdit `yaml:",inline"`
		} `yaml:"update"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, view, err := r.accessView(a.Node)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(view.AWS, func(d model.AWSPolicyDocument) bool { return d.Path == a.Path })
	if i < 0 {
		return fmt.Errorf("%s has no policy document %q", a.Node, a.Path)
	}
	doc := view.AWS[i]
	if !doc.Editable {
		return fmt.Errorf("%s %s is read-only in the editor: %s", a.Node, a.Path, doc.Reason)
	}
	if w := a.RemoveWhere; w != nil {
		doc.Statements = slices.DeleteFunc(doc.Statements, func(s model.AWSStatement) bool {
			return (w.Sid == "" || s.Sid == w.Sid) &&
				(w.Actions == nil || sameSet(s.Actions, w.Actions)) &&
				(w.Resources == nil || sameSet(s.Resources, w.Resources))
		})
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	self, err := r.tfNode(wf, a.Node)
	if err != nil {
		return err
	}
	for _, u := range a.Update {
		if u.Index < 0 || u.Index >= len(doc.Statements) {
			return fmt.Errorf("no statement %d", u.Index)
		}
		s := &doc.Statements[u.Index]
		if !s.Editable {
			return fmt.Errorf("statement %d is read-only: %s", u.Index, s.Reason)
		}
		if err := r.awsEditFits(wf, self, u.awsStatementEdit); err != nil {
			return err
		}
		applyAWSEdit(s, u.awsStatementEdit)
	}
	for _, add := range a.Add {
		if err := r.awsEditFits(wf, self, add); err != nil {
			return err
		}
		s := model.AWSStatement{Effect: "Allow", Editable: true}
		applyAWSEdit(&s, add)
		doc.Statements = append(doc.Statements, s)
	}
	return r.putAccess(model.AccessEdit{NodeID: id, AWS: &doc})
}

// awsRefSuffixRE is a picked reference with an ARN suffix
// (aws-policy.ts resourceExpr: "${<ref>}/*").
var awsRefSuffixRE = regexp.MustCompile(`^"\$\{([^{}"]+)\}([^"$%\\]*)"$`)

// awsEditFits accepts the expressions aws-statement-parts.tsx writes: a
// typed ARN or principal (a quoted string without ${...}), a reference
// picked with Link to… (resources and non-Service principals), or a picked
// resource with a suffix.
func (r *nocodeRun) awsEditFits(wf *model.Workflow, self *model.Node, e awsStatementEdit) error {
	typed := func(v string) bool {
		return len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) && !strings.Contains(v, "${")
	}
	for _, v := range e.Resources {
		if typed(v) {
			continue
		}
		ref := v
		if m := awsRefSuffixRE.FindStringSubmatch(v); m != nil {
			ref = m[1]
		}
		if _, err := r.checkLink(wf, self, ref); err != nil {
			return fmt.Errorf("resource %s: %w", v, err)
		}
	}
	for _, p := range e.Principals {
		for _, v := range p.Values {
			if typed(v) {
				continue
			}
			if p.Type == "Service" {
				return fmt.Errorf("principal %s: Service principals are typed names", v)
			}
			if _, err := r.checkLink(wf, self, v); err != nil {
				return fmt.Errorf("principal %s: %w", v, err)
			}
		}
	}
	return nil
}

func applyAWSEdit(s *model.AWSStatement, e awsStatementEdit) {
	if e.Sid != "" {
		s.Sid = e.Sid
	}
	if e.Effect != "" {
		s.Effect = e.Effect
	}
	if e.Actions != nil {
		s.Actions = e.Actions
	}
	if e.Resources != nil {
		s.Resources = e.Resources
	}
	if e.Principals != nil {
		s.Principals = e.Principals
	}
}

func sameSet(a, b []string) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(x string) bool { return !slices.Contains(b, x) })
}

// mergeFields overlays set (JSON field names among editable) onto v.
func mergeFields(v any, set map[string]any, editable []string) error {
	cur, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(cur, &m); err != nil {
		return err
	}
	for k, x := range set {
		if !slices.Contains(editable, k) {
			return fmt.Errorf("the editor doesn't edit %q (it edits %s)", k, strings.Join(editable, ", "))
		}
		m[k] = x
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (r *nocodeRun) accessAzure(n *yaml.Node) error {
	var a struct {
		Node string         `yaml:"node"`
		Set  map[string]any `yaml:"set"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, view, err := r.accessView(a.Node)
	if err != nil {
		return err
	}
	if view.Azure == nil || !view.Azure.Editable {
		return fmt.Errorf("%s has no editable Azure role assignment", a.Node)
	}
	if err := mergeFields(view.Azure, a.Set, []string{"scope", "roleName", "roleId", "principalId", "principalType"}); err != nil {
		return err
	}
	return r.putAccess(model.AccessEdit{NodeID: id, Azure: view.Azure})
}

func (r *nocodeRun) accessGCP(n *yaml.Node) error {
	var a struct {
		Node string         `yaml:"node"`
		Set  map[string]any `yaml:"set"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, view, err := r.accessView(a.Node)
	if err != nil {
		return err
	}
	if view.GCP == nil || !view.GCP.Editable {
		return fmt.Errorf("%s has no editable GCP IAM binding", a.Node)
	}
	if err := mergeFields(view.GCP, a.Set, []string{"kind", "resourceAttr", "resource", "role", "members"}); err != nil {
		return err
	}
	return r.putAccess(model.AccessEdit{NodeID: id, GCP: view.GCP})
}

func (r *nocodeRun) accessOCI(n *yaml.Node) error {
	var a struct {
		Node   string `yaml:"node"`
		Remove []int  `yaml:"remove"`
		Add    []struct {
			SubjectType  string   `yaml:"subjectType"`
			Subjects     []string `yaml:"subjects"`
			Verb         string   `yaml:"verb"`
			ResourceType string   `yaml:"resourceType"`
			Location     string   `yaml:"location"`
			Compartment  string   `yaml:"compartment"`
			Condition    string   `yaml:"condition"`
		} `yaml:"add"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	id, view, err := r.accessView(a.Node)
	if err != nil {
		return err
	}
	if view.OCI == nil || !view.OCI.Editable {
		return fmt.Errorf("%s has no editable OCI policy", a.Node)
	}
	p := view.OCI
	remove := slices.Clone(a.Remove)
	slices.Sort(remove)
	for _, i := range slices.Backward(remove) {
		if i < 0 || i >= len(p.Statements) {
			return fmt.Errorf("no statement %d", i)
		}
		if !p.Statements[i].Editable {
			return fmt.Errorf("statement %d is read-only: %s", i, p.Statements[i].Reason)
		}
		p.Statements = slices.Delete(p.Statements, i, i+1)
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	self, err := r.tfNode(wf, a.Node)
	if err != nil {
		return err
	}
	for _, s := range a.Add {
		st := model.OCIStatement{Editable: true, SubjectType: s.SubjectType, Subjects: s.Subjects, Verb: s.Verb,
			ResourceType: s.ResourceType, Location: s.Location, Compartment: s.Compartment, Condition: s.Condition}
		if err := r.ociSentenceFits(wf, self, st); err != nil {
			return err
		}
		p.Statements = append(p.Statements, st)
	}
	return r.putAccess(model.AccessEdit{NodeID: id, OCI: p})
}

// ociInterpRE is a ${...} interpolation the sentence builder writes: a
// reference picked with Link to… (or a group/compartment dropdown).
var ociInterpRE = regexp.MustCompile(`\$\{([^{}]*)\}`)

// ociSentenceFits accepts what oci-policy-editor.tsx writes: typed text
// (names, resource-types, conditions) without ${...}; a subject or
// compartment that is exactly one picked reference ("${<address>.name}");
// condition values appended by its Link to… as '${<address>.<attr>}'.
func (r *nocodeRun) ociSentenceFits(wf *model.Workflow, self *model.Node, st model.OCIStatement) error {
	whole := func(field, v string) error {
		if !strings.Contains(v, "${") {
			return nil
		}
		m := ociInterpRE.FindStringSubmatch(v)
		if m == nil || m[0] != v {
			return fmt.Errorf("%s %q: typing ${...} is code (the dropdown/Link to… inserts one whole reference)", field, v)
		}
		_, err := r.checkLink(wf, self, m[1])
		return err
	}
	for _, s := range st.Subjects {
		if err := whole("subject", s); err != nil {
			return err
		}
	}
	if err := whole("compartment", st.Compartment); err != nil {
		return err
	}
	rest := st.Condition
	for _, m := range ociInterpRE.FindAllStringSubmatch(st.Condition, -1) {
		if !strings.Contains(st.Condition, "'"+m[0]+"'") {
			return fmt.Errorf("condition %q: the Link to… of the where field appends '%s' (quoted)", st.Condition, m[0])
		}
		if _, err := r.checkLink(wf, self, m[1]); err != nil {
			return err
		}
		rest = strings.Replace(rest, m[0], "", 1)
	}
	if strings.Contains(rest, "${") {
		return fmt.Errorf("condition %q: typing ${...} is code", st.Condition)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Docker: palette, service/network/volume forms, connections, environments

func (r *nocodeRun) composeNames(kind string) ([]string, error) {
	wf, err := r.workflow()
	if err != nil {
		return nil, err
	}
	if wf.Compose == nil {
		return nil, fmt.Errorf("not a docker workflow")
	}
	var out []string
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind == kind || n.Kind == "external" && n.ExternalKind == kind {
			out = append(out, n.Name)
		}
	}
	return out, nil
}

// freeName mirrors docker/actions.ts freeName.
func freeName(base string, taken []string) string {
	if !slices.Contains(taken, base) {
		return base
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", base, i); !slices.Contains(taken, n) {
			return n
		}
	}
}

// composeAddService: a catalog image or a "Build from Dockerfile" starter
// from the palette (docker/actions.ts addFromPalette).
func (r *nocodeRun) composeAddService(n *yaml.Node) error {
	var a struct {
		CatalogID string `yaml:"catalogId"`
		Build     *struct {
			Starter string `yaml:"starter"`
		} `yaml:"build"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	switch {
	case a.CatalogID != "" && a.Build == nil:
		return r.call("POST", r.wfPath("/compose/services"), map[string]any{"catalogId": a.CatalogID}, nil)
	case a.Build != nil && a.CatalogID == "":
		taken, err := r.composeNames("service")
		if err != nil {
			return err
		}
		name := freeName("app", taken)
		build := map[string]string{"context": "./" + name}
		if a.Build.Starter != "" {
			build["starter"] = a.Build.Starter
		}
		return r.call("POST", r.wfPath("/compose/services"), map[string]any{"name": name, "build": build}, nil)
	}
	return fmt.Errorf("the palette adds a catalog image (catalogId) or a build starter (build)")
}

// serviceFormKeys are the ServicePatch keys the service form writes
// (docker/inspector/service-form.tsx, hardening-section.tsx), with the keys
// of their items; checkServiceValues (nocode_docker_test.go) checks values.
var serviceFormKeys = map[string][]string{
	"name":        nil,
	"image":       nil,
	"build":       {"context", "dockerfile", "target"},
	"command":     nil, // pro mode
	"ports":       {"host", "container", "protocol", "hostIp"},
	"environment": {"key", "value", "secret"},
	"volumes":     {"type", "source", "target", "readOnly"},
	"dependsOn":   {"service", "condition"},
	"healthcheck": {"test", "interval", "timeout", "retries", "startPeriod"},
	"restart":     nil,
	"replicas":    nil,
	"resources":   {"cpus", "memory", "pids"},
	"logging":     {"driver", "maxSize", "maxFile"},
	"labels":      nil, // any label keys (checkServiceValues)
	"networks":    nil,
	// Hardening section.
	"user":          nil,
	"readOnly":      nil,
	"capDrop":       nil,
	"capAdd":        nil,
	"securityOpt":   nil,
	"tmpfs":         nil,
	"privileged":    nil,
	"init":          nil,
	"secretFromEnv": {"key", "secret", "fileEnv"},
	// The override badge's reset and the "Only in <env>" switch.
	"resetOverrides": nil,
	"scope":          nil,
}

func checkKeys(what string, v any, allowed []string) error {
	switch x := v.(type) {
	case map[string]any:
		for k := range x {
			if !slices.Contains(allowed, k) {
				return fmt.Errorf("%s.%s: the form doesn't edit it", what, k)
			}
		}
	case []any:
		for i, item := range x {
			if err := checkKeys(fmt.Sprintf("%s[%d]", what, i), item, allowed); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *nocodeRun) composePatch(n *yaml.Node) error {
	var a struct {
		Service string         `yaml:"service"`
		Patch   map[string]any `yaml:"patch"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	for k, v := range a.Patch {
		sub, ok := serviceFormKeys[k]
		if !ok {
			return fmt.Errorf("%s: the service form doesn't edit it (code)", k)
		}
		if err := checkKeys(k, v, sub); err != nil && k != "labels" {
			return err
		}
	}
	wf, svc, err := r.composeService(a.Service)
	if err != nil {
		return err
	}
	if err := checkServiceValues(wf, a.Service, svc, a.Patch); err != nil {
		return err
	}
	if nets, ok := a.Patch["networks"].([]any); ok {
		taken, err := r.composeNames("network")
		if err != nil {
			return err
		}
		for _, nw := range nets {
			if s, _ := nw.(string); !slices.Contains(taken, s) {
				return fmt.Errorf("networks: the form checks existing networks, not %v", nw)
			}
		}
	}
	return r.call("PATCH", r.wfPath("/compose/services/"+url.PathEscape(a.Service)), a.Patch, nil)
}

func (r *nocodeRun) composeDeleteService(n *yaml.Node) error {
	var a struct{ Service string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	return r.call("DELETE", r.wfPath("/compose/services/"+url.PathEscape(a.Service)), nil, nil)
}

func topPath(kind string) (string, error) {
	switch kind {
	case "network", "volume":
		return "/compose/" + kind + "s", nil
	}
	return "", fmt.Errorf("kind is network or volume (secrets and configs are code)")
}

// composeAddTop: a network or volume from the palette (named backend /
// data), renamed in its inspector when name is given.
func (r *nocodeRun) composeAddTop(n *yaml.Node) error {
	var a struct{ Kind, Name string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	p, err := topPath(a.Kind)
	if err != nil {
		return err
	}
	taken, err := r.composeNames(a.Kind)
	if err != nil {
		return err
	}
	base := map[string]string{"network": "backend", "volume": "data"}[a.Kind]
	name := freeName(base, taken)
	if err := r.call("POST", r.wfPath(p), map[string]string{"name": name}, nil); err != nil {
		return err
	}
	if a.Name == "" || a.Name == name {
		return nil
	}
	return r.call("PATCH", r.wfPath(p+"/"+url.PathEscape(name)), map[string]string{"name": a.Name}, nil)
}

// topFormKeys are the network and volume form fields (docker-inspector.tsx).
var topFormKeys = map[string][]string{"network": {"name", "driver", "internal", "external"}, "volume": {"name", "driver", "external"}}

func (r *nocodeRun) composePatchTop(n *yaml.Node) error {
	var a struct {
		Kind  string         `yaml:"kind"`
		Name  string         `yaml:"name"`
		Patch map[string]any `yaml:"patch"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	p, err := topPath(a.Kind)
	if err != nil {
		return err
	}
	if err := checkKeys(a.Kind, a.Patch, topFormKeys[a.Kind]); err != nil {
		return err
	}
	return r.call("PATCH", r.wfPath(p+"/"+url.PathEscape(a.Name)), a.Patch, nil)
}

func (r *nocodeRun) composeDeleteTop(n *yaml.Node) error {
	var a struct{ Kind, Name string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	p, err := topPath(a.Kind)
	if err != nil {
		return err
	}
	return r.call("DELETE", r.wfPath(p+"/"+url.PathEscape(a.Name)), nil, nil)
}

// composeConnect: drag from a service onto a service, network or volume
// node and pick a listed recipe; target is the mount path the dialog asks
// for volumes (docker/actions.ts applyRecipe).
func (r *nocodeRun) composeConnect(n *yaml.Node) error {
	var a struct{ From, To, Recipe, Target string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	var recipes []connectRecipe
	if err := r.call("GET", r.wfPath("/compose/connect?from="+url.QueryEscape(a.From)+"&to="+url.QueryEscape(a.To)), nil, &recipes); err != nil {
		return err
	}
	i := slices.IndexFunc(recipes, func(rc connectRecipe) bool { return rc.ID == a.Recipe })
	if i < 0 {
		return fmt.Errorf("recipe %q is not offered for %s → %s", a.Recipe, a.From, a.To)
	}
	rc := recipes[i]
	if a.Target != "" && rc.Kind != "volume" {
		return fmt.Errorf("only volume recipes ask for a target path")
	}
	target := rc.Target
	if a.Target != "" {
		target = a.Target
	}
	body := map[string]string{"from": a.From, "to": a.To, "kind": rc.Kind, "recipeId": rc.ID}
	for k, v := range map[string]string{"condition": rc.Condition, "network": rc.Network, "target": target} {
		if v != "" {
			body[k] = v
		}
	}
	return r.call("POST", r.wfPath("/compose/connect"), body, nil)
}

// envCreate: New environment (from the base, or a copy of from).
func (r *nocodeRun) envCreate(n *yaml.Node) error {
	var a struct{ Name, From string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	return r.call("POST", r.wfPath("/environments"), map[string]string{"name": a.Name, "from": a.From}, nil)
}

// envSettings: the knobs of the environment editor; keys must be the ones
// GET …/settings lists for the target.
func (r *nocodeRun) envSettings(n *yaml.Node) error {
	var a struct {
		Env    string             `yaml:"env"`
		Target string             `yaml:"target"`
		Values map[string]*string `yaml:"values"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	var view model.EnvSettings
	if err := r.call("GET", r.wfPath("/environments/"+url.PathEscape(a.Env)+"/settings?target="+url.QueryEscape(a.Target)), nil, &view); err != nil {
		return err
	}
	for k := range a.Values {
		if !slices.ContainsFunc(view.Settings, func(s model.EnvSetting) bool { return s.Key == k }) {
			return fmt.Errorf("the environment editor has no setting %q for %s", k, view.Target)
		}
	}
	return r.call("PATCH", r.wfPath("/environments/"+url.PathEscape(a.Env)+"/settings"), map[string]any{"target": view.Target, "values": a.Values}, nil)
}

// envVariable: a variable of a docker environment's env file.
func (r *nocodeRun) envVariable(n *yaml.Node) error {
	var a struct {
		Env    string `yaml:"env"`
		Key    string `yaml:"key"`
		Value  string `yaml:"value"`
		Secret bool   `yaml:"secret"`
		Remove bool   `yaml:"remove"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	p := r.wfPath("/environments/" + url.PathEscape(a.Env) + "/variables/" + url.PathEscape(a.Key))
	if a.Remove {
		return r.call("DELETE", p, nil, nil)
	}
	return r.call("PUT", p, map[string]any{"value": a.Value, "secret": a.Secret}, nil)
}

// ---------------------------------------------------------------------------
// Kubernetes: palette, inspector forms, connections, RBAC editor

func (r *nocodeRun) kubeNode(ref string) (*model.KubeNode, error) {
	wf, err := r.workflow()
	if err != nil {
		return nil, err
	}
	if wf.Kubernetes == nil {
		return nil, fmt.Errorf("not a Kubernetes workflow")
	}
	ids := make([]string, 0, len(wf.Kubernetes.Graph.Nodes))
	for i, n := range wf.Kubernetes.Graph.Nodes {
		if n.ID == ref {
			return &wf.Kubernetes.Graph.Nodes[i], nil
		}
		ids = append(ids, n.ID)
	}
	return nil, fmt.Errorf("no object %q on the canvas of environment %q (objects: %s)", ref, wf.Kubernetes.Environment, strings.Join(ids, ", "))
}

// kubeAdd: a palette entry, as the palette sends it (its default name).
func (r *nocodeRun) kubeAdd(n *yaml.Node) error {
	var a struct {
		ItemID string `yaml:"itemId"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	return r.call("POST", r.wfPath("/kubernetes/catalog"), map[string]any{"itemId": a.ItemID, "position": model.Position{}}, nil)
}

// kubePodSpec mirrors kubernetes/inspector/paths.ts podSpecPath.
func kubePodSpec(kind string) string {
	switch kind {
	case "Pod":
		return "spec"
	case "CronJob":
		return "spec.jobTemplate.spec.template.spec"
	}
	return "spec.template.spec"
}

// kubeFormPath tells whether the inspector form of node writes path, and
// checks the value's shape (workload-form.tsx, object-forms.tsx, paths.ts).
// set is false for unset paths.
func kubeFormPath(node *model.KubeNode, path string, v any, set bool) error {
	bad := fmt.Errorf("%s: no inspector form of %s writes it (code)", path, node.Kind)
	if handled, err := kubeObjectExtra(node, path, v, set); handled {
		return err
	}
	if w := node.Workload; w != nil {
		ps := kubePodSpec(node.Kind)
		if handled, err := kubeWorkloadExtra(node, ps, path, v, set); handled {
			return err
		}
		switch {
		case path == "spec.replicas" && (node.Kind == "Deployment" || node.Kind == "StatefulSet" || node.Kind == "ReplicaSet"):
			return wantInt(path, v, set)
		case path == "spec.schedule" && node.Kind == "CronJob":
			return wantString(path, v, set)
		case strings.HasPrefix(path, ps+".volumes[name="):
			if !set {
				return nil
			}
			return volumeShape(path, v)
		case strings.HasPrefix(path, ps+".containers[name="):
			rest := strings.TrimPrefix(path, ps+".containers[name=")
			name, field, ok := strings.Cut(rest, "]")
			if !ok || !slices.ContainsFunc(w.Containers, func(c model.KubeContainer) bool { return c.Name == name }) {
				return fmt.Errorf("%s: the form edits the existing containers only", path)
			}
			field = strings.TrimPrefix(field, ".")
			return containerFormField(path, field, v, set)
		}
		return bad
	}
	switch {
	case node.Kind == "Service" && node.Service != nil:
		switch path {
		case "spec.type":
			if s, _ := v.(string); set && !slices.Contains([]string{"ClusterIP", "NodePort", "LoadBalancer"}, s) {
				return fmt.Errorf("%s: the select offers ClusterIP, NodePort, LoadBalancer", path)
			}
			return nil
		case "spec.ports":
			return listShape(path, v, set, []string{"name", "port", "targetPort", "nodePort", "protocol"})
		case "spec.selector":
			return stringMap(path, v, set)
		}
	case node.Routes != nil:
		switch path {
		case "spec.ingressClassName":
			return wantString(path, v, set)
		case "spec.rules":
			return listShape(path, v, set, []string{"host", "http"})
		case "spec.tls":
			return listShape(path, v, set, []string{"hosts", "secretName"})
		}
	case node.Kind == "HorizontalPodAutoscaler" && node.Scaler != nil:
		switch path {
		case "spec.minReplicas", "spec.maxReplicas":
			return wantInt(path, v, set)
		case "spec.metrics":
			return listShape(path, v, set, []string{"type", "resource"})
		}
	case node.Kind == "PersistentVolumeClaim":
		switch path {
		case "spec.resources.requests.storage", "spec.storageClassName":
			return wantString(path, v, set)
		}
	}
	return bad
}

var (
	containerResourceRE = regexp.MustCompile(`^resources\.(requests|limits)\.(cpu|memory)$`)
	containerEnvRE      = regexp.MustCompile(`^env\[name=[A-Za-z_][\w.-]*\](\.value|\.valueFrom)?$`)
)

func containerFormField(path, field string, v any, set bool) error {
	switch {
	case field == "image":
		return wantString(path, v, set)
	case field == "ports":
		return listShape(path, v, set, []string{"name", "containerPort", "protocol"})
	case field == "livenessProbe" || field == "readinessProbe" || field == "startupProbe":
		if !set {
			return nil
		}
		return checkKeys(path, v, []string{"httpGet", "tcpSocket", "grpc", "exec", "initialDelaySeconds", "periodSeconds", "timeoutSeconds", "failureThreshold"})
	case containerResourceRE.MatchString(field):
		return wantString(path, v, set)
	case containerEnvRE.MatchString(field):
		switch {
		case strings.HasSuffix(field, ".value"):
			return wantString(path, v, set)
		case strings.HasSuffix(field, ".valueFrom"):
			if !set {
				return nil
			}
			return checkKeys(path, v, []string{"secretKeyRef", "configMapKeyRef"})
		case set:
			return fmt.Errorf("%s: the form sets .value or .valueFrom", path)
		}
		return nil
	case field == "envFrom":
		return listShape(path, v, set, []string{"prefix", "secretRef", "configMapRef"})
	case field == "volumeMounts":
		return listShape(path, v, set, []string{"name", "mountPath", "subPath", "readOnly"})
	}
	return fmt.Errorf("%s: the container form doesn't edit %q (code)", path, field)
}

func wantInt(path string, v any, set bool) error {
	if _, ok := v.(int); set && !ok {
		return fmt.Errorf("%s: a number field", path)
	}
	return nil
}

func wantString(path string, v any, set bool) error {
	if _, ok := v.(string); set && !ok {
		return fmt.Errorf("%s: a text field (quote numbers)", path)
	}
	return nil
}

func stringMap(path string, v any, set bool) error {
	if !set {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: key/value pairs", path)
	}
	for k, x := range m {
		if _, s := x.(string); !s {
			return fmt.Errorf("%s.%s: a string", path, k)
		}
	}
	return nil
}

func listShape(path string, v any, set bool, keys []string) error {
	if !set {
		return nil
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return fmt.Errorf("%s: a non-empty list (an empty one is unset)", path)
	}
	return checkKeys(path, list, keys)
}

// volumeShape: the volume kinds the form writes (paths.ts volumeObject).
func volumeShape(path string, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: a volume object", path)
	}
	kinds := 0
	for k := range m {
		switch k {
		case "name":
		case "configMap", "secret", "persistentVolumeClaim", "emptyDir":
			kinds++
		default:
			return fmt.Errorf("%s.%s: the form writes configMap, secret, persistentVolumeClaim or emptyDir volumes", path, k)
		}
	}
	if kinds != 1 {
		return fmt.Errorf("%s: exactly one volume kind", path)
	}
	return nil
}

// kubePatch: inspector form edits of one object (actions.ts patchObject),
// only on fields the daemon reports writable (GET …/kubernetes/fields).
func (r *nocodeRun) kubePatch(n *yaml.Node) error {
	var a struct {
		Node  string         `yaml:"node"`
		Set   map[string]any `yaml:"set"`
		Unset []string       `yaml:"unset"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	node, err := r.kubeNode(a.Node)
	if err != nil {
		return err
	}
	var fields struct {
		Fields []struct {
			Path     string `json:"path"`
			Editable bool   `json:"editable"`
			Reason   string `json:"reason"`
		} `json:"fields"`
	}
	if err := r.call("GET", r.wfPath("/kubernetes/fields?node="+url.QueryEscape(node.ID)), nil, &fields); err != nil {
		return err
	}
	under := func(p, parent string) bool {
		return strings.HasPrefix(p, parent+".") || strings.HasPrefix(p, parent+"[")
	}
	writable := func(path string) error {
		for _, f := range fields.Fields {
			if f.Path == path {
				if !f.Editable {
					return fmt.Errorf("%s is read-only: %s", path, f.Reason)
				}
				return nil
			}
		}
		for _, f := range fields.Fields {
			if !f.Editable && (under(f.Path, path) || under(path, f.Path)) {
				return fmt.Errorf("%s is read-only: %s", path, f.Reason)
			}
		}
		if node.Editable || node.SourceKind == "helm" {
			return nil
		}
		return fmt.Errorf("%s: generated by the %s source, edit it in code", path, node.SourceKind)
	}
	for p, v := range a.Set {
		if err := kubeFormPath(node, p, v, true); err != nil {
			return err
		}
		if err := writable(p); err != nil {
			return err
		}
	}
	for _, p := range a.Unset {
		if err := kubeFormPath(node, p, nil, false); err != nil {
			return err
		}
		if err := writable(p); err != nil {
			return err
		}
	}
	return r.call("PATCH", r.wfPath("/kubernetes/objects"), map[string]any{"node": node.ID, "set": a.Set, "unset": a.Unset}, nil)
}

// kubeRecipeInputs are the inputs the connect dialog asks per recipe kind
// (kubernetes/canvas/connect-dialog.tsx RecipeInputs).
var kubeRecipeInputs = map[string][]string{"route": {"host", "path", "port"}, "volume": {"path"}, "env": {"key"}, "expose": {"port"}}

// kubeConnect: drag from → to ("" = a new object) and pick a listed recipe,
// answering its dialog.
func (r *nocodeRun) kubeConnect(n *yaml.Node) error {
	var a struct {
		From   string `yaml:"from"`
		To     string `yaml:"to"`
		Recipe string `yaml:"recipe"`
		Port   *int   `yaml:"port"`
		Path   string `yaml:"path"`
		Host   string `yaml:"host"`
		Key    string `yaml:"key"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	var recipes []kube.ConnectRecipe
	if err := r.call("GET", r.wfPath("/kubernetes/connect?from="+url.QueryEscape(a.From)+"&to="+url.QueryEscape(a.To)), nil, &recipes); err != nil {
		return err
	}
	i := slices.IndexFunc(recipes, func(rc kube.ConnectRecipe) bool { return rc.ID == a.Recipe })
	if i < 0 {
		ids := make([]string, len(recipes))
		for j, rc := range recipes {
			ids[j] = rc.ID
		}
		return fmt.Errorf("recipe %q is not offered for %s → %s (offered: %s)", a.Recipe, a.From, a.To, strings.Join(ids, ", "))
	}
	rc := recipes[i]
	asked := kubeRecipeInputs[rc.Kind]
	body := map[string]any{"from": a.From, "to": a.To, "recipeId": rc.ID, "kind": rc.Kind}
	inputs := map[string]any{}
	if a.Port != nil {
		inputs["port"] = *a.Port
	}
	for k, v := range map[string]string{"path": a.Path, "host": a.Host, "key": a.Key} {
		if v != "" {
			inputs[k] = v
		}
	}
	for k, v := range inputs {
		if !slices.Contains(asked, k) {
			return fmt.Errorf("the %s dialog doesn't ask for %s", rc.Kind, k)
		}
		body[k] = v
	}
	return r.call("POST", r.wfPath("/kubernetes/connect"), body, nil)
}

// kubeAccess: the Kubernetes RBAC editor (PUT …/kubernetes/access).
func (r *nocodeRun) kubeAccess(n *yaml.Node) error {
	// The editor's fields are the JSON names of KubeAccessEdit (nodeId aside).
	var raw map[string]any
	if err := n.Load(&raw); err != nil {
		return err
	}
	ref, _ := raw["node"].(string)
	delete(raw, "node")
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var edit model.KubeAccessEdit
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&edit); err != nil || edit.NodeID != "" {
		return fmt.Errorf("kubeAccess takes node plus the RBAC editor's fields (rules, roleRef, subjects, serviceAccount, automountToken): %v", err)
	}
	node, err := r.kubeNode(ref)
	if err != nil {
		return err
	}
	var view model.KubeAccessView
	if err := r.call("GET", r.wfPath("/kubernetes/access?node="+url.QueryEscape(node.ID)), nil, &view); err != nil {
		return err
	}
	if !view.Editable {
		return fmt.Errorf("%s is read-only in the RBAC editor: %s", ref, view.Reason)
	}
	edit.NodeID = node.ID
	return r.call("PUT", r.wfPath("/kubernetes/access"), edit, nil)
}

func (r *nocodeRun) kubeDelete(n *yaml.Node) error {
	var a struct{ Node string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	node, err := r.kubeNode(a.Node)
	if err != nil {
		return err
	}
	return r.call("DELETE", r.wfPath("/kubernetes/objects?node="+url.QueryEscape(node.ID)), nil, nil)
}
