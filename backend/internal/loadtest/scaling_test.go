package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// webHPA is the HPA policy of the synthetic runs: 2..5 pods at 70% CPU.
var webHPA = model.HPAPolicy{Name: "web", Min: 2, Max: 5, TargetCPU: 70}

// replicaPlan describes a synthetic Kubernetes run second by second.
type replicaPlan struct {
	duration int
	// at returns the replicas and HPA CPU reading (-1: none) at second t.
	at func(t int) (desired, ready, cpu int)
	// degraded seconds (p95 above 4× baseline); errored seconds.
	degraded [][2]int // inclusive ranges
	errored  [][2]int
	noHPA    bool
	cond     func(t int) *model.HPACondition
	env      string
	other    []string
}

func (p replicaPlan) run() scalingRun {
	in := scalingRun{target: "web", degraded: make([]degradation, p.duration), env: p.env}
	for _, r := range p.degraded {
		for t := r[0]; t <= r[1]; t++ {
			in.degraded[t-1].latency = true
		}
	}
	for _, r := range p.errored {
		for t := r[0]; t <= r[1]; t++ {
			in.degraded[t-1].errors = true
		}
	}
	if p.other != nil {
		in.elsewhere = func(kind, name string) []string {
			if kind != "Deployment" || name != "web" {
				return nil
			}
			return p.other
		}
	}
	for t := 0; t <= p.duration; t++ {
		d, r, cpu := p.at(t)
		w := model.WorkloadScale{NodeID: "Deployment/shop/web", Workload: "web", Desired: d, Ready: r}
		if !p.noHPA {
			w.HPA = &model.HPAStatus{HPAPolicy: webHPA, DesiredReplicas: d}
			if cpu >= 0 {
				w.HPA.CPUUtilization = &cpu
			}
			if p.cond != nil {
				if c := p.cond(t); c != nil {
					w.HPA.Conditions = []model.HPACondition{*c}
				}
			}
		}
		in.samples = append(in.samples, scaleSample{T: float64(t), Workloads: []model.WorkloadScale{w}})
	}
	return in
}

// stepUp: 2 pods, the HPA asks for `to` at `ask` and they are Ready at
// `ready`; CPU reads 30% until `hot`, then `cpu`.
func stepUp(ask, ready, to, hot, cpu int) func(int) (int, int, int) {
	return func(t int) (int, int, int) {
		d, r, c := 2, 2, 30
		if t >= ask {
			d = to
		}
		if t >= ready {
			r = to
		}
		if t >= hot {
			c = cpu
		}
		return d, r, c
	}
}

func steady(pods, cpu int) func(int) (int, int, int) {
	return func(int) (int, int, int) { return pods, pods, cpu }
}

func judge(t *testing.T, p replicaPlan) model.WorkloadScaling {
	t.Helper()
	s := analyzeScaling(p.run())
	if s == nil || len(s.Workloads) != 1 {
		t.Fatalf("scaling = %+v", s)
	}
	w := s.Workloads[0]
	if s.Verdict != w.Message || w.Message == "" || len(w.Timeline) != p.duration+1 || !w.Target {
		t.Fatalf("workload %+v, verdict %q", w, s.Verdict)
	}
	t.Log(w.Kind, ": ", w.Message)
	return w
}

func wantKind(t *testing.T, w model.WorkloadScaling, kind string, contains ...string) {
	t.Helper()
	if w.Kind != kind {
		t.Errorf("kind %q, want %q: %s", w.Kind, kind, w.Message)
	}
	for _, c := range contains {
		if !strings.Contains(w.Message, c) {
			t.Errorf("message %q lacks %q", w.Message, c)
		}
	}
}

func TestScalingScaledInTime(t *testing.T) {
	// Degraded for exactly the grace window before the new pods are Ready.
	w := judge(t, replicaPlan{duration: 180, at: stepUp(40, 55, 4, 30, 150), degraded: [][2]int{{45, 54}}})
	wantKind(t, w, model.ScalingScaled, "web scaled from 2 to 4 pods", "asked for more at 40 s", "Ready at 55 s", "for only 10 s", "; CPU peaked at 150% of requests vs a 70% target")
	if w.ScaleUpAt == nil || *w.ScaleUpAt != 40 || w.ReadyAt == nil || *w.ReadyAt != 55 || w.DegradedFor != 10 || w.PeakReplicas != 4 || *w.PeakCPU != 150 {
		t.Errorf("numbers %+v", w)
	}
	// Degradation after the new pods were Ready does not make it late.
	w = judge(t, replicaPlan{duration: 180, at: stepUp(40, 55, 4, 30, 150), degraded: [][2]int{{55, 90}}})
	wantKind(t, w, model.ScalingScaled, "before latency degraded")
}

func TestScalingLate(t *testing.T) {
	// One second over the grace window.
	w := judge(t, replicaPlan{duration: 180, at: stepUp(40, 55, 4, 30, 150), degraded: [][2]int{{44, 54}}})
	wantKind(t, w, model.ScalingLate, "p95 above 4× its baseline for 11 s before 2 new pods of web were Ready at 55 s", "lower the HPA's CPU target (70%) or raise minReplicas (2)")
	if w.DegradedAt == nil || *w.DegradedAt != 44 || w.DegradedFor != 11 {
		t.Errorf("numbers %+v", w)
	}
	// Errors count as degradation too.
	w = judge(t, replicaPlan{duration: 180, at: stepUp(40, 55, 3, 30, 150), errored: [][2]int{{30, 54}}})
	wantKind(t, w, model.ScalingLate, "errors above 5% for 25 s before 1 new pod of web was Ready at 55 s")
}

func TestScalingNewPodsNeverReady(t *testing.T) {
	never := func(ask int) func(int) (int, int, int) {
		return func(t int) (int, int, int) {
			if t >= ask {
				return 4, 2, 150
			}
			return 2, 2, 150
		}
	}
	// A scale-up 30 s before the end had time to start pods: late.
	w := judge(t, replicaPlan{duration: 120, at: never(90), degraded: [][2]int{{80, 120}}})
	wantKind(t, w, model.ScalingLate, "asked for 4 pods of web at 90 s but no new pod was Ready by the end (120 s)", "for 41 s", "startup")
	// 29 s before the end: too close to judge.
	w = judge(t, replicaPlan{duration: 120, at: never(91)})
	wantKind(t, w, model.ScalingTooShort, "at 91 s, too close to the end")
}

func TestScalingMaxed(t *testing.T) {
	// At 5 pods (Ready at 70 s), a reading 30 s later is still above
	// target×1.1 = 77%.
	hot := func(cpu int) func(int) (int, int, int) {
		return func(t int) (int, int, int) {
			d, r, c := stepUp(50, 70, 5, 20, 160)(t)
			if t >= 100 {
				c = cpu
			}
			return d, r, c
		}
	}
	w := judge(t, replicaPlan{duration: 180, at: hot(78)})
	wantKind(t, w, model.ScalingMaxed, "web reached maxReplicas (5) at 50 s", "CPU at 78% of requests vs a 70% target", "raise maxReplicas")
	// Exactly at the HPA tolerance the pods keep up: scaled.
	w = judge(t, replicaPlan{duration: 180, at: func(t int) (int, int, int) {
		d, r, c := stepUp(50, 70, 5, 20, 160)(t)
		if t >= 95 {
			c = 77
		}
		return d, r, c
	}})
	wantKind(t, w, model.ScalingScaled)
	// Latency degraded for more than the grace window at max: maxed.
	w = judge(t, replicaPlan{duration: 180, at: func(t int) (int, int, int) {
		d, r, _ := stepUp(50, 70, 5, 20, 160)(t)
		return d, r, 60
	}, degraded: [][2]int{{120, 130}}})
	wantKind(t, w, model.ScalingMaxed, "p95 above 4× its baseline for 11 s")
	// Already at maxReplicas when the run starts.
	w = judge(t, replicaPlan{duration: 120, at: steady(5, 120)})
	wantKind(t, w, model.ScalingMaxed, "from the start")
}

func TestScalingNoHPA(t *testing.T) {
	w := judge(t, replicaPlan{duration: 120, at: steady(1, -1), noHPA: true, env: "dev", other: []string{"prod"}})
	wantKind(t, w, model.ScalingNoHPA, "web has no HorizontalPodAutoscaler in the dev environment, so it stayed at 1 pod", "switch to the prod environment")
	if w.HPA != nil {
		t.Errorf("hpa %+v", w.HPA)
	}
	w = judge(t, replicaPlan{duration: 120, at: steady(2, -1), noHPA: true, other: []string{}})
	wantKind(t, w, model.ScalingNoHPA, "web has no HorizontalPodAutoscaler, so it stayed at 2 pods", "add an HPA")
	w = judge(t, replicaPlan{duration: 120, at: steady(2, -1), noHPA: true, env: "dev", other: []string{"prod", "staging"}})
	wantKind(t, w, model.ScalingNoHPA, "(prod, staging)")
}

func TestScalingNoMetrics(t *testing.T) {
	missing := &model.HPACondition{Type: "ScalingActive", Status: "False", Reason: "FailedGetResourceMetric", Message: `the HPA was unable to compute the replica count: failed to get cpu utilization: missing request for cpu in container "nginx" of Pod "web-1"`}
	// A missing request is definitive, even in a short run.
	w := judge(t, replicaPlan{duration: 30, at: steady(2, -1), cond: func(int) *model.HPACondition { return missing }})
	wantKind(t, w, model.ScalingNoMetrics, "FailedGetResourceMetric", "set resources.requests.cpu on every container of web")

	api := &model.HPACondition{Type: "ScalingActive", Status: "False", Reason: "FailedGetResourceMetric", Message: "unable to get metrics for resource cpu: unable to fetch metrics from resource metrics API: the server is currently unable to handle the request (get pods.metrics.k8s.io)"}
	// Half the samples without metrics (61 of 121).
	half := func(t int) *model.HPACondition {
		if t <= 60 {
			return api
		}
		return nil
	}
	w = judge(t, replicaPlan{duration: 120, at: steady(2, 30), cond: half})
	wantKind(t, w, model.ScalingNoMetrics, "metrics-server is not serving pod metrics")
	// Under half: the HPA worked.
	w = judge(t, replicaPlan{duration: 120, at: steady(2, 30), cond: func(t int) *model.HPACondition {
		if t < 60 {
			return api
		}
		return nil
	}})
	wantKind(t, w, model.ScalingNotNeeded)
	// No CPU reading over a whole run.
	w = judge(t, replicaPlan{duration: 120, at: steady(2, -1)})
	wantKind(t, w, model.ScalingNoMetrics, "reported no CPU utilization during the 120 s run")
}

func TestScalingNotNeededAndTooShort(t *testing.T) {
	w := judge(t, replicaPlan{duration: 60, at: steady(2, 40)})
	wantKind(t, w, model.ScalingNotNeeded, "CPU peaked at 40% of requests vs a 70% target and latency held, so the HPA kept web at 2 pods")
	w = judge(t, replicaPlan{duration: 59, at: steady(2, 40)})
	wantKind(t, w, model.ScalingTooShort, "The run lasted 59 s")
	// Short runs that scaled are judged.
	w = judge(t, replicaPlan{duration: 50, at: stepUp(20, 30, 3, 10, 150)})
	wantKind(t, w, model.ScalingScaled)
}

func TestScalingNeverScaledUnderPressure(t *testing.T) {
	// CPU above target×1.1 for a whole run without a scale-up.
	w := judge(t, replicaPlan{duration: 120, at: steady(2, 78), degraded: [][2]int{{50, 60}}})
	wantKind(t, w, model.ScalingLate, "kept web at 2 pods for the whole 120 s run although CPU peaked at 78%", "for 11 s", "kubectl describe hpa web")
	// At the tolerance it is not pressure.
	w = judge(t, replicaPlan{duration: 120, at: steady(2, 77)})
	wantKind(t, w, model.ScalingNotNeeded)
	// Latency degraded with CPU under target: not a CPU bottleneck.
	w = judge(t, replicaPlan{duration: 120, at: steady(2, 30), degraded: [][2]int{{30, 40}}})
	wantKind(t, w, model.ScalingLate, "p95 above 4× its baseline for 11 s but web's CPU stayed under the HPA target (peak 30% of requests vs 70%)", "not CPU")
	w = judge(t, replicaPlan{duration: 120, at: steady(2, 30), degraded: [][2]int{{30, 39}}})
	wantKind(t, w, model.ScalingNotNeeded)
}

func TestScalingHeadlineAndFold(t *testing.T) {
	in := replicaPlan{duration: 120, at: steady(1, -1), noHPA: true}.run()
	// A backend scaled by its own HPA reaches its maximum.
	for i := range in.samples {
		c := 160
		in.samples[i].Workloads = append(in.samples[i].Workloads, model.WorkloadScale{
			NodeID: "Deployment/shop/api", Workload: "api", Desired: 5, Ready: 5,
			HPA: &model.HPAStatus{HPAPolicy: webHPA, CPUUtilization: &c},
		})
	}
	s := analyzeScaling(in)
	if len(s.Workloads) != 2 || s.Workloads[0].Workload != "web" || s.Workloads[0].Kind != model.ScalingNoHPA || s.Workloads[1].Kind != model.ScalingMaxed {
		t.Fatalf("workloads %+v", s.Workloads)
	}
	if s.Verdict != s.Workloads[1].Message {
		t.Errorf("headline %q, want the maxed backend's", s.Verdict)
	}
	res := &model.LoadResources{Verdict: "web at 40% CPU (no limit) — not the bottleneck", Verdicts: []model.ResourceVerdict{{Service: "web", Kind: "ok"}}, Scaling: s}
	foldScaling(res)
	if res.Verdict != s.Verdict {
		t.Errorf("resource verdict %q", res.Verdict)
	}
	// A crash stays the headline.
	res = &model.LoadResources{Verdict: "api was killed", Verdicts: []model.ResourceVerdict{{Service: "api", Kind: "oom", Bottleneck: true}}, Scaling: s}
	foldScaling(res)
	if res.Verdict != "api was killed" {
		t.Errorf("resource verdict %q", res.Verdict)
	}
	// Not needed keeps the resource headline.
	nn := analyzeScaling(replicaPlan{duration: 120, at: steady(2, 30)}.run())
	res = &model.LoadResources{Verdict: "ok", Scaling: nn}
	foldScaling(res)
	if res.Verdict != "ok" {
		t.Errorf("resource verdict %q", res.Verdict)
	}
	if analyzeScaling(scalingRun{}) != nil {
		t.Error("no samples gives no scaling")
	}
}

func TestDegradations(t *testing.T) {
	secs := []secStat{
		{resolved: 10, ok: 10, p95: 10}, {resolved: 10, ok: 10, p95: 12}, {resolved: 10, ok: 10, p95: 11},
		{resolved: 10, ok: 10, p95: 44}, {resolved: 10, ok: 10, p95: 45}, // 4×11 = 44 is not above
		{resolved: 20, errors: 1, ok: 19, p95: 11}, {resolved: 10, errors: 6, ok: 4, p95: 11}, // 5% is not above
	}
	d := degradations(secs)
	want := []degradation{{}, {}, {}, {}, {latency: true}, {}, {errors: true}}
	for i := range want {
		if d[i] != want[i] {
			t.Errorf("second %d: %+v, want %+v", i+1, d[i], want[i])
		}
	}
}

// scalingFakeProbe is a Kubernetes-like probe: the targeted workload "api"
// with an HPA that never scales.
type scalingFakeProbe struct{ fakeProbe }

func (p *scalingFakeProbe) Scaling() []model.WorkloadScale {
	c := 20
	return []model.WorkloadScale{{NodeID: "Deployment/default/api", Workload: "api", Desired: 1, Ready: 1,
		HPA: &model.HPAStatus{HPAPolicy: model.HPAPolicy{Name: "api", Min: 1, Max: 3, TargetCPU: 70}, CPUUtilization: &c}}}
}

type scalingMonitor struct{ probe *scalingFakeProbe }

func (m scalingMonitor) MonitorResources(context.Context, string, model.LoadTarget) model.ResourceProbe {
	return m.probe
}

type fakeInspector struct{ env string }

func (f fakeInspector) ActiveEnvironment(string) (string, bool) { return f.env, true }
func (f fakeInspector) AutoscaledIn(context.Context, string, string, string, string) []string {
	return nil
}

func TestEngineRecordsScalingAndEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Monitor: scalingMonitor{&scalingFakeProbe{}}, Workflows: fakeInspector{"prod"}})
	run := runSpec(t, e, w, httpSpec(srv.URL, hold(3, 20)))
	if run.Environment != "prod" {
		t.Errorf("environment %q", run.Environment)
	}
	sc := run.Resources.Scaling
	if sc == nil || len(sc.Workloads) != 1 || sc.Workloads[0].Kind != model.ScalingTooShort || len(sc.Workloads[0].Timeline) != 4 || sc.Workloads[0].HPA.Max != 3 {
		t.Fatalf("scaling %+v", sc)
	}
	list := e.List(testWF)
	if len(list) != 1 || list[0].Environment != "prod" || list[0].Resources.Scaling == nil || len(list[0].Resources.Scaling.Workloads[0].Timeline) != 0 {
		t.Errorf("listed run keeps environment and scaling verdicts without timelines: %+v", list[0])
	}
}
