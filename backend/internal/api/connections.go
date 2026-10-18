package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Connections library: saved, already-deployed services. Responses never
// carry secrets (see store.Mask); the engine reads them through
// store.Connections.Connection.

const (
	checkTimeout   = 30 * time.Second
	suggestTimeout = 30 * time.Second
)

func (s *Server) connectionsUpdated(id string) {
	s.cfg.Events.Publish(model.Event{Type: model.EventConnectionsUpdated, Data: map[string]string{"id": id}})
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Connections.List())
}

func (s *Server) getConnection(w http.ResponseWriter, r *http.Request) error {
	conn, err := s.cfg.Connections.Get(r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, conn)
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) error {
	var in model.Connection
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	conn, err := s.cfg.Connections.Create(in)
	if err != nil {
		return err
	}
	s.connectionsUpdated(conn.ID)
	return writeJSON(w, http.StatusCreated, conn)
}

func (s *Server) putConnection(w http.ResponseWriter, r *http.Request) error {
	var in model.Connection
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	id := r.PathValue("id")
	if in.ID != "" && in.ID != id {
		return errorf(http.StatusBadRequest, "body id %q does not match path id %q", in.ID, id)
	}
	conn, err := s.cfg.Connections.Update(id, in)
	if err != nil {
		return err
	}
	s.connectionsUpdated(id)
	return ok(w, conn)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.cfg.Connections.Get(id); err != nil {
		return err
	}
	// History goes first: a refusal (e.g. a run still active) keeps the
	// connection so nothing is left orphaned.
	if err := s.cfg.LoadTest.DeleteConnectionRuns(id); errors.Is(err, loadtest.ErrRunActive) {
		return errorf(http.StatusConflict, "%v; stop it before deleting the connection", err)
	} else if err != nil {
		return fmt.Errorf("delete load test history: %w", err)
	}
	if err := s.cfg.Connections.Delete(id); err != nil {
		return err
	}
	s.connectionsUpdated(id)
	return noContent(w)
}

func (s *Server) testConnectionDraft(w http.ResponseWriter, r *http.Request) error {
	var in model.Connection
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	conn, err := s.cfg.Connections.Draft(in)
	if err != nil {
		return err
	}
	return s.check(w, r, conn)
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) error {
	conn, found := s.cfg.Connections.Connection(r.PathValue("id"))
	if !found {
		return errorf(http.StatusNotFound, "connection %q not found", r.PathValue("id"))
	}
	return s.check(w, r, conn)
}

func (s *Server) check(w http.ResponseWriter, r *http.Request, conn model.Connection) error {
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	return ok(w, loadtest.CheckConnection(ctx, conn))
}

func (s *Server) connectionTargets(w http.ResponseWriter, r *http.Request) error {
	conn, found := s.cfg.Connections.Connection(r.PathValue("id"))
	if !found {
		return errorf(http.StatusNotFound, "connection %q not found", r.PathValue("id"))
	}
	ctx, cancel := context.WithTimeout(r.Context(), suggestTimeout)
	defer cancel()
	targets, err := loadtest.SuggestConnection(ctx, conn)
	if err != nil {
		return errorf(http.StatusBadGateway, "list targets of %s: %v", conn.Name, err)
	}
	return ok(w, nonNil(targets))
}

func (s *Server) startConnectionLoadTest(w http.ResponseWriter, r *http.Request) error {
	var spec model.LoadTestSpec
	if err := decode(w, r, maxBody, &spec); err != nil {
		return err
	}
	id := r.PathValue("id")
	if _, err := s.cfg.Connections.Get(id); err != nil {
		return err
	}
	run, err := s.cfg.LoadTest.StartConnection(context.WithoutCancel(r.Context()), id, spec)
	if err != nil {
		return loadTestError(err)
	}
	return writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listConnectionLoadTests(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.cfg.Connections.Get(id); err != nil {
		return err
	}
	return ok(w, nonNil(s.cfg.LoadTest.ListConnection(id)))
}
