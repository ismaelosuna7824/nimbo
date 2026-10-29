// Package advisor proposes what a senior security and infrastructure
// engineer would add next to a design: "you built X, also consider Y". It
// reads the workflow model only (Terraform nodes, edges and attributes; the
// saved compose and Kubernetes graphs) and returns companions such as a WAF
// with a rate-based rule for a public API, backups for a data store or an
// autoscaler with bounds for a compute tier.
//
// Every recommendation has a trigger in the design and disappears once the
// design has its companion, so an agent can apply them in a loop. Guards
// against over-engineering:
//   - nothing is recommended without a trigger (no WAF without a public
//     entry point, no PodDisruptionBudget for a single replica);
//   - production-scope items are only listed for designs that look
//     production-intended: "prod", "production" or "prd" in the name,
//     description, environments or tags, or (Terraform) a public entry
//     point; docker and Kubernetes designs only by their names;
//   - each recommendation covers every entry point of its kind once
//     (Nodes), and at most maxRecommendations are returned, security first.
package advisor

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Addition is one thing a recommendation adds to the design.
type Addition struct {
	// Type is a Terraform resource type, a compose key or service, or a
	// Kubernetes kind.
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// Recommendation is one companion the design is missing.
type Recommendation struct {
	// ID is stable ("aws_public_api_waf").
	ID string `json:"id"`
	// Title is imperative.
	Title   string         `json:"title"`
	Why     string         `json:"why"`
	Pillars []model.Pillar `json:"pillars"`
	// Nodes are the addresses or objects that triggered it.
	Nodes []string   `json:"nodes,omitempty"`
	Add   []Addition `json:"add"`
	// Snippet is minimal HCL or YAML that applies it to this design (real
	// addresses). Blocks of existing resources hold only what to merge in.
	Snippet string `json:"snippet,omitempty"`
	// Scope is ScopeRecommended or ScopeProduction.
	Scope string `json:"scope"`
	// Local says whether the additions deploy locally: LocalRuns,
	// LocalExportOnly or "local equivalent: …".
	Local string `json:"local"`
	// Skip says when not to add it.
	Skip string `json:"skip,omitempty"`
	// Rules are the Nimbo rule codes that check it once added.
	Rules []string `json:"rules,omitempty"`
	// Docs is the official documentation.
	Docs string `json:"docs"`
}

// Scopes.
const (
	ScopeRecommended = "recommended" // every environment
	ScopeProduction  = "production"  // production and production-like environments
)

// Local values.
const (
	LocalRuns       = "runs locally"
	LocalExportOnly = "export only: not emulated locally"
	localEquivalent = "local equivalent: "
)

// maxRecommendations bounds the list: more is noise.
const maxRecommendations = 8

// Priorities order recommendations by value: security first.
const (
	prioEdge        = 10 // WAF and rate limiting on public entry points
	prioThrottle    = 15 // API throttling
	prioSecrets     = 20
	prioNetwork     = 22 // network isolation inside a cluster
	prioPrivate     = 25 // private placement of data stores
	prioData        = 30 // backups, point-in-time recovery, persistence
	prioAsync       = 40 // dead-letter queues
	prioRuntime     = 45 // limits, health checks, probes
	prioEncryption  = 50
	prioScaling     = 60
	prioAlarms      = 70
	prioLogs        = 75
	prioTracing     = 80
	prioEnvironment = 90
)

// rec is a Recommendation with its ordering priority.
type rec struct {
	Recommendation
	priority int
}

// Recommend returns the companions wf is missing, highest value first; empty
// when nothing applies. The order is deterministic.
func Recommend(wf *model.Workflow, cat *catalog.Catalog) []Recommendation {
	out := []Recommendation{}
	recs, production := candidates(wf, cat)
	for _, r := range recs {
		if r.Scope == ScopeProduction && !production {
			continue
		}
		if len(out) == maxRecommendations {
			break
		}
		out = append(out, r.Recommendation)
	}
	return out
}

// candidates are every recommendation that applies to wf in order, before
// the production filter and the cap, and whether wf looks
// production-intended.
func candidates(wf *model.Workflow, cat *catalog.Catalog) ([]rec, bool) {
	if wf == nil {
		return nil, false
	}
	var (
		recs       []rec
		production bool
	)
	switch {
	case wf.IsDocker():
		recs, production = dockerRecs(wf)
	case wf.IsKubernetes():
		recs, production = kubeRecs(wf)
	default:
		recs, production = terraformRecs(wf, cat)
	}
	for i := range recs {
		if recs[i].Add == nil {
			recs[i].Add = []Addition{}
		}
	}
	// Value first; within a value, what applies to every environment before
	// production-only items.
	scope := func(r rec) int {
		if r.Scope == ScopeProduction {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(recs, func(a, b rec) int {
		return cmp.Or(cmp.Compare(a.priority, b.priority), cmp.Compare(scope(a), scope(b)), cmp.Compare(a.ID, b.ID))
	})
	return recs, production
}

// prodWord matches production intent in names, environments and tags.
var prodWord = regexp.MustCompile(`(?i)(^|[^a-z0-9])(prod|production|prd)([^a-z0-9]|$)`)

// prodLike reports production intent in the workflow's name, description or
// any of names.
func prodLike(wf *model.Workflow, names ...string) bool {
	for _, s := range append([]string{wf.Name, wf.Description}, names...) {
		if prodWord.MatchString(s) {
			return true
		}
	}
	return false
}

// secretKey matches environment variable and data keys that hold
// credentials.
var secretKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|private_?key|credentials?)`)

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// joinLines joins snippet parts with a blank line between them.
func joinLines(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
	}
	if b.Len() == 0 {
		return ""
	}
	b.WriteString("\n")
	return b.String()
}
