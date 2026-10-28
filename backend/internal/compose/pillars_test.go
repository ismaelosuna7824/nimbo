package compose

import (
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Every dk_* rule names at least one Well-Architected pillar, without
// duplicates, and the pillar table names no unknown rule.
func TestRulePillars(t *testing.T) {
	for _, r := range Rules() {
		if len(r.Pillars) == 0 {
			t.Errorf("%s has no pillar", r.Code)
		}
		for i, p := range r.Pillars {
			if !slices.Contains(model.Pillars, p) || slices.Contains(r.Pillars[:i], p) {
				t.Errorf("%s: bad or repeated pillar %q", r.Code, p)
			}
		}
	}
	for code := range rulePillars {
		if _, ok := ruleByCode[code]; !ok {
			t.Errorf("rulePillars names unknown rule %s", code)
		}
	}
}
