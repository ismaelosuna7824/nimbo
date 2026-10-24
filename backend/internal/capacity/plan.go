// Package capacity plans replicas, requests/limits, nodes and monthly cost
// for a target request rate from what a finished load test of a docker or
// Kubernetes workflow measured. The math is pure (no I/O besides the plan
// store in store.go) so every rule is tested on its boundaries.
package capacity

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Planning rules.
const (
	// DefaultCeiling is the CPU fraction of a replica the plan runs it at
	// under the target load: the remaining 30% absorbs bursts and the
	// time an autoscaler needs to add pods. A workload's HPA CPU target
	// replaces it (clamped to [MinCeiling, MaxCeiling]).
	DefaultCeiling = 0.7
	MinCeiling     = 0.5
	MaxCeiling     = 0.85

	// DefaultReplicaCPU sizes replicas of services without a CPU limit
	// (or request); MinReplicaCPU is the smallest replica the plan
	// suggests for one.
	DefaultReplicaCPU = 0.5
	MinReplicaCPU     = 0.25
	cpuStep           = 0.05 // 50m

	// Memory: apps get their peak working set (extrapolated to their
	// planned load) plus 30%; data services keep at least their current
	// limit and get their peak plus 50% (caches and buffers grow).
	appMemMargin = 1.3
	depMemMargin = 1.5
	memStep      = 64 << 20
	minAppMem    = 128 << 20
	minDepMem    = 256 << 20
	// maxMemGrowth caps the linear extrapolation of memory growth.
	maxMemGrowth = 4.0

	// topBand: the cost of a request is read from the healthy seconds
	// within 80% of the highest healthy rate (where usage is well above
	// the noise of idle).
	topBand = 0.8
	// idleGrowth: a service whose CPU grew by less than this many cores
	// at the measured rate is not on the request path.
	idleGrowth = 0.01

	// Confidence boundaries: extrapolation factor and healthy seconds.
	highExtrapolation   = 1.5
	mediumExtrapolation = 4.0
	goodHealthySec      = 30
	minHealthySec       = 10
	unstable            = 0.5
	// perReplicaStretch: per-replica throughput more than this many times
	// what one replica served in the run is read off a short line.
	perReplicaStretch = 3.0
)

// HeadroomRule explains how replicas are rounded.
const HeadroomRule = "Replicas = ⌈target ÷ per-replica rate at the CPU ceiling⌉ + 1 (at least 2): the ceiling leaves about 30% CPU for bursts and the extra replica keeps the target served while one replica is lost, restarted or rolled out."

// Errors of Plan.
var (
	ErrNoMeasurements = errors.New("the run recorded no CPU/memory usage: run a load test against the workflow's own containers or cluster")
	ErrNoHealthy      = errors.New("the run degraded from its first seconds: nothing healthy to size from — run a gentler ramp")
)

// Service is what the planner knows about a measured service besides its
// usage: its role and its settings in the plan's environment.
type Service struct {
	// Dependency: a database, cache or broker (by image, or a
	// StatefulSet).
	Dependency bool
	// Replicas is the replica setting in the environment (0 = unknown).
	Replicas int
	// CPURequest: Kubernetes cores requested per replica (0 = none).
	CPURequest float64
	// HPA: the workload's autoscaler in the environment.
	HPA *model.HPAPolicy
}

// Input is everything Plan needs.
type Input struct {
	WorkflowID  string
	Kind        string // docker | kubernetes
	Environment string
	Request     model.CapacityRequest
	Run         *model.LoadTestRun
	// Services by graph node ID (missing: an app with no settings).
	Services map[string]Service
	Now      time.Time
}

// measured is one service's usage over the healthy seconds.
type measured struct {
	replicas    int
	idlePer     float64 // cores per replica at idle
	peakPer     float64 // cores per replica over the top band
	cost        float64 // cores per req/s (all replicas)
	limitPer    float64 // cores per replica (0: no limit)
	memLimitPer int64
	idleMemPer  int64
	peakMemPer  int64
	singleCore  bool
	troubled    bool // restarted or OOM-killed during the run
}

// Plan sizes in.Run's services for in.Request.TargetRPS.
func Plan(in Input) (*model.CapacityPlan, error) {
	req := in.Request
	if req.TargetRPS <= 0 || math.IsNaN(req.TargetRPS) || math.IsInf(req.TargetRPS, 0) {
		return nil, errors.New("targetRps must be a positive number")
	}
	if req.P95Ms < 0 {
		return nil, errors.New("p95Ms must be positive when set")
	}
	run := in.Run
	if run == nil || run.Resources == nil || !slices.ContainsFunc(run.Resources.Services, func(s model.LoadServiceResources) bool { return len(s.Timeline) > 0 }) {
		return nil, ErrNoMeasurements
	}

	// Healthy seconds: before the first degraded one, with traffic.
	cut := loadtest.FirstDegraded(run.Timeline)
	healthy := run.Timeline
	if cut >= 0 {
		healthy = run.Timeline[:cut]
	}
	rpsAt := map[float64]float64{}
	measuredRPS, latencyRPS := 0.0, 0.0
	for _, s := range healthy {
		if s.RPS <= 0 {
			continue
		}
		rpsAt[s.T] = s.RPS
		measuredRPS = math.Max(measuredRPS, s.RPS)
		if req.P95Ms > 0 && s.P95 > 0 && s.P95 <= req.P95Ms {
			latencyRPS = math.Max(latencyRPS, s.RPS)
		}
	}
	if measuredRPS == 0 {
		return nil, ErrNoHealthy
	}
	k := req.TargetRPS / measuredRPS

	plan := &model.CapacityPlan{
		WorkflowID: in.WorkflowID, Kind: in.Kind, Environment: in.Environment, CreatedAt: in.Now.UTC(),
		Run:       runInfo(run, len(rpsAt)),
		TargetRPS: req.TargetRPS, P95Ms: req.P95Ms,
		MeasuredRPS: round2(measuredRPS), Extrapolation: round2(k),
		CPUCeiling: DefaultCeiling, HeadroomRule: HeadroomRule,
		Services: []model.CapacityService{}, Clouds: []model.CloudEstimate{},
		Prices: PriceInfo(), Assumptions: assumptions(in.Kind), Caveats: []string{},
		ConfidenceReasons: []string{},
	}
	conf := newConfidence()
	switch {
	case k > mediumExtrapolation:
		conf.lower(model.ConfidenceLow, fmt.Sprintf("The target is %s× the highest healthy rate measured (%s req/s): far beyond the run.", trim(k, 1), trim(measuredRPS, 1)))
	case k > highExtrapolation:
		conf.lower(model.ConfidenceMedium, fmt.Sprintf("The target is %s× the highest healthy rate measured (%s req/s).", trim(k, 1), trim(measuredRPS, 1)))
	default:
		conf.note(fmt.Sprintf("The target is within %s× of the highest healthy rate measured (%s req/s).", trim(math.Max(k, 0), 2), trim(measuredRPS, 1)))
	}
	switch hs := len(rpsAt); {
	case hs < minHealthySec:
		conf.lower(model.ConfidenceLow, fmt.Sprintf("Only %d healthy seconds of load were recorded.", hs))
	case hs < goodHealthySec:
		conf.lower(model.ConfidenceMedium, fmt.Sprintf("Only %d healthy seconds of load were recorded (30+ is better).", hs))
	}
	if run.Summary != nil && run.Summary.Stability > unstable {
		conf.lower(model.ConfidenceMedium, fmt.Sprintf("Latency was unsteady during the run (variation %d%%).", int(math.Round(run.Summary.Stability*100))))
	}
	if req.P95Ms > 0 && latencyRPS == 0 {
		conf.lower(model.ConfidenceLow, fmt.Sprintf("p95 never stayed within %s ms, even at low load: more replicas won't fix latency the service has when idle.", trim(req.P95Ms, 0)))
		plan.Caveats = append(plan.Caveats, fmt.Sprintf("The p95 objective of %s ms was not met at any measured rate; the plan sizes for throughput only.", trim(req.P95Ms, 0)))
	}

	verdicts := map[string]string{}
	for _, v := range run.Resources.Verdicts {
		verdicts[v.Service] = v.Kind
	}
	var items []Item
	for _, r := range run.Resources.Services {
		if len(r.Timeline) == 0 {
			continue
		}
		m := measure(r, rpsAt, measuredRPS, verdicts[r.Service])
		info := in.Services[r.NodeID]
		svc := model.CapacityService{
			NodeID: r.NodeID, Service: r.Service, Target: r.Target,
			MeasuredReplicas: m.replicas, MeasuredRPS: round2(measuredRPS),
			IdleCPU: round3(m.idlePer), PeakCPU: round3(m.peakPer), CPUPerRPS: round6(m.cost),
			PeakMemBytes: m.peakMemPer, CurrentCPULimit: round3(m.limitPer), CurrentMemLimit: m.memLimitPer,
			CurrentReplicas: info.Replicas, CurrentHPA: info.HPA,
		}
		if m.troubled {
			conf.lower(model.ConfidenceLow, r.Service+" restarted or ran out of memory during the run: its numbers are incomplete.")
		}
		var cpu float64 // cores per replica
		var mem int64   // bytes per replica
		switch {
		case info.Dependency:
			cpu, mem = planDependency(&svc, m, req.TargetRPS, measuredRPS, plan, conf)
		case m.cost*measuredRPS < idleGrowth:
			cpu, mem = planIdle(&svc, m)
		default:
			cpu, mem = planApp(&svc, m, info, in.Kind, req, latencyRPS, measuredRPS, conf)
		}
		svc.CPURequest, svc.CPULimit = CPUQuantity(cpu), CPUQuantity(cpu)
		svc.MemRequest, svc.MemLimit = MemQuantity(mem), MemQuantity(mem)
		memGiB := float64(mem) / (1 << 30)
		svc.TotalCPU = round2(cpu * float64(svc.Replicas))
		svc.TotalMemGiB = round2(memGiB * float64(svc.Replicas))
		plan.TotalCPU += cpu * float64(svc.Replicas)
		plan.TotalMemGiB += memGiB * float64(svc.Replicas)
		for range svc.Replicas {
			items = append(items, Item{CPU: cpu, MemGiB: memGiB})
		}
		plan.Services = append(plan.Services, svc)
	}
	plan.TotalCPU, plan.TotalMemGiB = round2(plan.TotalCPU), round2(plan.TotalMemGiB)
	for _, c := range Clouds {
		if est, ok := Estimate(c, items); ok {
			plan.Clouds = append(plan.Clouds, est)
		} else {
			plan.Caveats = append(plan.Caveats, fmt.Sprintf("No %s shape in the price table fits the largest replica: split it or pick a bigger node.", strings.ToUpper(c)))
		}
	}
	plan.Confidence, plan.ConfidenceReasons = conf.level, conf.reasons
	return plan, nil
}

// measure reads one service's usage over the healthy seconds.
func measure(r model.LoadServiceResources, rpsAt map[float64]float64, measuredRPS float64, verdict string) measured {
	m := measured{singleCore: verdict == "single-core", troubled: r.Restarts > 0 || r.OOMKilled}
	replicas := func(p model.ResourcePoint) float64 { return float64(max(p.Containers, 1)) }
	// Idle: the baseline before load (T 0), else the quietest second.
	idleSet := false
	for _, p := range r.Timeline {
		per := p.CPUPercent / 100 / replicas(p)
		if p.T == 0 {
			m.idlePer, m.idleMemPer, idleSet = per, p.MemBytes/int64(replicas(p)), true
			break
		}
	}
	if !idleSet {
		m.idlePer, m.idleMemPer = math.Inf(1), math.MaxInt64
		for _, p := range r.Timeline {
			m.idlePer = math.Min(m.idlePer, p.CPUPercent/100/replicas(p))
			m.idleMemPer = min(m.idleMemPer, p.MemBytes/int64(replicas(p)))
		}
	}
	last := r.Timeline[len(r.Timeline)-1]
	if r.CPULimited {
		m.limitPer = r.CPULimit / replicas(last)
	}
	if r.MemLimited {
		m.memLimitPer = r.MemLimit / int64(replicas(last))
	}
	for _, p := range r.Timeline {
		m.peakMemPer = max(m.peakMemPer, p.MemBytes/int64(replicas(p)))
	}
	// Cost of a request: the mean over the top band of (CPU above idle)
	// per req/s.
	var costSum, peakSum float64
	n := 0
	for _, p := range r.Timeline {
		rps, ok := rpsAt[p.T]
		if !ok || rps < topBand*measuredRPS {
			continue
		}
		cores := p.CPUPercent / 100
		costSum += (cores - m.idlePer*replicas(p)) / rps
		peakSum += cores / replicas(p)
		m.replicas = max(m.replicas, int(replicas(p)))
		n++
	}
	if n > 0 {
		m.cost = math.Max(costSum/float64(n), 0)
		m.peakPer = peakSum / float64(n)
	} else {
		m.peakPer = m.idlePer
	}
	if m.replicas == 0 {
		m.replicas = int(replicas(last))
	}
	return m
}

// planApp sizes a horizontally scaled service; it returns the CPU (cores)
// and memory (bytes) of one replica.
func planApp(svc *model.CapacityService, m measured, info Service, kind string, req model.CapacityRequest, latencyRPS, measuredRPS float64, conf *confidence) (float64, int64) {
	svc.Role = model.CapacityApp
	ceiling := DefaultCeiling
	if info.HPA != nil && info.HPA.TargetCPU > 0 {
		ceiling = clamp(float64(info.HPA.TargetCPU)/100, MinCeiling, MaxCeiling)
	}
	svc.CPUCeiling = ceiling
	per, why := ReplicaCPU(m.limitPer, info.CPURequest, m.singleCore)
	// A replica must keep room above its idle usage.
	per = math.Max(per, ceilTo(2*m.idlePer/ceiling, cpuStep))
	usable := ceiling*per - m.idlePer
	perRPS := usable / m.cost
	svc.Limit = "cpu"
	if req.P95Ms > 0 && svc.Target && latencyRPS > 0 {
		if capRPS := latencyRPS / float64(m.replicas); capRPS < perRPS {
			perRPS, svc.Limit = capRPS, "latency"
		}
	}
	svc.PerReplicaRPS = round2(perRPS)
	raw := req.TargetRPS / perRPS
	svc.ReplicasRaw = round2(raw)
	svc.Replicas = Replicas(raw)
	svc.ProjectedCPU = round2(m.idlePer*float64(svc.Replicas) + m.cost*req.TargetRPS)
	mem := appMemory(m, req.TargetRPS/float64(svc.Replicas), measuredRPS/float64(m.replicas))
	if kind == "kubernetes" {
		svc.HPA = HPAFor(raw, svc.Replicas, ceiling)
		if info.HPA != nil {
			svc.HPA.Name = info.HPA.Name
		}
	}
	measuredPer := measuredRPS / float64(m.replicas)
	limitText := fmt.Sprintf("%s of %s", pctText(ceiling), cpuText(per))
	if svc.Limit == "latency" {
		limitText = fmt.Sprintf("p95 ≤ %s ms", trim(req.P95Ms, 0))
	}
	svc.Note = fmt.Sprintf("Used %s idle and %s at %s req/s per replica (%s per 100 req/s); one replica serves about %s req/s at %s%s. %s replicas needed → %d with headroom.",
		cpuText(m.idlePer), cpuText(m.peakPer), trim(measuredPer, 1), cpuText(m.cost*100), trim(perRPS, 0), limitText, why, trim(raw, 2), svc.Replicas)
	if stretch := perRPS / measuredPer; stretch > perReplicaStretch {
		conf.lower(model.ConfidenceMedium, fmt.Sprintf("%s's per-replica rate is extrapolated %s× beyond what one replica served.", svc.Service, trim(stretch, 1)))
		svc.Caveat = fmt.Sprintf("One replica served at most %s req/s in the run; %s req/s per replica assumes CPU keeps growing linearly with traffic.", trim(measuredPer, 1), trim(perRPS, 0))
	}
	return per, mem
}

// planDependency projects the load of a database, cache or broker: they
// don't scale by adding replicas, so the plan keeps their replicas and
// sizes them vertically for the projected CPU (never below today's
// limits).
func planDependency(svc *model.CapacityService, m measured, target, measuredRPS float64, plan *model.CapacityPlan, conf *confidence) (float64, int64) {
	svc.Role = model.CapacityDependency
	svc.CPUCeiling = DefaultCeiling
	svc.Replicas = max(m.replicas, 1)
	svc.ReplicasRaw = float64(svc.Replicas)
	total := m.idlePer*float64(svc.Replicas) + m.cost*target
	svc.ProjectedCPU = round2(total)
	per := ceilTo(total/float64(svc.Replicas)/DefaultCeiling, cpuStep)
	per = math.Max(per, MinReplicaCPU)
	if m.limitPer > 0 {
		per = math.Max(per, m.limitPer) // never sized down
	}
	mem := max(ceilBytes(float64(m.peakMemPer)*depMemMargin), m.memLimitPer, minDepMem)
	measuredCores := m.idlePer*float64(m.replicas) + m.cost*measuredRPS
	svc.Note = fmt.Sprintf("Used %s at %s req/s; at %s req/s its load is projected to %s (%s× the traffic, same instance count).",
		cpuText(measuredCores), trim(measuredRPS, 1), trim(target, 0), cpuText(total), trim(target/measuredRPS, 1))
	if target > measuredRPS {
		svc.Caveat = fmt.Sprintf("Not measured beyond %s req/s: scaling the app multiplies %s's queries, connections and locks, which rarely grow linearly. Validate with a test closer to %s req/s.", trim(measuredRPS, 1), svc.Service, trim(target, 0))
		if m.limitPer > 0 && total/float64(svc.Replicas) > DefaultCeiling*m.limitPer {
			conf.lower(model.ConfidenceMedium, fmt.Sprintf("%s's projected CPU is above %s of its current %s limit.", svc.Service, pctText(DefaultCeiling), cpuText(m.limitPer)))
			plan.Caveats = append(plan.Caveats, fmt.Sprintf("%s needs about %s at the target, above %s of its %s limit: give it %s, add read replicas or put a cache in front — it does not scale out like the app.", svc.Service, cpuText(total), pctText(DefaultCeiling), cpuText(m.limitPer), cpuText(per)))
		} else {
			plan.Caveats = append(plan.Caveats, fmt.Sprintf("%s: not measured beyond %s req/s (target %s req/s).", svc.Service, trim(measuredRPS, 1), trim(target, 0)))
		}
	}
	return per, mem
}

// planIdle keeps a service whose CPU did not follow the traffic.
func planIdle(svc *model.CapacityService, m measured) (float64, int64) {
	svc.Role = model.CapacityIdle
	svc.CPUCeiling = DefaultCeiling
	svc.Replicas = max(m.replicas, 1)
	svc.ReplicasRaw = float64(svc.Replicas)
	svc.Limit = "none"
	per := m.limitPer
	if per == 0 {
		per = math.Max(MinReplicaCPU, ceilTo(m.peakPer/DefaultCeiling, cpuStep))
	}
	svc.ProjectedCPU = round2(m.peakPer * float64(svc.Replicas))
	mem := max(ceilBytes(float64(m.peakMemPer)*appMemMargin), minAppMem)
	svc.Note = fmt.Sprintf("CPU did not grow with the traffic (%s at peak): not on the request path, keeps %d replica(s).", cpuText(m.peakPer), svc.Replicas)
	return per, mem
}

// ReplicaCPU is the CPU (cores) a replica is sized with: its current
// limit, else its Kubernetes request (at least MinReplicaCPU), else
// DefaultReplicaCPU; a single-threaded runtime never gets more than one
// core (replicas, not a bigger limit, add throughput). why explains a
// non-obvious choice.
func ReplicaCPU(limit, request float64, singleCore bool) (cores float64, why string) {
	switch {
	case limit > 0:
		cores = limit
	case request > 0:
		cores = math.Max(request, MinReplicaCPU)
		why = " (sized from its CPU request; it has no limit)"
	default:
		cores, why = DefaultReplicaCPU, " (no CPU limit: sized at half a core per replica)"
	}
	if singleCore && cores > 1 {
		cores, why = 1, " (single-threaded: one core per replica)"
	}
	return ceilTo(cores, cpuStep), why
}

// Replicas rounds a raw replica count up and adds one (N+1), at least 2.
func Replicas(raw float64) int {
	return max(int(math.Ceil(raw-1e-9))+1, 2)
}

// HPAFor is the autoscaler of an app planned at replicas for raw: the
// minimum serves the target at the ceiling without waiting for a scale-up
// (at least 2), the maximum leaves room for twice the target and never
// below the planned replicas; the CPU target is the ceiling.
func HPAFor(raw float64, replicas int, ceiling float64) *model.HPAPolicy {
	minR := max(int(math.Ceil(raw-1e-9)), 2)
	maxR := max(int(math.Ceil(2*raw-1e-9)), replicas, minR+1)
	return &model.HPAPolicy{Min: minR, Max: maxR, TargetCPU: int(math.Round(ceiling * 100))}
}

// appMemory is a replica's memory at its planned load: idle plus the
// measured growth scaled to the planned per-replica rate (growth capped
// at maxMemGrowth×), plus the margin, rounded up.
func appMemory(m measured, plannedPer, measuredPer float64) int64 {
	growth := float64(max(m.peakMemPer-m.idleMemPer, 0))
	if measuredPer > 0 && plannedPer > measuredPer {
		growth *= math.Min(plannedPer/measuredPer, maxMemGrowth)
	}
	need := float64(m.idleMemPer) + growth
	return max(ceilBytes(need*appMemMargin), minAppMem)
}

// ---------------------------------------------------------------------------
// Confidence

type confidence struct {
	level   string
	reasons []string
}

func newConfidence() *confidence {
	return &confidence{level: model.ConfidenceHigh, reasons: []string{}}
}

var confRank = map[string]int{model.ConfidenceHigh: 2, model.ConfidenceMedium: 1, model.ConfidenceLow: 0}

func (c *confidence) lower(to, reason string) {
	if confRank[to] < confRank[c.level] {
		c.level = to
	}
	c.reasons = append(c.reasons, reason)
}

func (c *confidence) note(reason string) { c.reasons = append(c.reasons, reason) }

// ---------------------------------------------------------------------------

func runInfo(run *model.LoadTestRun, healthySec int) model.CapacityRun {
	out := model.CapacityRun{ID: run.ID, Name: run.Spec.Name, StartedAt: run.StartedAt, HealthySec: healthySec}
	if run.EndedAt != nil {
		out.DurationSec = int(run.EndedAt.Sub(run.StartedAt).Seconds())
	}
	if s := run.Summary; s != nil {
		out.PeakRPS, out.BreakingPoint, out.Stability, out.ErrorRate, out.P95Ms = s.PeakRPS, s.BreakingPoint, round3(s.Stability), round3(s.ErrorRate), s.Latency.P95
	}
	return out
}

func assumptions(kind string) []string {
	out := []string{
		"CPU per request is read from the run's healthy seconds (before its breaking point) and assumed linear in traffic.",
		"Requests equal limits per replica, so the nodes reserve what each replica may use; " + HeadroomRule,
		"Nodes keep a system reserve (progressive kube-reserved formula plus 0.1 CPU / 256 MiB for per-node agents); at least 2 nodes so one failure never takes every replica.",
		"Databases, caches and brokers keep their instance count and are sized vertically; their load is projected, not measured.",
	}
	if kind == "kubernetes" {
		out = append(out, "The HPA minimum serves the target without waiting for a scale-up; its maximum allows twice the target (needs spare nodes or a cluster autoscaler).")
	}
	return out
}

// CPUQuantity formats cores as a Kubernetes quantity ("250m", "1").
func CPUQuantity(cores float64) string {
	m := int64(math.Round(cores * 1000))
	if m%1000 == 0 {
		return strconv.FormatInt(m/1000, 10)
	}
	return strconv.FormatInt(m, 10) + "m"
}

// MemQuantity formats bytes as a Kubernetes quantity ("384Mi", "1Gi").
func MemQuantity(b int64) string {
	if b >= 1<<30 && b%(1<<30) == 0 {
		return strconv.FormatInt(b>>30, 10) + "Gi"
	}
	return strconv.FormatInt((b+(1<<20)-1)>>20, 10) + "Mi"
}

func ceilTo(v, step float64) float64 { return math.Ceil(v/step-1e-9) * step }

func ceilBytes(b float64) int64 { return int64(math.Ceil(b/memStep-1e-9)) * memStep }

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// trim formats v with at most prec decimals ("2", "2.5").
func trim(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// cpuText formats cores for sentences: "350m CPU" below one core,
// "1.5 CPU" from one core on.
func cpuText(c float64) string {
	if c < 1 {
		return strconv.Itoa(int(math.Round(c*1000))) + "m CPU"
	}
	return trim(c, 2) + " CPU"
}

func pctText(f float64) string { return strconv.Itoa(int(math.Round(f*100))) + "%" }
