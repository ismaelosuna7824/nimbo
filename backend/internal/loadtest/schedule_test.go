package loadtest

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func arrivals(s schedule) []float64 {
	var out []float64
	for k := 0; ; k++ {
		t, ok := s.arrival(float64(k) + 0.5)
		if !ok || t >= s.duration() {
			return out
		}
		out = append(out, t)
	}
}

func TestScheduleSpreadsArrivalsEvenly(t *testing.T) {
	s := newSchedule([]model.LoadStage{{DurationSec: 0, Target: 5}, {DurationSec: 10, Target: 5}})
	got := arrivals(s)
	if len(got) != 50 {
		t.Fatalf("constant 5 rps for 10s: got %d arrivals, want 50", len(got))
	}
	for i, at := range got {
		want := (float64(i) + 0.5) / 5
		if math.Abs(at-want) > 1e-9 {
			t.Fatalf("arrival %d at %.6fs, want %.6fs", i, at, want)
		}
	}
}

func TestScheduleFollowsRamps(t *testing.T) {
	// 0 -> 100 over 10s, hold 100 for 5s, jump to 20 for 5s, ramp down to 0 over 5s.
	s := newSchedule([]model.LoadStage{
		{DurationSec: 10, Target: 100},
		{DurationSec: 5, Target: 100},
		{DurationSec: 0, Target: 20},
		{DurationSec: 5, Target: 20},
		{DurationSec: 5, Target: 0},
	})
	if s.duration() != 25 {
		t.Fatalf("duration = %v, want 25", s.duration())
	}
	got := arrivals(s)
	want := 500 + 500 + 100 + 50
	if len(got) != want {
		t.Fatalf("got %d arrivals, want %d", len(got), want)
	}
	perSecond := make([]int, 25)
	for _, at := range got {
		perSecond[int(at)]++
	}
	for sec, n := range perSecond {
		expected := s.integral(float64(sec+1)) - s.integral(float64(sec))
		if math.Abs(float64(n)-expected) > 1 {
			t.Errorf("second %d: %d arrivals, want ~%.1f", sec, n, expected)
		}
	}
	if v := s.target(12); v != 100 {
		t.Errorf("target(12) = %v, want 100", v)
	}
	if v := s.target(15); v != 20 {
		t.Errorf("target(15) right after the jump = %v, want 20", v)
	}
	if v := s.target(22.5); v != 10 {
		t.Errorf("target(22.5) on the ramp down = %v, want 10", v)
	}
}

func TestPresetsHaveExpectedShapes(t *testing.T) {
	want := map[string]struct {
		duration float64
		requests float64
		peak     float64
	}{
		"smoke":     {10, 50, 5},
		"load":      {100, 30*25 + 60*50 + 10*25, 50},
		"stress":    {150, 30 * (25 + 50 + 100 + 200 + 400), 400},
		"spike":     {50, 20*10 + 10*155 + 20*10, 300},
		"soak":      {600, 600 * 20, 20},
		"autoscale": {270, 120*100 + 120*200 + 30*100, 200},
	}
	for _, p := range Presets() {
		w, ok := want[p.Preset]
		if !ok {
			t.Fatalf("unexpected preset %q", p.Preset)
		}
		if p.Label == "" || p.Description == "" {
			t.Errorf("%s: missing label or description", p.Preset)
		}
		s := newSchedule(p.Stages)
		if s.duration() != w.duration || math.Abs(s.integral(s.duration())-w.requests) > 1e-9 {
			t.Errorf("%s: duration %v requests %v, want %v and %v", p.Preset, s.duration(), s.integral(s.duration()), w.duration, w.requests)
		}
		peak := 0.0
		for _, g := range s.segs {
			peak = max(peak, g.from, g.to)
		}
		if peak != w.peak {
			t.Errorf("%s: peak %v, want %v", p.Preset, peak, w.peak)
		}
	}
}

func TestTemplateRendersPlaceholders(t *testing.T) {
	tpl := compileTemplate(`{"n":{{seq}},"id":"{{uuid}}","at":"{{ now }}","r":{{rand}},"keep":"{{other}}"}`)
	if tpl.static() {
		t.Fatal("template with placeholders reported static")
	}
	out := tpl.render(42)
	re := regexp.MustCompile(`^\{"n":42,"id":"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}","at":"\d{4}-\d\d-\d\dT[^"]+Z","r":\d+,"keep":"\{\{other\}\}"\}$`)
	if !re.MatchString(out) {
		t.Fatalf("rendered %s", out)
	}
	if a, b := compileTemplate("{{uuid}}").render(1), compileTemplate("{{uuid}}").render(1); a == b {
		t.Fatalf("uuids repeat: %s", a)
	}
	static := compileTemplate("plain {{x}} text")
	if !static.static() || static.render(7) != "plain {{x}} text" {
		t.Fatalf("static template rendered %q", static.render(7))
	}
}

func TestDynamoItemMapsJSONTypes(t *testing.T) {
	item, err := DynamoItem(`{"s":"x","n":12.50,"b":true,"z":null,"l":[1,"a"],"m":{"k":"v"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, k := range []string{"s", "n", "b", "z", "l", "m"} {
		kinds = append(kinds, strings.TrimPrefix(fmt.Sprintf("%T", item[k]), "*types.AttributeValueMember"))
	}
	if got := strings.Join(kinds, ","); got != "S,N,BOOL,NULL,L,M" {
		t.Fatalf("attribute kinds = %s", got)
	}
	if _, err := DynamoItem(`[1]`); err == nil {
		t.Fatal("array accepted as item")
	}
}

func TestBreakingPoint(t *testing.T) {
	steady := func(target, p95 float64) secStat {
		return secStat{target: target, resolved: 100, executed: 100, ok: 100, p95: p95}
	}
	cases := []struct {
		name string
		secs []secStat
		want float64 // -1 = none
	}{
		{"steady", []secStat{steady(10, 20), steady(20, 22), steady(30, 21), steady(40, 25), steady(50, 30)}, -1},
		{"latency", []secStat{steady(10, 20), steady(20, 30), steady(30, 25), steady(40, 90), steady(50, 101), steady(60, 400)}, 50},
		{"errors", []secStat{steady(10, 20), {target: 20, resolved: 100, executed: 100, errors: 6, ok: 94, p95: 20}}, 20},
		{"saturation counts", []secStat{steady(10, 20), {target: 20, resolved: 50, executed: 40, errors: 10, ok: 40, p95: 20}}, 20},
		{"idle seconds and sub-floor jitter ignored", []secStat{{target: 0}, steady(5, 2), steady(10, 2), steady(15, 2), steady(20, 9)}, -1},
		// A fast endpoint queueing a little at high load is healthy: 1 ms
		// baseline, 12 ms p95 at 1466 req/s and no errors.
		{"fast endpoint stays healthy", []secStat{steady(50, 1), steady(100, 1), steady(150, 1), steady(500, 11), steady(1000, 12), steady(1466, 12)}, -1},
		{"fast endpoint saturating", []secStat{steady(50, 1), steady(100, 1), steady(150, 1), steady(1000, 12), steady(1200, 60), steady(1400, 300)}, 1200},
		{"one slow second", []secStat{steady(10, 20), steady(20, 22), steady(30, 21), steady(40, 400), steady(50, 25)}, -1},
	}
	for _, c := range cases {
		got := breakingPoint(c.secs)
		switch {
		case c.want < 0 && got != nil:
			t.Errorf("%s: breaking point %v, want none", c.name, *got)
		case c.want >= 0 && (got == nil || *got != c.want):
			t.Errorf("%s: breaking point %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStabilityIsCoefficientOfVariation(t *testing.T) {
	secs := []secStat{{ok: 1, p95: 10}, {ok: 1, p95: 30}, {ok: 0, p95: 0}, {ok: 1, p95: 20}}
	// mean 20, population stddev sqrt(200/3)
	want := round3(math.Sqrt(200.0/3) / 20)
	if got := stability(secs); got != want {
		t.Fatalf("stability = %v, want %v", got, want)
	}
}

func TestEvaluateThresholds(t *testing.T) {
	s := &model.LoadSummary{ErrorRate: 0.02, RPS: 48, Latency: model.LatencyStats{P95: 120, Max: 300}}
	got := evaluate([]model.Threshold{
		{Metric: "p95_ms", Op: "<", Value: 200},
		{Metric: "error_rate", Op: "<=", Value: 0.01},
		{Metric: "rps_min", Op: ">=", Value: 45},
		{Metric: "max_ms", Op: ">", Value: 300},
	}, s)
	wantActual := []float64{120, 0.02, 48, 300}
	wantPassed := []bool{true, false, true, false}
	for i, r := range got {
		if r.Actual != wantActual[i] || r.Passed != wantPassed[i] {
			t.Errorf("%s %s %v: actual %v passed %v, want %v %v", r.Metric, r.Op, r.Value, r.Actual, r.Passed, wantActual[i], wantPassed[i])
		}
	}
}
