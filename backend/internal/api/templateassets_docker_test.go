//go:build docker

package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// waitRunFor is waitRun with a deadline long enough for a real deploy.
func (e *testEnv) waitRunFor(id string, d time.Duration) model.Run {
	e.t.Helper()
	var run model.Run
	for deadline := time.Now().Add(d); ; time.Sleep(time.Second) {
		e.mustCall(http.StatusOK, "GET", "/runs/"+id, nil, &run)
		if run.EndedAt != nil {
			return run
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("run %s still %s", id, run.Status)
		}
	}
}

// A workflow created from a template whose Lambda zips an asset folder
// deploys on Floci: the run copies the assets into the work dir and the
// archive_file zips them. Extracting the worker into a module moves its
// sources along (the next deploy reads them there) and inlining moves them
// back; destroy cleans up. Deleting the workflow removes its sandbox
// containers.
func TestTemplateAssetsDeployOnFloci(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Jobs", "templateId": "aws-queue-worker"}, &wf)
	t.Cleanup(func() {
		if got := e.call("DELETE", "/workflows/"+wf.ID, nil, nil); got != http.StatusNoContent {
			t.Errorf("delete workflow: %d", got)
		}
	})
	run := func(body map[string]any) model.Run {
		t.Helper()
		var r model.Run
		e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/runs", body, &r)
		return e.waitRunFor(r.ID, 10*time.Minute)
	}
	apply := func(step string) {
		t.Helper()
		if r := run(map[string]any{"action": "apply"}); r.Status != model.RunSucceeded {
			t.Fatalf("apply %s: %s %s\n%s", step, r.Status, r.Error, runLog(r))
		}
	}
	exists := func(rel string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(e.home, "workflows", wf.ID, "work", filepath.FromSlash(rel))); err != nil {
			t.Errorf("work dir: %v", err)
		}
	}

	apply("from the template")
	exists(workerHandler)
	exists("build/worker.zip")

	var group []string
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		typ := n.Type
		if n.Kind == model.KindData {
			typ = "data." + typ
		}
		if slices.Contains(workerTypes, typ) {
			group = append(group, hclcodec.Address(n))
		}
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/extract", model.ModuleExtractRequest{NodeIDs: group, Name: "worker"}, &wf)
	apply("after extract")
	exists("modules/worker/" + workerHandler)
	exists("modules/worker/build/worker.zip")

	var call string
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == "module.worker" {
			call = wf.Nodes[i].ID
		}
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/modules/"+call+"/inline", nil, &wf)
	apply("after inline")
	work := filepath.Join(e.home, "workflows", wf.ID, "work")
	if got, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(workerHandler))); err != nil || !bytes.Equal(got, templateHandler(t)) {
		t.Errorf("work dir after inline %s: %v", workerHandler, err)
	}

	destroy := run(map[string]any{"action": "destroy", "confirmDestroy": true})
	if destroy.Status != model.RunSucceeded {
		t.Fatalf("destroy %s: %s\n%s", destroy.Status, destroy.Error, runLog(destroy))
	}
}
