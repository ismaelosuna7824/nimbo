package api

import (
	"net/http"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/filetemplates"
)

func TestFileTemplatesEndpoint(t *testing.T) {
	e := newEnv(t)
	var list []filetemplates.Template
	e.mustCall(http.StatusOK, "GET", "/catalog/file-templates", nil, &list)
	if len(list) != len(filetemplates.List()) || len(list) == 0 {
		t.Fatalf("got %d templates", len(list))
	}
	if tmpl := list[0]; tmpl.ID == "" || tmpl.Kind == "" || tmpl.Content == "" {
		t.Errorf("first template: %+v", tmpl)
	}
}
