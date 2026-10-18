package loadtest

import (
	"math"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// schedule is a piecewise-linear traffic curve built from load stages. In rps
// mode the curve is the arrival rate; in concurrency mode it is the number of
// virtual users. Times are in seconds since the load started.
type schedule struct {
	segs  []segment
	total float64
}

type segment struct {
	start, dur float64
	from, to   float64
	before     float64 // integral of the curve up to start
}

// newSchedule ramps each stage linearly from the previous stage's target (0
// for the first stage). A stage with DurationSec 0 is an instant jump.
func newSchedule(stages []model.LoadStage) schedule {
	var s schedule
	prev := 0.0
	for _, st := range stages {
		target := float64(st.Target)
		if st.DurationSec > 0 {
			d := float64(st.DurationSec)
			s.segs = append(s.segs, segment{start: s.total, dur: d, from: prev, to: target, before: s.integral(s.total)})
			s.total += d
		}
		prev = target
	}
	return s
}

func (s schedule) duration() float64 { return s.total }

// target is the curve value at t.
func (s schedule) target(t float64) float64 {
	for _, g := range s.segs {
		if t < g.start+g.dur {
			x := math.Max(t-g.start, 0)
			return g.from + (g.to-g.from)*x/g.dur
		}
	}
	return 0
}

// integral is the area under the curve on [0, t]: the expected number of
// arrivals in rps mode.
func (s schedule) integral(t float64) float64 {
	for _, g := range s.segs {
		if t < g.start+g.dur {
			x := math.Max(t-g.start, 0)
			return g.before + g.from*x + (g.to-g.from)*x*x/(2*g.dur)
		}
	}
	if len(s.segs) == 0 {
		return 0
	}
	last := s.segs[len(s.segs)-1]
	return last.before + (last.from+last.to)/2*last.dur
}

// arrival returns the time at which the integral reaches n, or false when the
// curve ends first. Arrival k of an open-model run happens at arrival(k+0.5),
// which spreads requests evenly at the instantaneous rate.
func (s schedule) arrival(n float64) (float64, bool) {
	for _, g := range s.segs {
		area := (g.from + g.to) / 2 * g.dur
		need := n - g.before
		if need > area {
			continue
		}
		if need <= 0 {
			return g.start, true
		}
		// Solve a*x^2 + b*x = need with a = slope/2, b = from, in the
		// rationalized form that stays stable when a is 0 or negative.
		a := (g.to - g.from) / (2 * g.dur)
		b := g.from
		disc := b*b + 4*a*need
		den := b + math.Sqrt(math.Max(disc, 0))
		if den <= 0 {
			continue
		}
		return g.start + math.Min(2*need/den, g.dur), true
	}
	return 0, false
}
