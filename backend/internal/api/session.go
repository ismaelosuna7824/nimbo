package api

import (
	"errors"
	"net/http"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// sessionState is what the desktop app reports about itself (the workflow
// open in the editor) so MCP agents can act on "the current workflow". It
// lives in memory only: a daemon restart means nothing is open.
type sessionState struct {
	mu             sync.Mutex
	openWorkflowID string
}

type sessionBody struct {
	OpenWorkflowID *string `json:"openWorkflowId"`
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) error {
	s.session.mu.Lock()
	id := s.session.openWorkflowID
	s.session.mu.Unlock()
	var out sessionBody
	if id != "" {
		// A workflow deleted while open is no longer "open".
		_, err := s.cfg.Store.Get(id)
		switch {
		case err == nil:
			out.OpenWorkflowID = &id
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	}
	return ok(w, out)
}

func (s *Server) putSession(w http.ResponseWriter, r *http.Request) error {
	var in sessionBody
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	id := ""
	if in.OpenWorkflowID != nil {
		id = *in.OpenWorkflowID
	}
	if id != "" {
		if _, err := s.cfg.Store.Get(id); err != nil {
			return err
		}
	}
	s.session.mu.Lock()
	s.session.openWorkflowID = id
	s.session.mu.Unlock()
	return noContent(w)
}
