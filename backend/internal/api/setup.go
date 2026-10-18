package api

import (
	"errors"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/setup"
)

// ---------------------------------------------------------------------------
// Setup diagnostics (onboarding)

// getSetup is GET /setup: every requirement check, evaluated now.
func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Setup.Run(r.Context()))
}

// setupAction is POST /setup/actions/{action}: starts a one-click fix whose
// progress arrives as setup.progress events.
func (s *Server) setupAction(w http.ResponseWriter, r *http.Request) error {
	action := r.PathValue("action")
	switch err := s.cfg.Setup.StartAction(action); {
	case errors.Is(err, setup.ErrUnknownAction):
		return errorf(http.StatusNotFound, "%s", err.Error())
	case errors.Is(err, setup.ErrActionRunning):
		return errorf(http.StatusConflict, "%s", err.Error())
	case err != nil:
		return err
	}
	return writeJSON(w, http.StatusAccepted, map[string]string{"action": action})
}
