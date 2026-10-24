package challenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Runtime is the workflow and its local runtime as the checks need them.
type Runtime interface {
	// Analyze opens the workflow for validate, rules_clean and has in
	// environment env ("" = the active one).
	Analyze(ctx context.Context, wfID, env string) (*Analysis, error)
	// Deploy applies the workflow's current files (compose up, Kubernetes
	// up of the active environment, Floci apply), waits for the run and
	// returns its ID.
	Deploy(ctx context.Context, wfID string) (runID string, err error)
	// Target is the load target of an endpoint of the deployed workflow.
	Target(ctx context.Context, wfID string, t model.ChallengeTarget) (model.LoadTarget, error)
	// NodeID is the graph node ID of a compose service or Kubernetes
	// workload, by name.
	NodeID(ctx context.Context, wfID, name string) (string, error)
	// Environment is the workflow's active environment ("" = base).
	Environment(wfID string) string
	// Readiness is the production readiness score of environment env ("" =
	// the active one).
	Readiness(ctx context.Context, wfID, env string) (int, error)
}

// LoadTests runs load tests (loadtest.Engine).
type LoadTests interface {
	Start(ctx context.Context, wfID string, spec model.LoadTestSpec) (*model.LoadTestRun, error)
	Get(id string) (*model.LoadTestRun, bool)
	Stop(id string) error
}

// Config wires a Checker.
type Config struct {
	Runtime   Runtime
	LoadTests LoadTests
	// Dir is the directory of a workflow's challenge.json (the last check).
	Dir func(wfID string) string
	// Finished is called with every check that ends (passed, failed,
	// error or canceled), e.g. to record progress.
	Finished func(wfID string, res model.ChallengeResult)
	// HTTP probes endpoints (default: 3 s timeout per request).
	HTTP *http.Client
	// Poll is how often runs and endpoints are polled (default 1 s);
	// ReadyTimeout bounds the wait for an endpoint (default 3 min).
	Poll         time.Duration
	ReadyTimeout time.Duration
	Logger       *slog.Logger
}

// Errors of Start and Cancel.
var (
	ErrRunning    = errors.New("a check of this challenge is already running")
	ErrNotRunning = errors.New("no check of this challenge is running")
)

// Checker runs "Check my solution": every check in order, deploying
// before the first one that needs the runtime. One check per workflow at a
// time; the latest result is kept in <Dir>/challenge.json.
type Checker struct {
	cfg    Config
	mu     sync.Mutex
	active map[string]*check
	wg     sync.WaitGroup
}

type check struct {
	res    model.ChallengeResult
	cancel context.CancelFunc
}

const resultFile = "challenge.json"

// New returns a Checker.
func New(cfg Config) *Checker {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 3 * time.Second}
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Second
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = 3 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Checker{cfg: cfg, active: map[string]*check{}}
}

// Start begins checking challenge c on the workflow and returns the first
// state (status running, every check pending).
func (c *Checker) Start(wfID string, ch model.Challenge) (model.ChallengeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.active[wfID]; ok {
		return model.ChallengeResult{}, ErrRunning
	}
	res := model.ChallengeResult{
		Challenge: ch.ID, Status: model.ChallengeRunning, Environment: c.cfg.Runtime.Environment(wfID),
		StartedAt: time.Now().UTC(), Goals: make([]model.ChallengeGoalResult, len(ch.Checks)),
	}
	for i, k := range ch.Checks {
		res.Goals[i] = model.ChallengeGoalResult{ID: k.ID, Label: k.Label, Type: k.Type, Status: model.ChallengePending, Checks: []model.ChallengeCheckResult{}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ck := &check{res: res, cancel: cancel}
	c.active[wfID] = ck
	c.save(wfID, res)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer cancel()
		c.run(ctx, wfID, ch, ck)
	}()
	return cloneResult(res), nil
}

// Cancel stops a running check: the current load test is stopped (its
// scenario reverted by the engine) and the check ends canceled.
func (c *Checker) Cancel(wfID string) error {
	c.mu.Lock()
	ck, ok := c.active[wfID]
	c.mu.Unlock()
	if !ok {
		return ErrNotRunning
	}
	ck.cancel()
	return nil
}

// Close cancels every running check and waits for them to end.
func (c *Checker) Close() {
	c.mu.Lock()
	for _, ck := range c.active {
		ck.cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// Last is the running check, else the latest finished one (nil before the
// first). A check left running by a daemon that stopped reads canceled.
func (c *Checker) Last(wfID string) *model.ChallengeResult {
	c.mu.Lock()
	if ck, ok := c.active[wfID]; ok {
		r := cloneResult(ck.res)
		c.mu.Unlock()
		return &r
	}
	c.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(c.cfg.Dir(wfID), resultFile))
	if err != nil {
		return nil
	}
	var r model.ChallengeResult
	if json.Unmarshal(data, &r) != nil {
		return nil
	}
	if r.Status == model.ChallengeRunning {
		interrupt(&r, "Interrupted: Nimbo stopped during the check.")
	}
	return &r
}

func (c *Checker) update(wfID string, ck *check, fn func(r *model.ChallengeResult)) {
	c.mu.Lock()
	fn(&ck.res)
	r := cloneResult(ck.res)
	c.mu.Unlock()
	c.save(wfID, r)
}

func (c *Checker) save(wfID string, r model.ChallengeResult) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err == nil {
		dir := c.cfg.Dir(wfID)
		if err = os.MkdirAll(dir, 0o755); err == nil {
			err = os.WriteFile(filepath.Join(dir, resultFile), data, 0o644)
		}
	}
	if err != nil {
		c.cfg.Logger.Warn("save challenge result", "workflow", wfID, "err", err)
	}
}

// deployState is the check's one deploy: done once, before the first
// check that needs the runtime.
type deployState struct {
	done  bool
	runID string
	err   error
}

func (c *Checker) run(ctx context.Context, wfID string, ch model.Challenge, ck *check) {
	defer func() {
		c.mu.Lock()
		delete(c.active, wfID)
		c.mu.Unlock()
		if c.cfg.Finished != nil {
			c.cfg.Finished(wfID, *c.Last(wfID))
		}
	}()
	finish := func(fn func(r *model.ChallengeResult)) {
		c.update(wfID, ck, func(r *model.ChallengeResult) {
			fn(r)
			now := time.Now().UTC()
			r.EndedAt, r.Phase = &now, ""
			r.Score = Score(r.Goals)
		})
	}
	var dep deployState
	deploy := func() error {
		if !dep.done {
			c.update(wfID, ck, func(r *model.ChallengeResult) { r.Phase = "Deploying your changes" })
			dep.runID, dep.err = c.cfg.Runtime.Deploy(ctx, wfID)
			dep.done = true
			c.update(wfID, ck, func(r *model.ChallengeResult) { r.DeployRun = dep.runID })
		}
		return dep.err
	}
	analyses := map[string]*Analysis{}
	for i, k := range ch.Checks {
		c.update(wfID, ck, func(r *model.ChallengeResult) {
			r.Phase = k.Label
			r.Goals[i].Status = model.ChallengeRunning
		})
		gr := c.grade(ctx, wfID, k, analyses, deploy, &dep, func(runID string) {
			c.update(wfID, ck, func(r *model.ChallengeResult) { r.Goals[i].RunID = runID })
		})
		if ctx.Err() != nil {
			finish(func(r *model.ChallengeResult) {
				r.Goals[i].RunID = gr.RunID
				interrupt(r, "Canceled.")
			})
			return
		}
		c.update(wfID, ck, func(r *model.ChallengeResult) { r.Goals[i] = gr })
	}
	finish(func(r *model.ChallengeResult) {
		r.Passed = true
		for _, g := range r.Goals {
			r.Passed = r.Passed && g.Passed
		}
		r.Status = model.ChallengeFailed
		if r.Passed {
			r.Status = model.ChallengePassed
		}
	})
}

// grade runs one check.
func (c *Checker) grade(ctx context.Context, wfID string, k model.ChallengeCheck, analyses map[string]*Analysis, deploy func() error, dep *deployState, started func(string)) model.ChallengeGoalResult {
	fail := func(status, format string, a ...any) model.ChallengeGoalResult {
		return model.ChallengeGoalResult{ID: k.ID, Label: k.Label, Type: k.Type, Status: status, Detail: fmt.Sprintf(format, a...), Checks: []model.ChallengeCheckResult{}}
	}
	switch k.Type {
	case model.CheckValidate, model.CheckRulesClean, model.CheckHas:
		a, ok := analyses[k.Environment]
		if !ok {
			var err error
			if a, err = c.cfg.Runtime.Analyze(ctx, wfID, k.Environment); err != nil {
				return fail(model.ChallengeError, "%v", err)
			}
			analyses[k.Environment] = a
		}
		return GradeStatic(k, a)
	case model.CheckReadiness:
		score, err := c.cfg.Runtime.Readiness(ctx, wfID, k.Readiness.Environment)
		if err != nil {
			return fail(model.ChallengeError, "%v", err)
		}
		res := fail(model.ChallengeFailed, "")
		res.Checks = append(res.Checks, model.ChallengeCheckResult{
			Label: "Readiness score", Passed: score >= k.Readiness.MinScore,
			Actual: fmt.Sprintf("%d", score), Expected: fmt.Sprintf("≥ %d", k.Readiness.MinScore),
		})
		finishGoal(&res)
		return res
	}
	if err := deploy(); err != nil {
		if ctx.Err() != nil {
			return fail(model.ChallengeCanceled, "Canceled.")
		}
		res := fail(model.ChallengeFailed, "The deploy failed: %v", err)
		res.RunID = dep.runID
		res.Checks = append(res.Checks, model.ChallengeCheckResult{Label: "Deploy", Actual: "failed", Expected: "succeeds"})
		if k.Type != model.CheckDeploy {
			res.Status = model.ChallengeError
		}
		return res
	}
	switch k.Type {
	case model.CheckDeploy:
		res := fail(model.ChallengePassed, "")
		res.RunID = dep.runID
		res.Checks = append(res.Checks, model.ChallengeCheckResult{Label: "Deploy", Passed: true, Actual: "succeeded", Expected: "succeeds"})
		finishGoal(&res)
		return res
	case model.CheckHTTP:
		return c.httpCheck(ctx, wfID, k)
	}
	spec, err := c.spec(ctx, wfID, k)
	if err != nil {
		return fail(model.ChallengeError, "%v", err)
	}
	if _, _, err := c.probe(ctx, spec.Target, 0, ""); err != nil {
		return fail(model.ChallengeError, "%v", err)
	}
	run, err := c.cfg.LoadTests.Start(ctx, wfID, spec)
	if err != nil {
		return fail(model.ChallengeError, "Couldn't start the load test: %v", err)
	}
	started(run.ID)
	ticker := time.NewTicker(c.cfg.Poll)
	defer ticker.Stop()
	for run.Status == model.LoadRunning {
		select {
		case <-ctx.Done():
			_ = c.cfg.LoadTests.Stop(run.ID)
			res := fail(model.ChallengeCanceled, "Canceled.")
			res.RunID = run.ID
			return res
		case <-ticker.C:
		}
		if r, ok := c.cfg.LoadTests.Get(run.ID); ok {
			run = r
		}
	}
	return GradeRun(k, run)
}

// httpCheck waits until the endpoint answers the expected status (and
// body) or the ready timeout passes.
func (c *Checker) httpCheck(ctx context.Context, wfID string, k model.ChallengeCheck) model.ChallengeGoalResult {
	h := k.HTTP
	res := model.ChallengeGoalResult{ID: k.ID, Label: k.Label, Type: k.Type, Checks: []model.ChallengeCheckResult{}}
	t, err := c.cfg.Runtime.Target(ctx, wfID, h.ChallengeTarget)
	if err != nil {
		res.Status, res.Detail = model.ChallengeError, err.Error()
		return res
	}
	want := h.Status
	if want == 0 {
		want = http.StatusOK
	}
	status, body, err := c.probe(ctx, t, want, h.Contains)
	r := model.ChallengeCheckResult{Label: fmt.Sprintf("%s %s", t.HTTP.Method, h.Path), Expected: fmt.Sprintf("HTTP %d", want), Actual: status}
	if h.Contains != "" {
		r.Expected += fmt.Sprintf(" with %q", h.Contains)
		if status != "" && body != "" {
			r.Actual += fmt.Sprintf(": %.80q", body)
		}
	}
	r.Passed = err == nil
	if err != nil && status == "" {
		r.Actual = err.Error()
	}
	res.Checks = append(res.Checks, r)
	finishGoal(&res)
	return res
}

// spec builds the load test of a load or scenario check: its target on
// the deployed workflow, traffic, thresholds and scenario (target
// resolved to a node).
func (c *Checker) spec(ctx context.Context, wfID string, k model.ChallengeCheck) (model.LoadTestSpec, error) {
	var (
		t      model.ChallengeTarget
		ls     model.ChallengeLoadSpec
		expect []model.ChallengeMetric
	)
	if k.Load != nil {
		t, ls, expect = k.Load.Target, k.Load.Spec, k.Load.Expect
	} else {
		t, ls, expect = k.Scenario.Target, k.Scenario.Spec, k.Scenario.Expect.Metrics
	}
	target, err := c.cfg.Runtime.Target(ctx, wfID, t)
	if err != nil {
		return model.LoadTestSpec{}, err
	}
	profile := model.LoadProfile{Preset: "custom", Mode: ls.Mode, Stages: slices.Clone(ls.Stages)}
	if ls.Preset != "" {
		p, ok := preset(ls.Preset)
		if !ok {
			return model.LoadTestSpec{}, fmt.Errorf("unknown preset %q", ls.Preset)
		}
		profile = p
	}
	if profile.Mode == "" {
		profile.Mode = model.ModeRPS
	}
	spec := model.LoadTestSpec{
		Name: "Challenge · " + k.Label, Target: target, Profile: profile,
		Thresholds: Thresholds(expect), TimeoutMs: ls.TimeoutMs, MaxWorkers: ls.MaxWorkers,
	}
	if spec.TimeoutMs == 0 {
		spec.TimeoutMs = 2000
	}
	if k.Scenario != nil {
		sc := k.Scenario.Scenario
		sc.Params = cloneMap(sc.Params)
		if sc.Target != "" {
			if sc.Target, err = c.cfg.Runtime.NodeID(ctx, wfID, sc.Target); err != nil {
				return model.LoadTestSpec{}, err
			}
		}
		spec.Scenario = &sc
	}
	return spec, nil
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var placeholder = regexp.MustCompile(`\{\{\s*(seq|rand|uuid|now)\s*\}\}`)

// probe polls t until three answers in a row have status want (0: any
// 2xx) and contain contains, so a service still starting (or restarting
// after a previous scenario) doesn't count against the check. It returns
// the last status and a body excerpt.
func (c *Checker) probe(ctx context.Context, t model.LoadTarget, want int, contains string) (string, string, error) {
	if t.HTTP == nil {
		return "", "", nil
	}
	url := placeholder.ReplaceAllString(t.HTTP.URL, "1")
	method := t.HTTP.Method
	if method == "" {
		method = http.MethodGet
	}
	deadline := time.Now().Add(c.cfg.ReadyTimeout)
	streak, last, body := 0, "", ""
	for streak < 3 {
		if time.Now().After(deadline) {
			return last, body, fmt.Errorf("%s %s did not answer as expected within %s (last: %s)", method, url, c.cfg.ReadyTimeout, orNone(last))
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return "", "", err
		}
		for k, v := range t.HTTP.Headers {
			req.Header.Set(k, v)
		}
		if h := t.HTTP.Headers["Host"]; h != "" {
			req.Host = h
		}
		resp, err := c.cfg.HTTP.Do(req)
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			last, body = resp.Status, string(b)
			okStatus := resp.StatusCode == want || (want == 0 && resp.StatusCode >= 200 && resp.StatusCode < 300)
			if okStatus && strings.Contains(body, contains) {
				streak++
			} else {
				streak = 0
			}
		} else {
			streak, last = 0, err.Error()
		}
		if ctx.Err() != nil {
			return last, body, ctx.Err()
		}
		if streak < 3 {
			select {
			case <-ctx.Done():
				return last, body, ctx.Err()
			case <-time.After(c.cfg.Poll):
			}
		}
	}
	return last, body, nil
}

func orNone(s string) string {
	if s == "" {
		return "no answer"
	}
	return s
}

func interrupt(r *model.ChallengeResult, msg string) {
	r.Status, r.Error, r.Passed = model.ChallengeCanceled, msg, false
	if r.EndedAt == nil {
		now := time.Now().UTC()
		r.EndedAt = &now
	}
	r.Phase = ""
	for i := range r.Goals {
		if r.Goals[i].Status == model.ChallengePending || r.Goals[i].Status == model.ChallengeRunning {
			r.Goals[i].Status, r.Goals[i].Detail = model.ChallengeCanceled, "Canceled."
		}
	}
}

func cloneResult(r model.ChallengeResult) model.ChallengeResult {
	out := r
	out.Goals = make([]model.ChallengeGoalResult, len(r.Goals))
	for i, g := range r.Goals {
		g.Checks = slices.Clone(g.Checks)
		out.Goals[i] = g
	}
	return out
}
