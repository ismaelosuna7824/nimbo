package kubeconvert

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Sizing requests/limits from a load test of the docker workflow.
//
// The recommendation follows the Vertical Pod Autoscaler's recommender:
// CPU is compressible, so the request covers the 90th percentile of the
// per-second usage (VPA's target percentile) plus VPA's default 15% safety
// margin (--recommendation-margin-fraction), and the limit leaves headroom
// for bursts. Memory is not compressible (going over the limit is an OOM
// kill), so the request covers the peak working set with 20% margin and
// the limit 50%. See
// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
// and the VPA recommender's docs (autoscaler/vertical-pod-autoscaler).

const (
	cpuMargin    = 1.15 // VPA's default safety margin over the p90
	cpuBurst     = 1.5  // limit over the peak
	cpuLimitMult = 2.0  // limit at least twice the request (burst room)
	memMargin    = 1.2  // request over the peak working set
	memHeadroom  = 1.5  // limit over the peak working set
	minCPU       = 0.05 // 50m
	minMem       = 64 << 20
	// minSamples: seconds of load a service needs to be sized from them.
	minSamples = 5
)

// usage is one container's usage during a load test (multi-replica
// services divided by their containers) and the Docker limits it ran
// under.
type usage struct {
	cpuP90, cpuPeak float64 // cores
	memPeak         int64   // bytes
	samples         int     // seconds of load
	cpuLimit        float64 // cores per container (the host's when unlimited)
	cpuLimited      bool
	memLimit        int64
	memLimited      bool
	verdict         string // the load test's verdict kind of the service
}

// recommendation is what recommend sizes a container at.
type recommendation struct {
	reqCPU, limCPU float64
	reqMem, limMem int64
	// censored: the service hit a Docker limit (cpu, memory) or pinned
	// one core (single-core), so the measurement is a lower bound.
	censored string
}

// recommend sizes a container from its load-test usage.
//
//   - CPU request = p90 × 1.15, rounded up to 10m, at least 50m.
//   - CPU limit = max(peak × 1.5, request × 2), rounded up to 50m.
//   - Memory request = peak × 1.2, memory limit = peak × 1.5, rounded up
//     to a MiB, at least 64Mi.
//
// Censored measurements: a service that was CPU-bound at its Docker CPU
// limit (or memory-bound / OOM-killed at its memory limit) would have used
// more, so the request is never below that limit and the limit is raised
// one step (twice the Docker limit). A single-core service (a
// single-threaded runtime pinning one core) gets at least one core: more
// replicas, not a bigger limit, add throughput.
func recommend(u usage) recommendation {
	r := recommendation{
		reqCPU: max(ceilTo(u.cpuP90*cpuMargin, 0.01), minCPU),
		reqMem: max(ceilMiB(float64(u.memPeak)*memMargin), minMem),
		limMem: max(ceilMiB(float64(u.memPeak)*memHeadroom), minMem),
	}
	r.limCPU = ceilTo(max(u.cpuPeak*cpuBurst, r.reqCPU*cpuLimitMult), 0.05)
	switch {
	case u.verdict == "cpu-bound" && u.cpuLimited:
		r.censored = "cpu"
		r.reqCPU = max(r.reqCPU, ceilTo(u.cpuLimit, 0.01))
		r.limCPU = max(r.limCPU, ceilTo(u.cpuLimit*cpuLimitMult, 0.05))
	case u.verdict == "single-core":
		r.censored = "single-core"
		r.reqCPU = max(r.reqCPU, 1)
		r.limCPU = max(r.limCPU, ceilTo(r.reqCPU*cpuLimitMult, 0.05))
	case (u.verdict == "memory-bound" || u.verdict == "oom") && u.memLimited:
		r.censored = "memory"
		r.reqMem = max(r.reqMem, ceilMiB(float64(u.memLimit)))
		r.limMem = max(r.limMem, ceilMiB(float64(u.memLimit)*2))
	}
	r.limCPU = max(r.limCPU, r.reqCPU)
	r.limMem = max(r.limMem, r.reqMem)
	return r
}

// ceilTo rounds v up to a multiple of step (float noise below 1e-9 ignored).
func ceilTo(v, step float64) float64 {
	n := math.Ceil(v/step - 1e-9)
	return math.Round(n*step*1000) / 1000
}

func ceilMiB(b float64) int64 { return int64(math.Ceil(b/(1<<20)-1e-9)) << 20 }

// measure reads the per-container usage of every service of a run, under
// load (T > 0: T 0 is the baseline before the load started).
func measure(run *model.LoadTestRun) map[string]usage {
	out := map[string]usage{}
	if run == nil || run.Resources == nil {
		return out
	}
	verdicts := map[string]string{}
	for _, v := range run.Resources.Verdicts {
		verdicts[v.Service] = v.Kind
	}
	for _, s := range run.Resources.Services {
		var cpu []float64
		var u usage
		containers := 1
		for _, p := range s.Timeline {
			if p.T <= 0 {
				continue
			}
			n := max(p.Containers, 1)
			containers = n
			c := p.CPUPercent / 100 / float64(n)
			cpu = append(cpu, c)
			u.cpuPeak = max(u.cpuPeak, c)
			u.memPeak = max(u.memPeak, p.MemBytes/int64(n))
		}
		if len(cpu) < minSamples {
			continue
		}
		slices.Sort(cpu)
		u.cpuP90 = percentile(cpu, 0.9)
		u.samples = len(cpu)
		u.cpuLimit, u.cpuLimited = s.CPULimit/float64(containers), s.CPULimited
		u.memLimit, u.memLimited = s.MemLimit/int64(containers), s.MemLimited
		u.verdict = verdicts[s.Service]
		out[s.Service] = u
	}
	return out
}

// percentile of sorted values (nearest rank).
func percentile(sorted []float64, q float64) float64 {
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// UsableRun reports why a load test run can't size a conversion of the
// compose project p (nil when it can): it must be finished, have recorded
// resources, and have measured at least one service of the project with
// enough seconds of load; a run whose target service is gone is stale.
func UsableRun(p *compose.Project, run *model.LoadTestRun) error {
	if run == nil {
		return errors.New("no load test run")
	}
	if run.Status == model.LoadRunning {
		return fmt.Errorf("load test %s is still running", run.ID)
	}
	if run.Resources == nil {
		return fmt.Errorf("load test %s recorded no container usage (it didn't target a running service of this workflow)", run.ID)
	}
	if p == nil || p.Model == nil {
		return errors.New("the compose files don't load")
	}
	if t := run.Resources.Service; t != "" && !activeService(p, t) {
		return fmt.Errorf("load test %s targeted %s, which is no longer a service of the compose project", run.ID, t)
	}
	if len(measuredServices(p, run)) == 0 {
		return fmt.Errorf("load test %s measured no current service for at least %d seconds of load", run.ID, minSamples)
	}
	return nil
}

func activeService(p *compose.Project, name string) bool {
	_, ok := p.Model.Services[name]
	return ok && !p.Inactive[name]
}

// measuredServices are the services of p the run measured, sorted.
func measuredServices(p *compose.Project, run *model.LoadTestRun) []string {
	var out []string
	for name := range measure(run) {
		if activeService(p, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// loadTestInfo summarizes the run for the report.
func loadTestInfo(p *compose.Project, run *model.LoadTestRun, used bool) *model.KubeConvertLoadTest {
	lt := &model.KubeConvertLoadTest{
		RunID: run.ID, Name: run.Spec.Name, Target: run.Resources.Service, StartedAt: run.StartedAt,
		Services: measuredServices(p, run), Used: used,
	}
	if s := run.Summary; s != nil {
		lt.RPS, lt.PeakRPS = round1(s.RPS), round1(s.PeakRPS)
	}
	for _, s := range run.Timeline {
		lt.Seconds = max(lt.Seconds, s.T)
	}
	if lt.Seconds == 0 {
		for _, s := range run.Resources.Services {
			for _, pt := range s.Timeline {
				lt.Seconds = max(lt.Seconds, pt.T)
			}
		}
	}
	return lt
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// provenance is the "from load test …" part of a sized service's mapping.
func (c *converter) provenance(u usage) string {
	run := c.opts.LoadTest
	at := run.StartedAt.Local().Format("2 Jan 15:04")
	rps := ""
	if run.Summary != nil {
		rps = fmt.Sprintf(" at %s rps", trimFloat(run.Summary.PeakRPS, 0))
	}
	return fmt.Sprintf("load test %s%s: p90 CPU %s, peak CPU %s, peak memory %s", at, rps,
		trimFloat(u.cpuP90, 2), trimFloat(u.cpuPeak, 2), memQuantity(ceilMiB(float64(u.memPeak))))
}

// trimFloat formats v with at most prec decimals.
func trimFloat(v float64, prec int) string {
	s := fmt.Sprintf("%.*f", prec, v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// farApart: an explicit value at least twice (or half) the measured one.
func farApart(explicit, measured float64) bool {
	return measured > 0 && (explicit >= 2*measured || explicit <= measured/2)
}
