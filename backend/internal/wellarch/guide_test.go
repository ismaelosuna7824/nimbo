package wellarch

import (
	"net/url"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// netcheckCodes are the finding codes emitted by internal/netcheck, which has
// no rule metadata table to import.
var netcheckCodes = []string{
	"net_nacl_ephemeral", "net_nat_no_igw", "net_no_egress", "net_peering_overlap",
	"net_private_subnet_igw", "net_public_subnet_no_igw", "net_route_cross_vpc",
	"net_route_table_unassociated", "net_rule_priority_duplicate", "net_sg_cross_vpc",
	"net_single_az", "net_subnet_outside_network", "net_subnet_overlap",
}

func knownCodes() map[string]bool {
	known := map[string]bool{}
	for _, set := range [][]model.BestPracticeRule{lint.Rules(), compose.Rules(), kuberules.Rules()} {
		for _, r := range set {
			known[r.Code] = true
		}
	}
	for _, c := range netcheckCodes {
		known[c] = true
	}
	return known
}

func TestGuides(t *testing.T) {
	known := knownCodes()
	if len(Platforms()) != 6 {
		t.Fatalf("platforms = %v", Platforms())
	}
	for _, name := range Platforms() {
		g, ok := Get(name)
		if !ok {
			t.Fatalf("%s: missing guide", name)
		}
		if g.Platform != name || g.Name == "" || g.Framework == "" {
			t.Errorf("%s: bad header %+v", name, g)
		}
		if len(g.Sources) == 0 {
			t.Errorf("%s: no sources", name)
		}
		for _, s := range g.Sources {
			u, err := url.Parse(s.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" || s.Title == "" {
				t.Errorf("%s: bad source %+v", name, s)
			}
		}
		if len(g.Pillars) != len(model.Pillars) {
			t.Fatalf("%s: %d pillars", name, len(g.Pillars))
		}
		total := 0
		for i, pg := range g.Pillars {
			if pg.Pillar != model.Pillars[i] {
				t.Errorf("%s: pillar %d = %s, want %s", name, i, pg.Pillar, model.Pillars[i])
			}
			if pg.Summary == "" {
				t.Errorf("%s/%s: empty summary", name, pg.Pillar)
			}
			if n := len(pg.Practices); n < 4 || n > 8 {
				t.Errorf("%s/%s: %d practices", name, pg.Pillar, n)
			}
			for _, pr := range pg.Practices {
				total++
				if pr.Title == "" || pr.Why == "" || len(pr.How) == 0 {
					t.Errorf("%s/%s: incomplete practice %q", name, pg.Pillar, pr.Title)
				}
				for _, h := range pr.How {
					if h == "" {
						t.Errorf("%s/%s: empty how in %q", name, pg.Pillar, pr.Title)
					}
				}
				for _, c := range pr.Rules {
					if !known[c] {
						t.Errorf("%s/%s: unknown rule code %q in %q", name, pg.Pillar, c, pr.Title)
					}
				}
			}
		}
		t.Logf("%s: %d practices", name, total)
	}
	if _, ok := Get("nope"); ok {
		t.Error("unknown platform returned a guide")
	}
}
