package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/patterns"
)

// Architecture patterns: the catalog of architecture styles and cloud
// design patterns (patterns package) mapped to each platform.

// patternPlatforms are the platforms a pattern block can target.
var patternPlatforms = []string{"aws", "azure", "gcp", "oci", "docker", "kubernetes"}

// Default and maximum number of patterns a requirement match returns.
const (
	defaultPatternLimit = 6
	maxPatternLimit     = 20
)

// patternSummary is a catalog entry in GET /patterns without a requirement.
type patternSummary struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Problem   string   `json:"problem"`
	Platforms []string `json:"platforms"`
}

// patternList is GET /patterns: the ranked matches of a requirement
// (full patterns) or, without one, a summary of the whole catalog.
type patternList struct {
	Requirement string             `json:"requirement,omitempty"`
	Platform    string             `json:"platform,omitempty"`
	Matches     []patterns.Pattern `json:"matches,omitempty"`
	Catalog     []patternSummary   `json:"catalog,omitempty"`
}

// listPatterns is GET /patterns?requirement=&platform=&limit=.
func (s *Server) listPatterns(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	requirement := strings.TrimSpace(q.Get("requirement"))
	platform := strings.ToLower(strings.TrimSpace(q.Get("platform")))
	if platform != "" && !slices.Contains(patternPlatforms, platform) {
		return errorf(http.StatusBadRequest, "unknown platform %q (one of %s)", platform, strings.Join(patternPlatforms, ", "))
	}
	limit := defaultPatternLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPatternLimit {
			return errorf(http.StatusBadRequest, "limit must be 1-%d", maxPatternLimit)
		}
		limit = n
	}
	out := patternList{Requirement: requirement, Platform: platform}
	if requirement != "" {
		out.Matches = patterns.Match(requirement, platform, limit)
		if out.Matches == nil {
			out.Matches = []patterns.Pattern{}
		}
		return ok(w, out)
	}
	out.Catalog = []patternSummary{}
	for _, p := range patterns.All() {
		sum := patternSummary{ID: p.ID, Name: p.Name, Kind: p.Kind, Problem: p.Problem, Platforms: []string{}}
		for _, b := range p.Blocks {
			sum.Platforms = append(sum.Platforms, b.Platform)
		}
		if platform != "" && !slices.Contains(sum.Platforms, platform) {
			continue
		}
		out.Catalog = append(out.Catalog, sum)
	}
	return ok(w, out)
}

// getPattern is GET /patterns/{id}.
func (s *Server) getPattern(w http.ResponseWriter, r *http.Request) error {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	p, found := patterns.Get(id)
	if !found {
		return errorf(http.StatusNotFound, "unknown pattern %q (GET /patterns lists them)", id)
	}
	return ok(w, p)
}
