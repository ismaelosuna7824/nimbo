package scenario

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// timeline builds n one-second samples (T 1..n) at 50 req/s, p95 20 ms,
// 2 in flight, then applies f to each.
func timeline(n int, f func(t int, s *model.LoadSample)) []model.LoadSample {
	out := make([]model.LoadSample, n)
	for i := range out {
		s := model.LoadSample{T: float64(i + 1), Target: 50, RPS: 50, P50: 10, P95: 20, P99: 25, Max: 30, InFlight: 2}
		if f != nil {
			f(i+1, &s)
		}
		out[i] = s
	}
	return out
}

func check(t *testing.T, res model.ScenarioResult, name string) model.ScenarioCheck {
	t.Helper()
	for _, c := range res.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, res.Checks)
	return model.ScenarioCheck{}
}

func TestKillReplicaVerdicts(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioKillReplica, Target: "service:api", AtSec: 20, DurationSec: 30}
	healed := Observation{Back: true, BackAfter: 2 * time.Second, SelfHealed: true, Verified: true, VerifyDetail: "restarted"}

	t.Run("survived: no errors, replica back on its own", func(t *testing.T) {
		res := Evaluate(Input{Spec: spec, TargetName: "api", Timeline: timeline(60, nil), InjectedAt: 20, RevertedAt: 50, TimeoutMs: 10000, Observation: healed})
		if res.Verdict != model.ScenarioSurvived {
			t.Fatalf("verdict %s: %s", res.Verdict, res.Message)
		}
		if res.RecoveredAtSec == nil || *res.RecoveredAtSec != 20 || res.RevertedAtSec == nil || *res.RevertedAtSec != 50 {
			t.Errorf("recovered %v reverted %v", res.RecoveredAtSec, res.RevertedAtSec)
		}
		if strings.Contains(res.Message, "Fix:") {
			t.Errorf("survived message has a fix: %s", res.Message)
		}
	})

	t.Run("degraded: errors until restart, then recovered", func(t *testing.T) {
		// Every request fails for 4 s after the kill (no load balancer).
		tl := timeline(60, func(sec int, s *model.LoadSample) {
			if sec > 20 && sec <= 24 {
				s.Errors, s.RPS, s.P95 = 50, 50, 0
			}
		})
		res := Evaluate(Input{Spec: spec, TargetName: "api", Timeline: tl, InjectedAt: 20, RevertedAt: 50, TimeoutMs: 10000, Observation: healed})
		if res.Verdict != model.ScenarioDegraded {
			t.Fatalf("verdict %s: %s", res.Verdict, res.Message)
		}
		if res.RecoveredAtSec == nil || *res.RecoveredAtSec != 24 {
			t.Errorf("recovered at %v, want 24", res.RecoveredAtSec)
		}
		if !strings.Contains(res.Message, "13%") || !strings.Contains(res.Message, HintLoadBalancer) {
			t.Errorf("message lacks the error rate or the load balancer hint: %s", res.Message)
		}
		if check(t, res, "Errors while a replica was down").Passed || !check(t, res, "Traffic moved off the dead replica").Passed {
			t.Errorf("checks %+v", res.Checks)
		}
	})

	t.Run("failed: never recovered", func(t *testing.T) {
		tl := timeline(60, func(sec int, s *model.LoadSample) {
			if sec > 20 {
				s.Errors, s.P95 = 50, 0
			}
		})
		res := Evaluate(Input{Spec: spec, Timeline: tl, InjectedAt: 20, RevertedAt: 50, Observation: Observation{Verified: true, VerifyDetail: "started by Nimbo"}})
		if res.Verdict != model.ScenarioFailed || res.RecoveredAtSec != nil {
			t.Fatalf("verdict %s recovered %v: %s", res.Verdict, res.RecoveredAtSec, res.Message)
		}
		if !strings.Contains(res.Message, HintRestartPolicy) {
			t.Errorf("no restart policy hint: %s", res.Message)
		}
		if check(t, res, "Replica replaced").Passed {
			t.Error("replica replaced passed without Back")
		}
	})

	t.Run("not self-healed is never survived", func(t *testing.T) {
		obs := Observation{Back: false, Verified: true, VerifyDetail: "started by Nimbo"}
		res := Evaluate(Input{Spec: spec, Timeline: timeline(60, nil), InjectedAt: 20, RevertedAt: 50, Observation: obs})
		if res.Verdict == model.ScenarioSurvived {
			t.Fatalf("survived although Nimbo restarted the replica: %s", res.Message)
		}
	})
}

func TestErrorRateBoundary(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioKillReplica, AtSec: 20, DurationSec: 30}
	obs := Observation{Back: true, SelfHealed: true}
	at := func(errors int) string {
		tl := timeline(60, func(sec int, s *model.LoadSample) {
			if sec == 21 {
				s.Errors = errors
			}
		})
		return Evaluate(Input{Spec: spec, Timeline: tl, InjectedAt: 20, RevertedAt: 50, Observation: obs}).Verdict
	}
	// 30 s x 50 req = 1500 requests: 15 errors is exactly 1%.
	if v := at(15); v != model.ScenarioSurvived {
		t.Errorf("1%% errors: %s, want survived", v)
	}
	if v := at(16); v != model.ScenarioDegraded {
		t.Errorf("just over 1%% errors: %s, want degraded", v)
	}
}

func TestRecoveryBoundary(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 20, DurationSec: 10}
	run := func(badAfter int) model.ScenarioResult {
		tl := timeline(80, func(sec int, s *model.LoadSample) {
			if sec > 20 && sec <= 30+badAfter {
				s.Errors, s.P95 = 50, 0
			}
		})
		return Evaluate(Input{Spec: spec, Timeline: tl, InjectedAt: 20, RevertedAt: 30, TimeoutMs: 2000, Observation: Observation{Back: true, Verified: true, VerifyDetail: "unpaused"}})
	}
	if r := run(RecoveryOKSec); r.Verdict != model.ScenarioDegraded || *r.RecoveredAtSec != 30+RecoveryOKSec {
		t.Errorf("recovery %d s: %s at %v", RecoveryOKSec, r.Verdict, r.RecoveredAtSec)
	} else if !check(t, r, "Recovered after it returned").Passed {
		t.Errorf("recovery of exactly %d s should pass: %+v", RecoveryOKSec, r.Checks)
	}
	if r := run(RecoveryOKSec + 1); check(t, r, "Recovered after it returned").Passed {
		t.Errorf("recovery of %d s should not pass", RecoveryOKSec+1)
	}
	if r := run(RecoveryFailedSec + 1); r.Verdict != model.ScenarioFailed {
		t.Errorf("recovery over %d s: %s, want failed", RecoveryFailedSec, r.Verdict)
	}
}

func TestDependencyDownHangVersusFailFast(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioDependencyDown, Target: "service:db", AtSec: 20, DurationSec: 15}
	obs := Observation{Back: true, Verified: true, VerifyDetail: "unpaused"}

	// Fail fast: errors at once, in-flight stays low, back right after.
	fast := timeline(70, func(sec int, s *model.LoadSample) {
		if sec > 20 && sec <= 35 {
			s.Errors, s.P95 = 50, 0
		}
	})
	res := Evaluate(Input{Spec: spec, TargetName: "db", Timeline: fast, InjectedAt: 20, RevertedAt: 35, TimeoutMs: 10000, Observation: obs})
	if res.Verdict != model.ScenarioDegraded || !check(t, res, "Failed fast while it was down").Passed {
		t.Fatalf("fail fast: %s %+v", res.Verdict, res.Checks)
	}
	if !strings.Contains(res.Message, HintFallback) {
		t.Errorf("no fallback hint: %s", res.Message)
	}

	// Hang: requests pile up waiting for the 10 s timeout.
	hang := timeline(70, func(sec int, s *model.LoadSample) {
		if sec > 20 && sec <= 35 {
			s.RPS, s.P95, s.InFlight = 0, 0, 50*(sec-20)
		}
	})
	res = Evaluate(Input{Spec: spec, TargetName: "db", Timeline: hang, InjectedAt: 20, RevertedAt: 35, TimeoutMs: 10000, Observation: obs})
	if res.Verdict != model.ScenarioFailed || check(t, res, "Failed fast while it was down").Passed {
		t.Fatalf("hang: %s %+v", res.Verdict, res.Checks)
	}
	if !strings.Contains(res.Message, HintTimeouts) || !strings.Contains(res.Message, "in-flight peaked at 750") {
		t.Errorf("hang message: %s", res.Message)
	}
}

func TestDependencyLatencyAmplification(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioDependencyLatency, Target: "service:db", AtSec: 20, DurationSec: 20, Params: map[string]string{"delayMs": "300"}}
	run := func(p95 float64) model.ScenarioResult {
		tl := timeline(70, func(sec int, s *model.LoadSample) {
			if sec > 20 && sec <= 40 {
				s.P95 = p95
			}
		})
		return Evaluate(Input{Spec: spec, TargetName: "db", Timeline: tl, InjectedAt: 20, RevertedAt: 40, TimeoutMs: 10000, Observation: Observation{Back: true, Verified: true, VerifyDetail: "qdisc removed"}})
	}
	// One sequential call: +300 ms is 1x the delay.
	if r := run(320); r.Verdict != model.ScenarioSurvived || !check(t, r, "Latency amplification").Passed {
		t.Errorf("1x: %s %s", r.Verdict, r.Message)
	}
	// Exactly LatencyAmplificationMax passes, above it is degraded with the retry hint.
	if r := run(20 + 300*LatencyAmplificationMax); !check(t, r, "Latency amplification").Passed {
		t.Errorf("%gx should pass", LatencyAmplificationMax)
	}
	r := run(20 + 900)
	if r.Verdict != model.ScenarioDegraded || !strings.Contains(r.Message, "3.0x") || !strings.Contains(r.Message, HintRetries) {
		t.Errorf("3x: %s %s", r.Verdict, r.Message)
	}
	if !check(t, r, "Fault reverted").Passed {
		t.Error("fault reverted check missing")
	}
}

func TestCacheFlushDatabaseLoad(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioCacheFlush, Target: "service:cache", AtSec: 20, DurationSec: 20}
	tl := timeline(60, func(sec int, s *model.LoadSample) {
		if sec > 20 && sec <= 23 {
			s.P95 = 200 // misses hit the database for 3 s
		}
	})
	db := model.LoadServiceResources{Service: "db"}
	for i := 0; i <= 60; i++ {
		cpu := 5.0
		if i > 20 && i <= 23 {
			cpu = 80
		}
		db.Timeline = append(db.Timeline, model.ResourcePoint{T: float64(i), CPUPercent: cpu})
	}
	res := Evaluate(Input{Spec: spec, TargetName: "cache", Timeline: tl, InjectedAt: 20, RevertedAt: 40, Observation: Observation{Keys: 120},
		Resources: &model.LoadResources{Services: []model.LoadServiceResources{{Service: "api", Target: true}, {Service: "cache"}, db}}})
	if res.Verdict != model.ScenarioDegraded {
		t.Fatalf("verdict %s: %s", res.Verdict, res.Message)
	}
	if c := check(t, res, "Database load"); c.Passed || !strings.Contains(c.Detail, "db CPU went from 5% to a peak of 80%") {
		t.Errorf("database load check %+v", c)
	}
	if c := check(t, res, "Cache re-warmed"); !c.Passed || *res.RecoveredAtSec != 23 {
		t.Errorf("re-warm %+v at %v", c, res.RecoveredAtSec)
	}
	if !strings.Contains(res.Message, "(120 keys)") || !strings.Contains(res.Message, HintCacheWarm) {
		t.Errorf("message %s", res.Message)
	}
}

func TestTrafficSpikeUsesScalingVerdict(t *testing.T) {
	spec := model.ScenarioSpec{ID: model.ScenarioTrafficSpike, AtSec: 20, DurationSec: 20, Params: map[string]string{"factor": "3"}}
	tl := timeline(60, func(sec int, s *model.LoadSample) {
		if sec > 20 && sec <= 40 {
			s.Target, s.RPS = 150, 150
		}
	})
	res := &model.LoadResources{Scaling: &model.LoadScaling{Workloads: []model.WorkloadScaling{{Workload: "web", Target: true, Kind: model.ScalingLate, Message: "pods were ready 40 s after p95 degraded"}}}}
	r := Evaluate(Input{Spec: spec, TargetName: "web", Timeline: tl, InjectedAt: 20, RevertedAt: 40, Resources: res, Kubernetes: true})
	if r.Verdict != model.ScenarioDegraded || check(t, r, "Autoscaling").Passed || !strings.Contains(r.Message, HintAutoscaleSpeed) {
		t.Fatalf("late HPA: %s %s", r.Verdict, r.Message)
	}
	res.Scaling.Workloads[0].Kind = model.ScalingScaled
	if r := Evaluate(Input{Spec: spec, TargetName: "web", Timeline: tl, InjectedAt: 20, RevertedAt: 40, Resources: res}); r.Verdict != model.ScenarioSurvived {
		t.Errorf("scaled HPA, no errors: %s %s", r.Verdict, r.Message)
	}

	// Only 60% of the spike completed.
	slow := timeline(60, func(sec int, s *model.LoadSample) {
		if sec > 20 && sec <= 40 {
			s.Target, s.RPS = 150, 90
		}
	})
	if r := Evaluate(Input{Spec: spec, Timeline: slow, InjectedAt: 20, RevertedAt: 40}); check(t, r, "Kept up with the spike").Passed {
		t.Errorf("60%% completed should not keep up: %+v", r.Checks)
	}
}

func TestInjectionErrorFails(t *testing.T) {
	res := Evaluate(Input{Spec: model.ScenarioSpec{ID: model.ScenarioDependencyDown}, InjectErr: errors.New("service db has no running container")})
	if res.Verdict != model.ScenarioFailed || !strings.Contains(res.Message, "no running container") || res.RevertedAtSec != nil {
		t.Errorf("%+v", res)
	}
}
