// Package readiness scores the production readiness of docker and
// Kubernetes workflows: the checklist categories, the evidence of real runs
// (load tests, incident scenarios, capacity plans) and one row per service
// or workload. kubeready builds the Kubernetes checklist; Docker builds the
// compose one; both finish with Summarize.
//
// Scoring model (deterministic: the same files and runs give the same
// numbers):
//
//   - Item points: pass and info 1, warn 0.5, fail 0. Todo (not verified
//     yet: no load test, scenario run or capacity plan) and skip (does not
//     apply) items earn nothing and are left out of the mean: missing
//     evidence is not a failure.
//   - Category score: 100 × mean points of its scored items, nil when none
//     is scored.
//   - Environment score: the Weights-weighted mean of the categories that
//     have a score (unrounded category means, rounded once).
//   - Confidence: the Weights-weighted share of verified items, over the
//     categories with at least one applicable item: scored / (scored +
//     todo). 100 means nothing is left untested.
//   - Status: blocked when any rule or runtime item fails (whatever the
//     score); needs_work when any item warns, a resilience item fails (a
//     scenario the system did not survive scores 0 but does not block),
//     or in production-like environments when any is untested; else ready.
package readiness

import (
	"fmt"
	"math"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Weights are each category's share of the environment score (sum 100).
var Weights = map[string]int{
	model.ReadinessReliability: 30,
	model.ReadinessPerformance: 20,
	model.ReadinessResilience:  15,
	model.ReadinessSecurity:    15,
	model.ReadinessMonitoring:  10,
	model.ReadinessHygiene:     10,
}

var titles = map[string]string{
	model.ReadinessReliability: "Reliability",
	model.ReadinessPerformance: "Performance and capacity",
	model.ReadinessResilience:  "Resilience",
	model.ReadinessSecurity:    "Security",
	model.ReadinessMonitoring:  "Monitoring",
	model.ReadinessHygiene:     "Hygiene",
}

// Order is the display order of the categories.
var Order = []string{model.ReadinessReliability, model.ReadinessPerformance, model.ReadinessResilience, model.ReadinessMonitoring, model.ReadinessSecurity, model.ReadinessHygiene}

// points is what a status earns; scored is false for todo and skip.
func points(s model.ReadinessItemStatus) (p float64, scored bool) {
	switch s {
	case model.ItemPass, model.ItemInfo:
		return 1, true
	case model.ItemWarn:
		return 0.5, true
	case model.ItemFail:
		return 0, true
	}
	return 0, false
}

// tally is the scoring state of a list of statuses.
type tally struct {
	got          float64
	scored, todo int
}

func (t *tally) add(s model.ReadinessItemStatus) {
	p, ok := points(s)
	if ok {
		t.got += p
		t.scored++
	}
	if s == model.ItemTodo {
		t.todo++
	}
}

func (t *tally) mean() (float64, bool) {
	if t.scored == 0 {
		return 0, false
	}
	return t.got / float64(t.scored), true
}

func pct(f float64) int { return int(math.Round(100 * f)) }

func scorePtr(t *tally) *int {
	m, ok := t.mean()
	if !ok {
		return nil
	}
	return new(pct(m))
}

// Group builds category id's group from its items.
func Group(id string, items []model.ReadinessItem) model.ReadinessGroup {
	g := model.ReadinessGroup{ID: id, Title: titles[id], Weight: Weights[id], Items: items}
	if g.Items == nil {
		g.Items = []model.ReadinessItem{}
	}
	var t tally
	for _, it := range g.Items {
		t.add(it.Status)
	}
	g.Score, g.Untested = scorePtr(&t), t.todo
	return g
}

// statusRank orders statuses worst first.
var statusRank = map[model.ReadinessItemStatus]int{
	model.ItemFail: 0, model.ItemWarn: 1, model.ItemTodo: 2, model.ItemInfo: 3, model.ItemPass: 4, model.ItemSkip: 5,
}

// Worst is the worst of statuses (skip when empty).
func Worst(statuses ...model.ReadinessItemStatus) model.ReadinessItemStatus {
	out := model.ItemSkip
	for _, s := range statuses {
		if statusRank[s] < statusRank[out] {
			out = s
		}
	}
	return out
}

// finishComponent sets a component's status and score from its cells.
func finishComponent(c model.ReadinessComponent) model.ReadinessComponent {
	var t tally
	c.Status = model.ItemSkip
	for _, cell := range c.Cells {
		t.add(cell.Status)
		c.Status = Worst(c.Status, cell.Status)
	}
	c.Score = scorePtr(&t)
	return c
}

// Summarize sets an environment's score, confidence, status and summary
// from its groups (built with Group).
func Summarize(e model.EnvironmentReadiness) model.EnvironmentReadiness {
	var weighted, weights, verified, applicable float64
	var all tally
	var fail *model.ReadinessItem
	open := 0 // warnings and failed scenarios
	for gi := range e.Groups {
		g := &e.Groups[gi]
		var t tally
		for ii := range g.Items {
			it := &g.Items[ii]
			t.add(it.Status)
			all.add(it.Status)
			switch {
			case it.Status == model.ItemWarn, it.Status == model.ItemFail && g.ID == model.ReadinessResilience:
				open++
			case it.Status == model.ItemFail && fail == nil:
				fail = it
			}
		}
		w := float64(Weights[g.ID])
		if m, ok := t.mean(); ok {
			weighted += w * m
			weights += w
		}
		if n := t.scored + t.todo; n > 0 {
			verified += w * float64(t.scored) / float64(n)
			applicable += w
		}
	}
	e.Score, e.Confidence = 100, 100
	if weights > 0 {
		e.Score = pct(weighted / weights)
	}
	if applicable > 0 {
		e.Confidence = pct(verified / applicable)
	}
	untested := ""
	if all.todo > 0 {
		untested = fmt.Sprintf("%d not tested yet", all.todo)
	}
	switch {
	case fail != nil:
		e.Status, e.Summary = model.ReadinessBlocked, "Blocked: "+fail.Message
	case open > 0 || e.ProductionLike && all.todo > 0:
		e.Status = model.ReadinessNeedsWork
		parts := []string{}
		if open > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", open, plural(open, "check needs work", "checks need work")))
		}
		if untested != "" {
			parts = append(parts, untested)
		}
		e.Summary = joinSentence(parts) + "."
	case e.ProductionLike:
		e.Status, e.Summary = model.ReadinessReady, "Ready for production."
	case untested != "":
		e.Status, e.Summary = model.ReadinessReady, "Ready; "+untested+"."
	default:
		e.Status, e.Summary = model.ReadinessReady, "Ready; production-only checks run in production-like environments."
	}
	return e
}

func joinSentence(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return parts[0] + "; " + parts[1]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
