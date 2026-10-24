package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/capacity"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Capacity planning of docker and Kubernetes workflows
// (/workflows/{id}/capacity-plan): POST sizes the workflow for a target
// request rate from a finished load test of an environment and keeps the
// plan as that environment's latest; GET returns it; POST .../apply writes
// the latest plan's replicas, autoscaler and requests/limits into the
// environment through its per-environment settings.

// maxTargetRPS bounds the target rate a plan accepts.
const maxTargetRPS = 10_000_000

// capacityWorkflow loads a docker or Kubernetes workflow and resolves the
// environment ("" = the active one; the base when it has none).
func (s *Server) capacityWorkflow(id, env string) (*model.Workflow, string, error) {
	wf, err := s.cfg.Store.Get(id)
	if err != nil {
		return nil, "", err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return nil, "", errorf(http.StatusBadRequest, "capacity plans need a docker or Kubernetes workflow (this is a %s workflow)", model.KindLabel(wf.Kind))
	}
	if env == "" {
		env = wf.ActiveEnvironment
	}
	if env != "" {
		if err := existingEnvironment(wf, env); err != nil {
			return nil, "", err
		}
	}
	return wf, env, nil
}

// POST /workflows/{id}/capacity-plan {targetRps, p95Ms?, environment?, runId?}
func (s *Server) planCapacity(w http.ResponseWriter, r *http.Request) error {
	var req model.CapacityRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if req.TargetRPS <= 0 || req.TargetRPS > maxTargetRPS {
		return errorf(http.StatusBadRequest, "targetRps must be between 0 and %d", maxTargetRPS)
	}
	if req.P95Ms < 0 {
		return errorf(http.StatusBadRequest, "p95Ms must be positive when set")
	}
	wf, env, err := s.capacityWorkflow(r.PathValue("id"), req.Environment)
	if err != nil {
		return err
	}
	run, err := s.capacityRun(wf, env, req.RunID)
	if err != nil {
		return err
	}
	settings := s.capacitySettings(r.Context(), wf, env, run)
	services := map[string]capacity.Service{}
	for id, st := range settings {
		services[id] = st.info
	}
	plan, err := capacity.Plan(capacity.Input{
		WorkflowID: wf.ID, Kind: kindName(wf), Environment: env,
		Request: req, Run: run, Services: services, Now: time.Now(),
	})
	if err != nil {
		if errors.Is(err, capacity.ErrNoMeasurements) || errors.Is(err, capacity.ErrNoHealthy) {
			return errorf(http.StatusConflict, "%v", err)
		}
		return errorf(http.StatusBadRequest, "%v", err)
	}
	switch {
	case env == "":
		plan.ApplyNote = "The base has no environment file to write to: create an environment (Environments tab) to apply a plan there."
	default:
		for i := range plan.Services {
			st := settings[plan.Services[i].NodeID]
			plan.Services[i].Apply = capacity.ApplyValues(plan.Kind, plan.Services[i], st.settings)
			if a := plan.Services[i].Apply; a != nil && len(a.Values) > 0 {
				plan.Applicable = true
			}
		}
		if !plan.Applicable {
			plan.ApplyNote = "Environment " + env + " already matches the plan."
		}
	}
	if err := capacity.SavePlan(s.cfg.Home, plan); err != nil {
		return fmt.Errorf("save capacity plan: %w", err)
	}
	return ok(w, plan)
}

// GET /workflows/{id}/capacity-plan?environment= (null when none)
func (s *Server) getCapacityPlan(w http.ResponseWriter, r *http.Request) error {
	wf, env, err := s.capacityWorkflow(r.PathValue("id"), r.URL.Query().Get("environment"))
	if err != nil {
		return err
	}
	if p, found := capacity.LastPlan(s.cfg.Home, wf.ID, env); found {
		return ok(w, p)
	}
	return ok(w, nil)
}

// POST /workflows/{id}/capacity-plan/apply {environment?, services?}
// writes the environment's latest plan (services: only these node IDs).
func (s *Server) applyCapacityPlan(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Environment string   `json:"environment"`
		Services    []string `json:"services"`
	}
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	wf, env, err := s.capacityWorkflow(r.PathValue("id"), req.Environment)
	if err != nil {
		return err
	}
	if env == "" {
		return errorf(http.StatusBadRequest, "the base has no environment file to write to: create an environment to apply a plan")
	}
	if err := refuseArchived(wf); err != nil {
		return err
	}
	plan, found := capacity.LastPlan(s.cfg.Home, wf.ID, env)
	if !found {
		return errorf(http.StatusNotFound, "environment %s has no capacity plan: plan one first", env)
	}
	var applied []string
	for _, svc := range plan.Services {
		a := svc.Apply
		if a == nil || len(a.Values) == 0 || len(req.Services) > 0 && !slices.Contains(req.Services, svc.NodeID) {
			continue
		}
		values := map[string]*string{}
		for k, v := range a.Values {
			values[k] = &v
		}
		if wf.IsKubernetes() {
			_, err = s.kubeSetEnvironmentSettings(wf.ID, env, a.Target, values)
		} else {
			_, err = s.composeSetEnvironmentSettings(wf.ID, env, a.Target, values)
		}
		if err != nil {
			if len(applied) > 0 {
				return errorf(http.StatusConflict, "applied %s; %s failed: %v", strings.Join(applied, ", "), svc.Service, err)
			}
			return err
		}
		applied = append(applied, svc.Service)
	}
	if len(applied) == 0 {
		return errorf(http.StatusConflict, "nothing to apply: environment %s already matches the plan", env)
	}
	// Everything is written: the saved plan has nothing left to apply.
	if len(req.Services) == 0 {
		plan.Applicable = false
		plan.ApplyNote = "Applied to " + env + " on " + time.Now().UTC().Format("2006-01-02 15:04") + " UTC: deploy the environment to run with it."
		if err := capacity.SavePlan(s.cfg.Home, plan); err != nil {
			return fmt.Errorf("save capacity plan: %w", err)
		}
	}
	out, err := s.cfg.Store.Get(wf.ID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"workflow": out, "applied": applied})
}

// capacityRun is runID (it must belong to wf) or the newest finished run
// of env with resource usage and no fault scenario.
func (s *Server) capacityRun(wf *model.Workflow, env, runID string) (*model.LoadTestRun, error) {
	if runID != "" {
		run, found := s.cfg.LoadTest.Get(runID)
		if !found || run.WorkflowID != wf.ID {
			return nil, errorf(http.StatusNotFound, "load test %s not found in this workflow", runID)
		}
		if run.Status == model.LoadRunning {
			return nil, errorf(http.StatusConflict, "load test %s is still running", runID)
		}
		return run, nil
	}
	for _, run := range s.cfg.LoadTest.List(wf.ID) {
		if run.Environment != env || run.Status == model.LoadRunning || run.Status == model.LoadErrored ||
			run.Resources == nil || len(run.Resources.Services) == 0 || run.Spec.Scenario != nil {
			continue
		}
		if full, found := s.cfg.LoadTest.Get(run.ID); found {
			return full, nil
		}
	}
	where := "the base"
	if env != "" {
		where = "environment " + env
	}
	return nil, errorf(http.StatusConflict, "no finished load test of %s recorded resource usage: run one from the Load test tab (a ramp of about a minute) while %s is active, then plan", where, where)
}

// serviceSettings is a measured service's role and its settings in the
// plan's environment.
type serviceSettings struct {
	info     capacity.Service
	settings []model.EnvSetting
}

// capacitySettings reads, for every service the run measured, whether it
// is a data service and its settings (replicas, autoscaler, requests) in
// env, by graph node ID.
func (s *Server) capacitySettings(ctx context.Context, wf *model.Workflow, env string, run *model.LoadTestRun) map[string]serviceSettings {
	out := map[string]serviceSettings{}
	if wf.IsDocker() {
		base, p := s.openComposeEnv(wf, ""), s.openComposeEnv(wf, env)
		images := map[string]string{}
		for _, n := range wf.Compose.Graph.Nodes {
			if n.Service != nil && n.Service.Image != nil {
				images[n.ID] = n.Service.Image.Ref
			}
		}
		for _, r := range run.Resources.Services {
			st := serviceSettings{info: capacity.Service{Dependency: compose.DataCategory(images[r.NodeID]) != ""}}
			if p.Model != nil {
				if list, err := compose.Settings(base, p, strings.TrimPrefix(r.NodeID, "service:")); err == nil {
					st.settings = list
					st.info.Replicas = intSetting(list, "replicas")
				}
			}
			out[r.NodeID] = st
		}
		return out
	}
	p, base := s.openKubeEnv(ctx, wf, env)
	objects := map[string]*kube.Object{}
	for _, o := range p.Objects {
		objects[o.ID] = o
	}
	for _, r := range run.Resources.Services {
		st := serviceSettings{}
		if o := objects[r.NodeID]; o != nil {
			st.info.Dependency = o.Kind == "StatefulSet" || slices.ContainsFunc(podImages(o), func(img string) bool { return compose.DataCategory(img) != "" })
		}
		if es, err := kube.EnvSettings(p, base, wf, r.NodeID); err == nil {
			st.settings = es.Settings
			st.info.Replicas = intSetting(es.Settings, "replicas")
			for _, x := range es.Settings {
				if c, ok := strings.CutPrefix(x.Key, "cpu.request:"); ok && c != "" && st.info.CPURequest == 0 {
					if cores := capacity.ParseCPU(x.Value); cores > 0 {
						st.info.CPURequest = cores
					}
				}
			}
			if minR := intSetting(es.Settings, "hpa.min"); minR > 0 {
				st.info.HPA = &model.HPAPolicy{Min: minR, Max: intSetting(es.Settings, "hpa.max"), TargetCPU: intSetting(es.Settings, "hpa.cpu")}
			}
		}
		out[r.NodeID] = st
	}
	return out
}

func kindName(wf *model.Workflow) string {
	if wf.IsKubernetes() {
		return "kubernetes"
	}
	return "docker"
}

// podImages are the images of a workload's containers.
func podImages(o *kube.Object) []string {
	var out []string
	spec := kube.PodSpec(o)
	list, _ := spec["containers"].([]any)
	for _, c := range list {
		if m, ok := c.(map[string]any); ok {
			if img, ok := m["image"].(string); ok {
				out = append(out, img)
			}
		}
	}
	return out
}

func intSetting(list []model.EnvSetting, key string) int {
	for _, s := range list {
		if s.Key == key {
			n, _ := strconv.Atoi(strings.TrimSpace(s.Value))
			return n
		}
	}
	return 0
}
