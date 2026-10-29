package endpoints

import (
	"slices"
	"testing"
)

func TestCodeRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"go net/http", `mux.HandleFunc("POST /orders", h.create)
mux.HandleFunc("GET /orders/{id}", h.get)
mux.Handle("GET /{$}", index)
http.HandleFunc("/files/{path...}", serve)
mux.HandleFunc("GET /health", ok)`,
			[]string{"GET /", "GET /health", "POST /orders", "GET /orders/{id}", "ANY /files/{path+}"}},
		{"chi and gin", `r.Get("/items", list)
r.POST("/items", create)`,
			[]string{"GET /items", "POST /items"}},
		{"express", "app.get('/notes/:id', get)\nrouter.delete(\"/notes/:id\", del)\napp.post(`/notes`, add)",
			[]string{"GET /notes/{id}", "DELETE /notes/{id}", "POST /notes"}},
		{"fastapi", `@app.get("/items/{item_id}")
@app.put("/items/{item_id}")
@app.get("/healthz")`,
			[]string{"GET /healthz", "GET /items/{item_id}", "PUT /items/{item_id}"}},
		{"flask", `@app.route("/users/<int:uid>", methods=["GET", "DELETE"])
@app.route("/ping")`,
			[]string{"GET /ping", "GET /users/{uid}", "DELETE /users/{uid}"}},
		{"not routes", `os.Get("/etc/passwd.txt")
http.Get("http://example.com/x")
cache.get("key")`,
			nil},
	} {
		if got := codeRoutes([]string{tc.src}); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
