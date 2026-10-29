package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

func TestCodeSourcesPerService(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := st.AssetsDir("wf")
	for name, body := range map[string]string{
		"src/index.js":                                      "lambda",
		"services/orders-api/cmd/api/main.go":               "main",
		"services/orders-api/internal/http/handler.go":      "handler",
		"services/orders-api/internal/http/handler_test.go": "test",
		"services/web/server.test.ts":                       "test",
		"services/web/README.md":                            "docs",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	all, services := (&Server{cfg: Config{Store: st}}).codeSources("wf")
	slices.Sort(all)
	if want := []string{"handler", "lambda", "main", "test", "test"}; !slices.Equal(all, want) {
		t.Errorf("all = %q, want %q", all, want)
	}
	got := services["orders-api"]
	slices.Sort(got)
	if !slices.Equal(got, []string{"handler", "main"}) || len(services) != 1 {
		t.Errorf("services = %q", services)
	}
}
