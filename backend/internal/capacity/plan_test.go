package capacity

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// svcLoad describes a synthetic service: idle cores per replica, cores
// per req/s, limits and memory.
type svcLoad struct {
	name       string
	idle, cost float64
	cpuLimit   float64 // cores per replica, 0 = none (host's 8)
	replicas   int
	memIdle    int64
	memPerRPS  int64
	memLimit   int64
	target     bool
}

const mib = 1 << 20

// ramp builds a run that ramps linearly to peak over secs seconds; from
// degradeAt on (0 = never) p95 jumps 10× and errors climb.
func ramp(secs int, peak float64, degradeAt int, svcs ...svcLoad) *model.LoadTestRun {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Duration(secs) * time.Second)
	run := &model.LoadTestRun{
		ID: "run1", WorkflowID: "wf", Status: model.LoadPassed, StartedAt: start, EndedAt: &end,
		Spec:      model.LoadTestSpec{Name: "ramp"},
		Summary:   &model.LoadSummary{PeakRPS: peak, Stability: 0.1},
		Resources: &model.LoadResources{},
	}
	rps := func(t int) float64 { return math.Round(peak * float64(t) / float64(secs)) }
	for t := 1; t <= secs; t++ {
		s := model.LoadSample{T: float64(t), Target: rps(t), RPS: rps(t), P50: 5, P95: 20, P99: 30}
		if degradeAt > 0 && t >= degradeAt {
			s.P95, s.Errors = 400, int(rps(t)/5)
		}
		run.Timeline = append(run.Timeline, s)
	}
	for _, sv := range svcs {
		r := max(sv.replicas, 1)
		limit, limited := 8.0, false
		if sv.cpuLimit > 0 {
			limit, limited = sv.cpuLimit*float64(r), true
		}
		res := model.LoadServiceResources{
			Service: sv.name, NodeID: "service:" + sv.name, Target: sv.target,
			CPULimit: limit, CPULimited: limited, MemLimit: 8 << 30,
		}
		if sv.memLimit > 0 {
			res.MemLimit, res.MemLimited = sv.memLimit*int64(r), true
		}
		for t := 0; t <= secs; t++ {
			load := 0.0
			if t > 0 {
				load = rps(t)
			}
			cores := sv.idle*float64(r) + sv.cost*load
			res.Timeline = append(res.Timeline, model.ResourcePoint{
				T: float64(t), CPUPercent: cores * 100, Containers: r,
				MemBytes: (sv.memIdle + sv.memPerRPS*int64(load)) * int64(r),
			})
		}
		run.Resources.Services = append(run.Resources.Services, res)
	}
	return run
}

func plan(t *testing.T, run *model.LoadTestRun, target float64, kind string, services map[string]Service) *model.CapacityPlan {
	t.Helper()
	p, err := Plan(Input{WorkflowID: "wf", Kind: kind, Request: model.CapacityRequest{TargetRPS: target}, Run: run, Services: services, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func service(t *testing.T, p *model.CapacityPlan, name string) model.CapacityService {
	t.Helper()
	for _, s := range p.Services {
		if s.Service == name {
			return s
		}
	}
	t.Fatalf("no service %s in %+v", name, p.Services)
	return model.CapacityService{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestReplicasAddOneAndKeepTwo(t *testing.T) {
	for _, c := range []struct {
		raw  float64
		want int
	}{{0, 2}, {0.3, 2}, {1, 2}, {1.0000000001, 2}, {1.01, 3}, {2, 3}, {2.5, 4}, {9.99, 11}} {
		if got := Replicas(c.raw); got != c.want {
			t.Errorf("Replicas(%v) = %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestHPAFor(t *testing.T) {
	h := HPAFor(2.4, Replicas(2.4), 0.7)
	if h.Min != 3 || h.Max != 5 || h.TargetCPU != 70 {
		t.Fatalf("HPAFor(2.4) = %+v", h)
	}
	// Tiny loads still keep two pods and room for one more.
	h = HPAFor(0.2, Replicas(0.2), 0.7)
	if h.Min != 2 || h.Max != 3 {
		t.Fatalf("HPAFor(0.2) = %+v", h)
	}
	// Max never below the planned replicas.
	h = HPAFor(1.5, Replicas(1.5), 0.6)
	if h.Max < Replicas(1.5) || h.TargetCPU != 60 {
		t.Fatalf("HPAFor(1.5) = %+v", h)
	}
}

func TestReplicaCPU(t *testing.T) {
	for _, c := range []struct {
		limit, request float64
		single         bool
		want           float64
	}{
		{1, 0, false, 1},
		{0.33, 0, false, 0.35}, // rounded up to 50m
		{0, 0.05, false, MinReplicaCPU},
		{0, 0.6, false, 0.6},
		{0, 0, false, DefaultReplicaCPU},
		{2, 0, true, 1}, // single-threaded: one core
		{0.5, 0, true, 0.5},
	} {
		if got, _ := ReplicaCPU(c.limit, c.request, c.single); !near(got, c.want) {
			t.Errorf("ReplicaCPU(%v, %v, %v) = %v, want %v", c.limit, c.request, c.single, got, c.want)
		}
	}
}

func TestPlanScalesAppAndProjectsDependency(t *testing.T) {
	run := ramp(60, 200, 0,
		svcLoad{name: "api", idle: 0.02, cost: 0.001, cpuLimit: 1, memIdle: 60 * mib, memPerRPS: 0, memLimit: 256 * mib, target: true},
		svcLoad{name: "db", idle: 0.01, cost: 0.002, cpuLimit: 1, memIdle: 40 * mib, memLimit: 512 * mib},
		svcLoad{name: "metrics", idle: 0.03, cost: 0, memIdle: 100 * mib},
	)
	p := plan(t, run, 1000, "docker", map[string]Service{"service:db": {Dependency: true}})
	if p.MeasuredRPS != 200 || p.Extrapolation != 5 {
		t.Fatalf("measured %v, extrapolation %v", p.MeasuredRPS, p.Extrapolation)
	}
	api := service(t, p, "api")
	// One replica: 70% of 1 CPU minus 0.02 idle = 0.68 CPU for requests
	// at 0.001 CPU per req/s = 680 req/s; 1000/680 = 1.47 → 2 + 1 = 3.
	if api.Role != model.CapacityApp || !near(api.PerReplicaRPS, 680) || api.Replicas != 3 || api.CPULimit != "1" || api.HPA != nil {
		t.Fatalf("api = %+v", api)
	}
	if api.MemLimit != "128Mi" { // 60 MiB × 1.3 → at least 128 MiB
		t.Fatalf("api memory = %s", api.MemLimit)
	}
	db := service(t, p, "db")
	// 0.01 + 0.002 × 1000 = 2.01 CPU projected; at 70% → 2.871 → 2.9 CPU.
	if db.Role != model.CapacityDependency || db.Replicas != 1 || !near(db.ProjectedCPU, 2.01) || db.CPULimit != "2900m" {
		t.Fatalf("db = %+v", db)
	}
	if db.MemLimit != "512Mi" { // never below its current limit
		t.Fatalf("db memory = %s", db.MemLimit)
	}
	if !strings.Contains(db.Caveat, "Not measured beyond 200 req/s") {
		t.Fatalf("db caveat = %q", db.Caveat)
	}
	if !strings.Contains(strings.Join(p.Caveats, "\n"), "db needs about 2.01 CPU") {
		t.Fatalf("caveats = %v", p.Caveats)
	}
	m := service(t, p, "metrics")
	if m.Role != model.CapacityIdle || m.Replicas != 1 {
		t.Fatalf("metrics = %+v", m)
	}
	if p.Confidence != model.ConfidenceLow {
		t.Fatalf("confidence %s at 5× (%v)", p.Confidence, p.ConfidenceReasons)
	}
	if len(p.Clouds) != len(Clouds) {
		t.Fatalf("clouds = %+v caveats %v", p.Clouds, p.Caveats)
	}
	// Totals: 3 × 1 + 2.9 + metrics.
	if p.TotalCPU < 5.9 {
		t.Fatalf("total CPU %v", p.TotalCPU)
	}
	for _, c := range p.Clouds {
		if c.Recommended.Nodes < MinNodes || c.Recommended.MonthlyUSD <= 0 || c.Recommended.MonthlyUSD != c.Options[0].MonthlyUSD {
			t.Fatalf("%s = %+v", c.Cloud, c.Recommended)
		}
	}
}

func TestPlanUsesHealthySecondsOnly(t *testing.T) {
	// Degrades from second 41 at about 133 req/s: the plan sizes from
	// the 40 seconds before.
	run := ramp(60, 200, 41, svcLoad{name: "api", idle: 0.02, cost: 0.004, cpuLimit: 1, memIdle: 64 * mib, target: true})
	p := plan(t, run, 140, "docker", nil)
	if p.Run.HealthySec != 40 || !near(p.MeasuredRPS, 133) {
		t.Fatalf("healthy %d, measured %v", p.Run.HealthySec, p.MeasuredRPS)
	}
	if p.Confidence != model.ConfidenceHigh {
		t.Fatalf("confidence %s (%v)", p.Confidence, p.ConfidenceReasons)
	}
}

func TestConfidenceBoundaries(t *testing.T) {
	run := ramp(60, 200, 0, svcLoad{name: "api", idle: 0.02, cost: 0.002, cpuLimit: 1, memIdle: 64 * mib, target: true})
	for _, c := range []struct {
		target float64
		want   string
	}{
		{300, model.ConfidenceHigh},   // 1.5×
		{301, model.ConfidenceMedium}, // just above 1.5×
		{800, model.ConfidenceMedium}, // 4×
		{801, model.ConfidenceLow},    // just above 4×
		{100, model.ConfidenceHigh},   // below what was measured
	} {
		if got := plan(t, run, c.target, "docker", nil).Confidence; got != c.want {
			t.Errorf("target %v: confidence %s, want %s", c.target, got, c.want)
		}
	}
	short := ramp(20, 200, 0, svcLoad{name: "api", idle: 0.02, cost: 0.002, cpuLimit: 1, memIdle: 64 * mib, target: true})
	if got := plan(t, short, 200, "docker", nil).Confidence; got != model.ConfidenceMedium {
		t.Errorf("20 healthy seconds: %s", got)
	}
}

func TestPerReplicaStretchLowersConfidence(t *testing.T) {
	// 0.0001 CPU per req/s: one 1-CPU replica would serve 6800 req/s
	// while it only saw 200 in the run.
	run := ramp(60, 200, 0, svcLoad{name: "api", idle: 0.02, cost: 0.0001, cpuLimit: 1, memIdle: 64 * mib, target: true})
	p := plan(t, run, 250, "docker", nil)
	if p.Confidence != model.ConfidenceMedium || service(t, p, "api").Caveat == "" {
		t.Fatalf("confidence %s, caveat %q", p.Confidence, service(t, p, "api").Caveat)
	}
}

func TestKubernetesHPAFollowsCeiling(t *testing.T) {
	// No CPU limit, 50m request, HPA at 80%: replicas are sized at the
	// 250m minimum with an 80% ceiling.
	run := ramp(60, 100, 0, svcLoad{name: "web", idle: 0.001, cost: 0.0005, memIdle: 10 * mib, target: true})
	hpa := &model.HPAPolicy{Name: "web", Min: 2, Max: 5, TargetCPU: 80}
	p := plan(t, run, 1000, "kubernetes", map[string]Service{"service:web": {CPURequest: 0.05, HPA: hpa}})
	web := service(t, p, "web")
	// (0.8 × 0.25 − 0.001) / 0.0005 = 398 req/s → 1000/398 = 2.51 → 4.
	if web.CPURequest != "250m" || !near(web.PerReplicaRPS, 398) || web.Replicas != 4 {
		t.Fatalf("web = %+v", web)
	}
	if web.HPA == nil || web.HPA.Min != 3 || web.HPA.Max != 6 || web.HPA.TargetCPU != 80 || web.HPA.Name != "web" {
		t.Fatalf("hpa = %+v", web.HPA)
	}
}

func TestLatencyObjectiveCapsPerReplicaRate(t *testing.T) {
	run := ramp(60, 200, 0, svcLoad{name: "api", idle: 0.02, cost: 0.001, cpuLimit: 1, memIdle: 64 * mib, target: true})
	// p95 rises above 50 ms from second 31 (100 req/s) on.
	for i := range run.Timeline {
		if run.Timeline[i].T > 30 {
			run.Timeline[i].P95 = 60
		}
	}
	p, err := Plan(Input{Kind: "docker", Request: model.CapacityRequest{TargetRPS: 400, P95Ms: 50}, Run: run, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	api := service(t, p, "api")
	if api.Limit != "latency" || !near(api.PerReplicaRPS, 100) || api.Replicas != 5 {
		t.Fatalf("api = %+v", api)
	}
	p, err = Plan(Input{Kind: "docker", Request: model.CapacityRequest{TargetRPS: 400, P95Ms: 5}, Run: run, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if p.Confidence != model.ConfidenceLow || service(t, p, "api").Limit != "cpu" {
		t.Fatalf("unmet objective: %s %+v", p.Confidence, service(t, p, "api"))
	}
}

func TestPlanRejectsUnusableRuns(t *testing.T) {
	if _, err := Plan(Input{Request: model.CapacityRequest{TargetRPS: 10}, Run: &model.LoadTestRun{}}); err != ErrNoMeasurements {
		t.Fatalf("no resources: %v", err)
	}
	run := ramp(10, 100, 1, svcLoad{name: "api", idle: 0.02, cost: 0.001, cpuLimit: 1, memIdle: 64 * mib})
	if _, err := Plan(Input{Request: model.CapacityRequest{TargetRPS: 10}, Run: run}); err != ErrNoHealthy {
		t.Fatalf("degraded at once: %v", err)
	}
	if _, err := Plan(Input{Request: model.CapacityRequest{TargetRPS: 0}, Run: run}); err == nil {
		t.Fatal("zero target accepted")
	}
}

func TestMemoryGrowthExtrapolatedAndCapped(t *testing.T) {
	m := measured{idleMemPer: 100 * mib, peakMemPer: 200 * mib}
	// Same per-replica load: (100 + 100) × 1.3 = 260 → 320 MiB.
	if got := appMemory(m, 50, 50); got != 320*mib {
		t.Fatalf("same load: %d MiB", got/mib)
	}
	// Twice the per-replica load: (100 + 200) × 1.3 = 390 → 448 MiB.
	if got := appMemory(m, 100, 50); got != 448*mib {
		t.Fatalf("2×: %d MiB", got/mib)
	}
	// Growth capped at 4×: (100 + 400) × 1.3 = 650 → 704 MiB.
	if got := appMemory(m, 1000, 50); got != 704*mib {
		t.Fatalf("capped: %d MiB", got/mib)
	}
}

func TestQuantities(t *testing.T) {
	for in, want := range map[float64]string{0.25: "250m", 1: "1", 2.9: "2900m", 0.05: "50m"} {
		if got := CPUQuantity(in); got != want {
			t.Errorf("CPUQuantity(%v) = %s", in, got)
		}
	}
	for in, want := range map[int64]string{128 * mib: "128Mi", 1 << 30: "1Gi", 1536 * mib: "1536Mi"} {
		if got := MemQuantity(in); got != want {
			t.Errorf("MemQuantity(%d) = %s", in, got)
		}
	}
}
