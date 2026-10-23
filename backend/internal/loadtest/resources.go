package loadtest

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ResourceMonitor samples the containers behind a workflow's load test
// target (a docker workflow's compose services, a Kubernetes workflow's
// workloads). MonitorResources returns nil when there is nothing to sample.
type ResourceMonitor interface {
	MonitorResources(ctx context.Context, wfID string, target model.LoadTarget) model.ResourceProbe
}

// describedProbe is a probe whose services are not compose services: it
// maps their names to graph node IDs and explains where its numbers come
// from (Kubernetes pods).
type describedProbe interface {
	NodeID(service string) string
	Note() string
}

const (
	// saturated is the fraction of a limit at which a resource is the
	// bottleneck.
	saturated = 0.9
	// singleCoreLow/High bound a pinned single core (in cores).
	singleCoreLow  = 0.85
	singleCoreHigh = 1.15
	singleCoreRun  = 3 // consecutive seconds
	// degradedErrorRate marks a run whose latency or errors degraded.
	degradedErrorRate = 0.05
)

const resourcesNote = "CPU and memory come from docker stats, sampled once a second at the same instants as the latency timeline (100% CPU = one core; replicas summed). Peaks are 2-second averages so a single spike does not count as saturation."

// resourceSample is the usage of every service at second T of the load.
type resourceSample struct {
	T     float64
	Usage []model.ServiceUsage
}

// resourceRecorder collects the samples of one run.
type resourceRecorder struct {
	probe  model.ResourceProbe
	before map[string]model.ContainerFacts

	mu      sync.Mutex
	samples []resourceSample
}

func newResourceRecorder(p model.ResourceProbe) *resourceRecorder {
	return &resourceRecorder{probe: p, before: p.Containers()}
}

func (rr *resourceRecorder) record(t float64) {
	u := rr.probe.Sample()
	rr.mu.Lock()
	rr.samples = append(rr.samples, resourceSample{T: t, Usage: u})
	rr.mu.Unlock()
}

// resourcesSummary copies res without the per-second timelines (lists and
// status events); nil stays nil.
func resourcesSummary(res *model.LoadResources) *model.LoadResources {
	if res == nil {
		return nil
	}
	out := *res
	out.Services = slices.Clone(res.Services)
	for i := range out.Services {
		out.Services[i].Timeline = []model.ResourcePoint{}
	}
	out.Verdicts = slices.Clone(res.Verdicts)
	return &out
}

// result correlates the samples with the run's timeline.
func (rr *resourceRecorder) result(timeline []model.LoadSample, sum *model.LoadSummary) *model.LoadResources {
	after := rr.probe.Containers()
	rr.mu.Lock()
	samples := slices.Clone(rr.samples)
	rr.mu.Unlock()
	res := correlate(rr.probe.Target(), samples, timeline, sum, rr.before, after)
	if dp, ok := rr.probe.(describedProbe); ok && res != nil {
		res.Note = dp.Note()
		for i := range res.Services {
			res.Services[i].NodeID = dp.NodeID(res.Services[i].Service)
		}
	}
	return res
}

// correlate builds the per-service resource timelines of a run and decides
// which container, if any, limited it.
func correlate(target string, samples []resourceSample, timeline []model.LoadSample, sum *model.LoadSummary, before, after map[string]model.ContainerFacts) *model.LoadResources {
	series := map[string][]model.ServiceUsage{}
	times := map[string][]float64{}
	for _, s := range samples {
		for _, u := range s.Usage {
			series[u.Service] = append(series[u.Service], u)
			times[u.Service] = append(times[u.Service], s.T)
		}
	}
	if len(series) == 0 {
		return nil
	}
	names := slices.Sorted(maps.Keys(series))
	if slices.Contains(names, target) {
		names = append([]string{target}, slices.DeleteFunc(names, func(n string) bool { return n == target })...)
	}
	rpsAt := func(t float64) float64 {
		for _, s := range timeline {
			if s.T == t {
				return s.RPS
			}
		}
		return 0
	}
	res := &model.LoadResources{Service: target, Services: []model.LoadServiceResources{}, Verdicts: []model.ResourceVerdict{}, Note: resourcesNote}
	for _, name := range names {
		us, ts := series[name], times[name]
		last := us[len(us)-1]
		svc := model.LoadServiceResources{
			Service: name, NodeID: model.ComposeServiceID(name), Target: name == target,
			CPULimit: last.CPULimit, CPULimited: last.CPULimited, MemLimit: last.MemLimit, MemLimited: last.MemLimited,
			Timeline: make([]model.ResourcePoint, len(us)),
		}
		for i, u := range us {
			svc.Timeline[i] = model.ResourcePoint{T: ts[i], CPUPercent: u.CPUPercent, MemBytes: u.MemBytes, Containers: u.Containers}
			svc.PeakCPU = math.Max(svc.PeakCPU, u.CPUPercent)
			svc.PeakMem = max(svc.PeakMem, u.MemBytes)
			svc.PeakMemOf = math.Max(svc.PeakMemOf, u.MemOfLimit())
		}
		// Sustained CPU: the mean of each pair of consecutive samples.
		peakAt := -1
		firstSat := -1
		for i := range us {
			of := rolling(us, i, model.ServiceUsage.CPUOfLimit)
			if of > svc.PeakCPUOf {
				svc.PeakCPUOf, peakAt = of, i
			}
			if firstSat < 0 && of >= saturated {
				firstSat = i
			}
		}
		svc.PeakCPUOf = round3(svc.PeakCPUOf)
		svc.PeakMemOf = round3(svc.PeakMemOf)
		a, b := before[name], after[name]
		svc.StartupMs = b.StartupMs
		if b.Restarts > a.Restarts {
			svc.Restarts = b.Restarts - a.Restarts
		}
		svc.OOMKilled = b.OOMKilled && !a.OOMKilled
		res.Services = append(res.Services, svc)

		v := model.ResourceVerdict{Service: name, Kind: "ok"}
		at := func(i int) {
			v.AtT = ts[i]
			v.AtRPS = rpsAt(ts[i])
		}
		cores := func(i int) float64 {
			return rolling(us, i, func(u model.ServiceUsage) float64 { return u.CPUPercent / 100 })
		}
		switch {
		case svc.OOMKilled:
			v.Kind, v.Bottleneck = "oom", true
			v.Message = fmt.Sprintf("%s was killed for running out of memory during the test (limit %s) — raise its memory limit or reduce memory use", name, binaryBytes(svc.MemLimit))
		case svc.Restarts > 0:
			v.Kind, v.Bottleneck = "restarted", true
			v.Message = fmt.Sprintf("%s restarted %d time(s) during the test — read its logs: requests failed while it was down", name, svc.Restarts)
		case firstSat >= 0:
			at(firstSat)
			v.Kind, v.Bottleneck = "cpu-bound", true
			if svc.CPULimited {
				v.Message = fmt.Sprintf("%s reached %d%% of its %s limit at %s req/s — CPU-bound; raise limits or add replicas", name, pct(sustainedLevel(us, firstSat)), cpuLabel(svc.CPULimit), rpsLabel(v.AtRPS))
			} else {
				v.Message = fmt.Sprintf("%s used %.1f of the host's %s at %s req/s — CPU-bound on this machine; optimize the hot path or spread the load", name, svc.PeakCPU/100, cpuLabel(svc.CPULimit), rpsLabel(v.AtRPS))
			}
		case svc.CPULimit > 1.5:
			if i, ok := pinnedCore(len(us), cores); ok {
				at(i)
				v.Kind, v.Bottleneck = "single-core", true
				v.Message = fmt.Sprintf("%s pinned one CPU core (%d%%) at %s req/s while %s were available — a single-threaded runtime (Node.js event loop, Python GIL) is the limit; add replicas or worker processes", name, int(math.Round(cores(i)*100)), rpsLabel(v.AtRPS), cpuLabel(svc.CPULimit))
				break
			}
			fallthrough
		default:
			if svc.MemLimited && svc.PeakMemOf >= saturated {
				v.Kind, v.Bottleneck = "memory-bound", true
				v.Message = fmt.Sprintf("%s used %s of its %s memory limit (%d%%) — close to an OOM kill; raise the memory limit", name, binaryBytes(svc.PeakMem), binaryBytes(svc.MemLimit), pct(svc.PeakMemOf))
				break
			}
			if peakAt >= 0 {
				at(peakAt)
			}
			if svc.CPULimited {
				v.Message = fmt.Sprintf("%s at %d%% of its %s limit — not the bottleneck", name, pct(svc.PeakCPUOf), cpuLabel(svc.CPULimit))
			} else {
				v.Message = fmt.Sprintf("%s at %d%% CPU (no limit) — not the bottleneck", name, int(math.Round(svc.PeakCPU)))
			}
		}
		res.Verdicts = append(res.Verdicts, v)
	}
	res.Verdict = headline(res, sum)
	return res
}

// sustainedLevel is the mean CPU fraction of the limit over the saturated
// seconds from first on, capped at 1: docker stats windows do not line up
// with the CFS quota periods, so single readings above 100% of a hard limit
// are sampling artifacts.
func sustainedLevel(us []model.ServiceUsage, first int) float64 {
	var sum float64
	n := 0
	for i := first; i < len(us); i++ {
		if rolling(us, i, model.ServiceUsage.CPUOfLimit) >= saturated {
			sum += us[i].CPUOfLimit()
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Min(1, sum/float64(n))
}

// rolling is the mean of f over samples i-1 and i (i alone for the first).
func rolling(us []model.ServiceUsage, i int, f func(model.ServiceUsage) float64) float64 {
	if i == 0 {
		return f(us[0])
	}
	return (f(us[i-1]) + f(us[i])) / 2
}

// pinnedCore finds singleCoreRun consecutive samples using about one core,
// with no sample beyond it; it returns the first of them.
func pinnedCore(n int, cores func(int) float64) (int, bool) {
	run, first := 0, -1
	found := -1
	for i := range n {
		c := cores(i)
		if c > singleCoreHigh+0.05 {
			return 0, false
		}
		if c >= singleCoreLow && c <= singleCoreHigh {
			if run == 0 {
				first = i
			}
			run++
			if run >= singleCoreRun && found < 0 {
				found = first
			}
		} else {
			run = 0
		}
	}
	return found, found >= 0
}

// headline picks the most important verdict: an OOM kill or restart, then
// the target's saturation, then any other service's.
func headline(res *model.LoadResources, sum *model.LoadSummary) string {
	rank := map[string]int{"oom": 0, "restarted": 1, "cpu-bound": 2, "single-core": 3, "memory-bound": 4}
	var best *model.ResourceVerdict
	score := func(v *model.ResourceVerdict) int {
		s := rank[v.Kind] * 2
		if v.Service != res.Service {
			s++
		}
		return s
	}
	for i := range res.Verdicts {
		v := &res.Verdicts[i]
		if v.Bottleneck && (best == nil || score(v) < score(best)) {
			best = v
		}
	}
	if best != nil {
		return best.Message
	}
	if sum != nil && (sum.BreakingPoint != nil || sum.ErrorRate >= degradedErrorRate) {
		return "No container reached its CPU or memory limit, yet latency or errors degraded — the limit is inside the app (connection pools, locks, I/O waits) or in the load generator"
	}
	busiest := -1
	for i, s := range res.Services {
		if busiest < 0 || s.PeakCPUOf > res.Services[busiest].PeakCPUOf {
			busiest = i
		}
	}
	s := res.Services[busiest]
	v := res.Verdicts[busiest]
	limit := "its " + cpuLabel(s.CPULimit) + " limit"
	if !s.CPULimited {
		limit = "the host's " + cpuLabel(s.CPULimit)
	}
	return fmt.Sprintf("No resource bottleneck: the busiest container, %s, peaked at %d%% of %s (at %s req/s)", s.Service, pct(s.PeakCPUOf), limit, rpsLabel(v.AtRPS))
}

func pct(f float64) int { return int(math.Round(f * 100)) }

// cpuLabel formats cores: "0.25 CPU", "1 CPU", "14 CPUs".
func cpuLabel(c float64) string {
	s := strconv.FormatFloat(c, 'f', -1, 64)
	if c > 1 {
		return s + " CPUs"
	}
	return s + " CPU"
}

func rpsLabel(r float64) string {
	if r >= 10 {
		return strconv.Itoa(int(math.Round(r)))
	}
	return strings.TrimSuffix(strconv.FormatFloat(r, 'f', 1, 64), ".0")
}

// binaryBytes formats a size with binary units ("256 MiB").
func binaryBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	s := strings.TrimSuffix(strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64), ".0")
	return s + " " + string("KMGTPE"[exp]) + "iB"
}
