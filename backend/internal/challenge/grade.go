package challenge

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func preset(name string) (model.LoadProfile, bool) {
	for _, p := range loadtest.Presets() {
		if p.Preset == name {
			return p, true
		}
	}
	return model.LoadProfile{}, false
}

// verdictRank orders scenario verdicts, best first.
var verdictRank = map[string]int{model.ScenarioSurvived: 2, model.ScenarioDegraded: 1, model.ScenarioFailed: 0}

// GradeRun grades a load or scenario check on its finished run. A nil
// run, a run that ended in error or was stopped, or a metric the run lacks
// fails the measurement with the reason as the actual value.
func GradeRun(c model.ChallengeCheck, run *model.LoadTestRun) model.ChallengeGoalResult {
	res := model.ChallengeGoalResult{ID: c.ID, Label: c.Label, Type: c.Type, Checks: []model.ChallengeCheckResult{}}
	if run != nil {
		res.RunID = run.ID
	}
	var missing string
	switch {
	case run == nil:
		missing = "no run"
	case run.Status == model.LoadErrored:
		missing = "run failed"
	case run.Status == model.LoadStopped:
		missing = "run stopped"
	case run.Status == model.LoadRunning:
		missing = "still running"
	}
	var expect []model.ChallengeMetric
	if c.Load != nil {
		expect = c.Load.Expect
	}
	if s := c.Scenario; s != nil {
		r := model.ChallengeCheckResult{Label: "Scenario verdict", Expected: s.Expect.Verdict + " or better"}
		switch {
		case missing != "":
			r.Actual = missing
		case run.Scenario == nil || run.Scenario.Verdict == "":
			r.Actual = "no scenario result"
		default:
			r.Actual = run.Scenario.Verdict
			r.Passed = verdictRank[run.Scenario.Verdict] >= verdictRank[s.Expect.Verdict]
		}
		res.Checks = append(res.Checks, r)
		expect = s.Expect.Metrics
	}
	for _, m := range expect {
		r := model.ChallengeCheckResult{Label: metricLabel(m.Metric), Expected: opSymbol[m.Op] + " " + format(m.Metric, m.Value)}
		if missing != "" {
			r.Actual = missing
		} else {
			r.Passed, r.Actual = measure(m, run)
		}
		res.Checks = append(res.Checks, r)
	}
	finishGoal(&res)
	switch {
	case missing != "" && run != nil && run.Error != "":
		res.Status, res.Detail = model.ChallengeError, "The run failed: "+run.Error
	case missing != "":
		res.Status, res.Detail = model.ChallengeError, "No result: "+missing
	}
	return res
}

// finishGoal sets Passed, Status and Detail from the measurements.
func finishGoal(res *model.ChallengeGoalResult) {
	var failed []string
	for _, c := range res.Checks {
		if !c.Passed {
			failed = append(failed, fmt.Sprintf("%s (got %s, needs %s)", c.Label, c.Actual, c.Expected))
		}
	}
	res.Passed = len(failed) == 0 && len(res.Checks) > 0
	if res.Passed {
		res.Status, res.Detail = model.ChallengePassed, fmt.Sprintf("All %d measurements passed.", len(res.Checks))
		return
	}
	res.Status, res.Detail = model.ChallengeFailed, "Not yet: "+strings.Join(failed, "; ")+"."
}

var opSymbol = map[string]string{"lt": "<", "lte": "≤", "gt": ">", "gte": "≥"}

func metricLabel(m string) string {
	switch m {
	case model.MetricErrorRate:
		return "Error rate"
	case model.MetricRPS:
		return "Throughput"
	case model.MetricReplicasMax:
		return "Most ready replicas"
	}
	return strings.ToUpper(strings.TrimSuffix(m, "_ms")) + " latency"
}

// measure reads a metric from run and compares it.
func measure(m model.ChallengeMetric, run *model.LoadTestRun) (bool, string) {
	if m.Metric == model.MetricReplicasMax {
		w := scaledWorkload(run, m.Workload)
		if w == nil {
			return false, "no replica data"
		}
		return compare(float64(w.PeakReplicas), m.Op, m.Value), strconv.Itoa(w.PeakReplicas)
	}
	s := run.Summary
	if s == nil || s.Requests == 0 {
		return false, "no requests"
	}
	var v float64
	switch m.Metric {
	case model.MetricP50:
		v = s.Latency.P50
	case model.MetricP90:
		v = s.Latency.P90
	case model.MetricP95:
		v = s.Latency.P95
	case model.MetricP99:
		v = s.Latency.P99
	case model.MetricMax:
		v = s.Latency.Max
	case model.MetricErrorRate:
		v = s.ErrorRate
	case model.MetricRPS:
		v = s.RPS
	default:
		return false, "unknown metric"
	}
	return compare(v, m.Op, m.Value), format(m.Metric, v)
}

// scaledWorkload is the named workload of the run's scaling report, else
// its target.
func scaledWorkload(run *model.LoadTestRun, name string) *model.WorkloadScaling {
	if run.Resources == nil || run.Resources.Scaling == nil {
		return nil
	}
	ws := run.Resources.Scaling.Workloads
	for i := range ws {
		if (name != "" && ws[i].Workload == name) || (name == "" && ws[i].Target) {
			return &ws[i]
		}
	}
	return nil
}

func compare(v float64, op string, want float64) bool {
	switch op {
	case "lt":
		return v < want
	case "lte":
		return v <= want
	case "gt":
		return v > want
	case "gte":
		return v >= want
	}
	return false
}

func format(metric string, v float64) string {
	switch metric {
	case model.MetricErrorRate:
		return strconv.FormatFloat(math.Round(v*1000)/10, 'f', -1, 64) + "%"
	case model.MetricRPS:
		return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + " req/s"
	case model.MetricReplicasMax:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	if v >= 100 {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64) + " ms"
	}
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + " ms"
}

// Score is 0–100: every check weighs the same, and inside one every
// measurement does.
func Score(goals []model.ChallengeGoalResult) int {
	if len(goals) == 0 {
		return 0
	}
	var sum float64
	for _, g := range goals {
		if len(g.Checks) == 0 {
			continue
		}
		n := 0
		for _, c := range g.Checks {
			if c.Passed {
				n++
			}
		}
		sum += float64(n) / float64(len(g.Checks))
	}
	return int(math.Round(100 * sum / float64(len(goals))))
}

var thresholdOp = map[string]string{"lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

// Thresholds turns latency, error and throughput expectations into load
// test thresholds, so the run in the Load test tab shows the same lines.
func Thresholds(ms []model.ChallengeMetric) []model.Threshold {
	var out []model.Threshold
	for _, m := range ms {
		if m.Metric != model.MetricReplicasMax {
			out = append(out, model.Threshold{Metric: m.Metric, Op: thresholdOp[m.Op], Value: m.Value})
		}
	}
	return out
}
