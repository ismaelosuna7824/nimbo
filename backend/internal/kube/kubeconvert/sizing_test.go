package kubeconvert

import (
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const mi = 1 << 20

func TestRecommend(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   usage
		want recommendation
	}{
		{
			name: "normal: p90 × 1.15 up to 10m, limit max(peak × 1.5, 2 × request) up to 50m, memory × 1.2 / × 1.5",
			in:   usage{cpuP90: 0.2, cpuPeak: 0.3, memPeak: 100 * mi, cpuLimit: 1, cpuLimited: true, memLimit: 256 * mi, memLimited: true, verdict: "ok"},
			want: recommendation{reqCPU: 0.23, limCPU: 0.5, reqMem: 120 * mi, limMem: 150 * mi},
		},
		{
			name: "a spiky peak sets the limit",
			in:   usage{cpuP90: 0.1, cpuPeak: 0.8, memPeak: 200 * mi, verdict: "ok"},
			want: recommendation{reqCPU: 0.12, limCPU: 1.2, reqMem: 240 * mi, limMem: 300 * mi},
		},
		{
			name: "idle: floors of 50m and 64Mi",
			in:   usage{cpuP90: 0.001, cpuPeak: 0.002, memPeak: 3 * mi, verdict: "ok"},
			want: recommendation{reqCPU: 0.05, limCPU: 0.1, reqMem: 64 * mi, limMem: 64 * mi},
		},
		{
			name: "exact multiples don't round up a step",
			in:   usage{cpuP90: 0.4, cpuPeak: 0.4, memPeak: 100 * mi, verdict: "ok"},
			want: recommendation{reqCPU: 0.46, limCPU: 0.95, reqMem: 120 * mi, limMem: 150 * mi},
		},
		{
			name: "cpu-bound at the Docker limit: request ≥ the limit, limit twice it",
			in:   usage{cpuP90: 0.45, cpuPeak: 0.5, memPeak: 80 * mi, cpuLimit: 0.5, cpuLimited: true, verdict: "cpu-bound"},
			want: recommendation{reqCPU: 0.52, limCPU: 1.05, reqMem: 96 * mi, limMem: 120 * mi, censored: "cpu"},
		},
		{
			name: "cpu-bound at a big limit: never below it",
			in:   usage{cpuP90: 1.5, cpuPeak: 2, memPeak: 80 * mi, cpuLimit: 2, cpuLimited: true, verdict: "cpu-bound"},
			want: recommendation{reqCPU: 2, limCPU: 4, reqMem: 96 * mi, limMem: 120 * mi, censored: "cpu"},
		},
		{
			name: "cpu-bound on the whole host (no limit): not censored",
			in:   usage{cpuP90: 3.5, cpuPeak: 4, memPeak: 80 * mi, cpuLimit: 4, verdict: "cpu-bound"},
			want: recommendation{reqCPU: 4.03, limCPU: 8.1, reqMem: 96 * mi, limMem: 120 * mi},
		},
		{
			name: "memory-bound: request ≥ the Docker memory limit, limit twice it",
			in:   usage{cpuP90: 0.1, cpuPeak: 0.1, memPeak: 120 * mi, memLimit: 128 * mi, memLimited: true, verdict: "memory-bound"},
			want: recommendation{reqCPU: 0.12, limCPU: 0.25, reqMem: 144 * mi, limMem: 256 * mi, censored: "memory"},
		},
		{
			name: "OOM-killed before its peak was sampled",
			in:   usage{cpuP90: 0.1, cpuPeak: 0.1, memPeak: 60 * mi, memLimit: 128 * mi, memLimited: true, verdict: "oom"},
			want: recommendation{reqCPU: 0.12, limCPU: 0.25, reqMem: 128 * mi, limMem: 256 * mi, censored: "memory"},
		},
		{
			name: "single-core: one core requested",
			in:   usage{cpuP90: 0.95, cpuPeak: 1, memPeak: 80 * mi, cpuLimit: 4, verdict: "single-core"},
			want: recommendation{reqCPU: 1.1, limCPU: 2.2, reqMem: 96 * mi, limMem: 120 * mi, censored: "single-core"},
		},
	} {
		if got := recommend(tc.in); got != tc.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	m := &usage{cpuP90: 0.2, cpuPeak: 0.3, memPeak: 100 * mi, verdict: "ok"}
	// No load test: the defaults.
	s := resolve(resourceSpec{}, nil, false)
	if s.resourceSpec != (resourceSpec{reqCPU: 0.125, limCPU: 0.5, reqMem: 256 * mi, limMem: 256 * mi}) || s.source() != model.SizingDefault {
		t.Errorf("defaults %+v %s", s.resourceSpec, s.source())
	}
	if s := resolve(resourceSpec{}, nil, true); s.limCPU != 1 || s.limMem != 512*mi {
		t.Errorf("data defaults %+v", s.resourceSpec)
	}
	// Measured replaces every default.
	s = resolve(resourceSpec{}, m, false)
	if s.resourceSpec != (resourceSpec{reqCPU: 0.23, limCPU: 0.5, reqMem: 120 * mi, limMem: 150 * mi}) || s.source() != model.SizingLoadTest || len(s.measured) != 4 {
		t.Errorf("measured %+v %s", s.resourceSpec, s.source())
	}
	// Explicit compose limits win; the measured requests fill the rest.
	s = resolve(resourceSpec{limCPU: 2, limMem: 1024 * mi}, m, false)
	if s.resourceSpec != (resourceSpec{reqCPU: 0.23, limCPU: 2, reqMem: 120 * mi, limMem: 1024 * mi}) || s.source() != model.SizingLoadTest ||
		strings.Join(s.compose, ",") != "cpu limit,memory limit" {
		t.Errorf("explicit limits %+v %v", s.resourceSpec, s.compose)
	}
	// An explicit limit caps a measured request; an explicit request
	// raises a measured limit.
	s = resolve(resourceSpec{limCPU: 0.1, reqMem: 512 * mi}, m, false)
	if s.reqCPU != 0.1 || s.limCPU != 0.1 || s.reqMem != 512*mi || s.limMem != 512*mi {
		t.Errorf("caps %+v", s.resourceSpec)
	}
	// Data services: a measurement below the defaults keeps the default
	// requests (cpu ¼ of the 1-core default limit, memory = the 512Mi
	// default limit); the limits grow to them.
	s = resolve(resourceSpec{}, m, true)
	if s.resourceSpec != (resourceSpec{reqCPU: 0.25, limCPU: 0.5, reqMem: 512 * mi, limMem: 512 * mi}) || strings.Join(s.kept, ",") != "cpu request,memory request" {
		t.Errorf("data below default %+v kept %v", s.resourceSpec, s.kept)
	}
	// … with explicit compose limits the defaults derive from them
	// (cpu 125m: the measured 230m is above it and stays).
	s = resolve(resourceSpec{limCPU: 0.5, limMem: 256 * mi}, m, true)
	if s.reqCPU != 0.23 || s.reqMem != 256*mi || s.limCPU != 0.5 || s.limMem != 256*mi || strings.Join(s.kept, ",") != "memory request" {
		t.Errorf("data explicit limits %+v kept %v", s.resourceSpec, s.kept)
	}
	// A measurement above the defaults raises them.
	busy := &usage{cpuP90: 1, cpuPeak: 1.2, memPeak: 700 * mi, verdict: "ok"}
	s = resolve(resourceSpec{}, busy, true)
	if s.resourceSpec != (resourceSpec{reqCPU: 1.15, limCPU: 2.3, reqMem: 840 * mi, limMem: 1050 * mi}) || len(s.kept) != 0 {
		t.Errorf("data above default %+v kept %v", s.resourceSpec, s.kept)
	}
	// The CPU above, memory below: only memory is kept.
	mixed := &usage{cpuP90: 1, cpuPeak: 1.2, memPeak: 100 * mi, verdict: "ok"}
	if s = resolve(resourceSpec{}, mixed, true); s.reqCPU != 1.15 || s.reqMem != 512*mi || strings.Join(s.kept, ",") != "memory request" {
		t.Errorf("data mixed %+v kept %v", s.resourceSpec, s.kept)
	}
	// Everything explicit: compose, no measurement used.
	s = resolve(resourceSpec{reqCPU: 0.1, limCPU: 1, reqMem: 64 * mi, limMem: 128 * mi}, m, false)
	if s.source() != model.SizingCompose || len(s.measured) != 0 {
		t.Errorf("all explicit %s %v", s.source(), s.measured)
	}
}

// point is one second of a service's usage (cpu in % of a core, summed
// over containers).
func point(tt, cpu float64, memMi int64, containers int) model.ResourcePoint {
	return model.ResourcePoint{T: tt, CPUPercent: cpu, MemBytes: memMi * mi, Containers: containers}
}

func TestMeasure(t *testing.T) {
	run := &model.LoadTestRun{ID: "r", Status: model.LoadPassed, Resources: &model.LoadResources{
		Services: []model.LoadServiceResources{
			{Service: "api", CPULimit: 2, CPULimited: true, MemLimit: 512 * mi, MemLimited: true, Timeline: []model.ResourcePoint{
				point(0, 900, 900, 2), // baseline: ignored
				point(1, 20, 100, 2), point(2, 40, 120, 2), point(3, 60, 140, 2), point(4, 80, 160, 2), point(5, 100, 180, 2),
				point(6, 120, 200, 2), point(7, 140, 220, 2), point(8, 160, 240, 2), point(9, 180, 260, 2), point(10, 200, 280, 2),
			}},
			{Service: "short", Timeline: []model.ResourcePoint{point(1, 50, 10, 1), point(2, 50, 10, 1)}},
		},
		Verdicts: []model.ResourceVerdict{{Service: "api", Kind: "ok"}},
	}}
	got := measure(run)
	api, ok := got["api"]
	// Per container: CPU 0.1..1.0 cores, p90 (nearest rank of 10) = 0.9.
	if !ok || api.cpuP90 != 0.9 || api.cpuPeak != 1 || api.memPeak != 140*mi || api.samples != 10 || api.cpuLimit != 1 || api.memLimit != 256*mi || api.verdict != "ok" {
		t.Errorf("api %+v", api)
	}
	if _, ok := got["short"]; ok {
		t.Error("a service with fewer than 5 seconds of load was sized")
	}
}

// loadRun is a finished run that measured api (and a service that no
// longer exists).
func loadRun(target string, verdict string) *model.LoadTestRun {
	var tl []model.ResourcePoint
	for i := range 20 {
		tl = append(tl, point(float64(i+1), float64(10+i), 90, 1))
	}
	return &model.LoadTestRun{
		ID: "run1", Status: model.LoadPassed, StartedAt: time.Date(2026, 10, 12, 14, 2, 0, 0, time.Local),
		Spec:     model.LoadTestSpec{Name: "smoke"},
		Timeline: []model.LoadSample{{T: 20, RPS: 180}},
		Summary:  &model.LoadSummary{RPS: 150, PeakRPS: 180},
		Resources: &model.LoadResources{Service: target, Services: []model.LoadServiceResources{
			{Service: "api", CPULimit: 0.25, CPULimited: true, MemLimit: 256 * mi, MemLimited: true, Timeline: tl},
			{Service: "gone", Timeline: tl},
		}, Verdicts: []model.ResourceVerdict{{Service: "api", Kind: verdict}}},
	}
}

const sizedCompose = `services:
  api:
    image: ghcr.io/acme/api:1.4.2
    ports: ["8080:8080"]
  worker:
    image: ghcr.io/acme/worker:1.4.2
    cpus: 4
`

func TestConvertSizesFromLoadTest(t *testing.T) {
	p := openFiles(t, map[string][]byte{"compose.yaml": []byte(sizedCompose)})
	res, err := Convert(p, Options{Format: model.ConvertKustomize, Name: "acme", LoadTest: loadRun("api", "ok")})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Report
	if r.LoadTest == nil || !r.LoadTest.Used || r.LoadTest.RunID != "run1" || r.LoadTest.PeakRPS != 180 || strings.Join(r.LoadTest.Services, ",") != "api" {
		t.Fatalf("load test %+v", r.LoadTest)
	}
	rows := map[string]model.KubeConvertSizing{}
	for _, s := range r.Sizing {
		rows[s.Service] = s
	}
	// api: CPU 0.10..0.29 cores, p90 0.27 → 320m; peak 0.29 → limit 650m; 90Mi → 108Mi / 135Mi.
	api := rows["api"]
	if api.Source != model.SizingLoadTest || api.Requests != (model.KubeResourceQty{CPU: "320m", Memory: "108Mi"}) || api.Limits != (model.KubeResourceQty{CPU: "650m", Memory: "135Mi"}) ||
		api.Measured == nil || api.Measured.CPUP90 != 0.27 || api.Censored != "" {
		t.Errorf("api sizing %+v %+v", api, api.Measured)
	}
	if !strings.HasPrefix(api.Detail, "from load test 12 Oct 14:02 at 180 rps: p90 CPU 0.27, peak CPU 0.29, peak memory 90Mi") {
		t.Errorf("api provenance %q", api.Detail)
	}
	found := false
	for _, m := range r.Mappings {
		if m.Service == "api" && strings.HasPrefix(m.From, "load test 12 Oct 14:02") && m.To == "requests cpu 320m / memory 108Mi, limits cpu 650m / memory 135Mi" {
			found = true
		}
	}
	if !found {
		t.Errorf("api mapping missing: %+v", r.Mappings)
	}
	// worker wasn't measured: its explicit cpus stay, the rest defaulted.
	if w := rows["worker"]; w.Source != model.SizingCompose || w.Limits.CPU != "4" || w.Measured != nil {
		t.Errorf("worker sizing %+v", w)
	}
	d := docs(t, res.Files, "k8s/base/")
	if val(d["Deployment/api"], "spec", "template", "spec", "containers", "0", "resources", "requests", "cpu") != "320m" {
		t.Error("api requests not written")
	}

	// Turned off: the run is reported, but not used.
	off, err := Convert(p, Options{Format: model.ConvertKustomize, Name: "acme", LoadTest: loadRun("api", "ok"), IgnoreLoadTest: true})
	if err != nil {
		t.Fatal(err)
	}
	if off.Report.LoadTest == nil || off.Report.LoadTest.Used || off.Report.Sizing[0].Source != model.SizingDefault {
		t.Errorf("ignored run %+v %+v", off.Report.LoadTest, off.Report.Sizing)
	}
}

func TestConvertCensoredAndExplicit(t *testing.T) {
	// api ran under cpus 0.25 and hit it: the compose limit stays, the
	// request is measured (capped by the limit), and the report says the
	// measurement is a lower bound and suggests a bigger limit.
	p := openFiles(t, map[string][]byte{"compose.yaml": []byte(`services:
  api:
    image: ghcr.io/acme/api:1.4.2
    ports: ["8080:8080"]
    cpus: 0.25
`)})
	res, err := Convert(p, Options{Format: model.ConvertHelm, Name: "acme", LoadTest: loadRun("api", "cpu-bound")})
	if err != nil {
		t.Fatal(err)
	}
	api := res.Report.Sizing[0]
	if api.Censored != "cpu" || api.Limits.CPU != "250m" || api.Requests.CPU != "250m" || !strings.Contains(api.Detail, "cpu limit from compose") || !strings.Contains(api.Detail, "CPU-bound at its Docker limit of 0.25 CPU") {
		t.Errorf("censored sizing %+v", api)
	}
	var censored, suggested bool
	for _, is := range res.Report.Unmapped {
		censored = censored || is.Feature == "resources" && strings.Contains(is.Detail, "sized one step above")
		suggested = suggested || is.Feature == "resources" && strings.Contains(is.Detail, "Kept the compose cpu limit 250m (measured suggests 650m)")
	}
	if !censored || !suggested {
		t.Errorf("issues %+v", res.Report.Unmapped)
	}
}

func TestConvertKeepsDataServiceDefaults(t *testing.T) {
	p := openFiles(t, map[string][]byte{"compose.yaml": []byte(`services:
  cache:
    image: redis:7.4-alpine
`)})
	run := loadRun("", "ok")
	run.Resources.Services[0].Service = "cache" // 0.10..0.29 cores, 90Mi
	res, err := Convert(p, Options{Format: model.ConvertKustomize, Name: "acme", LoadTest: run})
	if err != nil {
		t.Fatal(err)
	}
	row := res.Report.Sizing[0]
	// The measured CPU (320m) is above the data default request (250m)
	// and raises it; the measured memory (108Mi) isn't sized below 512Mi.
	if row.Requests != (model.KubeResourceQty{CPU: "320m", Memory: "512Mi"}) || row.Source != model.SizingLoadTest ||
		!strings.Contains(row.Detail, "memory request kept at the default: data services are not sized down from a short load test") {
		t.Errorf("cache sizing %+v", row)
	}
}

func TestUsableRun(t *testing.T) {
	p := openFiles(t, map[string][]byte{"compose.yaml": []byte(sizedCompose)})
	if err := UsableRun(p, loadRun("api", "ok")); err != nil {
		t.Errorf("usable: %v", err)
	}
	stale := loadRun("gone", "ok")
	if err := UsableRun(p, stale); err == nil || !strings.Contains(err.Error(), "no longer a service") {
		t.Errorf("stale target: %v", err)
	}
	running := loadRun("api", "ok")
	running.Status = model.LoadRunning
	if UsableRun(p, running) == nil {
		t.Error("running run accepted")
	}
	none := loadRun("", "ok")
	none.Resources.Services = none.Resources.Services[1:] // only "gone"
	if UsableRun(p, none) == nil {
		t.Error("run measuring no current service accepted")
	}
	if _, err := Convert(p, Options{Name: "acme", LoadTest: stale}); err == nil {
		t.Error("Convert accepted a stale run")
	}
}
