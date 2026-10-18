package catalog

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// builder accumulates new nodes for one AddNode/Connect call. Nodes are
// appended to wf.Nodes only on commit so pointers into wf.Nodes stay valid
// while a recipe runs.
type builder struct {
	c         *Catalog
	wf        *model.Workflow
	pending   []*model.Node
	suffix    string
	providers []string
}

func newBuilder(c *Catalog, wf *model.Workflow) *builder {
	return &builder{c: c, wf: wf, suffix: randString(6, lowerAlnum)}
}

const (
	lowerAlnum    = "abcdefghijklmnopqrstuvwxyz0123456789"
	passwordChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

func randString(n int, alphabet string) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("catalog: random: %v", err))
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}

func (b *builder) all() []*model.Node {
	out := make([]*model.Node, 0, len(b.wf.Nodes)+len(b.pending))
	for i := range b.wf.Nodes {
		out = append(out, &b.wf.Nodes[i])
	}
	return append(out, b.pending...)
}

// latest returns the most recently added node of kind+type.
func (b *builder) latest(kind model.NodeKind, typ string) *model.Node {
	all := b.all()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Kind == kind && all[i].Type == typ {
			return all[i]
		}
	}
	return nil
}

func (b *builder) nameTaken(kind model.NodeKind, typ, name string) bool {
	for _, n := range b.all() {
		if n.Kind == kind && n.Type == typ && n.Name == name {
			return true
		}
	}
	return false
}

func (b *builder) uniqueName(kind model.NodeKind, typ, hint string) string {
	hint = sanitizeName(hint)
	name := hint
	for i := 2; b.nameTaken(kind, typ, name); i++ {
		name = hint + "_" + strconv.Itoa(i)
	}
	return name
}

func (b *builder) uniqueID(addr string) string {
	taken := map[string]bool{}
	for _, n := range b.all() {
		taken[n.ID] = true
	}
	id := addr
	for i := 2; taken[id]; i++ {
		id = addr + "-" + strconv.Itoa(i)
	}
	return id
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func sanitizeName(s string) string {
	s = strings.Trim(nonIdent.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "node"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "n_" + s
	}
	return s
}

// bare appends an empty node; name must already be unique.
func (b *builder) bare(kind model.NodeKind, typ, name string, pos model.Position) *model.Node {
	n := &model.Node{Kind: kind, Type: typ, Name: name, Position: pos, Positioned: true, Body: emptyBody()}
	n.ID = b.uniqueID(address(n))
	b.pending = append(b.pending, n)
	if p, ok := b.c.providerOf(typ); ok {
		b.useProvider(p.info.Name)
	}
	return n
}

// helper appends a node named after hint (made unique).
func (b *builder) helper(kind model.NodeKind, typ, hint string, pos model.Position) *model.Node {
	return b.bare(kind, typ, b.uniqueName(kind, typ, hint), pos)
}

func (b *builder) useProvider(name string) {
	for _, p := range b.providers {
		if p == name {
			return
		}
	}
	b.providers = append(b.providers, name)
}

func (b *builder) commit() {
	for _, n := range b.pending {
		b.wf.Nodes = append(b.wf.Nodes, *n)
	}
	b.pending = nil
	for _, name := range b.providers {
		b.ensureProvider(name)
	}
}

// ensureProvider adds the required_providers entry and a provider block.
// Floci endpoints are never written here; the sandbox injects them at run
// time so exported code stays cloud-ready.
func (b *builder) ensureProvider(name string) {
	p, ok := b.c.byName[name]
	if !ok {
		return
	}
	wf := b.wf
	if wf.Terraform == nil {
		body := emptyBody()
		wf.Terraform = &body
	}
	rp := ensureBlock(wf.Terraform, "required_providers")
	if getAttr(&rp.Body, name) == nil {
		major, _, _ := strings.Cut(p.info.Version, ".")
		setAttr(&rp.Body, name, lit(map[string]string{"source": p.info.Source, "version": "~> " + major + ".0"}))
	}
	if p.cloud == "" {
		return
	}
	for _, pb := range wf.Providers {
		if pb.Name == name {
			return
		}
	}
	body := emptyBody()
	switch p.cloud {
	case model.CloudAWS:
		setAttr(&body, "region", lit(b.c.defaultAWSRegion()))
	case model.CloudAzure:
		body.Blocks = append(body.Blocks, model.Block{Type: "features", Body: emptyBody()})
	case model.CloudGCP:
		b.ensureVariable("gcp_project")
		setAttr(&body, "project", expr("var.gcp_project"))
		setAttr(&body, "region", lit("us-central1"))
	case model.CloudOCI:
		setAttr(&body, "region", lit("us-ashburn-1"))
	}
	wf.Providers = append(wf.Providers, model.NamedBlock{Name: name, Body: body})
}

func (b *builder) ensureVariable(name string) {
	for _, v := range b.wf.Variables {
		if v.Name == name {
			return
		}
	}
	cv, ok := b.c.cur.Variables[name]
	if !ok {
		return
	}
	body := emptyBody()
	setAttr(&body, "type", expr(cv.Type))
	setAttr(&body, "default", litRaw(cv.Default))
	setAttr(&body, "description", lit(cv.Description))
	b.wf.Variables = append(b.wf.Variables, model.NamedBlock{Name: name, Body: body})
}

// AddNode appends a node of typ to wf (unique name derived from typ when
// name is ""), prefilled with curated defaults. Curated starters may append
// helper nodes laid out near pos. It ensures wf has a required_providers
// entry and a provider block for the node's cloud. For modules, typ is the
// module source. Returns the ID of the primary node.
func (c *Catalog) AddNode(wf *model.Workflow, kind model.NodeKind, typ, name string, pos model.Position) (string, error) {
	if name != "" && !identRe.MatchString(name) {
		return "", fmt.Errorf("catalog: invalid name %q: use letters, digits, '_' or '-', starting with a letter or '_'", name)
	}
	b := newBuilder(c, wf)
	var n *model.Node
	switch kind {
	case model.KindModule:
		if strings.TrimSpace(typ) == "" {
			return "", fmt.Errorf("catalog: module source is required")
		}
		if name == "" {
			name = b.uniqueName(kind, "module", moduleHint(typ))
		} else if b.nameTaken(kind, "module", name) {
			return "", fmt.Errorf("catalog: module %q already exists", name)
		}
		n = b.bare(kind, "module", name, pos)
		setAttr(&n.Body, "source", lit(typ))
	case model.KindResource, model.KindData:
		if _, ok := c.Schema(kind, typ); !ok {
			return "", fmt.Errorf("catalog: unknown %s type %q", kind, typ)
		}
		if name != "" && b.nameTaken(kind, typ, name) {
			return "", fmt.Errorf("catalog: %s %s.%s already exists", kind, typ, name)
		}
		n = b.addCurated(kind, typ, name, pos, nil, "")
	default:
		return "", fmt.Errorf("catalog: unknown node kind %q", kind)
	}
	b.commit()
	return n.ID, nil
}

// moduleHint derives a name from a module source: "terraform-aws-modules/vpc/aws" -> "vpc".
func moduleHint(source string) string {
	s := strings.TrimSuffix(strings.Split(source, "?")[0], "/")
	if i := strings.Index(s, "//"); i > 0 && !strings.Contains(s[:i], ":") {
		s = s[:i]
	}
	parts := strings.Split(s, "/")
	if len(parts) == 3 && !strings.HasPrefix(s, ".") {
		return parts[1]
	}
	return strings.TrimSuffix(parts[len(parts)-1], ".git")
}

// defaultHint names a node after the last word of its type: aws_sqs_queue -> queue.
func defaultHint(typ string) string {
	return typ[strings.LastIndex(typ, "_")+1:]
}

// addCurated creates a node with its curated starter body and needs. name
// is exact when set; otherwise hint (or the curated/default hint) is made
// unique.
func (b *builder) addCurated(kind model.NodeKind, typ, name string, pos model.Position, override *initOverride, hint string) *model.Node {
	cr := b.c.cur.lookup(kind, typ)
	if name == "" {
		if hint == "" && cr != nil {
			hint = cr.Name
		}
		if hint == "" {
			hint = defaultHint(typ)
		}
		name = b.uniqueName(kind, typ, hint)
	}
	n := b.bare(kind, typ, name, pos)
	schema, _ := b.c.Schema(kind, typ)

	ph := placeholders{name: n.Name, suffix: b.suffix, needs: map[string]string{}}
	for _, other := range b.all() {
		if other.Kind == kind && other.Type == typ {
			ph.seq++
		}
	}

	var refs []model.Attr
	if cr != nil {
		helpers := 0
		for _, nd := range cr.Needs {
			if nd.Var != "" {
				b.ensureVariable(nd.Var)
				nd.Refs.each(func(attr, _ string) {
					refs = append(refs, model.Attr{Name: attr, Value: expr("var." + nd.Var)})
				})
				continue
			}
			var dep *model.Node
			if !nd.New {
				dep = b.latest(nd.kind(), nd.Type)
			}
			if dep == nil {
				if nd.Optional {
					continue
				}
				depHint := ""
				if nd.Name != "" {
					depHint = ph.render(nd.Name)
				}
				depPos := model.Position{X: pos.X - 320, Y: pos.Y + float64(helpers)*150}
				dep = b.addCurated(nd.kind(), nd.Type, "", depPos, nd.Init, depHint)
				helpers++
			}
			ph.needs[nd.Type] = address(dep)
			nd.Refs.each(func(attr, srcAttr string) {
				refs = append(refs, model.Attr{Name: attr, Value: expr(ref(dep, srcAttr))})
			})
		}

		cr.Attrs.each(func(key string, m attrMeta) {
			if strings.Contains(key, ".") {
				return
			}
			if override != nil {
				if src, ok := override.Exprs.get(key); ok {
					setAttr(&n.Body, key, expr(ph.render(src)))
					return
				}
				if raw, ok := override.Attrs.get(key); ok {
					setAttr(&n.Body, key, litRaw(ph.renderJSON(raw)))
					return
				}
			}
			switch {
			case m.Expr != "":
				setAttr(&n.Body, key, expr(ph.render(m.Expr)))
			case m.Init != nil:
				setAttr(&n.Body, key, litRaw(ph.renderJSON(m.Init)))
			case m.Default != nil && schema != nil && attrRequired(&schema.Block, key):
				setAttr(&n.Body, key, litRaw(m.Default))
			}
		})
		if override != nil {
			override.Attrs.each(func(key string, raw json.RawMessage) {
				setDefault(&n.Body, key, litRaw(ph.renderJSON(raw)))
			})
			override.Exprs.each(func(key, src string) {
				setDefault(&n.Body, key, expr(ph.render(src)))
			})
		}
		for _, r := range refs {
			setAttr(&n.Body, r.Name, r.Value)
		}
		blocks := cr.Blocks
		if override != nil && override.Blocks != nil {
			blocks = override.Blocks
		}
		for _, ib := range blocks {
			n.Body.Blocks = append(n.Body.Blocks, ph.buildBlock(ib))
		}
	}
	if schema != nil {
		addRequiredBlocks(&n.Body, &schema.Block)
	}
	return n
}

func attrRequired(sb *model.SchemaBlock, name string) bool {
	for _, a := range sb.Attributes {
		if a.Name == name {
			return a.Required
		}
	}
	return false
}

// addRequiredBlocks appends empty blocks for required nested blocks so forms
// show them and validation points at their attributes.
func addRequiredBlocks(body *model.Body, sb *model.SchemaBlock) {
	for _, nb := range sb.Blocks {
		if nb.MinItems == 0 {
			continue
		}
		have := 0
		for i := range body.Blocks {
			if body.Blocks[i].Type == nb.Name {
				have++
				addRequiredBlocks(&body.Blocks[i].Body, &nb.Block)
			}
		}
		for ; have < nb.MinItems; have++ {
			blk := model.Block{Type: nb.Name, Body: emptyBody()}
			addRequiredBlocks(&blk.Body, &nb.Block)
			body.Blocks = append(body.Blocks, blk)
		}
	}
}

// placeholders renders {{name}}, {{slug}}, {{slug:N}}, {{alnum}},
// {{alnum:N}}, {{rand}}, {{seq}}, {{password}} and {{need:<type>}} in
// curated strings.
type placeholders struct {
	name   string
	suffix string
	seq    int
	needs  map[string]string
}

var placeholderRe = regexp.MustCompile(`\{\{([a-z]+)(?::([^}]+))?\}\}`)

func (p placeholders) render(s string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeholderRe.FindStringSubmatch(m)
		arg := sub[2]
		limit := func(v string) string {
			if n, err := strconv.Atoi(arg); err == nil && len(v) > n {
				return strings.TrimRight(v[:n], "-")
			}
			return v
		}
		switch sub[1] {
		case "name":
			return p.name
		case "slug":
			return limit(strings.Trim(strings.ToLower(strings.ReplaceAll(p.name, "_", "-")), "-"))
		case "alnum":
			return limit(strings.ToLower(nonAlnum.ReplaceAllString(p.name, "")))
		case "rand":
			return p.suffix
		case "seq":
			return strconv.Itoa(p.seq)
		case "password":
			return randString(24, passwordChars)
		case "need":
			if addr, ok := p.needs[arg]; ok {
				return addr
			}
		}
		return m
	})
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

// renderJSON renders placeholders inside every string of a JSON value.
func (p placeholders) renderJSON(raw json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	out, err := json.Marshal(p.renderAny(v))
	if err != nil {
		return raw
	}
	return out
}

func (p placeholders) renderAny(v any) any {
	switch t := v.(type) {
	case string:
		return p.render(t)
	case []any:
		for i := range t {
			t[i] = p.renderAny(t[i])
		}
	case map[string]any:
		for k := range t {
			t[k] = p.renderAny(t[k])
		}
	}
	return v
}

func (p placeholders) buildBlock(ib initBlock) model.Block {
	blk := model.Block{Type: ib.Type, Body: emptyBody()}
	ib.Attrs.each(func(k string, raw json.RawMessage) {
		setAttr(&blk.Body, k, litRaw(p.renderJSON(raw)))
	})
	ib.Exprs.each(func(k, src string) {
		setAttr(&blk.Body, k, expr(p.render(src)))
	})
	for _, child := range ib.Blocks {
		blk.Body.Blocks = append(blk.Body.Blocks, p.buildBlock(child))
	}
	return blk
}
