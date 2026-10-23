package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func envNamesOf(wf model.Workflow) []string {
	var out []string
	for _, e := range wf.Environments {
		out = append(out, e.Name)
	}
	return out
}

// TestKubernetesEnvironmentsAPI drives /environments on the Kustomize
// template: duplicate prod, edit the copy, compare, switch, rename, delete.
func TestKubernetesEnvironmentsAPI(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "hello", "templateId": "k8s-kustomize-app"}, &wf)
	base := "/workflows/" + wf.ID + "/environments"

	var view model.EnvironmentsView
	e.mustCall(http.StatusOK, "GET", base, nil, &view)
	if view.Kind != model.WorkflowKubernetes || len(view.Environments) != 2 || len(view.Sources) != 1 || !view.Sources[0].Supported ||
		view.Environments[1].Name != "prod" || !view.Environments[1].ProdLike || !slices.Equal(view.Environments[1].Files, []string{"overlays/prod"}) {
		t.Fatalf("view %+v", view)
	}

	// Names are checked before anything is written.
	e.mustCall(http.StatusBadRequest, "POST", base, map[string]string{"name": "Staging"}, nil)
	e.mustCall(http.StatusBadRequest, "POST", base, map[string]string{"name": "base"}, nil)
	e.mustCall(http.StatusConflict, "POST", base, map[string]string{"name": "dev"}, nil)
	e.mustCall(http.StatusNotFound, "POST", base, map[string]string{"name": "qa", "from": "nope"}, nil)

	e.mustCall(http.StatusOK, "POST", base, map[string]string{"name": "staging", "from": "prod"}, &wf)
	if !slices.Equal(envNamesOf(wf), []string{"dev", "prod", "staging"}) || wf.Kubernetes.Sources[0].Environments["staging"] != "overlays/staging" {
		t.Fatalf("after create %v %v", envNamesOf(wf), wf.Kubernetes.Sources[0].Environments)
	}

	var settings model.EnvSettings
	e.mustCall(http.StatusOK, "GET", base+"/staging/settings", nil, &settings)
	if settings.Target != "Deployment/web" || settings.File != "overlays/staging/kustomization.yaml" || len(settings.Settings) < 6 {
		t.Fatalf("settings %+v", settings)
	}
	e.mustCall(http.StatusBadRequest, "PATCH", base+"/staging/settings", map[string]any{"target": "Deployment/web", "values": map[string]any{"replicas": "three"}}, nil)
	e.mustCall(http.StatusNotFound, "PATCH", base+"/staging/settings", map[string]any{"target": "Deployment/nope", "values": map[string]any{"replicas": "3"}}, nil)
	e.mustCall(http.StatusOK, "PATCH", base+"/staging/settings", map[string]any{"target": "Deployment/web", "values": map[string]any{"replicas": "3", "image:nginx": "1.29-alpine"}}, &wf)
	if k := e.file(wf.ID, "overlays/staging/kustomization.yaml"); !strings.Contains(k, "count: 3") || !strings.Contains(k, "newTag: 1.29-alpine") {
		t.Fatalf("overlay:\n%s", k)
	}

	var cmp model.EnvCompare
	e.mustCall(http.StatusOK, "GET", base+"/compare", nil, &cmp)
	var replicas []string
	for _, g := range cmp.Groups {
		for _, r := range g.Rows {
			if g.ID == "Deployment/web" && r.Key == "replicas" {
				replicas = r.Values
			}
		}
	}
	if !slices.Equal(replicas, []string{"1", "1", "2", "3"}) {
		t.Fatalf("compare replicas %v (%+v)", replicas, cmp.Columns)
	}

	// Switching renders staging; renaming the active environment follows it.
	wf.ActiveEnvironment = "staging"
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	if wf.Kubernetes.Environment != "staging" {
		t.Fatalf("active %q", wf.Kubernetes.Environment)
	}
	e.mustCall(http.StatusOK, "PATCH", base+"/staging", map[string]string{"name": "qa"}, &wf)
	if wf.ActiveEnvironment != "qa" || wf.Kubernetes.Environment != "qa" || !slices.Equal(envNamesOf(wf), []string{"dev", "prod", "qa"}) {
		t.Fatalf("after rename %q %q %v", wf.ActiveEnvironment, wf.Kubernetes.Environment, envNamesOf(wf))
	}
	found := false
	for _, n := range wf.Kubernetes.Graph.Nodes {
		if n.ID == "Deployment/hello-qa/web" && n.Workload != nil && n.Workload.Replicas != nil && *n.Workload.Replicas == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("qa graph %+v", wf.Kubernetes.Graph.Nodes)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "overlays", "staging")); !os.IsNotExist(err) {
		t.Fatalf("overlays/staging survived the rename: %v", err)
	}

	// Deleting the active environment makes the base active.
	var after model.Workflow // activeEnvironment is omitted when empty
	e.mustCall(http.StatusOK, "DELETE", base+"/qa", nil, &after)
	if after.ActiveEnvironment != "" || after.Kubernetes.Environment != "" || !slices.Equal(envNamesOf(after), []string{"dev", "prod"}) {
		t.Fatalf("after delete %q %v", after.ActiveEnvironment, envNamesOf(after))
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "overlays", "qa")); !os.IsNotExist(err) {
		t.Fatalf("overlays/qa survived: %v", err)
	}
	e.mustCall(http.StatusNotFound, "DELETE", base+"/qa", nil, nil)

	// Terraform workflows keep their own environments.
	tf := e.createEmpty("infra")
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/environments", nil, nil)
}
