package compose

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The desktop app renders the catalog: its icon registry and palette
// category labels live there.
const (
	techIconsDir = "../../../apps/desktop/src/components/tech-icons"
	paletteFile  = "../../../apps/desktop/src/features/docker/palette.tsx"
)

// techIconCategories reads the icon registry (ICONS in tech-icons/index.ts): id → category.
func techIconCategories(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(techIconsDir, "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "const ICONS = {")
	end := strings.Index(body, "} as const satisfies")
	if start < 0 || end < start {
		t.Fatal("ICONS registry not found in tech-icons/index.ts")
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*"?([\w-]+)"?: \["[^"]*", "(\w+)"`).FindAllStringSubmatch(body[start:end], -1) {
		out[m[1]] = m[2]
	}
	return out
}

// paletteCategories reads the keys of CATEGORY_LABEL in the Docker palette.
func paletteCategories(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(paletteFile)
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "CATEGORY_LABEL")
	end := strings.Index(body[start:], "\n}")
	if start < 0 || end < 0 {
		t.Fatal("CATEGORY_LABEL not found in palette.tsx")
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+): "`).FindAllStringSubmatch(body[start:start+end], -1) {
		out[m[1]] = true
	}
	return out
}

var placeholderRe = regexp.MustCompile(`\{\{([^}]*)\}\}`)

func TestImageCatalogInvariants(t *testing.T) {
	icons := techIconCategories(t)
	labels := paletteCategories(t)
	known := map[string]bool{}
	for _, c := range ImageCategories {
		known[c] = true
		if !labels[c] {
			t.Errorf("category %q has no label in the palette", c)
		}
	}
	for c := range labels {
		if !known[c] {
			t.Errorf("palette labels unknown category %q", c)
		}
	}
	if n := len(Images()); n < 150 {
		t.Errorf("catalog has %d images, want at least 150", n)
	}

	ids := map[string]bool{}
	perCategory := map[string]int{}
	for _, im := range Images() {
		if im.ID == "" || ids[im.ID] {
			t.Errorf("duplicate or empty id %q", im.ID)
		}
		ids[im.ID] = true
		where := im.ID
		perCategory[im.Category]++
		if im.Label == "" || im.Description == "" {
			t.Errorf("%s: label and description are required", where)
		}
		if !known[im.Category] {
			t.Errorf("%s: unknown category %q", where, im.Category)
		}
		if im.Image == "" || strings.ContainsAny(im.Image, ":@ ") {
			t.Errorf("%s: image %q must be a bare repository", where, im.Image)
		}
		if im.Tag == "" || strings.Contains(im.Tag, "latest") || strings.ContainsAny(im.Tag, "@ ") {
			t.Errorf("%s: tag %q must be pinned (not latest)", where, im.Tag)
		}
		if im.Ports == nil {
			t.Errorf("%s: ports must be a list", where)
		}
		seenPorts := map[int]bool{}
		for _, p := range im.Ports {
			if p < 1 || p > 65535 || seenPorts[p] {
				t.Errorf("%s: invalid or duplicate port %d", where, p)
			}
			seenPorts[p] = true
		}
		envKeys := map[string]bool{}
		for _, e := range im.Env {
			if envKeys[e.Key] {
				t.Errorf("%s: duplicate env %s", where, e.Key)
			}
			envKeys[e.Key] = true
			if isSecretEnv(e.Key, e.Value) && !e.Secret {
				t.Errorf("%s: env %s holds a credential but is not flagged secret", where, e.Key)
			}
		}
		volNames := map[string]bool{}
		for _, v := range im.Volumes {
			if !path.IsAbs(v.Target) || path.Clean(v.Target) != v.Target || v.Target == "/" {
				t.Errorf("%s: volume target %q must be a clean absolute path", where, v.Target)
			}
			if v.Name != "" && volNames[v.Name] {
				t.Errorf("%s: duplicate volume name %q", where, v.Name)
			}
			volNames[v.Name] = true
		}
		if h := im.Healthcheck; h != nil {
			if strings.TrimSpace(h.Test) == "" || h.Retries == nil || *h.Retries < 1 {
				t.Errorf("%s: healthcheck needs a test and retries", where)
			}
			for _, d := range []string{h.Interval, h.Timeout, h.StartPeriod} {
				if _, err := time.ParseDuration(d); err != nil {
					t.Errorf("%s: healthcheck duration %q: %v", where, d, err)
				}
			}
			// $VAR is interpolated by compose; the container's shell needs $$VAR.
			if regexp.MustCompile(`(^|[^$])\$[{A-Za-z]`).MatchString(h.Test) {
				t.Errorf("%s: healthcheck %q must escape $ as $$", where, h.Test)
			}
		}
		for _, c := range im.Connect {
			for _, m := range placeholderRe.FindAllStringSubmatch(c.Template, -1) {
				if k, isEnv := strings.CutPrefix(m[1], "env."); isEnv {
					if !envKeys[k] {
						t.Errorf("%s: connect %s uses {{env.%s}}, which the image does not set", where, c.Key, k)
					}
				} else if m[1] != "host" && m[1] != "port" {
					t.Errorf("%s: connect %s has unknown placeholder {{%s}}", where, c.Key, m[1])
				}
			}
		}
		for _, m := range placeholderRe.FindAllStringSubmatch(im.Command, -1) {
			if m[1] != "host" {
				t.Errorf("%s: command placeholder {{%s}} (only {{host}})", where, m[1])
			}
		}
		cat, ok := icons[im.Icon]
		if !ok || cat == "generic" {
			t.Errorf("%s: icon %q is not a technology icon of the registry", where, im.Icon)
		} else if _, err := os.Stat(filepath.Join(techIconsDir, "assets", im.Icon+".svg")); err != nil {
			t.Errorf("%s: icon asset: %v", where, err)
		}
	}
	for _, c := range ImageCategories {
		if perCategory[c] == 0 {
			t.Errorf("category %q has no image", c)
		}
	}
}

func TestImageCatalogSecrets(t *testing.T) {
	for _, tc := range []struct {
		id, key string
	}{
		{"neo4j", "NEO4J_AUTH"},
		{"meilisearch", "MEILI_MASTER_KEY"},
		{"litellm", "LITELLM_MASTER_KEY"},
		{"zitadel", "ZITADEL_MASTERKEY"},
		{"hasura", "HASURA_GRAPHQL_DATABASE_URL"}, // postgres://app:app@…
		{"mongo-express", "ME_CONFIG_MONGODB_URL"},
		{"typesense", "TYPESENSE_API_KEY"},
		{"influxdb", "DOCKER_INFLUXDB_INIT_ADMIN_TOKEN"},
	} {
		im, ok := Image(tc.id)
		if !ok {
			t.Fatalf("no catalog image %s", tc.id)
		}
		found := false
		for _, e := range im.Env {
			if e.Key == tc.key {
				found = true
				if !e.Secret {
					t.Errorf("%s: %s not flagged secret", tc.id, tc.key)
				}
			}
		}
		if !found {
			t.Errorf("%s: no env %s", tc.id, tc.key)
		}
	}
	// Plain settings stay visible.
	if im, _ := Image("postgres"); im.Env[0].Secret || im.Env[2].Secret {
		t.Errorf("postgres user/db flagged secret: %+v", im.Env)
	}
	if isSecretEnv("REDIS_URL", "redis://redis:6379") || isSecretEnv("URL", "http://localhost:3000") {
		t.Error("URLs without a password flagged secret")
	}
}

// The palette is fetched once per session; keep it small.
func TestImageCatalogPayload(t *testing.T) {
	b, err := json.Marshal(Images())
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 128<<10 {
		t.Errorf("GET /catalog/docker/images is %d bytes, keep it under 128 KiB", len(b))
	}
}

func TestImageForCatalogRepositories(t *testing.T) {
	for ref, want := range map[string]string{
		"getmeili/meilisearch:v1.40.0":                      "meilisearch",
		"docker.io/library/zookeeper:3.8":                   "zookeeper",
		"cr.weaviate.io/semitechnologies/weaviate:1.30.0":   "weaviate",
		"ghcr.io/goauthentik/server:2025.10.1":              "authentik",
		"quay.io/oauth2-proxy/oauth2-proxy:v7.12.0":         "oauth2-proxy",
		"docker.dragonflydb.io/dragonflydb/dragonfly:v1.30": "dragonfly",
	} {
		if im, ok := ImageFor(ref); !ok || im.ID != want {
			t.Errorf("ImageFor(%s) = %s, %v; want %s", ref, im.ID, ok, want)
		}
	}
}
