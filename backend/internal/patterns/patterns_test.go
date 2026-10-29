package patterns

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kubecatalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var fixedIDs = []string{
	"n-tier", "web-queue-worker", "microservices", "event-driven", "serverless", "modular-monolith", "big-data-streaming",
	"api-gateway", "backends-for-frontends", "gateway-offloading", "cache-aside", "cqrs", "event-sourcing", "saga",
	"transactional-outbox", "publisher-subscriber", "fan-out", "competing-consumers", "queue-based-load-leveling",
	"async-request-reply", "claim-check", "priority-queue", "pipes-and-filters", "circuit-breaker", "retry", "bulkhead",
	"rate-limiting", "health-endpoint-monitoring", "strangler-fig", "anti-corruption-layer", "sidecar",
	"database-per-service", "materialized-view", "sharding", "static-content-hosting", "valet-key",
	"scheduler-agent-supervisor", "leader-election", "deployment-stamps", "federated-identity", "external-configuration-store",
}

var styleIDs = []string{"n-tier", "web-queue-worker", "microservices", "event-driven", "serverless", "modular-monolith", "big-data-streaming"}

func TestIDsExactlyOnce(t *testing.T) {
	seen := map[string]int{}
	for _, p := range All() {
		seen[p.ID]++
	}
	for _, id := range fixedIDs {
		if seen[id] != 1 {
			t.Errorf("%s present %d times", id, seen[id])
		}
		if _, ok := Get(id); !ok {
			t.Errorf("Get(%q) failed", id)
		}
	}
	if len(seen) != len(fixedIDs) {
		t.Errorf("catalog has %d patterns, want %d", len(seen), len(fixedIDs))
	}
	if _, ok := Get("nope"); ok {
		t.Error("Get(nope) succeeded")
	}
}

func TestRequiredFields(t *testing.T) {
	platforms := []string{AWS, Azure, GCP, OCI, Docker, Kubernetes}
	for _, p := range All() {
		wantKind := KindPattern
		if slices.Contains(styleIDs, p.ID) {
			wantKind = KindStyle
		}
		if p.Kind != wantKind {
			t.Errorf("%s: kind %q, want %q", p.ID, p.Kind, wantKind)
		}
		if p.Name == "" || p.Problem == "" || len(p.UseWhen) == 0 || len(p.AvoidWhen) == 0 ||
			len(p.Tradeoffs) == 0 || len(p.Practices) == 0 || len(p.Keywords) == 0 || len(p.Sources) == 0 || len(p.Related) == 0 {
			t.Errorf("%s: missing required fields", p.ID)
		}
		seen := map[string]bool{}
		for _, b := range p.Blocks {
			if !slices.Contains(platforms, b.Platform) || seen[b.Platform] {
				t.Errorf("%s: bad or duplicate platform %q", p.ID, b.Platform)
			}
			seen[b.Platform] = true
			if len(b.Types) == 0 || b.How == "" || b.Local == "" {
				t.Errorf("%s/%s: empty block fields", p.ID, b.Platform)
			}
			if b.Local != LocalRuns && !strings.HasPrefix(b.Local, "export only: ") && !strings.HasPrefix(b.Local, "local equivalent: ") {
				t.Errorf("%s/%s: bad Local %q", p.ID, b.Platform, b.Local)
			}
		}
		for _, pl := range platforms {
			if !seen[pl] {
				t.Errorf("%s: no %s block", p.ID, pl)
			}
		}
		for _, s := range p.Sources {
			if s.Title == "" || !strings.HasPrefix(s.URL, "https://") {
				t.Errorf("%s: bad source %+v", p.ID, s)
			}
		}
	}
}

func TestRelatedExist(t *testing.T) {
	for _, p := range All() {
		for _, r := range p.Related {
			if _, ok := Get(r); !ok || r == p.ID {
				t.Errorf("%s: related %q invalid", p.ID, r)
			}
		}
	}
}

func TestTypesExist(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, it := range kubecatalog.Items() {
		for _, k := range it.Kinds {
			kinds[k] = true
		}
	}
	used := map[string]bool{}
	for _, p := range All() {
		for _, b := range p.Blocks {
			for _, typ := range b.Types {
				switch b.Platform {
				case Docker:
					if _, ok := compose.Image(typ); !ok {
						t.Errorf("%s: unknown compose image %q", p.ID, typ)
					}
				case Kubernetes:
					if !kinds[typ] {
						t.Errorf("%s: unknown kubernetes kind %q", p.ID, typ)
					}
				default:
					s, ok := cat.Schema(model.KindResource, typ)
					if !ok {
						t.Errorf("%s: unknown terraform type %q", p.ID, typ)
						continue
					}
					if want := p.ID + "/" + b.Platform; !strings.HasPrefix(typ, prefix(b.Platform)) {
						t.Errorf("%s: %q is not a %s type", want, typ, b.Platform)
					}
					used[typ] = true
					if notEmulated[typ] == s.FlociSupported {
						t.Errorf("notEmulated[%q] = %v but FlociSupported = %v", typ, notEmulated[typ], s.FlociSupported)
					}
				}
			}
		}
	}
	for typ := range notEmulated {
		if !used[typ] {
			t.Errorf("notEmulated lists unused type %q", typ)
		}
	}
}

func prefix(platform string) string {
	return map[string]string{AWS: "aws_", Azure: "azurerm_", GCP: "google_", OCI: "oci_"}[platform]
}

func TestTemplatesExist(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, list := range [][]model.Template{cat.Templates(), compose.Templates(), kubecatalog.Templates()} {
		for _, tm := range list {
			ids[tm.ID] = true
		}
	}
	for _, p := range All() {
		for _, id := range p.Templates {
			if !ids[id] {
				t.Errorf("%s: unknown template %q", p.ID, id)
			}
		}
	}
}

func ids(ps []Pattern) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func TestMatch(t *testing.T) {
	cases := []struct {
		req      string
		want     []string // must appear
		wantNot  []string // must not appear
		platform string
	}{
		{req: "A simple CRUD API for a todo list", want: []string{"n-tier", "serverless", "api-gateway", "cache-aside"}, wantNot: []string{"microservices", "event-sourcing", "cqrs", "saga", "event-driven"}},
		{req: "Una API CRUD sencilla de pedidos", want: []string{"n-tier", "serverless", "api-gateway", "cache-aside"}, wantNot: []string{"microservices", "event-sourcing", "saga"}},
		{req: "Plataforma de microservicios para pagos y pedidos con varios equipos", want: []string{"microservices", "saga"}},
		{req: "Necesitamos auditoría e historial completo de cada movimiento de la cuenta", want: []string{"event-sourcing"}},
		{req: "transacción distribuida entre inventario y pagos", want: []string{"saga"}},
		{req: "Los usuarios van a subir archivos grandes", want: []string{"valet-key", "claim-check"}},
		{req: "Queremos migrar el monolito legado poco a poco", want: []string{"strangler-fig"}},
		{req: "procesar eventos en tiempo real de sensores IoT", want: []string{"event-driven", "big-data-streaming"}},
		{req: "alto tráfico en el catálogo con picos en Black Friday", want: []string{"queue-based-load-leveling", "cache-aside"}},
		{req: "worker que procesa una cola de trabajos en segundo plano", want: []string{"web-queue-worker", "competing-consumers"}},
		{req: "notify several services when an order is placed", want: []string{"publisher-subscriber"}},
		{req: "simple microservices demo", want: []string{"microservices"}},
		{req: "cola de mensajes", want: []string{"competing-consumers"}, platform: Docker},
	}
	for _, c := range cases {
		got := ids(Match(c.req, c.platform, 0))
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("Match(%q) = %v, missing %s", c.req, got, w)
			}
		}
		for _, w := range c.wantNot {
			if slices.Contains(got, w) {
				t.Errorf("Match(%q) = %v, must not include %s", c.req, got, w)
			}
		}
	}
}

func TestMatchSimpleCRUDTop(t *testing.T) {
	got := ids(Match("simple CRUD REST API", "aws", 4))
	want := []string{"n-tier", "serverless", "api-gateway", "cache-aside"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMatchOrderAndLimit(t *testing.T) {
	got := Match("microservicios con eventos, colas, cache y api gateway para pedidos", "", 5)
	if len(got) != 5 {
		t.Fatalf("len %d, want 5", len(got))
	}
	seenPattern := false
	for _, p := range got {
		if p.Kind == KindPattern {
			seenPattern = true
		} else if seenPattern {
			t.Errorf("style %s after a pattern: %v", p.ID, ids(got))
		}
	}
	if len(Match("", "", 0)) != 0 {
		t.Error("empty requirement matched")
	}
	if len(Match("microservices", "nope", 0)) != 0 {
		t.Error("unknown platform matched")
	}
}

func TestMatchDeterministic(t *testing.T) {
	req := "event-driven orders with saga, outbox and pub/sub fan-out on kubernetes"
	first := ids(Match(req, "", 0))
	for range 20 {
		if got := ids(Match(req, "", 0)); !reflect.DeepEqual(got, first) {
			t.Fatalf("nondeterministic: %v vs %v", got, first)
		}
	}
}
