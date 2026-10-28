// Package scenario describes the incident scenarios a load test can inject
// into a docker or Kubernetes workflow's local runtime (Catalog), and grades
// how the load-tested service held up from the run's per-second timeline
// (Evaluate). Injecting and reverting faults is the sandbox's job
// (sandbox.Manager.InjectFault); the load test engine sequences both.
package scenario

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Verdict bounds. A second is "healthy" when its error rate is at most
// max(HealthyErrorFactor × baseline, ErrorRateOK) and its p95 at most
// max(P95Factor × baseline p95, baseline p95 + P95SlackMs).
const (
	// BaselineWindowSec is how many seconds before the injection form the
	// baseline (median p95 and in-flight, error rate).
	BaselineWindowSec = 10
	// HealthyStreakSec healthy seconds in a row mark the recovery.
	HealthyStreakSec = 3
	// ErrorRateOK is the extra error rate (over the baseline) a scenario
	// may cause and still count as survived.
	ErrorRateOK = 0.01
	// HealthyErrorFactor scales the baseline error rate for a healthy second.
	HealthyErrorFactor = 2.0
	// ErrorRateFailed: at least this share of requests failing during the
	// fault window fails the scenario.
	ErrorRateFailed = 0.5
	// P95Factor and P95SlackMs bound the p95 of a healthy second.
	P95Factor  = 2.0
	P95SlackMs = 50.0
	// RecoveryOKSec: a survived scenario is back to normal within this many
	// seconds after the fault ends (instant faults: after the injection).
	RecoveryOKSec = 10
	// RecoveryFailedSec: slower recoveries fail the scenario.
	RecoveryFailedSec = 60
	// KillDrainSec: after a replica is killed, requests should stop
	// reaching it (load balancer health checks, readiness) within this.
	KillDrainSec = 5
	// HangTimeoutShare: a window p95 at this share of the request timeout
	// means callers waited for timeouts instead of failing fast.
	HangTimeoutShare = 0.8
	// PileUpFactor and PileUpMin: in-flight requests above
	// max(PileUpFactor × baseline, baseline + PileUpMin) are piling up.
	PileUpFactor = 3.0
	PileUpMin    = 10
	// LatencyAmplificationMax: with injected delay d, a p95 growing by more
	// than this × d means each request waits on the dependency several
	// times (sequential calls or retries).
	LatencyAmplificationMax = 2.0
	// SpikeThroughputMin is the share of the spike's requested load the
	// target must complete.
	SpikeThroughputMin = 0.9
	// BackingCPUFactor and BackingCPUSlack bound a backing service's CPU
	// after a cache flush: peak above max(factor × baseline, baseline +
	// slack percentage points) is a stampede.
	BackingCPUFactor = 2.0
	BackingCPUSlack  = 20.0
)

// Fix hints (part of the verdict message).
const (
	HintLoadBalancer   = "put a load balancer with health checks in front of the replicas (or readiness probes on Kubernetes) so traffic skips a dead instance"
	HintMoreReplicas   = "run at least 2 replicas with a restart policy so one crash never takes the service down"
	HintRestartPolicy  = "set a restart policy (restart: unless-stopped) so a crashed container comes back on its own"
	HintTimeouts       = "set short client timeouts on the dependency (e.g. 1-2 s) and a circuit breaker so requests fail fast instead of piling up"
	HintFallback       = "add a fallback (serve cached or degraded data) or a circuit breaker for the dependency"
	HintReconnect      = "make the client reconnect with backoff, and keep readiness probes on the dependency so traffic returns as soon as it is back"
	HintRetries        = "cap retries and use exponential backoff with jitter; parallelize or cache dependency calls"
	HintCacheWarm      = "warm the cache on start and coalesce concurrent misses (single-flight) so a flush doesn't stampede the database"
	HintCapacity       = "add replicas or autoscaling (HPA on CPU) and shed or queue excess load at the edge"
	HintAutoscaleSpeed = "lower the HPA's CPU target or raise minReplicas so new pods are ready before latency degrades"
)

// Observation is what the runtime reported about a fault (Fault.Revert).
type Observation struct {
	// Back: the killed replica or the downed dependency was running (and
	// healthy/Ready) again, BackAfter after the injection.
	Back      bool
	BackAfter time.Duration
	// SelfHealed (kill_replica): the restart policy or controller brought
	// the replica back; false when Nimbo had to start it.
	SelfHealed bool
	// Verified: the runtime is back to its pre-fault state (container
	// unpaused, qdisc removed, replicas restored); VerifyDetail says how.
	Verified     bool
	VerifyDetail string
	// Keys is how many keys a cache_flush removed (-1 unknown).
	Keys int
}

// Fault is an injected fault (sandbox.Manager.InjectFault).
type Fault interface {
	// TargetName is the service or workload acted on ("" for none).
	TargetName() string
	// Detail says what was done to the runtime.
	Detail() string
	// Revert undoes the fault; it is idempotent and safe after a failed
	// injection. It reports what the runtime did meanwhile.
	Revert(ctx context.Context) (Observation, error)
}

// Instant reports scenarios that act once (DurationSec is then only the
// observation window).
func Instant(id string) bool {
	return id == model.ScenarioKillReplica || id == model.ScenarioCacheFlush
}

// Input is everything Evaluate grades. Times are on LoadSample.T's axis
// (seconds since load started).
type Input struct {
	Spec       model.ScenarioSpec
	TargetName string
	Timeline   []model.LoadSample
	InjectedAt float64
	// RevertedAt is when the fault ended (instant faults: InjectedAt +
	// DurationSec, the end of the observation window).
	RevertedAt float64
	// Injected and Inject error: a failed injection grades nothing.
	InjectErr error
	TimeoutMs int
	// Closed is concurrency mode: in-flight is the number of virtual
	// users, so pile-up is not measured.
	Closed      bool
	Observation Observation
	// Resources (optional) are the run's container usage (backing service
	// CPU after a cache flush) and autoscaling (traffic spike).
	Resources *model.LoadResources
	// Kubernetes selects Kubernetes wording in hints.
	Kubernetes bool
	// Detail is Fault.Detail.
	Detail string
	// Stopped: the user stopped the run before its end, so the fault's
	// effect and the recovery were not watched through: no verdict.
	Stopped bool
}

// stats are aggregates over a range of seconds.
type stats struct {
	n           int
	errRate     float64
	p95         float64 // median of per-second p95
	peakP95     float64
	inFlight    float64 // median
	peakFlight  int
	rps, target float64 // sums
	// succeeded is how many requests completed without error.
	succeeded float64
}

func errRate(s model.LoadSample) float64 {
	d := math.Max(s.RPS, float64(s.Errors))
	if d <= 0 {
		return 0
	}
	return float64(s.Errors) / d
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	slices.Sort(v)
	m := len(v) / 2
	if len(v)%2 == 1 {
		return v[m]
	}
	return (v[m-1] + v[m]) / 2
}

// window aggregates the samples with from < T <= to.
func window(tl []model.LoadSample, from, to float64) stats {
	var st stats
	var errs, attempts float64
	var p95s, flights []float64
	for _, s := range tl {
		if s.T <= from || s.T > to {
			continue
		}
		st.n++
		errs += float64(s.Errors)
		attempts += math.Max(s.RPS, float64(s.Errors))
		if s.P95 > 0 {
			p95s = append(p95s, s.P95)
			st.peakP95 = math.Max(st.peakP95, s.P95)
		}
		flights = append(flights, float64(s.InFlight))
		st.peakFlight = max(st.peakFlight, s.InFlight)
		st.rps += s.RPS
		st.target += s.Target
		st.succeeded += math.Max(s.RPS-float64(s.Errors), 0)
	}
	if attempts > 0 {
		st.errRate = errs / attempts
	}
	st.p95 = median(p95s)
	st.inFlight = median(flights)
	return st
}

// bounds are the healthy limits derived from the baseline.
type bounds struct {
	base    stats
	errMax  float64
	p95Max  float64
	pileMax float64
}

func newBounds(base stats) bounds {
	return bounds{
		base:    base,
		errMax:  math.Max(HealthyErrorFactor*base.errRate, ErrorRateOK),
		p95Max:  math.Max(P95Factor*base.p95, base.p95+P95SlackMs),
		pileMax: math.Max(PileUpFactor*base.inFlight, base.inFlight+PileUpMin),
	}
}

func (b bounds) healthy(s model.LoadSample) bool {
	if s.RPS <= 0 && s.Errors == 0 {
		return s.Target <= 0 // no traffic asked: nothing to judge
	}
	if errRate(s) > b.errMax {
		return false
	}
	return s.P95 <= b.p95Max || b.base.p95 == 0
}

// recoveredAt is the start of the first HealthyStreakSec-second healthy
// streak among the samples with T > from, as a time on the axis (from
// itself when the first samples are healthy); ok false when none.
func (b bounds) recoveredAt(tl []model.LoadSample, from float64) (float64, bool) {
	streak := 0
	start := 0.0
	for _, s := range tl {
		if s.T <= from {
			continue
		}
		if !b.healthy(s) {
			streak = 0
			continue
		}
		if streak == 0 {
			start = math.Max(s.T-1, from)
		}
		streak++
		if streak >= HealthyStreakSec {
			return start, true
		}
	}
	return 0, false
}

func pct(v float64) string {
	p := v * 100
	switch {
	case p == 0:
		return "0%"
	case p < 0.1:
		return "<0.1%"
	case p < 10:
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func secs(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f s", v)
	}
	return fmt.Sprintf("%.0f s", v)
}

// Evaluate grades a scenario from the run's timeline and the fault's
// observation.
func Evaluate(in Input) model.ScenarioResult {
	res := model.ScenarioResult{
		ID: in.Spec.ID, Target: in.Spec.Target, TargetName: in.TargetName,
		InjectedAtSec: int(math.Round(in.InjectedAt)), Checks: []model.ScenarioCheck{}, Detail: in.Detail,
	}
	if in.InjectErr != nil {
		res.Verdict = model.ScenarioFailed
		res.Message = "The fault could not be injected: " + in.InjectErr.Error()
		res.Checks = append(res.Checks, model.ScenarioCheck{Name: "Fault injected", Passed: false, Detail: in.InjectErr.Error()})
		return res
	}
	reverted := int(math.Round(in.RevertedAt))
	res.RevertedAtSec = &reverted

	addReverted := func() {
		if in.Observation.VerifyDetail != "" {
			res.Checks = append(res.Checks, model.ScenarioCheck{Name: "Fault reverted", Passed: in.Observation.Verified, Detail: in.Observation.VerifyDetail})
		}
	}
	if in.Stopped {
		res.Message = "Stopped before the end: no verdict (the fault's effect and the recovery were not watched through)."
		addReverted()
		return res
	}
	base := window(in.Timeline, in.InjectedAt-BaselineWindowSec, in.InjectedAt)
	// A target that already fails says nothing about the fault: grading it
	// would call an outage "survived" (as many errors as before).
	if base.succeeded == 0 || base.errRate >= ErrorRateFailed {
		res.Verdict = model.ScenarioInconclusive
		detail := "No request succeeded before the fault."
		if base.succeeded > 0 {
			detail = fmt.Sprintf("%s of requests failed before the fault.", pct(base.errRate))
		}
		res.Message = "Not assessed: the target was already failing before the fault (" + strings.TrimSuffix(detail, ".") +
			"): fix it or pick a path that answers, then try again."
		res.Checks = append(res.Checks, model.ScenarioCheck{Name: "Healthy before the fault", Passed: false, Detail: detail})
		addReverted()
		return res
	}
	b := newBounds(base)
	from := in.RevertedAt
	if Instant(in.Spec.ID) {
		from = in.InjectedAt
	}
	win := window(in.Timeline, in.InjectedAt, in.RevertedAt)
	recAt, recovered := b.recoveredAt(in.Timeline, from)
	if recovered {
		r := int(math.Round(recAt))
		res.RecoveredAtSec = &r
	}
	ev := &evaluation{in: in, b: b, win: win, from: from, recAt: recAt, recovered: recovered, res: &res}
	switch in.Spec.ID {
	case model.ScenarioKillReplica:
		ev.killReplica()
	case model.ScenarioDependencyDown:
		ev.dependencyDown()
	case model.ScenarioDependencyLatency:
		ev.dependencyLatency()
	case model.ScenarioCacheFlush:
		ev.cacheFlush()
	case model.ScenarioTrafficSpike:
		ev.trafficSpike()
	default:
		res.Verdict = model.ScenarioFailed
		res.Message = "Unknown scenario " + in.Spec.ID
	}
	addReverted()
	return res
}

type evaluation struct {
	in        Input
	b         bounds
	win       stats
	from      float64
	recAt     float64
	recovered bool
	res       *model.ScenarioResult
}

func (e *evaluation) check(name string, passed bool, format string, args ...any) bool {
	e.res.Checks = append(e.res.Checks, model.ScenarioCheck{Name: name, Passed: passed, Detail: fmt.Sprintf(format, args...)})
	return passed
}

// errorsOK reports whether the window's error rate is within ErrorRateOK
// of the baseline.
func (e *evaluation) errorsOK(st stats) bool { return st.errRate <= e.b.base.errRate+ErrorRateOK }

func (e *evaluation) p95OK(st stats) bool { return st.peakP95 <= e.b.p95Max || e.b.base.p95 == 0 }

func (e *evaluation) pileUp(st stats) bool {
	return !e.in.Closed && float64(st.peakFlight) > e.b.pileMax
}

// recoveryCheck adds the recovery check and returns the recovery time
// (seconds after from) and whether it is within RecoveryOKSec.
func (e *evaluation) recoveryCheck(name, after string) (float64, bool) {
	if !e.recovered {
		e.check(name, false, "Still degraded when the run ended %s after %s.", secs(e.lastT()-e.from), after)
		return math.Inf(1), false
	}
	d := e.recAt - e.from
	ok := d <= RecoveryOKSec
	if d <= 0 {
		e.check(name, true, "Errors and p95 stayed normal after %s.", after)
	} else {
		e.check(name, ok, "Back to normal %s after %s (errors <= %s, p95 <= %s for %d s).", secs(d), after, pct(e.b.errMax), ms(e.b.p95Max), HealthyStreakSec)
	}
	return d, ok
}

func (e *evaluation) lastT() float64 {
	if n := len(e.in.Timeline); n > 0 {
		return e.in.Timeline[n-1].T
	}
	return e.from
}

func (e *evaluation) settle(failed, survived bool, summary string, hints ...string) {
	switch {
	case failed:
		e.res.Verdict = model.ScenarioFailed
	case survived:
		e.res.Verdict = model.ScenarioSurvived
	default:
		e.res.Verdict = model.ScenarioDegraded
	}
	label := map[string]string{model.ScenarioFailed: "Failed", model.ScenarioSurvived: "Survived", model.ScenarioDegraded: "Degraded"}[e.res.Verdict]
	msg := label + ": " + summary
	if e.res.Verdict != model.ScenarioSurvived {
		var hs []string
		for _, h := range hints {
			if h != "" && !slices.Contains(hs, h) {
				hs = append(hs, h)
			}
		}
		if len(hs) > 0 {
			msg += ". Fix: " + strings.Join(hs, "; ")
		}
	}
	e.res.Message = msg + "."
}

func (e *evaluation) target() string {
	if e.in.TargetName != "" {
		return e.in.TargetName
	}
	return "the target"
}

func (e *evaluation) killReplica() {
	errOK := e.check("Errors while a replica was down", e.errorsOK(e.win),
		"%s of requests failed in the %s after the kill (baseline %s).", pct(e.win.errRate), secs(e.in.RevertedAt-e.in.InjectedAt), pct(e.b.base.errRate))
	drain := window(e.in.Timeline, e.in.InjectedAt+KillDrainSec, e.in.RevertedAt)
	drainOK := e.check("Traffic moved off the dead replica", drain.n == 0 || drain.errRate <= e.b.errMax,
		"%s errors from %d s after the kill on.", pct(drain.errRate), KillDrainSec)
	obs := e.in.Observation
	var back float64
	backOK := false
	switch {
	case !obs.Back:
		e.check("Replica replaced", false, "The killed replica was not running again by the end of the window.")
	case !obs.SelfHealed:
		e.check("Replica replaced", false, "Nothing restarted it: Nimbo started it again when the window ended.")
	default:
		back = obs.BackAfter.Seconds()
		backOK = true
		e.check("Replica replaced", true, "Running again %s after the kill.", secs(math.Max(back, 0)))
	}
	p95OK := e.check("p95 impact", e.p95OK(e.win), "p95 peaked at %s (baseline %s).", ms(e.win.peakP95), ms(e.b.base.p95))
	d, recOK := e.recoveryCheck("Recovered", "the kill")

	summary := fmt.Sprintf("killing one %s replica caused %s errors, p95 peak %s", e.target(), pct(e.win.errRate), ms(e.win.peakP95))
	if e.recovered {
		summary += fmt.Sprintf(", back to normal in %s", secs(math.Max(d, 0)))
	} else {
		summary += ", never back to normal"
	}
	hint := HintLoadBalancer
	if !backOK {
		hint = HintRestartPolicy
		if e.in.Kubernetes {
			hint = HintMoreReplicas
		}
	}
	e.settle(!e.recovered || d > RecoveryFailedSec || e.win.errRate >= ErrorRateFailed,
		errOK && drainOK && backOK && p95OK && recOK, summary, hint)
}

func (e *evaluation) dependencyDown() {
	hang := e.pileUp(e.win) || (e.in.TimeoutMs > 0 && e.win.peakP95 >= HangTimeoutShare*float64(e.in.TimeoutMs))
	fast := e.check("Failed fast while it was down", !hang,
		"In-flight requests peaked at %d (baseline %.0f), p95 peaked at %s (timeout %s).", e.win.peakFlight, e.b.base.inFlight, ms(e.win.peakP95), ms(float64(e.in.TimeoutMs)))
	errOK := e.check("Errors during the outage", e.errorsOK(e.win), "%s of requests failed while it was down (baseline %s).", pct(e.win.errRate), pct(e.b.base.errRate))
	d, recOK := e.recoveryCheck("Recovered after it returned", "it came back")
	after := window(e.in.Timeline, e.in.RevertedAt+RecoveryOKSec, e.in.RevertedAt+RecoveryOKSec+BaselineWindowSec)
	if after.n > 0 {
		e.check("Errors after it returned", after.errRate <= e.b.errMax, "%s of requests failed %d-%d s after it came back.", pct(after.errRate), RecoveryOKSec, RecoveryOKSec+BaselineWindowSec)
	}
	summary := fmt.Sprintf("with %s down %s, %s of requests failed, in-flight peaked at %d", e.target(), secs(e.in.RevertedAt-e.in.InjectedAt), pct(e.win.errRate), e.win.peakFlight)
	if e.recovered {
		summary += fmt.Sprintf(", recovered %s after it returned", secs(math.Max(d, 0)))
	} else {
		summary += ", not recovered when the run ended"
	}
	hints := []string{}
	if hang {
		hints = append(hints, HintTimeouts)
	}
	if !errOK {
		hints = append(hints, HintFallback)
	}
	if !recOK {
		hints = append(hints, HintReconnect)
	}
	e.settle(hang || !e.recovered || d > RecoveryFailedSec, fast && errOK && recOK, summary, hints...)
}

func (e *evaluation) dependencyLatency() {
	delay := paramFloat(e.in.Spec.Params, "delayMs", DefaultDelayMs)
	added := math.Max(e.win.p95-e.b.base.p95, 0)
	amp := 0.0
	if delay > 0 {
		amp = added / delay
	}
	ampOK := e.check("Latency amplification", amp <= LatencyAmplificationMax,
		"p95 grew by %s for %s of injected delay (%.1fx).", ms(added), ms(delay), amp)
	errOK := e.check("No timeouts or errors", e.errorsOK(e.win), "%s of requests failed while the dependency was slow (baseline %s).", pct(e.win.errRate), pct(e.b.base.errRate))
	pile := e.pileUp(e.win)
	e.check("No in-flight pile-up", !pile, "In-flight requests peaked at %d (baseline %.0f).", e.win.peakFlight, e.b.base.inFlight)
	d, recOK := e.recoveryCheck("Recovered after the latency was removed", "the latency was removed")
	summary := fmt.Sprintf("%s of extra latency on %s raised p95 by %s (%.1fx), %s errors", ms(delay), e.target(), ms(added), amp, pct(e.win.errRate))
	if e.recovered {
		summary += fmt.Sprintf(", recovered %s after it was removed", secs(math.Max(d, 0)))
	}
	hints := []string{}
	if !ampOK {
		hints = append(hints, HintRetries)
	}
	if !errOK || pile {
		hints = append(hints, HintTimeouts)
	}
	if !recOK {
		hints = append(hints, HintReconnect)
	}
	e.settle(!e.recovered || d > RecoveryFailedSec || e.win.errRate >= ErrorRateFailed, ampOK && errOK && !pile && recOK, summary, hints...)
}

func (e *evaluation) cacheFlush() {
	keys := ""
	if e.in.Observation.Keys >= 0 {
		keys = fmt.Sprintf(" (%d keys)", e.in.Observation.Keys)
	}
	p95OK := e.check("p95 after the flush", e.p95OK(e.win), "p95 peaked at %s (baseline %s) after flushing %s%s.", ms(e.win.peakP95), ms(e.b.base.p95), e.target(), keys)
	errOK := e.check("Errors after the flush", e.errorsOK(e.win), "%s of requests failed (baseline %s).", pct(e.win.errRate), pct(e.b.base.errRate))
	d, recOK := e.recoveryCheck("Cache re-warmed", "the flush")
	cpuOK := true
	if svc, base, peak, ok := backingCPU(e.in.Resources, e.in.TargetName, e.in.InjectedAt, e.in.RevertedAt); ok {
		cpuOK = e.check("Database load", peak <= math.Max(BackingCPUFactor*base, base+BackingCPUSlack),
			"%s CPU went from %.0f%% to a peak of %.0f%% after the flush.", svc, base, peak)
	}
	summary := fmt.Sprintf("flushing %s%s gave p95 peak %s (baseline %s), %s errors", e.target(), keys, ms(e.win.peakP95), ms(e.b.base.p95), pct(e.win.errRate))
	if e.recovered && d > 0 {
		summary += fmt.Sprintf(", re-warmed in %s", secs(d))
	}
	e.settle(!e.recovered || d > RecoveryFailedSec || e.win.errRate >= ErrorRateFailed, p95OK && errOK && recOK && cpuOK, summary, HintCacheWarm)
}

func (e *evaluation) trafficSpike() {
	factor := paramFloat(e.in.Spec.Params, "factor", DefaultSpikeFactor)
	errOK := e.check("Errors during the spike", e.errorsOK(e.win), "%s of requests failed at %.4gx load (baseline %s).", pct(e.win.errRate), factor, pct(e.b.base.errRate))
	p95OK := e.check("p95 during the spike", e.p95OK(e.win), "p95 peaked at %s (baseline %s).", ms(e.win.peakP95), ms(e.b.base.p95))
	share := 1.0
	if e.win.target > 0 {
		share = (e.win.rps - e.errorsIn(e.win)) / e.win.target
	}
	keptUp := e.check("Kept up with the spike", share >= SpikeThroughputMin, "Completed %.0f%% of the requested load.", math.Min(share, 1)*100)
	pile := e.pileUp(e.win)
	if !e.in.Closed {
		e.check("No in-flight pile-up", !pile, "In-flight requests peaked at %d (baseline %.0f).", e.win.peakFlight, e.b.base.inFlight)
	}
	scaleOK := true
	hints := []string{HintCapacity}
	if sc := e.in.Resources; sc != nil && sc.Scaling != nil {
		for _, w := range sc.Scaling.Workloads {
			if !w.Target {
				continue
			}
			scaleOK = w.Kind == model.ScalingScaled || w.Kind == model.ScalingNotNeeded || w.Kind == model.ScalingTooShort
			e.check("Autoscaling", scaleOK, "%s", w.Message)
			if w.Kind == model.ScalingLate {
				hints = append([]string{HintAutoscaleSpeed}, hints...)
			}
		}
	}
	d, recOK := e.recoveryCheck("Recovered after the spike", "the spike")
	summary := fmt.Sprintf("at %.4gx load for %s, %s errors and p95 peak %s (baseline %s)", factor, secs(e.in.RevertedAt-e.in.InjectedAt), pct(e.win.errRate), ms(e.win.peakP95), ms(e.b.base.p95))
	if e.recovered && d > 0 {
		summary += fmt.Sprintf(", recovered %s after it", secs(d))
	}
	e.settle(!e.recovered || d > RecoveryFailedSec || e.win.errRate >= ErrorRateFailed,
		errOK && p95OK && keptUp && !pile && scaleOK && recOK, summary, hints...)
}

func (e *evaluation) errorsIn(st stats) float64 {
	return st.errRate * st.rps
}

// backingCPU finds the non-target, non-cache service whose CPU grew most
// in (from, to] over its baseline: the database behind a flushed cache.
func backingCPU(r *model.LoadResources, cache string, from, to float64) (svc string, base, peak float64, ok bool) {
	if r == nil {
		return "", 0, 0, false
	}
	best := -math.MaxFloat64
	for _, s := range r.Services {
		if s.Target || s.Service == cache || len(s.Timeline) == 0 {
			continue
		}
		var bs []float64
		pk := 0.0
		for _, p := range s.Timeline {
			switch {
			case p.T > from-BaselineWindowSec && p.T <= from:
				bs = append(bs, p.CPUPercent)
			case p.T > from && p.T <= to:
				pk = math.Max(pk, p.CPUPercent)
			}
		}
		b := median(bs)
		if pk-b > best {
			best, svc, base, peak, ok = pk-b, s.Service, b, pk, true
		}
	}
	return svc, base, peak, ok
}
