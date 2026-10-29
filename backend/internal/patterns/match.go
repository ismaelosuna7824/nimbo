package patterns

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// heavy patterns carry distributed-systems cost; a simple requirement must
// not be steered to them unless it names them or a strong signal for them.
var heavy = map[string]bool{
	"microservices":              true,
	"event-driven":               true,
	"event-sourcing":             true,
	"cqrs":                       true,
	"saga":                       true,
	"transactional-outbox":       true,
	"database-per-service":       true,
	"big-data-streaming":         true,
	"sharding":                   true,
	"deployment-stamps":          true,
	"leader-election":            true,
	"backends-for-frontends":     true,
	"anti-corruption-layer":      true,
	"sidecar":                    true,
	"bulkhead":                   true,
	"scheduler-agent-supervisor": true,
}

// simpleBaseline is what a simple/CRUD requirement starts from.
var simpleBaseline = map[string]int{
	"n-tier":      6,
	"serverless":  5,
	"api-gateway": 4,
	"cache-aside": 3,
}

// simpleSignals mark a requirement that should stay simple.
var simpleSignals = []string{
	"crud", "simple", "sencillo", "sencilla", "basic", "basico", "basica",
	"mvp", "prototype", "prototipo", "small", "pequeno", "pequena",
	"todo app", "lista de tareas", "landing", "blog", "internal tool",
	"herramienta interna", "poc", "proof of concept",
}

// strongSignals are keywords that justify a heavy pattern even when the
// requirement also says "simple" (they name the need explicitly).
var strongSignals = map[string][]string{
	"microservices":      {"microservices", "microservicios", "microservice", "microservicio", "equipos independientes", "independent teams"},
	"event-sourcing":     {"event sourcing", "event store", "auditoria", "audit trail", "historial", "ledger", "replay"},
	"saga":               {"saga", "transaccion distribuida", "distributed transaction"},
	"cqrs":               {"cqrs"},
	"event-driven":       {"event driven", "orientado a eventos", "eventos", "events", "tiempo real", "real time"},
	"big-data-streaming": {"streaming", "kafka", "big data", "telemetria", "telemetry", "clickstream"},
}

// Match ranks patterns for a free-text requirement (English or Spanish) and
// an optional platform ("" = any). It returns at most limit patterns (all
// when limit <= 0), styles first then patterns, each by descending score
// then ID. A simple/CRUD requirement favors n-tier/serverless, api-gateway
// and cache-aside and excludes heavy patterns it does not explicitly ask for.
func Match(requirement, platform string, limit int) []Pattern {
	text := tokens(requirement)
	simple := false
	for _, s := range simpleSignals {
		if contains(text, tokens(s)) {
			simple = true
			break
		}
	}
	type scored struct {
		p     Pattern
		score int
	}
	var out []scored
	for _, p := range all {
		if platform != "" {
			if _, ok := p.block(platform); !ok {
				continue
			}
		}
		score := 0
		for _, k := range p.Keywords {
			kt := tokens(k)
			if contains(text, kt) {
				score += 1 + len(kt) // phrases weigh more than single words
			}
		}
		if contains(text, tokens(p.ID)) || contains(text, tokens(p.Name)) {
			score += 5
		}
		if simple {
			score += simpleBaseline[p.ID]
			if heavy[p.ID] && !strong(text, p.ID) {
				score = 0
			}
		}
		if score > 0 {
			out = append(out, scored{p, score})
		}
	}
	slices.SortFunc(out, func(a, b scored) int {
		if a.p.Kind != b.p.Kind {
			if a.p.Kind == KindStyle {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.p.ID, b.p.ID))
	})
	if limit > 0 && len(out) > limit {
		// Keep the best `limit` by score overall, then re-apply the
		// styles-first order.
		best := slices.Clone(out)
		slices.SortStableFunc(best, func(a, b scored) int {
			return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.p.ID, b.p.ID))
		})
		keep := map[string]bool{}
		for _, s := range best[:limit] {
			keep[s.p.ID] = true
		}
		out = slices.DeleteFunc(out, func(s scored) bool { return !keep[s.p.ID] })
	}
	res := make([]Pattern, len(out))
	for i, s := range out {
		res[i] = s.p
	}
	return res
}

func strong(text []string, id string) bool {
	for _, s := range strongSignals[id] {
		if contains(text, tokens(s)) {
			return true
		}
	}
	return false
}

var fold = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

// tokens lowercases, strips Spanish accents and splits on non-alphanumerics.
func tokens(s string) []string {
	s = fold.Replace(strings.ToLower(s))
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// contains reports whether phrase occurs contiguously in text; a text word
// also matches its plural ("evento" ~ "eventos", "cola" ~ "colas").
func contains(text, phrase []string) bool {
	if len(phrase) == 0 || len(phrase) > len(text) {
		return false
	}
	for i := 0; i+len(phrase) <= len(text); i++ {
		ok := true
		for j, w := range phrase {
			t := text[i+j]
			if t != w && t != w+"s" && t != w+"es" {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
