package loadtest

import (
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Error kinds produced by the engine itself; AWS API errors are reported as
// "aws:<ErrorCode>", database errors as "sql:<SQLSTATE>", "mysql:<errno>",
// "redis:<prefix>" and "mongo:<code name>".
const (
	errClientSaturated   = "client_saturated"
	errTimeout           = "timeout"
	errConnectionRefused = "connection_refused"
	errConnectionReset   = "connection_reset"
	errConnectionClosed  = "connection_closed"
	errDNS               = "dns_error"
	errNetwork           = "network_error"
	errHTTP5xx           = "http_5xx"
	errHTTP4xx           = "http_4xx"
	errFunction          = "function_error"
	errInvalidPayload    = "invalid_payload"
	errAuthFailed        = "auth_failed"
)

// Latencies are recorded in microseconds; anything slower than an hour is
// clamped (requests time out long before that).
const maxTrackableMicros = int64(time.Hour / time.Microsecond)

// outcome is the result of one executed request, without its latency.
type outcome struct {
	status  string // HTTP status code; "" when no response arrived
	errKind string // "" on success
	bytes   int64
}

// secStat is the per-second data needed to derive the summary.
type secStat struct {
	target   float64
	resolved int // executed + rejected (client_saturated)
	errors   int
	executed int
	ok       int
	p95      float64
}

// collector aggregates request outcomes. Latency statistics cover successful
// requests only: failures are reported through error rate and kinds, and
// mixing instant refusals or capped timeouts into percentiles would distort
// responsiveness.
type collector struct {
	mu sync.Mutex

	total  *hdrhistogram.Histogram
	second *hdrhistogram.Histogram

	// exact moments over successful latencies, in microseconds
	okCount   int64
	sum       float64
	sumSq     float64
	min, max  int64
	resolved  int
	executed  int
	errors    int
	bytesIn   int64
	statuses  map[string]int
	errKinds  map[string]int
	secOK     int
	secErrors int
	secExec   int
	secRes    int
	secMax    int64
}

func newCollector() *collector {
	return &collector{
		total:    hdrhistogram.New(1, maxTrackableMicros, 3),
		second:   hdrhistogram.New(1, maxTrackableMicros, 3),
		statuses: map[string]int{},
		errKinds: map[string]int{},
	}
}

func (c *collector) record(lat time.Duration, o outcome) {
	us := min(max(lat.Microseconds(), 1), maxTrackableMicros)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolved++
	c.executed++
	c.secRes++
	c.secExec++
	c.bytesIn += o.bytes
	if o.status != "" {
		c.statuses[o.status]++
	}
	if o.errKind != "" {
		c.errors++
		c.secErrors++
		c.errKinds[o.errKind]++
		return
	}
	c.secOK++
	c.okCount++
	c.sum += float64(us)
	c.sumSq += float64(us) * float64(us)
	if c.okCount == 1 || us < c.min {
		c.min = us
	}
	c.max = max(c.max, us)
	c.secMax = max(c.secMax, us)
	_ = c.total.RecordValue(us)
	_ = c.second.RecordValue(us)
}

// saturated records an arrival that could not start because every worker
// was busy.
func (c *collector) saturated() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolved++
	c.secRes++
	c.errors++
	c.secErrors++
	c.errKinds[errClientSaturated]++
}

// roll closes the current one-second window.
func (c *collector) roll(t, target float64, inFlight int) (model.LoadSample, secStat) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := model.LoadSample{
		T:        t,
		Target:   round3(target),
		RPS:      float64(c.secExec),
		Errors:   c.secErrors,
		InFlight: inFlight,
	}
	st := secStat{target: s.Target, resolved: c.secRes, errors: c.secErrors, executed: c.secExec, ok: c.secOK}
	if c.secOK > 0 {
		s.P50 = quantile(c.second, 50, c.secMax)
		s.P95 = quantile(c.second, 95, c.secMax)
		s.P99 = quantile(c.second, 99, c.secMax)
		s.Max = micros(c.secMax)
		st.p95 = s.P95
	}
	c.second.Reset()
	c.secOK, c.secErrors, c.secExec, c.secRes, c.secMax = 0, 0, 0, 0, 0
	return s, st
}

// summary aggregates the whole run. active is how long load was generated.
func (c *collector) summary(active time.Duration, secs []secStat) model.LoadSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := model.LoadSummary{
		Requests:    c.resolved,
		Errors:      c.errors,
		BytesIn:     c.bytesIn,
		StatusCodes: maps.Clone(c.statuses),
		ErrorKinds:  maps.Clone(c.errKinds),
	}
	if c.resolved > 0 {
		s.ErrorRate = float64(c.errors) / float64(c.resolved)
	}
	if active > 0 {
		s.RPS = round3(float64(c.executed) / active.Seconds())
	}
	for _, st := range secs {
		s.PeakRPS = max(s.PeakRPS, float64(st.executed))
	}
	if c.okCount > 0 {
		n := float64(c.okCount)
		mean := c.sum / n
		variance := max(c.sumSq/n-mean*mean, 0)
		s.Latency = model.LatencyStats{
			Min:    micros(c.min),
			Mean:   round3(mean / 1000),
			StdDev: round3(math.Sqrt(variance) / 1000),
			P50:    quantile(c.total, 50, c.max),
			P90:    quantile(c.total, 90, c.max),
			P95:    quantile(c.total, 95, c.max),
			P99:    quantile(c.total, 99, c.max),
			Max:    micros(c.max),
		}
	}
	s.Stability = stability(secs)
	s.BreakingPoint = breakingPoint(secs)
	return s
}

// stability is the coefficient of variation of per-second p95 over the
// seconds that had successful requests.
func stability(secs []secStat) float64 {
	var vals []float64
	for _, st := range secs {
		if st.ok > 0 {
			vals = append(vals, st.p95)
		}
	}
	if len(vals) < 2 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	if mean == 0 {
		return 0
	}
	var sq float64
	for _, v := range vals {
		sq += (v - mean) * (v - mean)
	}
	return round3(math.Sqrt(sq/float64(len(vals))) / mean)
}

// Breaking-point rule, shared by the run summary (BreakingPoint), the
// scaling verdicts and capacity planning (FirstDegraded).
const (
	// breakErrorRate: a second with more errors than this is degraded at
	// once — failed requests are never noise.
	breakErrorRate = 0.05
	// baselineSecs: the baseline is the median p95 of the first seconds
	// with successful requests.
	baselineSecs = 3
	// A second's p95 is over the baseline when it exceeds
	// max(breakP95Factor × baseline, baseline + minDegradationMs). The
	// factor alone is meaningless on fast endpoints: a 1 ms baseline makes
	// a perfectly healthy 12 ms p95 at 1500 req/s look "12× slower". The
	// absolute floor asks for latency a caller would notice (the same
	// 50 ms slack the scenario verdicts use, scenario.P95SlackMs) before
	// calling it queueing; on slow endpoints (baseline above ~17 ms) the
	// factor dominates as before.
	breakP95Factor   = 4
	minDegradationMs = 50
	// breakSustainSec: latency must stay over the baseline this many
	// seconds in a row to count. One slow second is a GC pause, a cold
	// connection or a scheduler hiccup; saturation persists. Every second
	// of a sustained streak is flagged, so the breaking point is where the
	// streak started.
	breakSustainSec = 2
)

// degradation says why one second of a run degraded: p95 sustained over
// its baseline, or an error rate above breakErrorRate.
type degradation struct {
	latency bool
	errors  bool
}

func (d degradation) any() bool { return d.latency || d.errors }

// slowP95 is the p95 above which a second is over a baseline.
func slowP95(baseline float64) float64 {
	return max(breakP95Factor*baseline, baseline+minDegradationMs)
}

// degradations flags every second whose error rate exceeds breakErrorRate
// or that belongs to a streak of at least breakSustainSec seconds whose p95
// is above slowP95 of the baseline (index i is second i+1 of the
// timeline). Seconds without traffic, with errors or within the baseline
// end a streak.
func degradations(secs []secStat) []degradation {
	out := make([]degradation, len(secs))
	var base []float64
	baseline := -1.0
	streak := 0 // consecutive seconds over the baseline, ending at i
	for i, st := range secs {
		over := false
		switch {
		case st.resolved == 0:
		case float64(st.errors)/float64(st.resolved) > breakErrorRate:
			out[i].errors = true
		case st.ok == 0:
		case baseline < 0:
			base = append(base, st.p95)
			if len(base) == baselineSecs {
				slices.Sort(base)
				baseline = base[1]
			}
		default:
			over = st.p95 > slowP95(baseline)
		}
		if !over {
			streak = 0
			continue
		}
		streak++
		switch {
		case streak == breakSustainSec:
			for j := i - streak + 1; j <= i; j++ {
				out[j].latency = true
			}
		case streak > breakSustainSec:
			out[i].latency = true
		}
	}
	return out
}

// breakingPoint returns the target of the first degraded second.
func breakingPoint(secs []secStat) *float64 {
	for i, d := range degradations(secs) {
		if d.any() {
			return &secs[i].target
		}
	}
	return nil
}

// FirstDegraded is the index of the first second of a recorded timeline
// whose latency or errors degraded (the run's breaking point), -1 when
// none did. Timelines don't keep arrivals rejected by busy workers apart
// from executed requests, so the error rate is taken over the executed
// ones: an upper bound, which only makes the healthy part shorter.
func FirstDegraded(timeline []model.LoadSample) int {
	secs := make([]secStat, len(timeline))
	for i, s := range timeline {
		ok := 0
		if s.P95 > 0 {
			ok = 1
		}
		exec := int(s.RPS)
		secs[i] = secStat{target: s.Target, resolved: max(exec, s.Errors), errors: s.Errors, executed: exec, ok: ok, p95: s.P95}
	}
	for i, d := range degradations(secs) {
		if d.any() {
			return i
		}
	}
	return -1
}

var thresholdMetrics = map[string]func(*model.LoadSummary) float64{
	"p50_ms":     func(s *model.LoadSummary) float64 { return s.Latency.P50 },
	"p90_ms":     func(s *model.LoadSummary) float64 { return s.Latency.P90 },
	"p95_ms":     func(s *model.LoadSummary) float64 { return s.Latency.P95 },
	"p99_ms":     func(s *model.LoadSummary) float64 { return s.Latency.P99 },
	"max_ms":     func(s *model.LoadSummary) float64 { return s.Latency.Max },
	"error_rate": func(s *model.LoadSummary) float64 { return s.ErrorRate },
	"rps_min":    func(s *model.LoadSummary) float64 { return s.RPS },
}

var thresholdOps = map[string]func(a, b float64) bool{
	"<":  func(a, b float64) bool { return a < b },
	"<=": func(a, b float64) bool { return a <= b },
	">":  func(a, b float64) bool { return a > b },
	">=": func(a, b float64) bool { return a >= b },
}

// evaluate checks validated thresholds against s. error_rate is a fraction
// (0..1); rps_min compares the achieved overall rps.
func evaluate(ths []model.Threshold, s *model.LoadSummary) []model.ThresholdResult {
	out := make([]model.ThresholdResult, 0, len(ths))
	for _, th := range ths {
		actual := thresholdMetrics[th.Metric](s)
		out = append(out, model.ThresholdResult{Threshold: th, Actual: actual, Passed: thresholdOps[th.Op](actual, th.Value)})
	}
	return out
}

func micros(us int64) float64 { return round3(float64(us) / 1000) }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// quantile reads a percentile in ms. HDR reports the upper bound of the
// value's bucket, which can exceed the exact maximum by the histogram's
// 0.1% precision.
func quantile(h *hdrhistogram.Histogram, q float64, maxMicros int64) float64 {
	return micros(min(h.ValueAtQuantile(q), maxMicros))
}
