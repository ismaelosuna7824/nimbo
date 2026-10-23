package main

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// Load test runs record the active environment of docker workflows like
// Kubernetes ones (LoadTestRun.Environment).
func TestLoadTestWorkflowsActiveEnvironment(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	save := func(wf *model.Workflow) {
		t.Helper()
		if err := st.Save(wf); err != nil {
			t.Fatal(err)
		}
	}
	save(&model.Workflow{ID: "dk-1", Name: "dk", Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}, Environment: "prod"}})
	save(&model.Workflow{ID: "dk-2", Name: "dk2", Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Files: []string{"compose.yaml"}}})
	save(&model.Workflow{ID: "tf-1", Name: "tf"})
	w := loadTestWorkflows{st: st}
	if env, ok := w.ActiveEnvironment("dk-1"); env != "prod" || !ok {
		t.Errorf("docker env = %q, %v", env, ok)
	}
	if env, ok := w.ActiveEnvironment("dk-2"); env != "" || !ok {
		t.Errorf("docker base = %q, %v", env, ok)
	}
	if _, ok := w.ActiveEnvironment("tf-1"); ok {
		t.Error("terraform workflow has an environment")
	}
}
