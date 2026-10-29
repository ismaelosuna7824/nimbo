package api

import (
	"net/http"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestWorkflowModeCreateUpdate(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]any{"name": "Full", "mode": "full"}, &wf)
	if wf.Mode != model.ModeFull {
		t.Fatalf("create mode = %q", wf.Mode)
	}
	if status := e.call("POST", "/workflows", map[string]any{"name": "Bad", "mode": "code"}, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid create mode: status %d", status)
	}

	// A body without mode keeps it (older clients); one with mode changes it.
	var got model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &got)
	got.Mode = ""
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, got, &got)
	if got.Mode != model.ModeFull {
		t.Fatalf("mode lost without mode in the body: %q", got.Mode)
	}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, map[string]any{"name": got.Name, "mode": "architecture"}, &got)
	if got.Mode != model.ModeArchitecture {
		t.Fatalf("update mode = %q", got.Mode)
	}
	if status := e.call("PUT", "/workflows/"+wf.ID, map[string]any{"name": got.Name, "mode": "code"}, nil); status != http.StatusBadRequest {
		t.Fatalf("invalid update mode: status %d", status)
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &got)
	if got.Mode != model.ModeArchitecture {
		t.Fatalf("stored mode = %q", got.Mode)
	}

	var dk model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]any{"name": "Stack", "kind": "docker"}, &dk)
	if dk.Mode != "" || dk.EffectiveMode() != model.ModeArchitecture {
		t.Fatalf("default mode = %q", dk.Mode)
	}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+dk.ID, map[string]any{"name": dk.Name, "kind": "docker", "mode": "full"}, &dk)
	if dk.Mode != model.ModeFull {
		t.Fatalf("docker update mode = %q", dk.Mode)
	}
}
