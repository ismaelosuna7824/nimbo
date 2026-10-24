package main

import (
	"context"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// loadTestWorkflows answers the load test engine's questions about docker
// and Kubernetes workflows (loadtest.WorkflowInspector) from the store.
type loadTestWorkflows struct{ st *store.Store }

func (w loadTestWorkflows) ActiveEnvironment(wfID string) (string, bool) {
	wf, err := w.st.Get(wfID)
	if err != nil {
		return "", false
	}
	// Kubernetes.Environment / Compose.Environment are the active
	// environment once it names one the files define ("" renders the base).
	switch {
	case wf.IsKubernetes() && wf.Kubernetes != nil:
		return wf.Kubernetes.Environment, true
	case wf.IsDocker() && wf.Compose != nil:
		return wf.Compose.Environment, true
	}
	return "", false
}

func (w loadTestWorkflows) Workflow(wfID string) (*model.Workflow, bool) {
	wf, err := w.st.Get(wfID)
	return wf, err == nil
}

func (w loadTestWorkflows) AutoscaledIn(ctx context.Context, wfID, env, kind, name string) []string {
	wf, err := w.st.Get(wfID)
	if err != nil || !wf.IsKubernetes() {
		return nil
	}
	var out []string
	for _, e := range wf.Environments {
		if e.Name == env || ctx.Err() != nil {
			continue
		}
		p := kube.Open(ctx, w.st.AssetsDir(wf.ID), wf, kube.Options{Environment: e.Name, NoSourceMap: true})
		if p.Autoscales(kind, name) {
			out = append(out, e.Name)
		}
	}
	return out
}
