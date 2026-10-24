package api

import (
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// rampRun is a finished load test run of env: a 60 s ramp to 200 req/s
// against api (1 CPU limit) with db and cache.
func rampRun(env, id string) model.LoadTestRun {
	start := time.Now().Add(-time.Hour).UTC()
	end := start.Add(time.Minute)
	run := model.LoadTestRun{
		ID: id, Environment: env, Status: model.LoadPassed, StartedAt: start, EndedAt: &end,
		Spec:      model.LoadTestSpec{Name: "ramp"},
		Summary:   &model.LoadSummary{PeakRPS: 200, Stability: 0.1},
		Resources: &model.LoadResources{Service: "api"},
	}
	type svc struct {
		name       string
		idle, cost float64
		limit      float64
		mem        int64
	}
	svcs := []svc{{"api", 0.02, 0.003, 1, 80 << 20}, {"db", 0.01, 0.002, 1, 60 << 20}, {"cache", 0.005, 0.0005, 0.5, 10 << 20}}
	for t := 1; t <= 60; t++ {
		rps := math.Round(200 * float64(t) / 60)
		run.Timeline = append(run.Timeline, model.LoadSample{T: float64(t), Target: rps, RPS: rps, P50: 4, P95: 12, P99: 20})
	}
	for _, s := range svcs {
		res := model.LoadServiceResources{Service: s.name, NodeID: "service:" + s.name, Target: s.name == "api", CPULimit: s.limit, CPULimited: true, MemLimit: 512 << 20, MemLimited: true}
		for t := 0; t <= 60; t++ {
			rps := 0.0
			if t > 0 {
				rps = run.Timeline[t-1].RPS
			}
			res.Timeline = append(res.Timeline, model.ResourcePoint{T: float64(t), CPUPercent: (s.idle + s.cost*rps) * 100, MemBytes: s.mem, Containers: 1})
		}
		run.Resources.Services = append(run.Resources.Services, res)
	}
	return run
}

func TestCapacityPlanAndApplyDocker(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	base := "/workflows/" + wf.ID
	e.mustCall(http.StatusOK, "POST", base+"/environments", map[string]string{"name": "prod"}, nil)

	// No run of prod yet.
	var errBody struct {
		Error string `json:"error"`
	}
	if got := e.call("POST", base+"/capacity-plan", map[string]any{"targetRps": 1000, "environment": "prod"}, &errBody); got != http.StatusConflict || !strings.Contains(errBody.Error, "environment prod") {
		t.Fatalf("no run: %d %q", got, errBody.Error)
	}
	e.mustCall(http.StatusBadRequest, "POST", base+"/capacity-plan", map[string]any{"targetRps": 0, "environment": "prod"}, nil)

	e.saveRun(wf.ID, rampRun("", "run-base"))
	e.saveRun(wf.ID, rampRun("prod", "run-prod"))
	var plan model.CapacityPlan
	e.mustCall(http.StatusOK, "POST", base+"/capacity-plan", map[string]any{"targetRps": 1000, "environment": "prod"}, &plan)
	if plan.Run.ID != "run-prod" || plan.Environment != "prod" || plan.MeasuredRPS != 200 || plan.Confidence != model.ConfidenceLow || len(plan.Clouds) != 4 {
		t.Fatalf("plan = %+v", plan)
	}
	roles := map[string]string{}
	var api model.CapacityService
	for _, s := range plan.Services {
		roles[s.Service] = s.Role
		if s.Service == "api" {
			api = s
		}
	}
	if roles["api"] != model.CapacityApp || roles["db"] != model.CapacityDependency || roles["cache"] != model.CapacityDependency {
		t.Fatalf("roles = %v", roles)
	}
	// (0.7 − 0.02) / 0.003 = 226.7 req/s per replica → 1000 / 226.7 = 4.41 → 6.
	if api.Replicas != 6 || api.Apply == nil || api.Apply.Values["replicas"] != "6" || !plan.Applicable {
		t.Fatalf("api = %+v apply %+v", api, api.Apply)
	}

	var last model.CapacityPlan
	e.mustCall(http.StatusOK, "GET", base+"/capacity-plan?environment=prod", nil, &last)
	if last.Run.ID != "run-prod" || last.TargetRPS != 1000 {
		t.Fatalf("last = %+v", last)
	}

	var applied struct {
		Workflow model.Workflow `json:"workflow"`
		Applied  []string       `json:"applied"`
	}
	e.mustCall(http.StatusOK, "POST", base+"/capacity-plan/apply", map[string]any{"environment": "prod"}, &applied)
	if !strings.Contains(strings.Join(applied.Applied, ","), "api") {
		t.Fatalf("applied = %v", applied.Applied)
	}
	if f := e.file(wf.ID, "compose.prod.yaml"); !strings.Contains(f, "replicas: 6") {
		t.Fatalf("prod override:\n%s", f)
	}
	if strings.Contains(e.file(wf.ID, "compose.yaml"), "replicas") {
		t.Fatal("base changed")
	}
	e.mustCall(http.StatusOK, "GET", base+"/capacity-plan?environment=prod", nil, &last)
	if last.Applicable || !strings.Contains(last.ApplyNote, "Applied to prod") {
		t.Fatalf("after apply: applicable %v note %q", last.Applicable, last.ApplyNote)
	}
}

func TestCapacityPlanRefusesTerraform(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("tf")
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+wf.ID+"/capacity-plan", map[string]any{"targetRps": 10}, nil)
}
