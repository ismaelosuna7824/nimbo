package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/patterns"
)

func TestPatternsCatalog(t *testing.T) {
	e := newEnv(t)
	var all patternList
	e.mustCall(http.StatusOK, "GET", "/patterns", nil, &all)
	if len(all.Catalog) != len(patterns.All()) || len(all.Matches) != 0 {
		t.Fatalf("catalog = %d entries, want %d", len(all.Catalog), len(patterns.All()))
	}
	for _, s := range all.Catalog {
		if s.ID == "" || s.Name == "" || (s.Kind != "style" && s.Kind != "pattern") || s.Problem == "" {
			t.Errorf("summary %+v", s)
		}
	}

	var k8s patternList
	e.mustCall(http.StatusOK, "GET", "/patterns?platform=kubernetes", nil, &k8s)
	for _, s := range k8s.Catalog {
		if !slices.Contains(s.Platforms, "kubernetes") {
			t.Errorf("%s has no kubernetes block", s.ID)
		}
	}

	var m patternList
	e.mustCall(http.StatusOK, "GET", "/patterns?requirement=orders+api+that+publishes+events+to+several+consumers&platform=aws&limit=3", nil, &m)
	if len(m.Matches) == 0 || len(m.Matches) > 3 || m.Platform != "aws" {
		t.Fatalf("matches = %+v", m)
	}
	want := patterns.Match("orders api that publishes events to several consumers", "aws", 3)
	for i := range want {
		if m.Matches[i].ID != want[i].ID {
			t.Errorf("match %d = %s, want %s", i, m.Matches[i].ID, want[i].ID)
		}
	}

	e.mustCall(http.StatusBadRequest, "GET", "/patterns?platform=heroku", nil, nil)
	e.mustCall(http.StatusBadRequest, "GET", "/patterns?requirement=x&limit=0", nil, nil)

	var p patterns.Pattern
	e.mustCall(http.StatusOK, "GET", "/patterns/cqrs", nil, &p)
	if p.ID != "cqrs" || len(p.Blocks) == 0 || len(p.Sources) == 0 {
		t.Fatalf("cqrs = %+v", p)
	}
	e.mustCall(http.StatusNotFound, "GET", "/patterns/big-ball-of-mud", nil, nil)
}
