package api

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/capacity"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func readinessItem(t *testing.T, e model.EnvironmentReadiness, id string) model.ReadinessItem {
	t.Helper()
	for _, g := range e.Groups {
		for _, it := range g.Items {
			if it.ID == id {
				return it
			}
		}
	}
	t.Fatalf("no item %q", id)
	return model.ReadinessItem{}
}

// GET /readiness covers docker workflows: monitoring and a saved capacity
// plan show up in the items and the component rows.
func TestDockerReadiness(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	var rep model.WorkflowReadiness
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/readiness", nil, &rep)
	if rep.Kind != "docker" || len(rep.Environments) != 1 {
		t.Fatalf("report %+v", rep)
	}
	base := rep.Environments[0]
	if it := readinessItem(t, base, "monitoring"); it.Status != model.ItemWarn {
		t.Errorf("monitoring before: %+v", it)
	}
	if it := readinessItem(t, base, model.ScenarioKillReplica); it.Status != model.ItemTodo || it.Action == nil || it.Action.Kind != model.ActionRunScenario {
		t.Errorf("kill replica: %+v", it)
	}
	if len(base.Components) != 3 || base.Status != model.ReadinessNeedsWork {
		t.Errorf("base %s %+v", base.Status, base.Components)
	}

	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/monitoring", nil, nil)
	plan := &model.CapacityPlan{WorkflowID: wf.ID, Kind: "docker", CreatedAt: time.Now(), TargetRPS: 200, Confidence: model.ConfidenceHigh,
		Run: model.CapacityRun{ID: "r1"}, Services: []model.CapacityService{{NodeID: "service:api", Service: "api", Role: model.CapacityApp, Replicas: 3}}}
	if err := capacity.SavePlan(e.home, plan); err != nil {
		t.Fatal(err)
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/readiness?environment=", nil, &rep)
	base = rep.Environments[0]
	if it := readinessItem(t, base, "monitoring"); it.Status != model.ItemPass {
		t.Errorf("monitoring after: %+v", it)
	}
	if it := readinessItem(t, base, "capacity"); it.Status != model.ItemPass || it.Action == nil || it.Action.RunID != "r1" {
		t.Errorf("capacity: %+v", it)
	}
	names := []string{}
	for _, c := range base.Components {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"api", "cache", "db"}) {
		t.Errorf("components with monitoring added: %v", names)
	}
	e.mustCall(http.StatusNotFound, "GET", "/workflows/"+wf.ID+"/readiness?environment=prod", nil, nil)
}

func TestKubeReadinessAndKinds(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"kind": "kubernetes", "name": "hello", "templateId": "k8s-kustomize-app"}, &wf)
	var rep model.WorkflowReadiness
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/readiness", nil, &rep)
	if rep.Kind != "kubernetes" || len(rep.Environments) != 2 {
		t.Fatalf("report %+v", rep)
	}
	for _, env := range rep.Environments {
		if len(env.Components) == 0 || len(env.Groups) != 6 {
			t.Errorf("%s: %d components, %d groups", env.Name, len(env.Components), len(env.Groups))
		}
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/readiness?environment=prod", nil, &rep)
	if len(rep.Environments) != 1 || rep.Environments[0].Name != "prod" {
		t.Errorf("only prod: %+v", rep.Environments)
	}
	tf := e.createEmpty("infra")
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+tf.ID+"/readiness", nil, nil)
}
