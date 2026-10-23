package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func open(t *testing.T, files map[string]string, compose ...string) *Project {
	t.Helper()
	if len(compose) == 0 {
		compose = []string{"compose.yaml"}
	}
	return Open(context.Background(), writeTree(t, files), compose, nil, Options{})
}

func codes(p *Project) []string {
	var out []string
	for _, d := range p.Diagnostics {
		out = append(out, string(d.Severity)+":"+d.Code)
	}
	return out
}

func edit(t *testing.T, p *Project, fn func(e *Editor) error) map[string]string {
	t.Helper()
	e, err := p.Edit()
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(e); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range e.Files() {
		out[k] = string(v)
	}
	return out
}

func TestTemplatesAreClean(t *testing.T) {
	for _, tmpl := range Templates() {
		files, ok := TemplateFiles(tmpl.ID)
		if !ok || files["compose.yaml"] == nil {
			t.Fatalf("%s: no compose.yaml", tmpl.ID)
		}
		tree := map[string]string{}
		for k, v := range files {
			tree[k] = string(v)
		}
		p := open(t, tree)
		if p.Model == nil {
			t.Fatalf("%s: %v", tmpl.ID, p.Diagnostics)
		}
		// Templates follow the opt-in practices too (limits, cache mounts);
		// only the hardening opt-in is left to the user (databases need
		// capabilities and writable paths).
		all := model.BestPracticeSettings{EnabledRules: []string{"dk_no_limits", "dk_cache_mount"}}
		for _, d := range FilterSettings(p.Diagnostics, all) {
			t.Errorf("%s: %s %s:%d %s", tmpl.ID, d.Code, d.File, d.Line, d.Message)
		}
		if len(p.Unresolved) > 0 {
			t.Errorf("%s: unresolved %v", tmpl.ID, p.Unresolved)
		}
	}
	if _, ok := tmplFile("docker-go-nginx", "api/go.mod"); !ok {
		t.Error("go.mod.nimbo is not renamed")
	}
}

func tmplFile(id, rel string) ([]byte, bool) {
	files, _ := TemplateFiles(id)
	b, ok := files[rel]
	return b, ok
}

func TestGraphOfMultiStageAndInference(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `x-env: &env
  LOG_LEVEL: info
services:
  api:
    build:
      context: ./api
      target: runtime
      args:
        GO_VERSION: "1.23"
    environment:
      <<: *env
      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app
    ports:
      - "8080:8080"
    networks: [back]
  db:
    image: postgres:16
    profiles: [db]
    volumes:
      - data:/var/lib/postgresql/data:ro
    networks: [back]
networks:
  back: {}
volumes:
  data:
`,
		".env": "DB_PASSWORD=secret\n",
		"api/Dockerfile": `ARG GO_VERSION=1.22
FROM golang:${GO_VERSION}-alpine AS build
RUN <<EOF
go version
EOF
FROM build AS test
RUN go test ./...
FROM alpine:3.20 AS runtime
COPY --from=build /out/app /app
EXPOSE 8080
CMD ["/app"]
`,
		"api/go.mod": "module x\n\nrequire github.com/gin-gonic/gin v1.10.0\n",
	})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	g := p.Graph(nil)
	byID := map[string]model.ComposeNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	api := byID["service:api"]
	if api.Service == nil || !slices.Equal(api.Service.BaseImages, []string{"golang:1.23-alpine", "alpine:3.20"}) {
		t.Fatalf("base images: %+v", api.Service)
	}
	if !slices.Equal(api.Service.Stack, []string{"go", "gin"}) {
		t.Errorf("stack %v", api.Service.Stack)
	}
	if _, ok := byID["stage:api:test"]; ok {
		t.Error("stage test is not built for target runtime")
	}
	if byID["stage:api:runtime"].Stage == nil || !byID["stage:api:runtime"].Stage.Final {
		t.Error("runtime stage is final")
	}
	if !byID["service:db"].Inactive {
		t.Error("db is in an inactive profile")
	}
	var kinds []string
	for _, e := range g.Edges {
		kinds = append(kinds, e.Kind+":"+e.Source+">"+e.Target)
	}
	for _, want := range []string{
		"copy_from:stage:api:build>stage:api:runtime", "build:stage:api:runtime>service:api",
		"inferred:service:db>service:api", "network:network:back>service:api", "volume:volume:data>service:db",
	} {
		if !slices.Contains(kinds, want) {
			t.Errorf("missing edge %s in %v", want, kinds)
		}
	}
	for _, e := range api.Service.Environment {
		if e.Key == "DATABASE_URL" && (e.Value == nil || !strings.Contains(*e.Value, "secret")) {
			t.Errorf("DATABASE_URL not resolved: %+v", e)
		}
	}
	if !slices.Contains(codes(p), "info:dk_root_user") {
		t.Errorf("root user not reported: %v", codes(p))
	}
}

func TestChecks(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  web:
    image: nginx
    ports: ["8080:80"]
    depends_on:
      api:
        condition: service_healthy
      ghost:
        condition: service_started
  api:
    build: ./missing
    ports: ["8080:8080"]
    environment:
      API_TOKEN: abc123
      PORT: ${PORT}
`,
	})
	got := codes(p)
	for _, want := range []string{"error:dk_unknown_ref", "error:dk_port_clash", "error:dk_missing_context", "info:dk_latest_tag", "warning:dk_no_healthcheck", "warning:dk_secret_env", "warning:dk_unresolved_var"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, d := range p.Diagnostics {
		if d.Line == 0 || d.File == "" {
			t.Errorf("%s has no location", d.Code)
		}
		if d.Detail == "" || d.Fix == "" {
			t.Errorf("%s has no why/fix", d.Code)
		}
		// The referencing entry ("ghost:"), not the first key of depends_on.
		if d.Code == "dk_unknown_ref" && d.Line != 8 {
			t.Errorf("dk_unknown_ref on line %d, want 8", d.Line)
		}
	}
}

func TestSyntaxAndSchemaErrorsHaveLines(t *testing.T) {
	p := open(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx:1\n   ports: [1]\n"})
	if len(p.Diagnostics) == 0 || p.Diagnostics[0].Code != "dk_yaml" || p.Diagnostics[0].Line == 0 {
		t.Fatalf("%+v", p.Diagnostics)
	}
	p = open(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx:1\n    restart: always\n    portz: [80]\n"})
	if len(p.Diagnostics) == 0 || p.Diagnostics[0].Code != "dk_schema" || p.Diagnostics[0].Line != 5 {
		t.Fatalf("%+v", p.Diagnostics)
	}
}

func TestOverridesIncludeExtends(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `include:
  - infra/compose.yaml
services:
  web:
    extends:
      file: common.yaml
      service: base
    image: nginx:1.27
`,
		"compose.override.yaml": "services:\n  web:\n    ports:\n      - \"9090:80\"\n",
		"common.yaml":           "services:\n  base:\n    restart: always\n    environment:\n      MODE: prod\n",
		"infra/compose.yaml":    "services:\n  cache:\n    image: redis:7\n",
	}, "compose.yaml", "compose.override.yaml")
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	web := p.Model.Services["web"]
	if web.Restart != "always" || len(web.Ports) != 1 {
		t.Errorf("merge: %+v", web)
	}
	if _, ok := p.Model.Services["cache"]; !ok {
		t.Error("included service missing")
	}
	if f, l := p.ServiceLine("cache"); f != "infra/compose.yaml" || l != 2 {
		t.Errorf("cache at %s:%d", f, l)
	}
	g := p.Graph(nil)
	if !slices.ContainsFunc(g.Edges, func(e model.ComposeEdge) bool { return e.Kind == "extends" && e.Source == "extends:common.yaml#base" }) {
		t.Errorf("no extends edge: %+v", g.Edges)
	}
	// Ports are edited in the override, where they are written.
	out := edit(t, p, func(e *Editor) error {
		raw, _ := json.Marshal([]model.ServicePort{{Host: "9090", Container: "80"}, {Host: "9443", Container: "443"}})
		return e.PatchService("web", map[string]json.RawMessage{"ports": raw})
	})
	if want := "services:\n  web:\n    ports:\n      - \"9090:80\"\n      - \"9443:443\"\n"; out["compose.override.yaml"] != want {
		t.Errorf("override:\n%s", out["compose.override.yaml"])
	}
}

const golden = `# Demo project
services:
  # the web front
  web:
    image: "nginx:1.25"   # pinned
    ports:
      - '8080:80'
    environment:
      - MODE=prod
      - TOKEN=${TOKEN}
    depends_on: [api]

  api:
    build: ./api
    environment:
      DB: postgres://db/app
    networks:
      - back

networks:
  back:

volumes: {}
`

func TestEditsKeepFormatting(t *testing.T) {
	files := map[string]string{"compose.yaml": golden, ".env": "TOKEN=x\n", "api/Dockerfile": "FROM alpine:3.20\nUSER nobody\n"}
	cases := []struct {
		name string
		fn   func(e *Editor) error
		want string
	}{
		{"image keeps quotes and comment", func(e *Editor) error {
			return e.PatchService("web", map[string]json.RawMessage{"image": json.RawMessage(`"nginx:1.27"`)})
		}, strings.Replace(golden, `"nginx:1.25"`, `"nginx:1.27"`, 1)},
		{"env add in list form keeps ${}", func(e *Editor) error {
			return e.PatchService("web", map[string]json.RawMessage{"environment": json.RawMessage(`[{"key":"MODE","value":"prod"},{"key":"TOKEN","secret":true},{"key":"NEW","value":"1"}]`)})
		}, strings.Replace(golden, "      - TOKEN=${TOKEN}\n", "      - TOKEN=${TOKEN}\n      - NEW=1\n", 1)},
		{"rename updates flow depends_on", func(e *Editor) error { return e.RenameService("api", "backend") },
			strings.Replace(strings.Replace(golden, "[api]", "[backend]", 1), "  api:\n", "  backend:\n", 1)},
		{"remove service drops refs and comments", func(e *Editor) error { return e.RemoveService("web") },
			"# Demo project\nservices:\n  api:\n    build: ./api\n    environment:\n      DB: postgres://db/app\n    networks:\n      - back\n\nnetworks:\n  back:\n\nvolumes: {}\n"},
		{"add volume into {} and mount it", func(e *Editor) error {
			return e.Connect(ConnectRequest{From: "api", To: "volume:cache", Kind: "volume", Target: "/cache"})
		}, strings.Replace(strings.Replace(golden, "volumes: {}\n", "volumes:\n  cache: {}\n", 1), "    networks:\n      - back\n", "    networks:\n      - back\n    volumes:\n      - cache:/cache\n", 1)},
		{"depends_on condition turns list into mapping", func(e *Editor) error {
			return e.Connect(ConnectRequest{From: "web", To: "api", Kind: "depends_on", Condition: "service_healthy"})
		}, strings.Replace(golden, "    depends_on: [api]\n", "    depends_on:\n      api:\n        condition: service_healthy\n", 1)},
		{"network rename", func(e *Editor) error {
			return e.RenameTop("network", "back", "backend")
		}, strings.Replace(strings.Replace(golden, "      - back\n", "      - backend\n", 1), "  back:\n", "  backend:\n", 1)},
		{"add service keeps the blank line between services", func(e *Editor) error {
			return e.AddService(ServiceSpec{Name: "cache", Image: "redis:7-alpine", Ports: []model.ServicePort{{Host: "6379", Container: "6379"}}})
		}, strings.Replace(golden, "      - back\n\nnetworks:", "      - back\n\n  cache:\n    image: redis:7-alpine\n    ports:\n      - \"6379:6379\"\n\nnetworks:", 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := open(t, files)
			got := edit(t, p, c.fn)["compose.yaml"]
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
			if q := Open(context.Background(), p.Dir, p.Files, nil, Options{Content: map[string][]byte{"compose.yaml": []byte(got)}}); q.Model == nil {
				t.Errorf("result does not load: %v", q.Diagnostics)
			}
		})
	}
}

// The graph shows a healthcheck test interpolated ("$${USER}" reads "${USER}"); the inspector sends it back with the
// field it changed, which must not turn the container's variable into a compose one.
func TestHealthcheckPatchKeepsEscapedTest(t *testing.T) {
	src := "services:\n  db:\n    image: postgres:16\n    healthcheck:\n      test: [\"CMD-SHELL\", \"pg_isready -U $${POSTGRES_USER}\"]\n      interval: 5s\n"
	p := open(t, map[string]string{"compose.yaml": src})
	got := edit(t, p, func(e *Editor) error {
		return e.PatchService("db", map[string]json.RawMessage{"healthcheck": json.RawMessage(`{"test":"pg_isready -U ${POSTGRES_USER}","interval":"5s","retries":3}`)})
	})["compose.yaml"]
	if want := strings.Replace(src, "interval: 5s\n", "interval: 5s\n      retries: 3\n", 1); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The graph shows environment values interpolated (a URL with the .env password in it); the inspector shows and
// sends back the value as written, so neither an unchanged nor an edited entry copies the password into the file.
func TestEnvironmentRawRoundTrip(t *testing.T) {
	src := "services:\n  api:\n    image: node:22\n    environment:\n      URL: postgres://app:${PASS}@db/app # from .env\n      MODE: dev\n"
	files := map[string]string{"compose.yaml": src, ".env": "PASS=s3cret\n"}
	p := open(t, files)
	var env []model.ServiceEnv
	for _, n := range p.Graph(nil).Nodes {
		if n.ID == "service:api" {
			env = n.Service.Environment
		}
	}
	if len(env) != 2 || env[1].Key != "URL" || env[1].Raw == nil || *env[1].Raw != "postgres://app:${PASS}@db/app" || *env[1].Value != "postgres://app:s3cret@db/app" || env[0].Raw != nil {
		t.Fatalf("environment facts = %+v", env)
	}
	unchanged := edit(t, p, func(e *Editor) error {
		return e.PatchService("api", map[string]json.RawMessage{"environment": json.RawMessage(`[{"key":"MODE","value":"prod"},{"key":"URL","value":"postgres://app:${PASS}@db/app"}]`)})
	})["compose.yaml"]
	if want := strings.Replace(src, "MODE: dev", "MODE: prod", 1); unchanged != want {
		t.Errorf("unchanged raw value: got:\n%s\nwant:\n%s", unchanged, want)
	}
	edited := edit(t, open(t, files), func(e *Editor) error {
		return e.PatchService("api", map[string]json.RawMessage{"environment": json.RawMessage(`[{"key":"MODE","value":"dev"},{"key":"URL","value":"postgres://app:${PASS}@db2/app"}]`)})
	})["compose.yaml"]
	if strings.Contains(edited, "s3cret") || !strings.Contains(edited, "postgres://app:${PASS}@db2/app") {
		t.Errorf("edited raw value: got:\n%s", edited)
	}
}

func TestDockerfileParser(t *testing.T) {
	df, problems := ParseDockerfile([]byte("# escape=`\nFROM --platform=$BUILDPLATFORM alpine:3.20 AS Base\nRUN echo a `\n  && echo b\nCOPY <<-EOT /x\n\thello\n\tEOT\nUSER app\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if df.Escape != '`' || len(df.Instructions) != 4 || df.Instructions[1].EndLine != 4 || df.Instructions[2].Heredocs[0].Content != "hello\n" {
		t.Fatalf("%+v", df.Instructions)
	}
	if df.Stages[0].Name != "base" || df.Stages[0].Platform != "$BUILDPLATFORM" {
		t.Fatalf("%+v", df.Stages[0])
	}
	if _, p := ParseDockerfile([]byte("RUN x\nFROM a\nBOGUS y\nRUN <<EOF\nnever closed\n")); len(p) != 3 {
		t.Fatalf("problems %v", p)
	}
	out, err := SetStageBase([]byte("FROM --platform=linux/amd64 node:20 AS build\nRUN x\nFROM build\n"), 0, "node:22-alpine")
	if err != nil || string(out) != "FROM --platform=linux/amd64 node:22-alpine AS build\nRUN x\nFROM build\n" {
		t.Fatalf("%q %v", out, err)
	}
}
