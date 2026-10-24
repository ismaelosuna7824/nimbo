package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// recipe is a curated way to connect a source type to a target type. match
// and apply see the nodes in recipe orientation; bidirectional recipes are
// also offered when the user drags the other way.
type recipe struct {
	id, label, desc string
	match           func(c *Catalog, src, dst *model.Node) bool
	bidi            bool
	// sets lists plain reference attributes the recipe writes ("src.vpc_id"),
	// so the equivalent generic recipe is not offered twice.
	sets  []string
	apply func(x *conn) error
}

// Recipe ID prefixes for generic recipes.
const (
	genericRefPrefix = "ref:"
	dependsOnID      = "depends_on"
)

// conn is the context of one curated recipe application.
type conn struct {
	c        *Catalog
	b        *builder
	src, dst *model.Node
	mid      model.Position
	helpers  int
}

// helper adds a helper node laid out between source and target.
func (x *conn) helper(kind model.NodeKind, typ, hint string) *model.Node {
	offsets := []float64{0, 130, -130, 260, -260, 390, -390}
	dy := offsets[x.helpers%len(offsets)]
	dx := float64(x.helpers/len(offsets)) * 60
	x.helpers++
	return x.b.helper(kind, typ, hint, model.Position{X: x.mid.X + dx, Y: x.mid.Y + dy})
}

func findNode(wf *model.Workflow, id string) *model.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}

// Connections lists the ways to connect sourceID to targetID: curated recipes
// first (direct orientation before reversed), then generic reference
// recipes ranked by attribute name matching, then depends_on.
func (c *Catalog) Connections(wf *model.Workflow, sourceID, targetID string) []model.ConnectionRecipe {
	src, dst := findNode(wf, sourceID), findNode(wf, targetID)
	if src == nil || dst == nil || src == dst {
		return nil
	}
	// Grant recipes ("What can it do?") come first.
	out := c.accessRecipes(wf, src, dst)
	covered := map[string]bool{} // nodeID + "." + attr
	cover := func(r *recipe, rs, rd *model.Node) {
		for _, s := range r.sets {
			role, attr, _ := strings.Cut(s, ".")
			n := rs
			if role == "dst" {
				n = rd
			}
			covered[n.ID+"."+attr] = true
		}
	}
	var reversed []model.ConnectionRecipe
	for i := range recipes {
		r := &recipes[i]
		switch {
		case r.match(c, src, dst):
			out = append(out, model.ConnectionRecipe{ID: r.id, Label: r.label, Description: r.desc, SourceType: src.Type, TargetType: dst.Type, Curated: true})
			cover(r, src, dst)
		case r.bidi && r.match(c, dst, src):
			reversed = append(reversed, model.ConnectionRecipe{ID: r.id, Label: r.label, Description: r.desc, SourceType: src.Type, TargetType: dst.Type, Curated: true})
			cover(r, dst, src)
		}
	}
	out = append(out, reversed...)

	var cands []genericCandidate
	cands = append(cands, c.genericCandidates(dst, src, true)...)
	cands = append(cands, c.genericCandidates(src, dst, false)...)
	slices.SortStableFunc(cands, func(a, b genericCandidate) int { return b.score - a.score })
	const maxGeneric = 8
	added := 0
	for _, g := range cands {
		if covered[g.holder.ID+"."+g.attr] || added == maxGeneric {
			continue
		}
		out = append(out, model.ConnectionRecipe{
			ID:          g.id(),
			Label:       fmt.Sprintf("Set %s of %s to %s.%s", g.attr, g.holder.Name, g.referenced.Name, g.refAttr),
			Description: fmt.Sprintf("%s.%s = %s", address(g.holder), g.attr, g.valueSrc()),
			SourceType:  src.Type,
			TargetType:  dst.Type,
		})
		added++
	}
	if !dependsOn(dst, src) {
		out = append(out, model.ConnectionRecipe{
			ID:          dependsOnID,
			Label:       fmt.Sprintf("Make %s depend on %s", dst.Name, src.Name),
			Description: fmt.Sprintf("Adds %s to depends_on of %s; Terraform creates %s first.", address(src), address(dst), src.Name),
			SourceType:  src.Type,
			TargetType:  dst.Type,
		})
	}
	return out
}

func dependsOn(n, on *model.Node) bool {
	a := getAttr(&n.Body, "depends_on")
	return a != nil && referencesNode(valueSource(a.Value), address(on))
}

// Connect applies recipeID (from Connections) to wf.
func (c *Catalog) Connect(wf *model.Workflow, sourceID, targetID, recipeID string) error {
	src, dst := findNode(wf, sourceID), findNode(wf, targetID)
	if src == nil || dst == nil {
		return fmt.Errorf("catalog: unknown node %q or %q", sourceID, targetID)
	}
	offered := false
	for _, r := range c.Connections(wf, sourceID, targetID) {
		if r.ID == recipeID {
			offered = true
		}
	}
	if !offered {
		return fmt.Errorf("catalog: recipe %q does not apply to %s -> %s", recipeID, address(src), address(dst))
	}
	b := newBuilder(c, wf)
	switch {
	case strings.HasPrefix(recipeID, model.AccessRecipePrefix):
		level, _ := access.ParseRecipeID(recipeID)
		if err := c.applyAccess(b, wf, src, dst, level); err != nil {
			return err
		}
	case recipeID == dependsOnID:
		a := getAttr(&dst.Body, "depends_on")
		var cur *model.Value
		if a != nil {
			cur = &a.Value
		}
		setAttr(&dst.Body, "depends_on", appendList(cur, address(src)))
	case strings.HasPrefix(recipeID, genericRefPrefix):
		g, err := parseGenericID(recipeID, src, dst)
		if err != nil {
			return err
		}
		g.apply()
	default:
		var r *recipe
		for i := range recipes {
			if recipes[i].id == recipeID {
				r = &recipes[i]
			}
		}
		x := &conn{c: c, b: b, src: src, dst: dst}
		if !r.match(c, src, dst) {
			x.src, x.dst = dst, src
		}
		x.mid = model.Position{X: (src.Position.X + dst.Position.X) / 2, Y: (src.Position.Y + dst.Position.Y) / 2}
		if err := r.apply(x); err != nil {
			return err
		}
	}
	b.commit()
	return nil
}

// ---------------------------------------------------------------------------
// Generic reference recipes

type genericCandidate struct {
	holder, referenced *model.Node
	holderIsTarget     bool
	attr, refAttr      string
	list               bool
	score              int
}

func (g genericCandidate) id() string {
	role := "source"
	if g.holderIsTarget {
		role = "target"
	}
	return genericRefPrefix + role + ":" + g.attr + ":" + g.refAttr
}

func (g genericCandidate) valueSrc() string {
	if g.list {
		return "[" + ref(g.referenced, g.refAttr) + "]"
	}
	return ref(g.referenced, g.refAttr)
}

func (g genericCandidate) apply() {
	if !g.list {
		setAttr(&g.holder.Body, g.attr, expr(ref(g.referenced, g.refAttr)))
		return
	}
	var cur *model.Value
	if a := getAttr(&g.holder.Body, g.attr); a != nil {
		cur = &a.Value
	}
	setAttr(&g.holder.Body, g.attr, appendList(cur, ref(g.referenced, g.refAttr)))
}

func parseGenericID(id string, src, dst *model.Node) (genericCandidate, error) {
	parts := strings.Split(strings.TrimPrefix(id, genericRefPrefix), ":")
	if len(parts) != 3 {
		return genericCandidate{}, fmt.Errorf("catalog: malformed recipe id %q", id)
	}
	g := genericCandidate{holder: src, referenced: dst, attr: parts[1], refAttr: parts[2]}
	if parts[0] == "target" {
		g.holder, g.referenced, g.holderIsTarget = dst, src, true
	}
	g.list = isPluralRefAttr(g.attr)
	return g, nil
}

var (
	singularSuffixes = []string{"_arn", "_id", "_name", "_url"}
	pluralSuffixes   = map[string]string{"_arns": "_arn", "_ids": "_id", "_names": "_name", "_urls": "_url"}
)

func isPluralRefAttr(attr string) bool {
	for p := range pluralSuffixes {
		if strings.HasSuffix(attr, p) {
			return true
		}
	}
	return false
}

func typeIs(t json.RawMessage, want ...string) bool {
	var s string
	if json.Unmarshal(t, &s) == nil {
		return slices.Contains(want, s)
	}
	return false
}

func isStringList(t json.RawMessage) bool {
	var v []json.RawMessage
	if json.Unmarshal(t, &v) != nil || len(v) != 2 {
		return false
	}
	return (typeIs(v[0], "list") || typeIs(v[0], "set")) && typeIs(v[1], "string")
}

// genericCandidates proposes setting one attribute of holder to an attribute
// of referenced, ranked by how well the attribute name matches the
// referenced type (vpc_id ↔ aws_vpc.id).
func (c *Catalog) genericCandidates(holder, referenced *model.Node, holderIsTarget bool) []genericCandidate {
	if holder.Kind == model.KindModule || referenced.Kind == model.KindModule {
		return nil
	}
	hs, ok := c.Schema(holder.Kind, holder.Type)
	if !ok {
		return nil
	}
	rs, ok := c.Schema(referenced.Kind, referenced.Type)
	if !ok {
		return nil
	}
	rattrs := map[string]bool{}
	for _, a := range rs.Block.Attributes {
		rattrs[a.Name] = true
	}
	rp, _ := c.providerOf(referenced.Type)
	rtail := strings.TrimPrefix(referenced.Type, rp.info.Name+"_")
	isAWS := rp.cloud == model.CloudAWS
	raddr := address(referenced)

	var out []genericCandidate
	for _, a := range hs.Block.Attributes {
		if !a.Required && !a.Optional {
			continue
		}
		list := isStringList(a.Type)
		if !list && !typeIs(a.Type, "string") {
			continue
		}
		switch a.Name {
		case "id", "name", "region", "description", "tags", "tags_all":
			continue
		}
		if cur := getAttr(&holder.Body, a.Name); cur != nil && referencesNode(valueSource(cur.Value), raddr) {
			continue
		}
		x, suffix := splitRefAttr(a.Name)
		if list && !isPluralRefAttr(a.Name) {
			continue
		}
		var score int
		var refAttr string
		switch {
		case a.Name == "arn" && rattrs["arn"]:
			score, refAttr = 10, "arn"
		case suffix != "":
			score = matchScore(x, rtail)
			if score == 0 && suffix == "_arn" && genericSinks[x] && rattrs["arn"] {
				score = 15
			}
			refAttr = pickRefAttr(suffix, x, rattrs)
		default:
			score = matchScore(a.Name, rtail)
			if score > 0 {
				score -= 20
			}
			for _, cand := range []string{"arn", "id", "name"} {
				if cand == "arn" && !isAWS {
					continue
				}
				if rattrs[cand] {
					refAttr = cand
					break
				}
			}
		}
		if score <= 0 || refAttr == "" {
			continue
		}
		if a.Required {
			score += 10
		}
		if holderIsTarget {
			score += 5
		}
		out = append(out, genericCandidate{
			holder: holder, referenced: referenced, holderIsTarget: holderIsTarget,
			attr: a.Name, refAttr: refAttr, list: list, score: score,
		})
	}
	return out
}

var genericSinks = map[string]bool{
	"target": true, "resource": true, "source": true, "destination": true, "endpoint": true,
	"principal": true, "event_source": true, "dead_letter_target": true,
}

// splitRefAttr splits "vpc_id" into ("vpc", "_id"), "subnet_ids" into
// ("subnet", "_id"); attributes without a reference suffix return ("", "").
func splitRefAttr(name string) (x, suffix string) {
	for plural, single := range pluralSuffixes {
		if strings.HasSuffix(name, plural) {
			return strings.TrimSuffix(name, plural), single
		}
	}
	for _, s := range singularSuffixes {
		if strings.HasSuffix(name, s) {
			return strings.TrimSuffix(name, s), s
		}
	}
	return "", ""
}

func pickRefAttr(suffix, x string, rattrs map[string]bool) string {
	var prefs []string
	switch suffix {
	case "_arn":
		prefs = []string{"arn"}
	case "_id":
		prefs = []string{"id"}
	case "_name":
		prefs = []string{x + "_name", "name", "id"}
	case "_url":
		prefs = []string{"url", x + "_url"}
	}
	for _, p := range prefs {
		if rattrs[p] {
			return p
		}
	}
	return ""
}

// matchScore rates how well attribute stem x names the referenced type tail
// ("vpc" vs "vpc" = 100, "queue" vs "sqs_queue" = 80, ...).
func matchScore(x, rtail string) int {
	if x == "" {
		return 0
	}
	tokens := strings.Split(rtail, "_")
	last := tokens[len(tokens)-1]
	switch {
	case x == rtail:
		return 100
	case strings.HasSuffix(rtail, "_"+x):
		return 80
	case strings.HasSuffix(x, "_"+rtail):
		return 70
	case x == last || strings.HasSuffix(x, "_"+last):
		return 60
	}
	xt := strings.Split(x, "_")
	for _, t := range tokens {
		if len(t) >= 3 && slices.Contains(xt, t) {
			return 30
		}
	}
	return 0
}
