package api

import (
	"errors"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/agents"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
)

// ---------------------------------------------------------------------------
// Settings

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Settings.Get())
}

// settingsDefaults is GET /settings/defaults: the values an empty field
// falls back to (the UI's "reset to default").
func (s *Server) settingsDefaults(w http.ResponseWriter, r *http.Request) error {
	return ok(w, settings.Defaults())
}

// putSettings validates and stores the settings, then applies them: sandbox
// tools/images for sandboxes started afterwards, and the AWS region of new
// provider blocks.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) error {
	var in model.Settings
	if err := decode(w, r, maxBody, &in); err != nil {
		return err
	}
	saved, err := s.cfg.Settings.Put(in)
	if errors.Is(err, settings.ErrInvalid) {
		return errorf(http.StatusBadRequest, "%v", err)
	}
	if err != nil {
		return err
	}
	s.applySettings(saved)
	return ok(w, saved)
}

// applySettings pushes settings into the components that use them.
func (s *Server) applySettings(st model.Settings) {
	s.cfg.Sandbox.ApplySettings(st)
	s.cfg.Catalog.SetDefaultAWSRegion(st.DefaultAWSRegion)
}

// ---------------------------------------------------------------------------
// AI agents (MCP clients)

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.Agents.List())
}

func (s *Server) installAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.cfg.Agents.Install(r.Context(), r.PathValue("id"))
	if err != nil {
		return agentError(err)
	}
	return ok(w, a)
}

func (s *Server) uninstallAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.cfg.Agents.Uninstall(r.Context(), r.PathValue("id"))
	if err != nil {
		return agentError(err)
	}
	return ok(w, a)
}

// agentError maps agent setup failures: unknown client 404, unusable
// binary 409, anything else (unparsable config, CLI failure) 422 with the
// message shown to the user.
func agentError(err error) error {
	switch {
	case errors.Is(err, agents.ErrUnknownClient):
		return errorf(http.StatusNotFound, "%v", err)
	case errors.Is(err, agents.ErrUnusableExecutable):
		return &httpError{status: http.StatusConflict, code: "unusable_executable", msg: err.Error()}
	default:
		return errorf(http.StatusUnprocessableEntity, "%v", err)
	}
}

// mcpCommand is GET /mcp/command: how agents start Nimbo's MCP server.
type mcpCommand struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Problem explains why one-click install is unavailable ("" = usable).
	Problem string `json:"problem,omitempty"`
}

func (s *Server) mcpCommand(w http.ResponseWriter, r *http.Request) error {
	exe, args := s.cfg.Agents.Command()
	res := mcpCommand{Command: exe, Args: args}
	if err := s.cfg.Agents.CheckExecutable(); err != nil {
		res.Problem = err.Error()
	}
	return ok(w, res)
}

// MCPCatalog is the body of GET /mcp/catalog: what agents can do through
// Nimbo. Config.MCPCatalog provides it (package mcp is a client of this API,
// so api cannot import it).
type MCPCatalog struct {
	Tools   any `json:"tools"`   // []mcp.ToolInfo
	Prompts any `json:"prompts"` // []mcp.PromptInfo
}

func (s *Server) mcpCatalog(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.cfg.MCPCatalog())
}
