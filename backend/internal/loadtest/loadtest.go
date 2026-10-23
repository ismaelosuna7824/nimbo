// Package loadtest generates HTTP, AWS SDK, SQL, Redis and MongoDB traffic
// against a workflow's sandbox or a saved connection (an already-deployed
// service), measures it and keeps the results.
//
// Profiles are piecewise-linear: every stage ramps from the previous stage's
// target (0 for the first) to its own over DurationSec, so a stage with
// DurationSec 0 is an instant jump. In rps mode (open model) arrivals are
// spread evenly at the instantaneous rate and never wait for the target; in
// concurrency mode (closed model) virtual users loop request after request.
//
// Latency statistics cover successful requests only; failures are reported
// through the error rate, error kinds and status codes. AWS resources may be
// given by name, URL (SQS) or ARN; an ARN also selects the region (default
// us-east-1 for the sandbox, the connection's region otherwise). DynamoDB
// items and keys are plain JSON objects (strings become S, numbers N,
// booleans BOOL, arrays L, objects M).
//
// Traffic that leaves the local sandbox (any target with a ConnectionID, or
// an HTTP URL whose host is not local) requires LoadTestSpec.ConfirmExternal.
package loadtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Resolver locates a workflow's sandbox emulators on the host.
type Resolver interface {
	EmulatorURL(wfID string, cloud model.Cloud) (string, bool)
	HostURL(wfID, emulatorURL string) string
}

// ConnectionResolver looks up saved connections, secrets in clear.
type ConnectionResolver interface {
	Connection(id string) (model.Connection, bool)
}

// WorkflowInspector tells the engine about docker and Kubernetes
// workflows: the environment a run is recorded under and, for the
// autoscaling verdict, which other environments autoscale a workload.
type WorkflowInspector interface {
	// ActiveEnvironment is the environment a docker or Kubernetes workflow
	// renders and runs ("" = the base); ok is false for other workflows.
	ActiveEnvironment(wfID string) (env string, ok bool)
	// AutoscaledIn lists the workflow's environments other than env whose
	// rendered objects have a HorizontalPodAutoscaler scaling the workload
	// kind/name.
	AutoscaledIn(ctx context.Context, wfID, env, kind, name string) []string
}

// EngineConfig configures an Engine. Resolver may be nil when only plain
// host URLs are tested; Connections may be nil when no connection is used.
// Monitor (optional) samples the containers of docker and Kubernetes
// workflows during their runs (LoadTestRun.Resources); Workflows
// (optional) records Kubernetes runs' environment.
type EngineConfig struct {
	Home        string
	Events      model.Publisher
	Resolver    Resolver
	Connections ConnectionResolver
	Monitor     ResourceMonitor
	Workflows   WorkflowInspector
}

const (
	maxConcurrentRuns = 3
	defaultTimeoutMs  = 10_000
	defaultMaxWorkers = 1000
	maxDurationSec    = 24 * 60 * 60
	// maxPoolSize caps database client pools: real servers have connection
	// limits far below the HTTP worker cap.
	maxPoolSize = 100
)

var (
	ErrNotFound    = errors.New("load test not found")
	ErrTooManyRuns = fmt.Errorf("%d load tests are already running; stop one or wait for it to finish", maxConcurrentRuns)
	// ErrConfirmationRequired is returned before any request is sent when
	// the target is outside the local sandbox and ConfirmExternal is false.
	ErrConfirmationRequired = errors.New("load test targets a service outside the local sandbox; confirmExternal is required")
	ErrConnectionNotFound   = errors.New("connection not found")
	// ErrRunActive is returned by DeleteConnectionRuns while one of the
	// connection's runs is still running.
	ErrRunActive = errors.New("a load test of this connection is running; stop it first")
)

// Engine runs load tests and stores their results under
// <home>/workflows/<wf>/loadtests/<id>.json (workflow runs) or
// <home>/connections/<conn>/loadtests/<id>.json (connection runs). At most
// maxConcurrentRuns run at once, whoever owns them.
type Engine struct {
	home        string
	events      model.Publisher
	resolver    Resolver
	connections ConnectionResolver
	monitor     ResourceMonitor
	workflows   WorkflowInspector

	mu   sync.Mutex
	runs map[string]*runner // running, plus finished runs that could not be saved
}

func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{home: cfg.Home, events: cfg.Events, resolver: cfg.Resolver, connections: cfg.Connections, monitor: cfg.Monitor, workflows: cfg.Workflows, runs: map[string]*runner{}}
}

// Start validates spec, then runs it in the background on behalf of the
// workflow. The returned run is in status running; a target that fails its
// preflight request ends the run with status error. Targets with a
// ConnectionID run against that saved connection.
func (e *Engine) Start(ctx context.Context, wfID string, spec model.LoadTestSpec) (*model.LoadTestRun, error) {
	if !safeName(wfID) {
		return nil, fmt.Errorf("invalid workflow id %q", wfID)
	}
	return e.start(ctx, wfID, "", spec)
}

// StartConnection is Start for a run owned by a saved connection (started
// from the Connections library). spec.Target.ConnectionID defaults to connID
// and must not name another connection.
func (e *Engine) StartConnection(ctx context.Context, connID string, spec model.LoadTestSpec) (*model.LoadTestRun, error) {
	if !safeName(connID) {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connID)
	}
	switch spec.Target.ConnectionID {
	case "":
		spec.Target.ConnectionID = connID
	case connID:
	default:
		return nil, fmt.Errorf("the target uses connection %q but the run belongs to connection %q", spec.Target.ConnectionID, connID)
	}
	return e.start(ctx, "", connID, spec)
}

// start runs spec owned by exactly one of wfID / connID.
func (e *Engine) start(ctx context.Context, wfID, connID string, spec model.LoadTestSpec) (*model.LoadTestRun, error) {
	if err := normalizeSpec(&spec); err != nil {
		return nil, err
	}
	p, err := e.plan(wfID, &spec)
	if err != nil {
		return nil, err
	}
	if p.external && !spec.ConfirmExternal {
		return nil, ErrConfirmationRequired
	}
	exec, err := e.newExecutor(ctx, wfID, &spec, p)
	if err != nil {
		if p.conn != nil {
			return nil, errors.New(redactSecrets(err.Error(), p.conn))
		}
		return nil, err
	}
	r := &runner{
		e:          e,
		sched:      newSchedule(spec.Profile.Stages),
		exec:       exec,
		conn:       p.conn,
		timeout:    time.Duration(spec.TimeoutMs) * time.Millisecond,
		maxWorkers: spec.MaxWorkers,
		col:        newCollector(),
		run: model.LoadTestRun{
			ID:           newRunID(),
			WorkflowID:   wfID,
			ConnectionID: connID,
			External:     p.external,
			Spec:         spec,
			Status:       model.LoadRunning,
			StartedAt:    time.Now().UTC(),
			Timeline:     []model.LoadSample{},
		},
	}
	if e.workflows != nil && wfID != "" {
		r.run.Environment, _ = e.workflows.ActiveEnvironment(wfID)
	}
	r.ctx, r.cancel = context.WithCancel(context.WithoutCancel(ctx))

	e.mu.Lock()
	running := 0
	for _, other := range e.runs {
		if other.snapshot(false).Status == model.LoadRunning {
			running++
		}
	}
	if running >= maxConcurrentRuns {
		e.mu.Unlock()
		r.cancel()
		exec.close()
		return nil, ErrTooManyRuns
	}
	e.runs[r.run.ID] = r
	e.mu.Unlock()

	e.publishStatus(r)
	go r.execute()
	out := r.snapshot(true)
	return &out, nil
}

// Stop ends a running test; requests in flight are canceled and the run ends
// with status stopped. Stopping a finished run is a no-op.
func (e *Engine) Stop(id string) error {
	e.mu.Lock()
	r, ok := e.runs[id]
	e.mu.Unlock()
	if ok {
		r.stop()
		return nil
	}
	if _, ok := e.Get(id); ok {
		return nil
	}
	return ErrNotFound
}

// Get returns a run with its timeline.
func (e *Engine) Get(id string) (*model.LoadTestRun, bool) {
	if !safeName(id) {
		return nil, false
	}
	e.mu.Lock()
	r, ok := e.runs[id]
	e.mu.Unlock()
	if ok {
		out := r.snapshot(true)
		return &out, true
	}
	for _, pattern := range []string{
		filepath.Join(e.home, "workflows", "*", "loadtests", id+".json"),
		filepath.Join(e.home, "connections", "*", "loadtests", id+".json"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if run, err := loadRun(m); err == nil {
				return run, true
			}
		}
	}
	return nil, false
}

// List returns the workflow's runs, newest first, with empty timelines.
func (e *Engine) List(wfID string) []model.LoadTestRun {
	if !safeName(wfID) {
		return []model.LoadTestRun{}
	}
	return e.list(e.dir(wfID), func(run *model.LoadTestRun) bool { return run.WorkflowID == wfID })
}

// ListConnection returns the runs owned by the connection (started from the
// Connections library), newest first, with empty timelines.
func (e *Engine) ListConnection(connID string) []model.LoadTestRun {
	if !safeName(connID) {
		return []model.LoadTestRun{}
	}
	return e.list(e.connDir(connID), func(run *model.LoadTestRun) bool {
		return run.WorkflowID == "" && run.ConnectionID == connID
	})
}

// DeleteConnectionRuns deletes the run history of a connection. It fails
// with ErrRunActive, deleting nothing, while one of its runs is running.
func (e *Engine) DeleteConnectionRuns(connID string) error {
	if !safeName(connID) {
		return fmt.Errorf("%w: %q", ErrConnectionNotFound, connID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var owned []string
	for id, r := range e.runs {
		s := r.snapshot(false)
		if s.WorkflowID != "" || s.ConnectionID != connID {
			continue
		}
		if s.Status == model.LoadRunning {
			return ErrRunActive
		}
		owned = append(owned, id)
	}
	for _, id := range owned {
		delete(e.runs, id)
	}
	if err := os.RemoveAll(e.connDir(connID)); err != nil {
		return err
	}
	// The connection's directory only holds run history; keep it if
	// anything else lives there.
	_ = os.Remove(filepath.Dir(e.connDir(connID)))
	return nil
}

// list merges live runs matching own with the runs saved in dir.
func (e *Engine) list(dir string, own func(*model.LoadTestRun) bool) []model.LoadTestRun {
	out := []model.LoadTestRun{}
	e.mu.Lock()
	var live []*runner
	for _, r := range e.runs {
		live = append(live, r)
	}
	e.mu.Unlock()
	seen := map[string]bool{}
	for _, r := range live {
		if s := r.snapshot(false); own(&s) {
			seen[s.ID] = true
			out = append(out, s)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		id, ok := strings.CutSuffix(ent.Name(), ".json")
		if !ok || ent.IsDir() || seen[id] {
			continue
		}
		run, err := loadRun(filepath.Join(dir, ent.Name()))
		if err != nil || !own(run) {
			continue
		}
		run.Timeline = []model.LoadSample{}
		run.Resources = resourcesSummary(run.Resources)
		out = append(out, *run)
	}
	slices.SortFunc(out, func(a, b model.LoadTestRun) int {
		if c := b.StartedAt.Compare(a.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out
}

// newExecutor builds the executor of a planned target. Pools of database
// clients are sized to the run's workers, capped at maxPoolSize.
func (e *Engine) newExecutor(ctx context.Context, wfID string, spec *model.LoadTestSpec, p plan) (executor, error) {
	t := &spec.Target
	pool := min(spec.MaxWorkers, maxPoolSize)
	switch t.Kind {
	case model.TargetHTTP:
		h, err := newHTTPExec(t.HTTP, p.url, spec.MaxWorkers, p.conn)
		if err != nil {
			return nil, err
		}
		h.warmup = !p.external
		return h, nil
	case model.TargetAWS:
		if p.conn != nil {
			return newAWSConnExec(ctx, t.AWS, *p.conn, spec.MaxWorkers)
		}
		if e.resolver == nil {
			return nil, errors.New("aws targets need a sandbox resolver")
		}
		endpoint, ok := e.resolver.EmulatorURL(wfID, model.CloudAWS)
		if !ok {
			return nil, errors.New("the AWS sandbox of this workflow is not running; start it and deploy before load testing")
		}
		return newAWSExec(t.AWS, endpoint, spec.MaxWorkers)
	case model.TargetSQL:
		return newSQLExec(t.SQL, *p.conn, pool)
	case model.TargetRedis:
		return newRedisExec(t.Redis, *p.conn, pool)
	case model.TargetMongo:
		return newMongoExec(t.Mongo, *p.conn, pool)
	default:
		return nil, fmt.Errorf("unknown target kind %q", t.Kind)
	}
}

// normalizeSpec applies defaults and rejects specs that cannot run.
func normalizeSpec(s *model.LoadTestSpec) error {
	if s.Target.Kind == "" {
		switch {
		case s.Target.HTTP != nil:
			s.Target.Kind = model.TargetHTTP
		case s.Target.AWS != nil:
			s.Target.Kind = model.TargetAWS
		case s.Target.SQL != nil:
			s.Target.Kind = model.TargetSQL
		case s.Target.Redis != nil:
			s.Target.Kind = model.TargetRedis
		case s.Target.Mongo != nil:
			s.Target.Kind = model.TargetMongo
		}
	}
	if s.Name == "" {
		s.Name = s.Target.Label
	}
	if s.Profile.Preset == "" {
		s.Profile.Preset = "custom"
	}
	switch s.Profile.Mode {
	case "":
		s.Profile.Mode = model.ModeRPS
	case model.ModeRPS, model.ModeConcurrency:
	default:
		return fmt.Errorf("unknown load mode %q", s.Profile.Mode)
	}
	if s.TimeoutMs < 0 || s.MaxWorkers < 0 {
		return errors.New("timeoutMs and maxWorkers must not be negative")
	}
	if s.TimeoutMs == 0 {
		s.TimeoutMs = defaultTimeoutMs
	}
	if s.MaxWorkers == 0 {
		s.MaxWorkers = defaultMaxWorkers
	}
	if len(s.Profile.Stages) == 0 {
		return errors.New("the profile needs at least one stage")
	}
	total := 0
	for _, st := range s.Profile.Stages {
		if st.DurationSec < 0 || st.Target < 0 {
			return errors.New("stage durations and targets must not be negative")
		}
		if s.Profile.Mode == model.ModeConcurrency && st.Target > s.MaxWorkers {
			return fmt.Errorf("concurrency target %d exceeds maxWorkers %d", st.Target, s.MaxWorkers)
		}
		total += st.DurationSec
	}
	if total == 0 || total > maxDurationSec {
		return fmt.Errorf("the profile must last between 1 and %d seconds", maxDurationSec)
	}
	if s.Thresholds == nil {
		s.Thresholds = []model.Threshold{}
	}
	for _, th := range s.Thresholds {
		if thresholdMetrics[th.Metric] == nil {
			return fmt.Errorf("unknown threshold metric %q", th.Metric)
		}
		if thresholdOps[th.Op] == nil {
			return fmt.Errorf("unknown threshold operator %q", th.Op)
		}
		if math.IsNaN(th.Value) || math.IsInf(th.Value, 0) {
			return fmt.Errorf("threshold %s needs a finite value", th.Metric)
		}
	}
	return nil
}

func (e *Engine) dir(wfID string) string {
	return filepath.Join(e.home, "workflows", wfID, "loadtests")
}

func (e *Engine) connDir(connID string) string {
	return filepath.Join(e.home, "connections", connID, "loadtests")
}

// save writes the run atomically into its owner's directory.
func (e *Engine) save(run model.LoadTestRun) error {
	dir := e.dir(run.WorkflowID)
	if run.WorkflowID == "" {
		dir = e.connDir(run.ConnectionID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, run.ID+".json")); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}

func loadRun(path string) (*model.LoadTestRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run model.LoadTestRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	if run.Timeline == nil {
		run.Timeline = []model.LoadSample{}
	}
	return &run, nil
}

func (e *Engine) forget(r *runner) {
	e.mu.Lock()
	delete(e.runs, r.run.ID)
	e.mu.Unlock()
}

func (e *Engine) publish(ev model.Event) {
	if e.events != nil {
		e.events.Publish(ev)
	}
}

func (e *Engine) publishStatus(r *runner) {
	run := r.snapshot(false)
	e.publish(model.Event{Type: model.EventLoadTestStatus, WorkflowID: run.WorkflowID, Data: run})
}

func newRunID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "lt-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// safeName accepts ids that are safe as a single path element and glob
// pattern.
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\*?[]`)
}
