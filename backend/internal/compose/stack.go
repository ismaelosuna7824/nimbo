package compose

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Technology hints of a build context, for icons: the language/runtime
// first, then frameworks, read from the manifests at the context's top
// level (package.json, requirements.txt / pyproject.toml / Pipfile, go.mod,
// pom.xml / build.gradle(.kts), *.csproj / *.fsproj, Gemfile,
// composer.json, Cargo.toml, mix.exs, deno.json).

// maxManifest bounds what is read of one manifest.
const maxManifest = 1 << 20

type depHint struct {
	match string // dependency name (or substring for text manifests)
	id    string
}

var (
	nodeDeps = []depHint{
		{"next", "nextjs"}, {"@nestjs/core", "nestjs"}, {"express", "express"}, {"fastify", "fastify"},
		{"koa", "koa"}, {"@angular/core", "angular"}, {"react", "react"}, {"vue", "vue"},
		{"svelte", "svelte"}, {"nuxt", "nuxt"}, {"@remix-run/node", "remix"}, {"astro", "astro"},
		{"hono", "hono"}, {"socket.io", "socketio"}, {"prisma", "prisma"}, {"typescript", "typescript"},
	}
	pythonDeps = []depHint{
		{"fastapi", "fastapi"}, {"django", "django"}, {"flask", "flask"}, {"uvicorn", "uvicorn"},
		{"gunicorn", "gunicorn"}, {"celery", "celery"}, {"streamlit", "streamlit"}, {"aiohttp", "aiohttp"},
	}
	goDeps = []depHint{
		{"github.com/gin-gonic/gin", "gin"}, {"github.com/labstack/echo", "echo"}, {"github.com/gofiber/fiber", "fiber"},
		{"github.com/go-chi/chi", "chi"}, {"github.com/gorilla/mux", "gorilla"},
	}
	javaDeps = []depHint{
		{"spring-boot", "spring"}, {"quarkus", "quarkus"}, {"micronaut", "micronaut"}, {"kotlin", "kotlin"},
	}
	rubyDeps    = []depHint{{"rails", "rails"}, {"sinatra", "sinatra"}}
	phpDeps     = []depHint{{"laravel/framework", "laravel"}, {"symfony/", "symfony"}, {"slim/slim", "slim"}}
	rustDeps    = []depHint{{"actix-web", "actix"}, {"axum", "axum"}, {"rocket", "rocket"}, {"tokio", "tokio"}}
	elixirDeps  = []depHint{{":phoenix", "phoenix"}}
	dotnetDeps  = []depHint{{"Microsoft.AspNetCore", "aspnet"}, {"Microsoft.NET.Sdk.Web", "aspnet"}}
	pyReqNameRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_.\-\[\]]+)`)
)

// Stack reads the technology hints of the build context ctx (slash path
// relative to the project dir) through read; list names its entries.
func Stack(ctx string, read func(rel string) ([]byte, error), list func(dir string) []string) []string {
	var out []string
	add := func(ids ...string) {
		for _, id := range ids {
			if id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	file := func(name string) string {
		data, err := read(path.Join(ctx, name))
		if err != nil || len(data) > maxManifest {
			return ""
		}
		return string(data)
	}
	entries := list(ctx)
	has := func(name string) bool { return slices.Contains(entries, name) }

	if pkg := file("package.json"); pkg != "" {
		var m struct {
			Dependencies    map[string]any `json:"dependencies"`
			DevDependencies map[string]any `json:"devDependencies"`
		}
		_ = json.Unmarshal([]byte(pkg), &m)
		switch {
		case has("bun.lockb") || has("bun.lock"):
			add("bun")
		default:
			add("node")
		}
		for _, h := range nodeDeps {
			_, a := m.Dependencies[h.match]
			_, b := m.DevDependencies[h.match]
			if a || b {
				add(h.id)
			}
		}
	}
	if has("deno.json") || has("deno.jsonc") {
		add("deno")
	}
	var py strings.Builder
	for _, n := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		py.WriteString(strings.ToLower(file(n)))
		py.WriteByte('\n')
	}
	if strings.TrimSpace(py.String()) != "" || slices.ContainsFunc(entries, func(e string) bool { return strings.HasSuffix(e, ".py") }) {
		add("python")
		names := map[string]bool{}
		for _, m := range pyReqNameRe.FindAllStringSubmatch(py.String(), -1) {
			n, _, _ := strings.Cut(m[1], "[")
			names[n] = true
		}
		text := py.String()
		for _, h := range pythonDeps {
			if names[h.match] || strings.Contains(text, `"`+h.match) || strings.Contains(text, h.match+" =") {
				add(h.id)
			}
		}
	}
	if mod := file("go.mod"); mod != "" {
		add("go")
		for _, h := range goDeps {
			if strings.Contains(mod, h.match) {
				add(h.id)
			}
		}
	}
	java := file("pom.xml") + file("build.gradle") + file("build.gradle.kts")
	if java != "" {
		if has("build.gradle.kts") && strings.Contains(java, "kotlin") {
			add("kotlin")
		} else {
			add("java")
		}
		for _, h := range javaDeps {
			if strings.Contains(java, h.match) {
				add(h.id)
			}
		}
	}
	for _, e := range entries {
		if strings.HasSuffix(e, ".csproj") || strings.HasSuffix(e, ".fsproj") || strings.HasSuffix(e, ".sln") {
			add("dotnet")
			if strings.HasSuffix(e, ".csproj") {
				add("csharp")
			}
			proj := file(e)
			for _, h := range dotnetDeps {
				if strings.Contains(proj, h.match) {
					add(h.id)
				}
			}
		}
	}
	if gem := file("Gemfile"); gem != "" {
		add("ruby")
		for _, h := range rubyDeps {
			if strings.Contains(gem, "'"+h.match+"'") || strings.Contains(gem, `"`+h.match+`"`) {
				add(h.id)
			}
		}
	}
	if php := file("composer.json"); php != "" {
		add("php")
		for _, h := range phpDeps {
			if strings.Contains(php, `"`+h.match) {
				add(h.id)
			}
		}
	} else if slices.ContainsFunc(entries, func(e string) bool { return strings.HasSuffix(e, ".php") }) {
		add("php")
	}
	if cargo := file("Cargo.toml"); cargo != "" {
		add("rust")
		for _, h := range rustDeps {
			if strings.Contains(cargo, h.match) {
				add(h.id)
			}
		}
	}
	if mix := file("mix.exs"); mix != "" {
		add("elixir")
		for _, h := range elixirDeps {
			if strings.Contains(mix, h.match) {
				add(h.id)
			}
		}
	}
	return out
}
