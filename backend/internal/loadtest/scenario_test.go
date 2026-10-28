package loadtest

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

func TestScheduleWithSpike(t *testing.T) {
	s := newSchedule(hold(10, 10)).withSpike(4, 3, 3)
	if s.duration() != 10 {
		t.Fatalf("duration %v", s.duration())
	}
	for _, c := range []struct{ t, want float64 }{{1, 10}, {3.9, 10}, {4, 30}, {6.9, 30}, {7, 10}, {9.5, 10}} {
		if got := s.target(c.t); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("target(%v) = %v, want %v", c.t, got, c.want)
		}
	}
	if got := s.integral(10); math.Abs(got-160) > 1e-9 {
		t.Errorf("integral %v, want 160 (100 + 2 x 30 extra)", got)
	}
	// A spike over a ramp multiplies the ramp's values.
	r := newSchedule([]model.LoadStage{{DurationSec: 10, Target: 100}}).withSpike(5, 5, 2)
	if got := r.target(6); math.Abs(got-120) > 1e-9 {
		t.Errorf("ramp spike target(6) = %v, want 120", got)
	}
}

// fakeFaults stands in for the sandbox: the fault makes the test server
// answer 503 while injected.
type fakeFaults struct {
	down      *atomic.Bool
	mu        sync.Mutex
	injected  []time.Time
	reverted  []time.Time
	prepared  int
	injectErr error
}

func (f *fakeFaults) PrepareFaults(context.Context) error {
	f.mu.Lock()
	f.prepared++
	f.mu.Unlock()
	return nil
}

func (f *fakeFaults) InjectFault(_ context.Context, _ string, spec model.ScenarioSpec, _ model.LoadTarget) (scenario.Fault, error) {
	if f.injectErr != nil {
		return nil, f.injectErr
	}
	f.mu.Lock()
	f.injected = append(f.injected, time.Now())
	f.mu.Unlock()
	f.down.Store(true)
	return &fakeFault{f: f}, nil
}

type fakeFault struct{ f *fakeFaults }

func (ff *fakeFault) TargetName() string { return "db" }
func (ff *fakeFault) Detail() string     { return "paused db-1" }
func (ff *fakeFault) Revert(context.Context) (scenario.Observation, error) {
	ff.f.down.Store(false)
	ff.f.mu.Lock()
	ff.f.reverted = append(ff.f.reverted, time.Now())
	ff.f.mu.Unlock()
	return scenario.Observation{Back: true, Verified: true, VerifyDetail: "unpaused db-1"}, nil
}

func scenarioWF() *model.Workflow {
	return &model.Workflow{ID: testWF, Kind: model.WorkflowDocker, Compose: &model.ComposeModel{Graph: model.ComposeGraph{
		Nodes: []model.ComposeNode{
			{ID: "service:api", Kind: model.ComposeService, Name: "api", Service: &model.ServiceFacts{Replicas: new(2)}},
			{ID: "service:db", Kind: model.ComposeService, Name: "db", Service: &model.ServiceFacts{Image: &model.ImageRef{Ref: "postgres:16-alpine"}}},
		},
		Edges: []model.ComposeEdge{{Source: "service:db", Target: "service:api", Kind: model.ComposeEdgeDependsOn}},
	}}}
}

func faultServer(down *atomic.Bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
}

func TestScenarioInjectsAndRevertsOnSchedule(t *testing.T) {
	down := &atomic.Bool{}
	srv := faultServer(down)
	defer srv.Close()
	faults := &fakeFaults{down: down}
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Workflows: fakeInspector{wf: scenarioWF()}, Faults: faults})
	spec := httpSpec(srv.URL, hold(12, 40))
	spec.Scenario = &model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 5, DurationSec: 3}
	run := runSpec(t, e, w, spec)

	if len(faults.injected) != 1 || len(faults.reverted) != 1 || faults.prepared != 1 {
		t.Fatalf("prepared %d, injected %d, reverted %d", faults.prepared, len(faults.injected), len(faults.reverted))
	}
	if d := faults.reverted[0].Sub(faults.injected[0]); d < 2500*time.Millisecond || d > 4*time.Second {
		t.Errorf("fault lasted %v, want ~3 s", d)
	}
	sc := run.Scenario
	if sc == nil || sc.Verdict == "" || sc.TargetName != "db" || sc.Detail != "paused db-1" {
		t.Fatalf("scenario %+v", sc)
	}
	if sc.InjectedAtSec != 5 || sc.RevertedAtSec == nil || *sc.RevertedAtSec != 8 {
		t.Errorf("injected %d reverted %v", sc.InjectedAtSec, sc.RevertedAtSec)
	}
	// 503s for ~3 s of 12: errors seen, then recovered after the revert.
	if sc.Verdict != model.ScenarioDegraded || sc.RecoveredAtSec == nil || *sc.RecoveredAtSec < 8 || *sc.RecoveredAtSec > 9 {
		t.Errorf("verdict %s recovered %v: %s", sc.Verdict, sc.RecoveredAtSec, sc.Message)
	}
	if run.Summary.Errors == 0 {
		t.Error("no errors recorded while the dependency was down")
	}
	// The saved run carries the scenario.
	saved, ok := e.Get(run.ID)
	if !ok || saved.Scenario == nil || saved.Scenario.Verdict != sc.Verdict {
		t.Errorf("saved %+v", saved.Scenario)
	}
}

func TestScenarioRevertedWhenRunStops(t *testing.T) {
	down := &atomic.Bool{}
	srv := faultServer(down)
	defer srv.Close()
	faults := &fakeFaults{down: down}
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Workflows: fakeInspector{wf: scenarioWF()}, Faults: faults})
	spec := httpSpec(srv.URL, hold(60, 20))
	spec.Scenario = &model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 5, DurationSec: 40}
	run, err := e.Start(context.Background(), testWF, spec)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(6500 * time.Millisecond)
	if !down.Load() {
		t.Fatal("fault not injected at 5 s")
	}
	stopped := time.Now()
	if err := e.Stop(run.ID); err != nil {
		t.Fatal(err)
	}
	got := w.wait(t, run.ID, 10*time.Second)
	if down.Load() || len(faults.reverted) != 1 {
		t.Fatalf("fault not reverted on stop (reverted %d)", len(faults.reverted))
	}
	if d := faults.reverted[0].Sub(stopped); d > time.Second {
		t.Errorf("revert took %v after stop", d)
	}
	if got.Status != model.LoadStopped || got.Scenario == nil || got.Scenario.RevertedAtSec == nil || *got.Scenario.RevertedAtSec > 8 {
		t.Errorf("status %s scenario %+v", got.Status, got.Scenario)
	}
	// Stopped by the user: no verdict on a fault watched for a second.
	if got.Scenario != nil && (got.Scenario.Verdict != "" || !strings.HasPrefix(got.Scenario.Message, "Stopped before the end")) {
		t.Errorf("stopped run graded %q: %s", got.Scenario.Verdict, got.Scenario.Message)
	}
}

func TestShutdownRevertsRunningFaults(t *testing.T) {
	down := &atomic.Bool{}
	srv := faultServer(down)
	defer srv.Close()
	faults := &fakeFaults{down: down}
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: newWatcher(), Workflows: fakeInspector{wf: scenarioWF()}, Faults: faults})
	spec := httpSpec(srv.URL, hold(60, 20))
	spec.Scenario = &model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 5, DurationSec: 40}
	if _, err := e.Start(context.Background(), testWF, spec); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.Shutdown(ctx)
	if down.Load() || len(faults.reverted) != 1 {
		t.Fatalf("shutdown left the fault injected (reverted %d)", len(faults.reverted))
	}
}

func TestScenarioValidationOnStart(t *testing.T) {
	down := &atomic.Bool{}
	srv := faultServer(down)
	defer srv.Close()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: newWatcher(), Workflows: fakeInspector{wf: scenarioWF()}, Faults: &fakeFaults{down: down}})
	for _, tc := range []struct {
		sc   model.ScenarioSpec
		want string
	}{
		{model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 5, DurationSec: 10}, "ends after the load profile"},
		{model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:api", AtSec: 5, DurationSec: 2}, "cannot target"},
		{model.ScenarioSpec{ID: model.ScenarioCacheFlush, Target: "service:db", AtSec: 5, DurationSec: 2}, "does not apply"},
	} {
		spec := httpSpec(srv.URL, hold(10, 5))
		spec.Scenario = &tc.sc
		if _, err := e.Start(context.Background(), testWF, spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s on %s: err %v, want %q", tc.sc.ID, tc.sc.Target, err, tc.want)
		}
	}
	// Without workflow knowledge (connections, plain engines) scenarios are refused.
	plain, _ := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(20, 5))
	spec.Scenario = &model.ScenarioSpec{ID: model.ScenarioTrafficSpike, AtSec: 5, DurationSec: 5}
	if _, err := plain.Start(context.Background(), testWF, spec); err == nil {
		t.Error("scenario accepted without a workflow inspector")
	}
}

func TestTrafficSpikeRaisesLoad(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Workflows: fakeInspector{wf: scenarioWF()}})
	spec := httpSpec(srv.URL, hold(12, 20))
	spec.Target.NodeID = "service:api"
	spec.Scenario = &model.ScenarioSpec{ID: model.ScenarioTrafficSpike, AtSec: 5, DurationSec: 3, Params: map[string]string{"factor": "4"}}
	run := runSpec(t, e, w, spec)
	// 20 req/s for 12 s plus 3 s at 80 req/s: 240 + 180.
	if n := hits.Load(); n < 400 || n > 440 {
		t.Errorf("%d requests, want ~420", n)
	}
	if run.Timeline[6].Target != 80 || run.Timeline[9].Target != 20 {
		t.Errorf("targets %v / %v", run.Timeline[6].Target, run.Timeline[9].Target)
	}
	sc := run.Scenario
	if sc == nil || sc.Verdict != model.ScenarioSurvived || sc.Target != "service:api" || *sc.RevertedAtSec != 8 {
		t.Errorf("spike result %+v", sc)
	}
}
