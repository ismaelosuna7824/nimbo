package catalog

import (
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Query filters and ranks palette entries. Empty fields do not filter; Kind
// "" returns resources and data sources (resources rank first). Limit 0
// means DefaultSearchLimit.
type Query struct {
	Text          string
	Cloud         model.Cloud
	Kind          model.NodeKind
	Category      string
	SupportedOnly bool
	PopularOnly   bool
	Limit         int
}

// DefaultSearchLimit caps Search results when Query.Limit is 0.
const DefaultSearchLimit = 200

// summary is a precomputed palette entry; Floci support is evaluated per
// query because live services can change it.
type summary struct {
	kind     model.NodeKind
	typ      string
	tail     string // type without provider prefix, "_" replaced by " "
	cloud    model.Cloud
	p        *provider
	label    string
	lower    string // lowercase label
	svcKey   string
	svcLabel string // lowercase service label
	category string
	popular  bool
	curated  bool
}

func (c *Catalog) allSummaries() []summary {
	c.summariesOnce.Do(func() {
		for _, p := range c.providers {
			if p.cloud == "" {
				continue
			}
			for _, kind := range []model.NodeKind{model.KindResource, model.KindData} {
				names := p.resources
				if kind == model.KindData {
					names = p.data
				}
				for _, typ := range names {
					c.summaries = append(c.summaries, c.newSummary(kind, typ, p))
				}
			}
		}
	})
	return c.summaries
}

func (c *Catalog) newSummary(kind model.NodeKind, typ string, p *provider) summary {
	si := c.serviceInfo(kind, typ, p.cloud)
	s := summary{
		kind:     kind,
		typ:      typ,
		tail:     strings.ReplaceAll(strings.TrimPrefix(typ, p.info.Name+"_"), "_", " "),
		cloud:    p.cloud,
		p:        p,
		svcKey:   si.key,
		category: si.category,
	}
	if cs := c.clouds[p.cloud]; cs != nil {
		if svc := cs.serviceOf(typ); svc != nil {
			s.svcLabel = strings.ToLower(svc.Label)
		}
	}
	if cr := c.cur.lookup(kind, typ); cr != nil && cr.Label != "" {
		s.label = cr.Label
		s.popular = cr.Popular
		s.curated = true
	} else {
		s.label = autoLabel(p, typ)
	}
	s.lower = strings.ToLower(s.label)
	return s
}

// autoLabel turns "aws_cloudwatch_log_group" into "Cloudwatch Log Group".
func autoLabel(p *provider, typ string) string {
	words := strings.Split(strings.TrimPrefix(typ, p.info.Name+"_"), "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func (c *Catalog) summarize(kind model.NodeKind, typ string, p *provider) model.ResourceSummary {
	if p.cloud == "" {
		label := autoLabel(p, typ)
		icon := "box"
		if cr := c.cur.lookup(kind, typ); cr != nil {
			label, icon = cr.Label, cr.Icon
		}
		return model.ResourceSummary{Kind: kind, Type: typ, Category: "Other", Label: label, Icon: icon, FlociSupported: true}
	}
	return c.toSummary(c.newSummary(kind, typ, p))
}

func (c *Catalog) toSummary(s summary) model.ResourceSummary {
	si := c.serviceInfo(s.kind, s.typ, s.cloud)
	icon := si.icon
	if cr := c.cur.lookup(s.kind, s.typ); cr != nil && cr.Icon != "" {
		icon = cr.Icon
	}
	if icon == "" {
		icon = c.cur.CategoryIcons[s.category]
	}
	return model.ResourceSummary{
		Kind:           s.kind,
		Type:           s.typ,
		Cloud:          s.cloud,
		Service:        s.svcKey,
		Category:       s.category,
		Label:          s.label,
		Icon:           icon,
		Popular:        s.popular,
		FlociSupported: si.floci,
	}
}

// Search returns palette entries matching q, best match first. Without text,
// popular entries come first, then alphabetical by label.
func (c *Catalog) Search(q Query) []model.ResourceSummary {
	if q.Kind == model.KindModule {
		return nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	full := strings.ToLower(strings.TrimSpace(q.Text))
	tokens := strings.Fields(strings.ReplaceAll(full, "_", " "))
	// "aws_dynamo" or "google pubsub": the provider name filters instead of
	// having to match a word.
	providerFilter := ""
	if len(tokens) > 1 {
		if p, ok := c.byName[tokens[0]]; ok && p.cloud != "" {
			providerFilter, tokens = tokens[0], tokens[1:]
		}
	}
	type hit struct {
		s     summary
		score int
	}
	var hits []hit
	for _, s := range c.allSummaries() {
		if q.Cloud != "" && s.cloud != q.Cloud || providerFilter != "" && s.p.info.Name != providerFilter {
			continue
		}
		if q.Kind != "" && s.kind != q.Kind {
			continue
		}
		if q.Category != "" && s.category != q.Category {
			continue
		}
		if q.PopularOnly && !s.popular {
			continue
		}
		score := 0
		if len(tokens) > 0 {
			score = textScore(s, full, tokens)
			if score <= 0 {
				continue
			}
		}
		if s.popular {
			score += 150
		}
		if s.curated {
			score += 40
		}
		if s.kind == model.KindResource {
			score += 20
		}
		score += cloudBonus[s.cloud]
		if q.SupportedOnly && !c.serviceInfo(s.kind, s.typ, s.cloud).floci {
			continue
		}
		hits = append(hits, hit{s, score})
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if len(a.s.typ) != len(b.s.typ) && len(tokens) > 0 {
			return len(a.s.typ) - len(b.s.typ)
		}
		if a.s.lower != b.s.lower {
			return strings.Compare(a.s.lower, b.s.lower)
		}
		return strings.Compare(a.s.typ, b.s.typ)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]model.ResourceSummary, len(hits))
	for i, h := range hits {
		out[i] = c.toSummary(h.s)
	}
	return out
}

// cloudBonus breaks ties between equally good matches in display order.
var cloudBonus = map[model.Cloud]int{model.CloudAWS: 6, model.CloudAzure: 4, model.CloudGCP: 2}

// textScore ranks how well a palette entry matches the query: exact type,
// exact label, then per-token matches on label words, type words, service
// and category. Every token must match somewhere; multi-word queries that
// prefix the label or type get a phrase bonus.
func textScore(s summary, full string, tokens []string) int {
	fullUnderscore := strings.ReplaceAll(full, " ", "_")
	if fullUnderscore == s.typ || fullUnderscore == strings.ReplaceAll(s.tail, " ", "_") {
		return 5000
	}
	if full == s.lower {
		return 4000
	}
	total := 0
	for _, t := range tokens {
		best := 0
		switch {
		case hasWord(s.lower, t, false):
			best = 600
		case hasWord(s.tail, t, false):
			best = 500
		case hasWord(s.lower, t, true):
			best = 400
		case hasWord(s.tail, t, true):
			best = 350
		case s.svcKey == t || hasWord(s.svcLabel, t, true):
			best = 300
		case strings.Contains(s.lower, t):
			best = 200
		case strings.Contains(s.tail, t):
			best = 150
		case strings.Contains(strings.ToLower(s.category), t):
			best = 50
		}
		if best == 0 {
			return 0
		}
		total += best
	}
	if phrase := strings.Join(tokens, " "); len(tokens) > 1 && (strings.HasPrefix(s.lower, phrase) || strings.HasPrefix(s.tail, phrase)) {
		total += 300
	}
	return total
}

// hasWord reports whether a word of s equals t (or starts with t when
// prefix is set). Words split on spaces, slashes, dashes and parentheses.
func hasWord(s, t string, prefix bool) bool {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '(' || r == ')' || r == '/' || r == '-' }) {
		if w == t || prefix && strings.HasPrefix(w, t) {
			return true
		}
	}
	return false
}

// Categories lists the categories that have at least one type in cloud
// (every cloud when cloud is ""), in canonical order.
func (c *Catalog) Categories(cloud model.Cloud) []string {
	used := map[string]bool{}
	for _, s := range c.allSummaries() {
		if cloud == "" || s.cloud == cloud {
			used[s.category] = true
		}
	}
	var out []string
	for _, cat := range c.cur.Categories {
		if used[cat] {
			out = append(out, cat)
		}
	}
	return out
}
