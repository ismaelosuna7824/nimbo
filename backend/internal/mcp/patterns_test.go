package mcp

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/patterns"
)

func TestArchitecturePatterns(t *testing.T) {
	cs, _ := session(t)

	// Ranked matches for a requirement, one platform's block each.
	var m patternMatchesView
	mustTool(t, cs, "architecture_patterns", map[string]any{"requirement": "orders API with spiky traffic that notifies billing and shipping", "platform": "k8s"}, &m)
	want := patterns.Match("orders API with spiky traffic that notifies billing and shipping", "kubernetes", 6)
	if len(m.Matches) == 0 || len(m.Matches) != len(want) || !strings.Contains(m.Next, "did not add") {
		t.Fatalf("matches = %+v, want %d", m, len(want))
	}
	for i, v := range m.Matches {
		if v.ID != want[i].ID || v.Problem == "" || len(v.UseWhen) == 0 || len(v.AvoidWhen) == 0 {
			t.Errorf("match %d = %+v, want %s", i, v, want[i].ID)
		}
		for _, b := range v.Blocks {
			if b.Platform != "kubernetes" {
				t.Errorf("%s: block for %s", v.ID, b.Platform)
			}
		}
	}

	// Catalog without a requirement.
	var c struct {
		Catalog []struct {
			ID string `json:"id"`
		} `json:"catalog"`
	}
	mustTool(t, cs, "architecture_patterns", map[string]any{}, &c)
	if len(c.Catalog) != len(patterns.All()) {
		t.Fatalf("catalog = %d, want %d", len(c.Catalog), len(patterns.All()))
	}

	// One pattern in full, narrowed to a platform.
	var p patternView
	mustTool(t, cs, "architecture_patterns", map[string]any{"pattern": "queue-based-load-leveling", "platform": "aws"}, &p)
	if p.ID != "queue-based-load-leveling" || len(p.Tradeoffs) == 0 || len(p.Practices) == 0 || len(p.Sources) == 0 || p.Next == "" {
		t.Fatalf("pattern = %+v", p)
	}
	for _, b := range p.Blocks {
		if b.Platform != "aws" {
			t.Errorf("block for %s", b.Platform)
		}
	}

	for _, args := range []map[string]any{{"pattern": "big-ball-of-mud"}, {"requirement": "an API", "platform": "heroku"}} {
		if text, isErr := callTool(t, cs, "architecture_patterns", args); !isErr {
			t.Errorf("%v: no error: %s", args, text)
		}
	}
}
