package endpoints

import (
	"regexp"
	"slices"
	"strings"
)

// Route discovery: the HTTP routes a container's code registers, so its
// endpoints list callable paths instead of "/" (which many services don't
// serve). Recognizes the common router call shapes:
//
//	mux.HandleFunc("GET /items/{id}", …)        Go net/http (1.22 patterns)
//	r.Get("/items", …) / r.GET(…)                chi, gin, echo, fiber
//	app.get('/items/:id', …)                     Express, Fastify, Hono
//	@app.get("/items/{id}")                      FastAPI
//	@app.route("/items", methods=["POST"])       Flask
var (
	goMuxRoute    = regexp.MustCompile(`\.Handle(?:Func)?\(\s*"(?:([A-Z]+)\s+)?(/[^"\s]*)"`)
	methodRoute   = regexp.MustCompile("\\.(?i:(get|post|put|patch|delete))\\(\\s*[\"'`](/[^\"'`\\s]*)[\"'`]")
	flaskRoute    = regexp.MustCompile(`\.route\(\s*["'](/[^"'\s]*)["']([^)]*)\)`)
	flaskMethods  = regexp.MustCompile(`methods\s*=\s*[\[(]([^\])]*)[\])]`)
	quotedWord    = regexp.MustCompile(`["']([A-Za-z]+)["']`)
	expressParam  = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)
	flaskParam    = regexp.MustCompile(`<(?:[a-z]+:)?([A-Za-z_][A-Za-z0-9_]*)>`)
	goRestParam   = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\.\.\.\}`)
	maxCodeRoutes = 20
)

// codeRoutes lists the route keys ("GET /items/{id}", "ANY /") registered
// by sources, deduplicated. Parameter syntaxes become {name}; Go's
// {name...} becomes the greedy {name+}. Parameter-free GET routes come
// first, so the default request reads something that exists.
func codeRoutes(sources []string) []string {
	var keys []string
	add := func(method, path string) {
		path = normalizeRoute(path)
		if path == "" {
			return
		}
		if method = strings.ToUpper(method); method == "" {
			method = "ANY"
		}
		if k := method + " " + path; !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, src := range sources {
		for _, m := range goMuxRoute.FindAllStringSubmatch(src, -1) {
			add(m[1], m[2])
		}
		for _, m := range methodRoute.FindAllStringSubmatch(src, -1) {
			add(m[1], m[2])
		}
		for _, m := range flaskRoute.FindAllStringSubmatch(src, -1) {
			methods := []string{"GET"}
			if mm := flaskMethods.FindStringSubmatch(m[2]); mm != nil {
				methods = methods[:0]
				for _, w := range quotedWord.FindAllStringSubmatch(mm[1], -1) {
					methods = append(methods, w[1])
				}
			}
			for _, method := range methods {
				add(method, m[1])
			}
		}
	}
	rank := func(k string) int {
		if strings.HasPrefix(k, "GET ") && !strings.Contains(k, "{") {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(keys, func(a, b string) int { return rank(a) - rank(b) })
	if len(keys) > maxCodeRoutes {
		keys = keys[:maxCodeRoutes]
	}
	return keys
}

// normalizeRoute rewrites a router's path syntax to "{param}" form; "" for
// paths that aren't routes (file paths, URLs with a scheme).
func normalizeRoute(p string) string {
	p = goRestParam.ReplaceAllString(p, "{$1+}")
	if strings.Contains(p, "//") || strings.Contains(p, ".") {
		return ""
	}
	p = strings.TrimSuffix(p, "{$}") // Go: exact match of the path
	p = flaskParam.ReplaceAllString(p, "{$1}")
	p = expressParam.ReplaceAllString(p, "{$1}")
	if p == "" {
		p = "/"
	}
	return p
}
