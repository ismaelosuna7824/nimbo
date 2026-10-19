package loadtest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const testWF = "wf-test"

// statusWatcher collects terminal status events.
type statusWatcher struct {
	mu       sync.Mutex
	finished map[string]model.LoadTestRun
	changed  chan struct{}
	samples  atomic.Int64
}

func newWatcher() *statusWatcher {
	return &statusWatcher{finished: map[string]model.LoadTestRun{}, changed: make(chan struct{}, 1)}
}

func (w *statusWatcher) Publish(ev model.Event) {
	switch ev.Type {
	case model.EventLoadTestSample:
		w.samples.Add(1)
	case model.EventLoadTestStatus:
		if run := ev.Data.(model.LoadTestRun); run.Status != model.LoadRunning {
			w.mu.Lock()
			w.finished[run.ID] = run
			w.mu.Unlock()
			select {
			case w.changed <- struct{}{}:
			default:
			}
		}
	}
}

func (w *statusWatcher) wait(t *testing.T, id string, limit time.Duration) model.LoadTestRun {
	t.Helper()
	deadline := time.After(limit)
	for {
		w.mu.Lock()
		run, ok := w.finished[id]
		w.mu.Unlock()
		if ok {
			return run
		}
		select {
		case <-w.changed:
		case <-deadline:
			t.Fatalf("run %s did not finish within %v", id, limit)
		}
	}
}

func hold(sec, target int) []model.LoadStage {
	return []model.LoadStage{{DurationSec: 0, Target: target}, {DurationSec: sec, Target: target}}
}

func httpSpec(url string, stages []model.LoadStage) model.LoadTestSpec {
	return model.LoadTestSpec{
		Target:  model.LoadTarget{Kind: model.TargetHTTP, Label: "test", HTTP: &model.HTTPTarget{Method: "GET", URL: url}},
		Profile: model.LoadProfile{Mode: model.ModeRPS, Stages: stages},
	}
}

// runSpec starts spec and returns the finished run as returned by Get.
func runSpec(t *testing.T, e *Engine, w *statusWatcher, spec model.LoadTestSpec) *model.LoadTestRun {
	t.Helper()
	run, err := e.Start(context.Background(), testWF, spec)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, st := range spec.Profile.Stages {
		total += st.DurationSec
	}
	w.wait(t, run.ID, time.Duration(total)*time.Second+15*time.Second)
	got, ok := e.Get(run.ID)
	if !ok {
		t.Fatalf("run %s not found after finishing", run.ID)
	}
	return got
}

func newTestEngine(t *testing.T) (*Engine, *statusWatcher) {
	w := newWatcher()
	return NewEngine(EngineConfig{Home: t.TempDir(), Events: w}), w
}

func TestOpenModelAchievesTargetRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	e, w := newTestEngine(t)

	run := runSpec(t, e, w, httpSpec(srv.URL, hold(5, 200)))
	s := run.Summary
	t.Logf("requests=%d rps=%.1f peak=%.0f p95=%.3fms", s.Requests, s.RPS, s.PeakRPS, s.Latency.P95)
	if run.Status != model.LoadPassed || s.Errors != 0 {
		t.Fatalf("status %s, %d errors (%v)", run.Status, s.Errors, s.ErrorKinds)
	}
	if s.RPS < 180 || s.RPS > 220 {
		t.Fatalf("achieved %.1f rps, want 200 ±10%%", s.RPS)
	}
	if s.BytesIn != int64(2*s.Requests) {
		t.Errorf("bytesIn = %d, want %d", s.BytesIn, 2*s.Requests)
	}
	if len(run.Timeline) != 5 {
		t.Fatalf("timeline has %d samples, want 5", len(run.Timeline))
	}
	for _, sm := range run.Timeline {
		if sm.Target != 200 || sm.RPS < 170 || sm.RPS > 230 {
			t.Errorf("sample t=%v target=%v rps=%v", sm.T, sm.Target, sm.RPS)
		}
	}
	if s.BreakingPoint != nil {
		t.Errorf("healthy server reported breaking point %v", *s.BreakingPoint)
	}
	if n := w.samples.Load(); n != 5 {
		t.Errorf("published %d sample events, want 5", n)
	}
}

func TestPercentilesMatchKnownLatency(t *testing.T) {
	// Every 10th request takes 100ms, the rest 10ms.
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1)%10 == 0 {
			time.Sleep(100 * time.Millisecond)
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer srv.Close()
	e, w := newTestEngine(t)

	lat := runSpec(t, e, w, httpSpec(srv.URL, hold(4, 50))).Summary.Latency
	t.Logf("latency %+v", lat)
	check := func(name string, got, lo, hi float64) {
		if got < lo || got > hi {
			t.Errorf("%s = %.3fms, want within [%v, %v]", name, got, lo, hi)
		}
	}
	check("min", lat.Min, 10, 13)
	check("p50", lat.P50, 10, 14)
	check("p95", lat.P95, 100, 115)
	check("p99", lat.P99, 100, 115)
	check("max", lat.Max, 100, 130)
	check("mean", lat.Mean, 18.5, 23)   // 0.9*10 + 0.1*100 = 19
	check("stddev", lat.StdDev, 26, 30) // sqrt(0.9*81 + 0.1*6561) = 27
}

func TestErrorKindsAndStatusCodes(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) % 4 {
		case 0:
			w.WriteHeader(http.StatusInternalServerError)
		case 1:
			w.WriteHeader(http.StatusNotFound)
		case 2:
			select {
			case <-time.After(time.Second):
			case <-r.Context().Done():
			}
		}
	}))
	defer srv.Close()
	e, w := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(2, 40))
	spec.TimeoutMs = 100

	s := runSpec(t, e, w, spec).Summary
	t.Logf("requests=%d codes=%v kinds=%v", s.Requests, s.StatusCodes, s.ErrorKinds)
	if s.StatusCodes["200"] == 0 || s.StatusCodes["404"] == 0 || s.StatusCodes["500"] == 0 || s.ErrorKinds[errTimeout] == 0 {
		t.Fatalf("missing outcomes: codes=%v kinds=%v", s.StatusCodes, s.ErrorKinds)
	}
	if s.ErrorKinds[errHTTP5xx] != s.StatusCodes["500"] || s.ErrorKinds[errHTTP4xx] != s.StatusCodes["404"] {
		t.Errorf("error kinds %v do not match status codes %v", s.ErrorKinds, s.StatusCodes)
	}
	if s.Errors != s.ErrorKinds[errHTTP5xx]+s.ErrorKinds[errHTTP4xx]+s.ErrorKinds[errTimeout] {
		t.Errorf("errors = %d, kinds %v", s.Errors, s.ErrorKinds)
	}
	answered := s.StatusCodes["200"] + s.StatusCodes["404"] + s.StatusCodes["500"]
	if answered+s.ErrorKinds[errTimeout] != s.Requests || s.Requests < 75 || s.Requests > 85 {
		t.Errorf("requests = %d, answered %d + timeouts %d", s.Requests, answered, s.ErrorKinds[errTimeout])
	}
	if want := float64(s.Errors) / float64(s.Requests); s.ErrorRate != want {
		t.Errorf("error rate = %v, want %v", s.ErrorRate, want)
	}
}

func TestThresholdVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}))
	defer srv.Close()
	e, w := newTestEngine(t)

	pass := httpSpec(srv.URL, hold(2, 20))
	pass.Thresholds = []model.Threshold{{Metric: "p95_ms", Op: "<", Value: 1000}, {Metric: "error_rate", Op: "<=", Value: 0.01}}
	fail := httpSpec(srv.URL, hold(2, 20))
	fail.Thresholds = []model.Threshold{{Metric: "p50_ms", Op: "<", Value: 5}, {Metric: "rps_min", Op: ">=", Value: 15}}

	passed := runSpec(t, e, w, pass)
	if passed.Status != model.LoadPassed || !passed.Thresholds[0].Passed || !passed.Thresholds[1].Passed {
		t.Errorf("status %s thresholds %+v, want passed", passed.Status, passed.Thresholds)
	}
	failed := runSpec(t, e, w, fail)
	if failed.Status != model.LoadFailed {
		t.Fatalf("status %s, want failed", failed.Status)
	}
	if th := failed.Thresholds[0]; th.Passed || th.Actual < 20 {
		t.Errorf("p50 threshold %+v, want failed with actual >= 20ms", th)
	}
	if th := failed.Thresholds[1]; !th.Passed {
		t.Errorf("rps threshold %+v, want passed", th)
	}
}

func TestBreakingPointDetectedWhenServerDegrades(t *testing.T) {
	// Fast (20ms) until more than 8 requests are in flight, then 250ms.
	var inFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		defer inFlight.Add(-1)
		if inFlight.Add(1) > 8 {
			time.Sleep(250 * time.Millisecond)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}))
	defer srv.Close()
	e, w := newTestEngine(t)

	run := runSpec(t, e, w, httpSpec(srv.URL, []model.LoadStage{{DurationSec: 8, Target: 800}}))
	s := run.Summary
	for _, sm := range run.Timeline {
		t.Logf("t=%v target=%v rps=%v inFlight=%d p95=%v", sm.T, sm.Target, sm.RPS, sm.InFlight, sm.P95)
	}
	if s.BreakingPoint == nil {
		t.Fatal("no breaking point detected")
	}
	// 8 in flight at 20ms each is reached around 400 rps.
	if bp := *s.BreakingPoint; bp < 250 || bp > 700 {
		t.Fatalf("breaking point %v, want around 400", bp)
	}
	if s.Stability <= 0 {
		t.Errorf("stability = %v for a degrading run", s.Stability)
	}
}

func TestStopEndsRunPromptly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	e, w := newTestEngine(t)

	run, err := e.Start(context.Background(), testWF, httpSpec(srv.URL, hold(60, 20)))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	stopAt := time.Now()
	if err := e.Stop(run.ID); err != nil {
		t.Fatal(err)
	}
	got := w.wait(t, run.ID, 2*time.Second)
	if d := time.Since(stopAt); d > 500*time.Millisecond {
		t.Errorf("run took %v to stop", d)
	}
	if got.Status != model.LoadStopped || got.Summary == nil {
		t.Fatalf("status %s summary %v, want stopped with a summary", got.Status, got.Summary)
	}
	// 1.5s at 20 rps; the rate is computed over the time before the stop.
	if s := got.Summary; s.Requests < 27 || s.Requests > 33 || s.RPS < 18 || s.RPS > 22 {
		t.Errorf("requests %d rps %.1f, want ~30 at ~20 rps", s.Requests, s.RPS)
	}
	if err := e.Stop(run.ID); err != nil {
		t.Errorf("stopping a finished run: %v", err)
	}
	if err := e.Stop("lt-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stopping an unknown run: %v", err)
	}
}

func TestStopCancelsInFlightRequests(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			return // preflight
		}
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	e, w := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(60, 20))
	spec.TimeoutMs = 30_000
	run, err := e.Start(context.Background(), testWF, spec)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	stopAt := time.Now()
	_ = e.Stop(run.ID)
	got := w.wait(t, run.ID, 3*time.Second)
	if d := time.Since(stopAt); d > time.Second {
		t.Errorf("run with hanging requests took %v to stop", d)
	}
	if got.Status != model.LoadStopped || got.Summary.Requests != 0 {
		t.Fatalf("status %s requests %d, want stopped with canceled requests dropped", got.Status, got.Summary.Requests)
	}
}

func TestClientSaturatedWhenWorkersExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()
	e, w := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(2, 50))
	spec.MaxWorkers = 1

	s := runSpec(t, e, w, spec).Summary
	t.Logf("requests=%d codes=%v kinds=%v", s.Requests, s.StatusCodes, s.ErrorKinds)
	sat := s.ErrorKinds[errClientSaturated]
	if s.Requests != 100 {
		t.Errorf("requests = %d, want all 100 arrivals accounted for", s.Requests)
	}
	if sat < 70 || s.StatusCodes["200"]+sat != s.Requests {
		t.Errorf("client_saturated = %d, 200s = %d of %d", sat, s.StatusCodes["200"], s.Requests)
	}
}

func TestClosedModelKeepsConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}))
	defer srv.Close()
	e, w := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(3, 5))
	spec.Profile.Mode = model.ModeConcurrency

	run := runSpec(t, e, w, spec)
	if p := peak.Load(); p != 5 {
		t.Errorf("server saw %d concurrent requests, want 5", p)
	}
	for _, sm := range run.Timeline {
		// 5 users at ~10ms each is ~500 rps.
		if sm.Target != 5 || sm.RPS < 300 || sm.RPS > 510 {
			t.Errorf("sample t=%v target=%v rps=%v", sm.T, sm.Target, sm.RPS)
		}
	}
}

func TestClosedModelPacesInstantFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	e, w := newTestEngine(t)
	spec := httpSpec(srv.URL, hold(1, 5))
	spec.Profile.Mode = model.ModeConcurrency

	s := runSpec(t, e, w, spec).Summary
	t.Logf("requests=%d kinds=%v", s.Requests, s.ErrorKinds)
	// 5 users, each failed iteration lasting at least 10ms: at most ~500.
	if s.Requests < 300 || s.Requests > 520 {
		t.Fatalf("requests = %d over 1s with 5 users, want 300..520", s.Requests)
	}
	if s.ErrorKinds[errHTTP5xx] != s.Requests {
		t.Errorf("error kinds %v, want every request http_5xx", s.ErrorKinds)
	}
}

func TestUnreachableTargetFailsFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	e, w := newTestEngine(t)

	run, err := e.Start(context.Background(), testWF, httpSpec("http://"+addr+"/", hold(30, 10)))
	if err != nil {
		t.Fatal(err)
	}
	got := w.wait(t, run.ID, 2*time.Second)
	if got.Status != model.LoadErrored || got.Summary != nil {
		t.Fatalf("status %s, want error without summary", got.Status)
	}
	t.Logf("error: %s", got.Error)
	if want := "connection_refused"; !strings.Contains(got.Error, want) {
		t.Errorf("error %q does not mention %s", got.Error, want)
	}
}

// A sandbox port that accepts and drops connections (Docker publishing an
// instance whose web server is still starting) is waited for, so a load test
// started right after a deploy runs instead of failing its preflight.
func TestPreflightWaitsForStartingSandboxService(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var ready atomic.Bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew && !ready.Load() {
			c.Close()
		}
	}
	srv.Start()
	defer srv.Close()
	time.AfterFunc(2*time.Second, func() { ready.Store(true) })

	e, w := newTestEngine(t)
	got := runSpec(t, e, w, httpSpec(srv.URL+"/", hold(1, 5)))
	if got.Status != model.LoadPassed {
		t.Fatalf("status %s (%s), want passed once the service answers", got.Status, got.Error)
	}
}

func TestConcurrentRunsCappedAndPersisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	home := t.TempDir()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: home, Events: w})

	var ids []string
	for range maxConcurrentRuns {
		run, err := e.Start(context.Background(), testWF, httpSpec(srv.URL, hold(60, 5)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, run.ID)
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.Start(context.Background(), testWF, httpSpec(srv.URL, hold(60, 5))); !errors.Is(err, ErrTooManyRuns) {
		t.Fatalf("fourth run: %v, want ErrTooManyRuns", err)
	}
	time.Sleep(1200 * time.Millisecond)
	for _, id := range ids {
		_ = e.Stop(id)
		w.wait(t, id, 2*time.Second)
	}

	reopened := NewEngine(EngineConfig{Home: home})
	list := reopened.List(testWF)
	if len(list) != len(ids) {
		t.Fatalf("listed %d runs, want %d", len(list), len(ids))
	}
	for i, run := range list {
		if want := ids[len(ids)-1-i]; run.ID != want {
			t.Errorf("list[%d] = %s, want %s (newest first)", i, run.ID, want)
		}
		if len(run.Timeline) != 0 || run.Summary == nil || run.Status != model.LoadStopped {
			t.Errorf("listed run %s: %d samples, summary %v, status %s", run.ID, len(run.Timeline), run.Summary != nil, run.Status)
		}
	}
	full, ok := reopened.Get(ids[0])
	if !ok || len(full.Timeline) == 0 {
		t.Fatalf("Get(%s) = %v, timeline missing", ids[0], ok)
	}
	if len(reopened.List("other-wf")) != 0 {
		t.Error("runs leaked into another workflow")
	}
}
