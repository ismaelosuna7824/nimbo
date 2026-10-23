package loadtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// recordedRun is a load test of the node+postgres+redis docker template
// (GET /notes, ramp 0→1000 rps over 30 s then 15 s at 1000 rps), recorded
// with the api service limited to 0.25 CPU and then to 1 CPU.
type recordedRun struct {
	Target   string             `json:"target"`
	Timeline []model.LoadSample `json:"timeline"`
	Summary  model.LoadSummary  `json:"summary"`
	Services []struct {
		Service    string                `json:"service"`
		CPULimit   float64               `json:"cpuLimit"`
		CPULimited bool                  `json:"cpuLimited"`
		MemLimit   int64                 `json:"memLimit"`
		MemLimited bool                  `json:"memLimited"`
		Timeline   []model.ResourcePoint `json:"timeline"`
	} `json:"services"`
}

func loadRecorded(t *testing.T, name string) (recordedRun, []resourceSample) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var rec recordedRun
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	byT := map[float64][]model.ServiceUsage{}
	var order []float64
	for _, s := range rec.Services {
		for _, p := range s.Timeline {
			if _, ok := byT[p.T]; !ok {
				order = append(order, p.T)
			}
			byT[p.T] = append(byT[p.T], model.ServiceUsage{
				Service: s.Service, Containers: p.Containers, CPUPercent: p.CPUPercent, MemBytes: p.MemBytes,
				CPULimit: s.CPULimit, CPULimited: s.CPULimited, MemLimit: s.MemLimit, MemLimited: s.MemLimited,
			})
		}
	}
	samples := make([]resourceSample, len(order))
	for i, t := range order {
		samples[i] = resourceSample{T: t, Usage: byT[t]}
	}
	return rec, samples
}

func verdictOf(res *model.LoadResources, svc string) model.ResourceVerdict {
	for _, v := range res.Verdicts {
		if v.Service == svc {
			return v
		}
	}
	return model.ResourceVerdict{}
}

func TestCorrelateRecordedQuarterCPU(t *testing.T) {
	rec, samples := loadRecorded(t, "resources-node-api-025cpu.json")
	res := correlate(rec.Target, samples, rec.Timeline, &rec.Summary, nil, nil)
	t.Log(res.Verdict)
	api := verdictOf(res, "api")
	if api.Kind != "cpu-bound" || !api.Bottleneck || res.Verdict != api.Message {
		t.Fatalf("api verdict %+v, headline %q", api, res.Verdict)
	}
	if !strings.HasPrefix(api.Message, "api reached ") || !strings.HasSuffix(api.Message, "of its 0.25 CPU limit at "+rpsLabel(api.AtRPS)+" req/s — CPU-bound; raise limits or add replicas") {
		t.Errorf("message %q", api.Message)
	}
	// The ramp crosses the api's capacity (~600-900 rps at 0.25 CPU) well
	// after the start and well before the end of the ramp.
	if api.AtRPS < 300 || api.AtRPS > 1000 || api.AtT < 5 {
		t.Errorf("saturation at %v req/s, t=%v", api.AtRPS, api.AtT)
	}
	for _, svc := range []string{"cache", "db"} {
		if v := verdictOf(res, svc); v.Bottleneck || !strings.HasSuffix(v.Message, "— not the bottleneck") {
			t.Errorf("%s: %+v", svc, v)
		}
	}
	if res.Services[0].Service != "api" || !res.Services[0].Target || len(res.Services[0].Timeline) != len(rec.Timeline)+1 {
		t.Errorf("target first with a baseline point: %+v", res.Services[0].Service)
	}
	if res.Services[0].PeakCPUOf < 0.9 {
		t.Errorf("api peak %v of its limit", res.Services[0].PeakCPUOf)
	}
}

func TestCorrelateRecordedOneCPU(t *testing.T) {
	rec, samples := loadRecorded(t, "resources-node-api-1cpu.json")
	res := correlate(rec.Target, samples, rec.Timeline, &rec.Summary, nil, nil)
	t.Log(res.Verdict)
	for _, v := range res.Verdicts {
		if v.Bottleneck {
			t.Errorf("unexpected bottleneck %+v", v)
		}
	}
	if !strings.HasPrefix(res.Verdict, "No resource bottleneck: the busiest container, api, peaked at ") || !strings.Contains(res.Verdict, "of its 1 CPU limit") {
		t.Errorf("headline %q", res.Verdict)
	}
	if v := verdictOf(res, "api"); !strings.HasPrefix(v.Message, "api at ") || !strings.HasSuffix(v.Message, "of its 1 CPU limit — not the bottleneck") {
		t.Errorf("api %q", v.Message)
	}
}

// synthetic builds samples of one service from per-second CPU percents.
func synthetic(svc string, limit float64, limited bool, memLimit int64, cpu []float64, mem []int64) ([]resourceSample, []model.LoadSample) {
	var samples []resourceSample
	var tl []model.LoadSample
	for i, c := range cpu {
		u := model.ServiceUsage{Service: svc, Containers: 1, CPUPercent: c, CPULimit: limit, CPULimited: limited, MemLimit: memLimit, MemLimited: memLimit > 0}
		if mem != nil {
			u.MemBytes = mem[i]
		}
		if !u.MemLimited {
			u.MemLimit = 16 << 30
		}
		samples = append(samples, resourceSample{T: float64(i), Usage: []model.ServiceUsage{u}})
		if i > 0 {
			tl = append(tl, model.LoadSample{T: float64(i), RPS: float64(i * 10)})
		}
	}
	return samples, tl
}

func TestCorrelateVerdicts(t *testing.T) {
	t.Run("single core", func(t *testing.T) {
		s, tl := synthetic("api", 14, false, 0, []float64{2, 40, 80, 99, 100, 101, 98}, nil)
		res := correlate("api", s, tl, nil, nil, nil)
		v := verdictOf(res, "api")
		if v.Kind != "single-core" || v.AtRPS != 30 || !strings.Contains(v.Message, "pinned one CPU core") || !strings.Contains(v.Message, "14 CPUs were available") {
			t.Errorf("%+v", v)
		}
	})
	t.Run("multi core is not single core", func(t *testing.T) {
		s, tl := synthetic("api", 14, false, 0, []float64{2, 100, 100, 100, 250, 300}, nil)
		if v := verdictOf(correlate("api", s, tl, nil, nil, nil), "api"); v.Bottleneck {
			t.Errorf("%+v", v)
		}
	})
	t.Run("unlimited host saturation", func(t *testing.T) {
		s, tl := synthetic("api", 4, false, 0, []float64{10, 300, 380, 390}, nil)
		if v := verdictOf(correlate("api", s, tl, nil, nil, nil), "api"); v.Kind != "cpu-bound" || !strings.Contains(v.Message, "of the host's 4 CPUs") {
			t.Errorf("%+v", v)
		}
	})
	t.Run("one spike is not saturation", func(t *testing.T) {
		s, tl := synthetic("api", 0.5, true, 0, []float64{5, 10, 49, 10, 12}, nil)
		if v := verdictOf(correlate("api", s, tl, nil, nil, nil), "api"); v.Bottleneck || v.Message != "api at 59% of its 0.5 CPU limit — not the bottleneck" {
			t.Errorf("%+v", v)
		}
	})
	t.Run("memory bound", func(t *testing.T) {
		s, tl := synthetic("api", 1, true, 256<<20, []float64{5, 10, 10}, []int64{100 << 20, 200 << 20, 245 << 20})
		v := verdictOf(correlate("api", s, tl, nil, nil, nil), "api")
		if v.Kind != "memory-bound" || v.Message != "api used 245 MiB of its 256 MiB memory limit (96%) — close to an OOM kill; raise the memory limit" {
			t.Errorf("%+v", v)
		}
	})
	t.Run("oom and restarts", func(t *testing.T) {
		s, tl := synthetic("api", 1, true, 256<<20, []float64{5, 95, 99}, nil)
		before := map[string]model.ContainerFacts{"api": {Restarts: 1}}
		res := correlate("api", s, tl, nil, before, map[string]model.ContainerFacts{"api": {Restarts: 3, OOMKilled: true}})
		if v := verdictOf(res, "api"); v.Kind != "oom" || res.Services[0].Restarts != 2 || !res.Services[0].OOMKilled {
			t.Errorf("%+v %+v", v, res.Services[0])
		}
		res = correlate("api", s, tl, nil, before, map[string]model.ContainerFacts{"api": {Restarts: 2}})
		if v := verdictOf(res, "api"); v.Kind != "restarted" || res.Verdict != v.Message {
			t.Errorf("%+v", v)
		}
	})
	t.Run("degraded without a saturated container", func(t *testing.T) {
		s, tl := synthetic("api", 1, true, 0, []float64{5, 20, 30}, nil)
		bp := 20.0
		res := correlate("api", s, tl, &model.LoadSummary{BreakingPoint: &bp}, nil, nil)
		if !strings.HasPrefix(res.Verdict, "No container reached its CPU or memory limit, yet latency or errors degraded") {
			t.Errorf("%q", res.Verdict)
		}
	})
	t.Run("target bottleneck wins the headline", func(t *testing.T) {
		mk := func(svc string, c float64) model.ServiceUsage {
			return model.ServiceUsage{Service: svc, CPUPercent: c, CPULimit: 1, CPULimited: true, Containers: 1}
		}
		s := []resourceSample{
			{T: 0, Usage: []model.ServiceUsage{mk("api", 10), mk("db", 10)}},
			{T: 1, Usage: []model.ServiceUsage{mk("api", 95), mk("db", 97)}},
			{T: 2, Usage: []model.ServiceUsage{mk("api", 99), mk("db", 99)}},
		}
		res := correlate("api", s, []model.LoadSample{{T: 1, RPS: 80}, {T: 2, RPS: 90}}, nil, nil, nil)
		if !strings.HasPrefix(res.Verdict, "api reached 99% of its 1 CPU limit at 90 req/s") {
			t.Errorf("%q", res.Verdict)
		}
	})
	if correlate("api", nil, nil, nil, nil, nil) != nil {
		t.Error("no samples gives no resources")
	}
}

// fakeProbe reports a service whose CPU grows with each sample.
type fakeProbe struct {
	mu       sync.Mutex
	n        int
	contains atomic.Int64
}

func (p *fakeProbe) Target() string { return "api" }

func (p *fakeProbe) Sample() []model.ServiceUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	return []model.ServiceUsage{{Service: "api", Containers: 1, CPUPercent: float64(p.n * 30), CPULimit: 1, CPULimited: true, MemBytes: 64 << 20, MemLimit: 256 << 20, MemLimited: true}}
}

func (p *fakeProbe) Containers() map[string]model.ContainerFacts {
	p.contains.Add(1)
	ms := int64(1200)
	return map[string]model.ContainerFacts{"api": {StartupMs: &ms}}
}

type fakeMonitor struct {
	probe *fakeProbe
	wfID  string
	node  string
}

func (m *fakeMonitor) MonitorResources(_ context.Context, wfID string, target model.LoadTarget) model.ResourceProbe {
	m.wfID, m.node = wfID, target.NodeID
	return m.probe
}

func TestEngineRecordsResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	w := newWatcher()
	mon := &fakeMonitor{probe: &fakeProbe{}}
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Monitor: mon})
	spec := httpSpec(srv.URL, hold(4, 20))
	spec.Target.NodeID = "service:api"
	run := runSpec(t, e, w, spec)
	if mon.wfID != testWF || mon.node != "service:api" {
		t.Errorf("monitor called with %q %q", mon.wfID, mon.node)
	}
	res := run.Resources
	if res == nil || len(res.Services) != 1 {
		t.Fatalf("resources %+v", res)
	}
	api := res.Services[0]
	if len(api.Timeline) != 5 || api.Timeline[0].T != 0 || api.Timeline[4].T != 4 || !api.Target || api.StartupMs == nil || *api.StartupMs != 1200 {
		t.Errorf("api %+v", api)
	}
	// 30, 60, 90, 120, 150 % of one CPU: saturated from t=3 (rolling 105%).
	if v := res.Verdicts[0]; v.Kind != "cpu-bound" || v.AtT != 3 || res.Verdict != v.Message {
		t.Errorf("verdict %+v", v)
	}
	if mon.probe.contains.Load() != 2 {
		t.Errorf("container facts read %d times, want before and after", mon.probe.contains.Load())
	}
	list := e.List(testWF)
	if len(list) != 1 || list[0].Resources == nil || len(list[0].Resources.Services[0].Timeline) != 0 || list[0].Resources.Verdict == "" {
		t.Errorf("listed resources keep the verdict without timelines: %+v", list[0].Resources)
	}
}
