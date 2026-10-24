package loadtest

import (
	"context"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// vuReconcileInterval is how often the closed model adjusts the number of
// virtual users to the ramp.
const vuReconcileInterval = 50 * time.Millisecond

// minPreflightTimeout leaves room for cold starts: the first request to a
// Lambda-backed target makes Floci start the function's container.
const minPreflightTimeout = 30 * time.Second

// failurePacing is the minimum length of a failed closed-model iteration, so
// virtual users hitting a dead target back off instead of spinning.
const failurePacing = 10 * time.Millisecond

// elsewhereTimeout bounds rendering the workflow's other environments for
// the no-HPA verdict.
const elsewhereTimeout = 20 * time.Second

type runner struct {
	e          *Engine
	sched      schedule
	exec       executor
	conn       *model.Connection // nil for sandbox targets; used to redact errors
	timeout    time.Duration
	maxWorkers int
	col        *collector
	resources  *resourceRecorder // docker and Kubernetes workflow targets; nil otherwise
	sc         scenarioState     // guarded by mu
	chaos      sync.WaitGroup    // the scenario's inject/revert goroutine
	done       chan struct{}     // closed when execute returns

	ctx     context.Context
	cancel  context.CancelFunc
	stopped atomic.Bool

	inFlight atomic.Int64
	seq      atomic.Int64
	reqs     sync.WaitGroup

	mu       sync.Mutex
	run      model.LoadTestRun
	secs     []secStat
	start    time.Time // when load generation began (after preflight)
	stopTime time.Time
}

// snapshot copies the run; the timeline is included only when asked.
func (r *runner) snapshot(timeline bool) model.LoadTestRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.run
	if timeline {
		out.Timeline = slices.Clone(r.run.Timeline)
	} else {
		out.Timeline = []model.LoadSample{}
		out.Resources = resourcesSummary(r.run.Resources)
	}
	return out
}

func (r *runner) stop() {
	r.mu.Lock()
	if r.stopTime.IsZero() {
		r.stopTime = time.Now()
	}
	r.mu.Unlock()
	r.stopped.Store(true)
	r.cancel()
}

func (r *runner) execute() {
	defer close(r.done)
	defer r.exec.close()
	defer r.cancel()

	r.prepareFault()
	if m := r.e.monitor; m != nil && r.run.WorkflowID != "" && r.run.Spec.Target.ConnectionID == "" {
		if probe := m.MonitorResources(r.ctx, r.run.WorkflowID, r.run.Spec.Target); probe != nil {
			r.resources = newResourceRecorder(probe)
		}
	}

	pctx, cancel := context.WithTimeout(r.ctx, max(r.timeout, minPreflightTimeout))
	err := r.exec.preflight(pctx)
	cancel()
	if err != nil {
		if r.stopped.Load() {
			r.finish(nil)
			return
		}
		r.finish(err)
		return
	}

	r.mu.Lock()
	r.start = time.Now()
	r.mu.Unlock()
	if r.resources != nil {
		r.resources.record(0)
	}
	if sc := r.run.Spec.Scenario; sc != nil && sc.ID != model.ScenarioTrafficSpike {
		r.chaos.Add(1)
		go func() {
			defer r.chaos.Done()
			r.runFault()
		}()
	}

	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		r.sample()
	}()
	if r.run.Spec.Profile.Mode == model.ModeConcurrency {
		r.closedModel()
	} else {
		r.openModel()
	}
	sampler.Wait()
	// Requests issued before the end finish within their own timeout and
	// count towards the summary; after Stop they are canceled and dropped.
	r.reqs.Wait()
	r.chaos.Wait()
	r.finish(nil)
}

// fire executes one request and records it unless the run was stopped while
// it was in flight. It reports the request's latency and whether it failed.
func (r *runner) fire() (time.Duration, bool) {
	r.inFlight.Add(1)
	defer r.inFlight.Add(-1)
	seq := r.seq.Add(1)
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	t0 := time.Now()
	o := r.exec.do(ctx, seq)
	lat := time.Since(t0)
	cancel()
	if r.ctx.Err() == nil {
		r.col.record(lat, o)
	}
	return lat, o.errKind != ""
}

func (r *runner) at(sec float64) time.Time {
	return r.start.Add(time.Duration(sec * float64(time.Second)))
}

// sleepUntil waits for t and reports false if the run was stopped first.
func (r *runner) sleepUntil(timer *time.Timer, t time.Time) bool {
	d := time.Until(t)
	if d <= 0 {
		return r.ctx.Err() == nil
	}
	timer.Reset(d)
	select {
	case <-r.ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}

// openModel issues arrivals at the scheduled rate regardless of how fast the
// target answers. Arrivals that find every worker busy are recorded as
// client_saturated instead of being delayed.
func (r *runner) openModel() {
	slots := make(chan struct{}, r.maxWorkers)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	total := r.sched.duration()
	for k := 0; ; k++ {
		t, ok := r.sched.arrival(float64(k) + 0.5)
		if !ok || t >= total {
			return
		}
		if !r.sleepUntil(timer, r.at(t)) {
			return
		}
		select {
		case slots <- struct{}{}:
			r.reqs.Add(1)
			go func() {
				defer r.reqs.Done()
				defer func() { <-slots }()
				r.fire()
			}()
		default:
			r.col.saturated()
		}
	}
}

// closedModel runs virtual users that loop request after request; their
// number follows the ramp.
func (r *runner) closedModel() {
	var mu sync.Mutex
	var alive []bool
	desired := 0
	vu := func(i int) {
		defer r.reqs.Done()
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		for {
			mu.Lock()
			if i >= desired || r.ctx.Err() != nil {
				alive[i] = false
				mu.Unlock()
				return
			}
			mu.Unlock()
			if lat, failed := r.fire(); failed && lat < failurePacing {
				r.sleepUntil(timer, time.Now().Add(failurePacing-lat))
			}
		}
	}
	setUsers := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		desired = n
		for len(alive) < n {
			alive = append(alive, false)
		}
		for i := range n {
			if !alive[i] {
				alive[i] = true
				r.reqs.Add(1)
				go vu(i)
			}
		}
	}
	defer setUsers(0)

	end := r.at(r.sched.duration())
	tick := time.NewTicker(vuReconcileInterval)
	defer tick.Stop()
	deadline := time.NewTimer(time.Until(end))
	defer deadline.Stop()
	for {
		el := time.Since(r.start).Seconds()
		setUsers(min(int(math.Round(r.sched.target(el))), r.maxWorkers))
		select {
		case <-r.ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// sample publishes one LoadSample per second of the schedule.
func (r *runner) sample() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	total := int(r.sched.duration())
	for k := 1; k <= total; k++ {
		if !r.sleepUntil(timer, r.at(float64(k))) {
			return
		}
		target := r.sched.integral(float64(k)) - r.sched.integral(float64(k-1))
		s, st := r.col.roll(float64(k), target, int(r.inFlight.Load()))
		if r.resources != nil {
			r.resources.record(float64(k))
		}
		r.mu.Lock()
		r.run.Timeline = append(r.run.Timeline, s)
		r.secs = append(r.secs, st)
		r.mu.Unlock()
		r.e.publish(model.Event{
			Type:       model.EventLoadTestSample,
			WorkflowID: r.run.WorkflowID,
			Data:       sampleEvent{RunID: r.run.ID, Sample: s},
		})
	}
}

type sampleEvent struct {
	RunID  string           `json:"runId"`
	Sample model.LoadSample `json:"sample"`
}

// finish settles the run status, persists it and releases its slot.
func (r *runner) finish(preflightErr error) {
	now := time.Now()
	// The status leaves "running" only once resources and the scenario
	// verdict are set, so pollers never see a finished run without them.
	var status model.LoadTestStatus
	r.mu.Lock()
	r.run.EndedAt = &now
	switch {
	case preflightErr != nil:
		status = model.LoadErrored
		r.run.Error = "target unreachable: " + redactSecrets(preflightErr.Error(), r.conn)
	case r.start.IsZero():
		status = model.LoadStopped
	default:
		active := time.Duration(r.sched.duration() * float64(time.Second))
		if !r.stopTime.IsZero() {
			active = min(active, r.stopTime.Sub(r.start))
		}
		sum := r.col.summary(active, r.secs)
		r.run.Summary = &sum
		r.run.Thresholds = evaluate(r.run.Spec.Thresholds, &sum)
		status = model.LoadPassed
		for _, th := range r.run.Thresholds {
			if !th.Passed {
				status = model.LoadFailed
			}
		}
		if r.stopped.Load() {
			status = model.LoadStopped
		}
	}
	r.mu.Unlock()

	if r.resources != nil && preflightErr == nil {
		r.mu.Lock()
		timeline, sum, secs, env := slices.Clone(r.run.Timeline), r.run.Summary, slices.Clone(r.secs), r.run.Environment
		r.mu.Unlock()
		if sum != nil {
			res := r.resources.result(timeline, sum)
			if res != nil {
				res.Scaling = r.resources.scaling(secs, env, r.elsewhere(env))
				foldScaling(res)
			}
			r.mu.Lock()
			r.run.Resources = res
			r.mu.Unlock()
		}
	}
	var sc *model.ScenarioResult
	if r.run.Spec.Scenario != nil {
		sc = r.scenarioResult()
	}
	r.mu.Lock()
	r.run.Scenario = sc
	r.run.Status = status
	r.mu.Unlock()

	if err := r.e.save(r.snapshot(true)); err != nil {
		r.mu.Lock()
		msg := "results not saved: " + err.Error()
		if r.run.Error != "" {
			msg = r.run.Error + "; " + msg
		}
		r.run.Error = msg
		r.mu.Unlock()
		r.e.publishStatus(r)
		return // stays in memory so Get keeps working
	}
	r.e.publishStatus(r)
	r.e.forget(r)
}

// elsewhere lists the workflow's other environments that autoscale a
// workload (nil without a workflow inspector).
func (r *runner) elsewhere(env string) func(kind, name string) []string {
	w := r.e.workflows
	if w == nil || r.run.WorkflowID == "" {
		return nil
	}
	return func(kind, name string) []string {
		ctx, cancel := context.WithTimeout(context.Background(), elsewhereTimeout)
		defer cancel()
		return w.AutoscaledIn(ctx, r.run.WorkflowID, env, kind, name)
	}
}
