package loadtest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

// FaultInjector injects incident scenarios into docker and Kubernetes
// workflows' runtimes (sandbox.Manager).
type FaultInjector interface {
	// PrepareFaults readies the helper tools injection needs (it may
	// build an image the first time).
	PrepareFaults(ctx context.Context) error
	InjectFault(ctx context.Context, wfID string, spec model.ScenarioSpec, target model.LoadTarget) (scenario.Fault, error)
}

const (
	// prepareTimeout bounds readying the injection tools before load.
	prepareTimeout = 3 * time.Minute
	// injectTimeout bounds one injection.
	injectTimeout = time.Minute
	// revertTimeout bounds one revert (scaled workloads coming back).
	revertTimeout = 3 * time.Minute
)

// ErrScenarioUnsupported is returned for a scenario on a run that cannot
// have one.
var ErrScenarioUnsupported = errors.New("scenarios run only in load tests of a docker or Kubernetes workflow")

// validateScenario checks spec.Scenario against the workflow (applicable
// scenario and target, params, window inside the profile) and copies it so
// the caller's spec is not shared. It returns the workflow kind.
func (e *Engine) validateScenario(wfID string, spec *model.LoadTestSpec, profileSec float64) (string, error) {
	if spec.Scenario == nil {
		return "", nil
	}
	sc := *spec.Scenario
	sc.Params = maps.Clone(sc.Params)
	spec.Scenario = &sc
	if wfID == "" || e.workflows == nil || spec.Target.ConnectionID != "" {
		return "", ErrScenarioUnsupported
	}
	wf, ok := e.workflows.Workflow(wfID)
	if !ok {
		return "", ErrScenarioUnsupported
	}
	if err := scenario.Validate(wf, &sc, int(profileSec)); err != nil {
		return "", err
	}
	if sc.ID != model.ScenarioTrafficSpike && e.faults == nil {
		return "", errors.New("fault injection is not available")
	}
	return wf.Kind, nil
}

// scenarioState is a run's scenario while it runs.
type scenarioState struct {
	kind       string // workflow kind
	fault      scenario.Fault
	injected   bool
	injectedAt float64
	revertedAt float64
	injectErr  error
	obs        scenario.Observation
	revertErr  error
}

// prepareFault readies the injection tools before load starts; a failure
// is the scenario's (the load test still runs).
func (r *runner) prepareFault() {
	sc := r.run.Spec.Scenario
	if sc == nil || sc.ID == model.ScenarioTrafficSpike {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, prepareTimeout)
	defer cancel()
	if err := r.e.faults.PrepareFaults(ctx); err != nil && r.ctx.Err() == nil {
		r.mu.Lock()
		r.sc.injectErr = err
		r.mu.Unlock()
	}
}

// runFault injects the fault at AtSec and reverts it at AtSec+DurationSec,
// or at once when the run stops; the revert never depends on the run's
// context.
func (r *runner) runFault() {
	sc := *r.run.Spec.Scenario
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	r.mu.Lock()
	failed := r.sc.injectErr != nil
	r.mu.Unlock()
	if failed || !r.sleepUntil(timer, r.at(float64(sc.AtSec))) {
		return
	}
	ictx, cancel := context.WithTimeout(r.ctx, injectTimeout)
	f, err := r.e.faults.InjectFault(ictx, r.run.WorkflowID, sc, r.run.Spec.Target)
	cancel()
	if err != nil && r.ctx.Err() != nil {
		return // stopped while injecting: InjectFault reverted what it did
	}
	at := time.Since(r.start).Seconds()
	r.mu.Lock()
	r.sc.injectedAt = at
	r.sc.injectErr = err
	r.sc.fault = f
	r.sc.injected = err == nil
	r.run.Scenario = r.liveResult()
	r.mu.Unlock()
	r.e.publishStatus(r)
	if err != nil {
		return
	}
	r.sleepUntil(timer, r.at(float64(sc.AtSec+sc.DurationSec)))
	rctx, cancel := context.WithTimeout(context.Background(), revertTimeout)
	defer cancel()
	revertedAt := time.Since(r.start).Seconds()
	obs, rerr := f.Revert(rctx)
	r.mu.Lock()
	r.sc.revertedAt = revertedAt
	r.sc.obs, r.sc.revertErr = obs, rerr
	r.run.Scenario = r.liveResult()
	r.mu.Unlock()
	r.e.publishStatus(r)
}

// liveResult is the scenario result while the run is in progress: the
// injection (and revert) times without a verdict. Callers hold r.mu.
func (r *runner) liveResult() *model.ScenarioResult {
	sc := r.run.Spec.Scenario
	res := &model.ScenarioResult{ID: sc.ID, Target: sc.Target, InjectedAtSec: int(math.Round(r.sc.injectedAt)), Checks: []model.ScenarioCheck{}}
	if r.sc.fault != nil {
		res.TargetName, res.Detail = r.sc.fault.TargetName(), r.sc.fault.Detail()
	}
	if r.sc.injectErr != nil {
		res.Message = "The fault could not be injected: " + r.sc.injectErr.Error()
	}
	if r.sc.revertedAt > 0 {
		v := int(math.Round(r.sc.revertedAt))
		res.RevertedAtSec = &v
	}
	return res
}

// scenarioResult grades the run's scenario once the run is over (nil when
// the run stopped before the fault was due).
func (r *runner) scenarioResult() *model.ScenarioResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	sc := r.run.Spec.Scenario
	if sc == nil || r.start.IsZero() {
		return nil
	}
	in := scenario.Input{
		Spec: *sc, Timeline: r.run.Timeline, TimeoutMs: r.run.Spec.TimeoutMs,
		Closed: r.run.Spec.Profile.Mode == model.ModeConcurrency, Resources: r.run.Resources,
		Kubernetes: r.sc.kind == model.WorkflowKubernetes,
		Stopped:    r.stopped.Load(),
	}
	end := r.sched.duration()
	if !r.stopTime.IsZero() {
		end = math.Min(end, r.stopTime.Sub(r.start).Seconds())
	}
	if sc.ID == model.ScenarioTrafficSpike {
		if end <= float64(sc.AtSec) {
			return nil
		}
		in.InjectedAt, in.RevertedAt = float64(sc.AtSec), math.Min(float64(sc.AtSec+sc.DurationSec), end)
		if res := r.run.Resources; res != nil {
			in.TargetName = res.Service
		}
		out := scenario.Evaluate(in)
		out.Target = r.run.Spec.Target.NodeID
		out.Detail = fmt.Sprintf("load x%g from %d s to %d s", scenario.Param(sc.Params, "factor", scenario.DefaultSpikeFactor), sc.AtSec, sc.AtSec+sc.DurationSec)
		return &out
	}
	if !r.sc.injected && r.sc.injectErr == nil {
		return nil
	}
	in.InjectedAt, in.RevertedAt, in.InjectErr = r.sc.injectedAt, r.sc.revertedAt, r.sc.injectErr
	in.Observation = r.sc.obs
	if r.sc.fault != nil {
		in.TargetName, in.Detail = r.sc.fault.TargetName(), r.sc.fault.Detail()
	}
	if r.sc.injectErr != nil {
		in.InjectedAt = float64(sc.AtSec)
	}
	out := scenario.Evaluate(in)
	if r.sc.revertErr != nil {
		out.Checks = append(out.Checks, model.ScenarioCheck{Name: "Fault reverted", Passed: false, Detail: r.sc.revertErr.Error()})
		out.Message += " Reverting the fault failed: " + r.sc.revertErr.Error()
	}
	return &out
}

// withSpike multiplies the curve by factor on [at, at+dur): segments are
// split at both edges and the integrals recomputed.
func (s schedule) withSpike(at, dur, factor float64) schedule {
	end := at + dur
	var cuts []float64
	for _, g := range s.segs {
		cuts = append(cuts, g.start)
	}
	cuts = append(cuts, at, end, s.total)
	pts := map[float64]bool{}
	var times []float64
	for _, c := range cuts {
		if c >= 0 && c <= s.total && !pts[c] {
			pts[c] = true
			times = append(times, c)
		}
	}
	slices.Sort(times)
	var out schedule
	for i := 0; i+1 < len(times); i++ {
		a, b := times[i], times[i+1]
		if b <= a {
			continue
		}
		// Values just inside [a, b): the curve is linear there.
		from, to := s.target(a), s.valueBefore(b)
		if a >= at && b <= end {
			from, to = from*factor, to*factor
		}
		out.segs = append(out.segs, segment{start: out.total, dur: b - a, from: from, to: to, before: out.integral(out.total)})
		out.total += b - a
	}
	return out
}

// valueBefore is the curve's left limit at t (the end value of the
// segment that ends at t).
func (s schedule) valueBefore(t float64) float64 {
	for _, g := range s.segs {
		if t > g.start && t <= g.start+g.dur {
			x := t - g.start
			return g.from + (g.to-g.from)*x/g.dur
		}
	}
	return 0
}
