package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// plan_capacity: POST /workflows/{id}/capacity-plan.

func registerCapacityTools(r *registry, t *tools) {
	r.group = groupLoadTests
	addTool(r, "plan_capacity", "Plan capacity",
		"Size a docker or Kubernetes workflow for a target request rate from a finished load test of one environment (default: the active environment's newest finished run with container usage, fault-scenario runs excluded). "+
			"Only the run's healthy seconds (before its breaking point) count: per service it measures CPU at idle and per req/s and the peak memory per replica, then plans replicas = ⌈target ÷ per-replica rate at a 70% CPU ceiling (or the workload's HPA target)⌉ + 1 (at least 2), requests = limits per replica, an HPA min/max for Kubernetes apps, "+
			"and keeps databases/caches/brokers at their instance count, projecting their CPU with an explicit 'not measured beyond X req/s' caveat. "+
			"Returns per-service rows, total vCPU/GiB, the cheapest node shape and node count per cloud (AWS, Azure, GCP, OCI; ARM and x86 shapes, first-fit packing after a system reserve, at least 2 nodes) with an estimated monthly on-demand compute cost from a pinned, dated price table (the date and regions are in prices), "+
			"a confidence (high/medium/low from how far the target is beyond the measured rate and the run's quality) with reasons, assumptions and caveats. "+
			"The plan is kept as the environment's latest (production_readiness reads it); the user applies it to the environment from the app's Load test → Capacity view. "+
			"Run a load test first (e.g. run_load_test preset load or a ~60 s ramp) — the closer its peak is to the target, the higher the confidence.",
		t.planCapacity)
}

type planCapacityIn struct {
	Workflow    string  `json:"workflow,omitempty" jsonschema:"docker or Kubernetes workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	TargetRPS   float64 `json:"target_rps" jsonschema:"peak requests per second to plan for"`
	P95Ms       float64 `json:"p95_ms,omitempty" jsonschema:"optional p95 latency objective in ms: caps each replica at the highest rate the run served within it"`
	Environment string  `json:"environment,omitempty" jsonschema:"environment to plan (default: the active one)"`
	RunID       string  `json:"run_id,omitempty" jsonschema:"load test run to size from (default: the newest finished run of the environment with container usage)"`
}

type capacityServiceView struct {
	Service       string            `json:"service"`
	Node          string            `json:"node"`
	Role          string            `json:"role"`
	Replicas      int               `json:"replicas"`
	ReplicasRaw   float64           `json:"replicas_raw"`
	PerReplicaRPS float64           `json:"per_replica_rps,omitempty"`
	Limit         string            `json:"limited_by,omitempty"`
	CPU           string            `json:"cpu"`
	Memory        string            `json:"memory"`
	HPA           *model.HPAPolicy  `json:"hpa,omitempty"`
	ProjectedCPU  float64           `json:"projected_cpu"`
	Note          string            `json:"note"`
	Caveat        string            `json:"caveat,omitempty"`
	Apply         map[string]string `json:"apply,omitempty"`
}

type capacityCloudView struct {
	Cloud      string  `json:"cloud"`
	Region     string  `json:"region"`
	Shape      string  `json:"shape"`
	Arch       string  `json:"arch"`
	Nodes      int     `json:"nodes"`
	MonthlyUSD float64 `json:"monthly_usd"`
	CPUUsed    float64 `json:"cpu_used"`
	MemUsed    float64 `json:"mem_used"`
	Cheapest   string  `json:"cheapest_other_arch,omitempty"`
}

func (t *tools) planCapacity(ctx context.Context, in planCapacityIn) (any, error) {
	if in.TargetRPS <= 0 {
		return nil, errors.New("target_rps must be a positive number")
	}
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if !wf.IsDocker() && !wf.IsKubernetes() {
		return nil, fmt.Errorf("%s is a %s workflow; plan_capacity is for docker and Kubernetes workflows", wf.Name, model.KindLabel(wf.Kind))
	}
	var p model.CapacityPlan
	body := model.CapacityRequest{TargetRPS: in.TargetRPS, P95Ms: in.P95Ms, Environment: in.Environment, RunID: in.RunID}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/capacity-plan", body, &p); err != nil {
		return nil, err
	}
	services := make([]capacityServiceView, 0, len(p.Services))
	for _, s := range p.Services {
		v := capacityServiceView{
			Service: s.Service, Node: s.NodeID, Role: s.Role, Replicas: s.Replicas, ReplicasRaw: s.ReplicasRaw,
			PerReplicaRPS: s.PerReplicaRPS, Limit: s.Limit, CPU: s.CPURequest, Memory: s.MemRequest, HPA: s.HPA,
			ProjectedCPU: s.ProjectedCPU, Note: s.Note, Caveat: s.Caveat,
		}
		if s.Apply != nil && len(s.Apply.Values) > 0 {
			v.Apply = s.Apply.Values
		}
		services = append(services, v)
	}
	clouds := make([]capacityCloudView, 0, len(p.Clouds))
	for _, c := range p.Clouds {
		r := c.Recommended
		v := capacityCloudView{Cloud: c.Cloud, Region: c.Region, Shape: r.Shape, Arch: r.Arch, Nodes: r.Nodes, MonthlyUSD: r.MonthlyUSD, CPUUsed: r.CPUUsed, MemUsed: r.MemUsed}
		for _, o := range c.Options {
			if o.Arch != r.Arch {
				v.Cheapest = fmt.Sprintf("%s ×%d $%.2f/month", o.Shape, o.Nodes, o.MonthlyUSD)
				break
			}
		}
		clouds = append(clouds, v)
	}
	return map[string]any{
		"workflow": p.WorkflowID, "kind": p.Kind, "environment": p.Environment,
		"run":                p.Run,
		"target_rps":         p.TargetRPS,
		"measured_rps":       p.MeasuredRPS,
		"extrapolation":      p.Extrapolation,
		"confidence":         p.Confidence,
		"confidence_reasons": p.ConfidenceReasons,
		"headroom_rule":      p.HeadroomRule,
		"services":           services,
		"total_cpu":          p.TotalCPU,
		"total_mem_gib":      p.TotalMemGiB,
		"clouds":             clouds,
		"prices":             p.Prices,
		"assumptions":        p.Assumptions,
		"caveats":            p.Caveats,
		"applicable":         p.Applicable,
		"apply_note":         p.ApplyNote,
	}, nil
}
