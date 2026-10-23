package api

import (
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/filetemplates"
)

// fileTemplates lists the Code tab's "New from template…" files.
func (s *Server) fileTemplates(w http.ResponseWriter, r *http.Request) error {
	return ok(w, filetemplates.List())
}
