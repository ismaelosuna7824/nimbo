package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func (e *testEnv) summary(id string) model.WorkflowSummary {
	e.t.Helper()
	var list []model.WorkflowSummary
	e.mustCall(http.StatusOK, "GET", "/workflows", nil, &list)
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	e.t.Fatalf("workflow %s missing from list", id)
	return model.WorkflowSummary{}
}

func TestArchiveRefusesRuntimeAndUnarchiveRestores(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Old project")
	events, cancel := e.hub.Subscribe(wf.ID)
	defer cancel()

	var got model.Workflow
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/archive", nil, &got)
	if !got.Archived {
		t.Fatal("archive response is not archived")
	}
	// Stopping the sandbox may publish sandbox events first.
	updated := false
	for !updated {
		select {
		case ev := <-events:
			updated = ev.Type == model.EventWorkflowUpdated
		default:
			t.Fatal("archive published no workflow.updated event")
		}
	}
	// The list still returns it, flagged; the UI decides what to hide.
	if s := e.summary(wf.ID); !s.Archived {
		t.Fatal("summary not archived")
	}

	// A full-document save from a stale client must not unarchive.
	got.Archived = false
	got.Description = "edited"
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, got, &got)
	if !got.Archived || got.Description != "edited" {
		t.Fatalf("put changed archive state or dropped edit: archived=%v desc=%q", got.Archived, got.Description)
	}

	for _, c := range []struct {
		path string
		body any
	}{
		{"/workflows/" + wf.ID + "/runs", map[string]string{"action": "validate"}},
		{"/workflows/" + wf.ID + "/loadtests", map[string]any{"target": map[string]string{"url": "http://127.0.0.1:1"}}},
		{"/workflows/" + wf.ID + "/sandbox/start", nil},
	} {
		var body struct{ Error string }
		if status := e.call("POST", c.path, c.body, &body); status != http.StatusConflict || !strings.Contains(body.Error, "archived") {
			t.Fatalf("POST %s = %d %q, want 409 workflow is archived", c.path, status, body.Error)
		}
	}

	var dup model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/duplicate", map[string]string{}, &dup)
	if dup.Archived {
		t.Fatal("duplicate of an archived workflow is archived")
	}

	var restored model.Workflow // fresh: archived is omitempty, so false is absent on the wire
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/unarchive", nil, &restored)
	if restored.Archived || e.summary(wf.ID).Archived {
		t.Fatal("unarchive did not restore")
	}
	if status := e.call("POST", "/workflows/missing-000000/archive", nil, nil); status != http.StatusNotFound {
		t.Fatalf("archive missing = %d, want 404", status)
	}
}
