package loadtest

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// scalingProbe is a probe that also samples the replicas and
// HorizontalPodAutoscalers of the workloads in a run's scope (the targeted
// workload and every workload with an HPA): Kubernetes workflows only.
type scalingProbe interface {
	Scaling() []model.WorkloadScale
}

// Autoscaling timing. An HPA sees CPU through metrics-server (a 15 s
// resolution), re-evaluates every 15 s (kube-controller-manager
// --horizontal-pod-autoscaler-sync-period) and new pods need time to pull,
// start and pass readiness, so a scale-up takes from ~30 s to over a minute:
// https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
const (
	// minScalingRunSec: runs shorter than this end before an HPA can react.
	minScalingRunSec = 60
	// podStartGraceSec: a scale-up this close to the end of the run may not
	// have had time to start its pods.
	podStartGraceSec = 30
	// metricsLagSec: an HPA CPU reading reflects new pods only this long
	// after they became Ready.
	metricsLagSec = 30
	// degradedGraceSec: latency degraded for at most this many seconds
	// before new pods were Ready still counts as scaling in time.
	degradedGraceSec = 10
	// hpaTolerance: the HPA ignores usage within 10% of its target
	// (--horizontal-pod-autoscaler-tolerance), so only readings above
	// target×1.1 are pressure.
	hpaTolerance = 0.1
	// noMetricsShare: an HPA whose ScalingActive condition was False in at
	// least this share of the samples could not read its metrics.
	noMetricsShare = 0.5
)

const scalingNote = "Replicas come from the workloads' status once a second (desired = the replicas the HPA asked for, ready = pods passing their readiness probe). CPU utilization is the HPA's own reading: % of the pods' CPU requests from metrics-server, refreshed about every 15 s. An HPA needs 30–90 s to add Ready pods, so judge autoscaling with runs of a few minutes (the Autoscaling test preset)."

// scaleSample is the replicas and HPAs of the workloads at second T.
type scaleSample struct {
	T         float64
	Workloads []model.WorkloadScale
}

// scalingRun is what analyzeScaling judges.
type scalingRun struct {
	target  string // the targeted workload's label ("" when unknown)
	samples []scaleSample
	// degraded flags each second of load (index i = second i+1); its
	// length is the run's duration.
	degraded []degradation
	env      string
	// elsewhere lists the other environments whose objects autoscale the
	// workload kind/name (nil: unknown).
	elsewhere func(kind, name string) []string
}

// analyzeScaling builds the per-workload replica timelines of a Kubernetes
// run and decides whether autoscaling kept up; nil without samples.
func analyzeScaling(in scalingRun) *model.LoadScaling {
	type acc struct {
		ws   model.WorkloadScaling
		hpas []*model.HPAStatus
	}
	byID := map[string]*acc{}
	for _, s := range in.samples {
		t := int(math.Round(s.T))
		for _, w := range s.Workloads {
			a := byID[w.NodeID]
			if a == nil {
				a = &acc{ws: model.WorkloadScaling{NodeID: w.NodeID, Workload: w.Workload, Target: w.Workload == in.target, Timeline: []model.ScalingPoint{}}}
				byID[w.NodeID] = a
			}
			p := model.ScalingPoint{T: t, Desired: w.Desired, Ready: w.Ready}
			if w.HPA != nil && w.HPA.CPUUtilization != nil {
				v := *w.HPA.CPUUtilization
				p.CPUUtilization = &v
			}
			a.ws.Timeline = append(a.ws.Timeline, p)
			a.hpas = append(a.hpas, w.HPA)
		}
	}
	if len(byID) == 0 {
		return nil
	}
	ids := slices.SortedFunc(maps.Keys(byID), func(a, b string) int {
		x, y := byID[a].ws, byID[b].ws
		if x.Target != y.Target {
			if x.Target {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Workload, y.Workload)
	})
	out := &model.LoadScaling{Workloads: []model.WorkloadScaling{}, Note: scalingNote}
	for _, id := range ids {
		a := byID[id]
		ws := a.ws
		for _, p := range ws.Timeline {
			ws.PeakReplicas = max(ws.PeakReplicas, p.Ready)
			if p.CPUUtilization != nil && (ws.PeakCPU == nil || *p.CPUUtilization > *ws.PeakCPU) {
				v := *p.CPUUtilization
				ws.PeakCPU = &v
			}
		}
		var last *model.HPAStatus
		for _, h := range a.hpas {
			if h != nil {
				last = h
			}
		}
		if last == nil {
			judgeNoHPA(&ws, in)
		} else {
			policy := last.HPAPolicy
			ws.HPA = &policy
			judgeHPA(&ws, a.hpas, in)
		}
		out.Workloads = append(out.Workloads, ws)
	}
	out.Verdict = scalingHeadline(out.Workloads)
	return out
}

// scalingRank orders verdict kinds for the headline (most actionable
// first).
var scalingRank = map[string]int{
	model.ScalingMaxed: 0, model.ScalingLate: 1, model.ScalingNoMetrics: 2, model.ScalingNoHPA: 3,
	model.ScalingTooShort: 4, model.ScalingScaled: 5, model.ScalingNotNeeded: 6,
}

// scalingHeadline is the most actionable workload verdict, the targeted
// workload's on ties.
func scalingHeadline(ws []model.WorkloadScaling) string {
	best := -1
	for i, w := range ws {
		if best < 0 || scalingRank[w.Kind] < scalingRank[ws[best].Kind] || (scalingRank[w.Kind] == scalingRank[ws[best].Kind] && w.Target && !ws[best].Target) {
			best = i
		}
	}
	return ws[best].Message
}

// headlineKind is the kind of the workload whose message is the headline.
func headlineKind(s *model.LoadScaling) string {
	for _, w := range s.Workloads {
		if w.Message == s.Verdict {
			return w.Kind
		}
	}
	return ""
}

func judgeNoHPA(ws *model.WorkloadScaling, in scalingRun) {
	ws.Kind = model.ScalingNoHPA
	replicas := ws.Timeline[len(ws.Timeline)-1].Desired
	where := ""
	if in.env != "" {
		where = " in the " + in.env + " environment"
	}
	ws.Message = fmt.Sprintf("%s has no HorizontalPodAutoscaler%s, so it stayed at %s under load", ws.Workload, where, pods(replicas))
	var others []string
	if kind, _, name, ok := model.ParseKubeNodeID(ws.NodeID); ok && in.elsewhere != nil {
		others = in.elsewhere(kind, name)
	}
	switch len(others) {
	case 0:
		ws.Message += " — add an HPA (palette › HorizontalPodAutoscaler) and set resources.requests.cpu on its containers to test autoscaling"
	case 1:
		ws.Message += fmt.Sprintf(" — switch to the %s environment, which autoscales it, deploy and run again", others[0])
	default:
		ws.Message += fmt.Sprintf(" — switch to an environment that autoscales it (%s), deploy and run again", strings.Join(others, ", "))
	}
}

// judgeHPA decides the verdict of a workload with an HPA.
func judgeHPA(ws *model.WorkloadScaling, hpas []*model.HPAStatus, in scalingRun) {
	pts := ws.Timeline
	pol := *ws.HPA
	duration := len(in.degraded)
	first := pts[0]
	// degradedIn counts the degraded seconds t with from <= t < to.
	degradedIn := func(from, to int) (int, degradation) {
		n := 0
		var why degradation
		for t := max(from, 1); t < to && t <= duration; t++ {
			if d := in.degraded[t-1]; d.any() {
				n++
				why.latency = why.latency || d.latency
				why.errors = why.errors || d.errors
			}
		}
		return n, why
	}
	for t := 1; t <= duration; t++ {
		if in.degraded[t-1].any() {
			ws.DegradedAt = &t
			break
		}
	}
	over := func(p model.ScalingPoint) bool {
		return pol.TargetCPU > 0 && p.CPUUtilization != nil && float64(*p.CPUUtilization) > float64(pol.TargetCPU)*(1+hpaTolerance)
	}
	var scaleUp, readyAt, maxedAt, maxReadyAt *int
	for _, p := range pts {
		if scaleUp == nil && p.Desired > first.Desired {
			scaleUp = &p.T
		}
		if scaleUp != nil && readyAt == nil && p.Ready > first.Ready {
			readyAt = &p.T
		}
		if pol.Max > 0 && maxedAt == nil && p.Desired >= pol.Max {
			maxedAt = &p.T
		}
		if pol.Max > 0 && maxReadyAt == nil && p.Ready >= pol.Max {
			maxReadyAt = &p.T
		}
	}
	ws.ScaleUpAt, ws.ReadyAt = scaleUp, readyAt
	cpuPeak := ""
	if ws.PeakCPU != nil && pol.TargetCPU > 0 {
		cpuPeak = fmt.Sprintf("CPU peaked at %d%% of requests vs a %d%% target", *ws.PeakCPU, pol.TargetCPU)
	}

	// The HPA could not read its metrics.
	inactive, total := 0, 0
	var cond model.HPACondition
	for _, h := range hpas {
		if h == nil {
			continue
		}
		total++
		for _, c := range h.Conditions {
			if c.Type == "ScalingActive" && c.Status == "False" {
				inactive++
				cond = c
			}
		}
	}
	missingRequest := strings.Contains(cond.Message, "missing request")
	noMetrics := scaleUp == nil && total > 0 && float64(inactive) >= noMetricsShare*float64(total)
	if noMetrics && missingRequest {
		ws.Kind, ws.Message = model.ScalingNoMetrics, noMetricsMessage(ws.Workload, cond)
		return
	}
	if scaleUp == nil && first.Desired < pol.Max && duration < minScalingRunSec {
		ws.Kind = model.ScalingTooShort
		ws.Message = fmt.Sprintf("The run lasted %d s: too short to judge %s's autoscaling — an HPA needs about a minute to add Ready pods (15 s metrics, 15 s sync, pod startup); use the Autoscaling test preset", duration, ws.Workload)
		return
	}
	if noMetrics {
		ws.Kind, ws.Message = model.ScalingNoMetrics, noMetricsMessage(ws.Workload, cond)
		return
	}
	if scaleUp == nil && pol.TargetCPU > 0 && ws.PeakCPU == nil && duration >= minScalingRunSec {
		ws.Kind = model.ScalingNoMetrics
		ws.Message = fmt.Sprintf("The HPA of %s reported no CPU utilization during the %d s run, so it could not scale — check that metrics-server runs (kubectl top pods) and that every container sets resources.requests.cpu", ws.Workload, duration)
		return
	}

	// Maxed out: at maxReplicas and still saturated once the last pods
	// were Ready and their usage reached the HPA.
	if pol.Max > 0 && (maxedAt != nil || first.Desired >= pol.Max) {
		from := 0
		switch {
		case maxReadyAt != nil:
			from = *maxReadyAt
		case maxedAt != nil:
			from = *maxedAt
		}
		hot := 0
		for _, p := range pts {
			if p.T >= from+metricsLagSec && over(p) {
				hot = max(hot, *p.CPUUtilization)
			}
		}
		n, why := degradedIn(from, duration+1)
		if hot > 0 || n > degradedGraceSec {
			at := "from the start"
			if maxedAt != nil && *maxedAt > 0 {
				at = fmt.Sprintf("at %d s", *maxedAt)
			}
			var load []string
			if hot > 0 {
				load = append(load, fmt.Sprintf("CPU at %d%% of requests vs a %d%% target", hot, pol.TargetCPU))
			}
			if n > degradedGraceSec {
				load = append(load, fmt.Sprintf("%s for %d s", degradedWhat(why), n))
			}
			ws.Kind = model.ScalingMaxed
			ws.Message = fmt.Sprintf("%s reached maxReplicas (%d) %s and stayed saturated (%s) — raise maxReplicas or make each pod handle more load (optimize the hot path, raise its CPU request)", ws.Workload, pol.Max, at, strings.Join(load, "; "))
			return
		}
	}

	if scaleUp != nil {
		peak := 0
		for _, p := range pts {
			peak = max(peak, p.Desired)
		}
		if readyAt == nil {
			if duration-*scaleUp < podStartGraceSec {
				ws.Kind = model.ScalingTooShort
				ws.Message = fmt.Sprintf("The HPA asked for %s of %s at %d s, too close to the end of the %d s run to see them Ready — run longer (the Autoscaling test preset)", pods(peak), ws.Workload, *scaleUp, duration)
				return
			}
			n, why := degradedIn(1, duration+1)
			ws.DegradedFor = n
			ws.Kind = model.ScalingLate
			ws.Message = fmt.Sprintf("The HPA asked for %s of %s at %d s but no new pod was Ready by the end (%d s)", pods(peak), ws.Workload, *scaleUp, duration)
			if n > 0 {
				ws.Message += fmt.Sprintf(", with %s for %d s", degradedWhat(why), n)
			}
			ws.Message += " — check the new pods' startup (image pull, readiness probe, Pending pods without room on the node)"
			return
		}
		n, why := degradedIn(1, *readyAt)
		ws.DegradedFor = n
		added := 0
		for _, p := range pts {
			if p.T == *readyAt {
				added = p.Ready - first.Ready
			}
		}
		if n > degradedGraceSec {
			ws.Kind = model.ScalingLate
			ws.Message = fmt.Sprintf("%s for %d s before %s of %s %s Ready at %d s — lower the HPA's CPU target (%d%%) or raise minReplicas (%d) so capacity is there before the load", degradedWhat(why), n, newPods(added), ws.Workload, wasWere(added), *readyAt, pol.TargetCPU, pol.Min)
			return
		}
		ws.Kind = model.ScalingScaled
		ws.Message = fmt.Sprintf("%s scaled from %d to %s: the HPA asked for more at %d s and new pods were Ready at %d s", ws.Workload, first.Ready, pods(ws.PeakReplicas), *scaleUp, *readyAt)
		if n > 0 {
			ws.Message += fmt.Sprintf(", with %s for only %d s before", degradedWhat(why), n)
		} else {
			ws.Message += ", before latency degraded"
		}
		if cpuPeak != "" {
			ws.Message += "; " + cpuPeak
		}
		return
	}

	// No scale-up during a long enough run.
	hot := false
	for _, p := range pts {
		hot = hot || over(p)
	}
	n, why := degradedIn(1, duration+1)
	ws.DegradedFor = n
	switch {
	case hot:
		ws.Kind = model.ScalingLate
		ws.Message = fmt.Sprintf("The HPA kept %s at %s for the whole %d s run although %s", ws.Workload, pods(first.Desired), duration, cpuPeak)
		if n > 0 {
			ws.Message += fmt.Sprintf(", with %s for %d s", degradedWhat(why), n)
		}
		ws.Message += fmt.Sprintf(" — read its conditions (kubectl describe hpa %s) and lower its CPU target or raise minReplicas", pol.Name)
	case n > degradedGraceSec:
		ws.Kind = model.ScalingLate
		ws.Message = fmt.Sprintf("%s for %d s but %s's CPU stayed under the HPA target", degradedWhat(why), n, ws.Workload)
		if ws.PeakCPU != nil {
			ws.Message += fmt.Sprintf(" (peak %d%% of requests vs %d%%)", *ws.PeakCPU, pol.TargetCPU)
		}
		ws.Message += ", so it never scaled — the bottleneck is not CPU (connection pools, I/O, a dependency); raise minReplicas or scale on a metric that tracks it"
	default:
		ws.Kind = model.ScalingNotNeeded
		peak := "Usage stayed under the HPA's targets"
		if cpuPeak != "" {
			peak = cpuPeak
		}
		held := "latency held"
		if n > 0 {
			held = fmt.Sprintf("latency held (%s for only %d s)", degradedWhat(why), n)
		}
		ws.Message = fmt.Sprintf("%s and %s, so the HPA kept %s at %s — no scaling needed at this load; send more traffic to see it scale", peak, held, ws.Workload, pods(first.Desired))
	}
}

func noMetricsMessage(workload string, c model.HPACondition) string {
	why := strings.TrimSpace(c.Reason + ": " + c.Message)
	fix := "check that metrics-server runs (kubectl top pods) and that every container sets resources.requests.cpu"
	switch {
	case strings.Contains(c.Message, "missing request"):
		fix = "set resources.requests.cpu on every container of " + workload + ": the HPA measures usage as a share of the requests"
	case strings.Contains(c.Message, "metrics API") || strings.Contains(c.Message, "metrics.k8s.io"):
		fix = "metrics-server is not serving pod metrics: wait until kubectl top pods works or check the metrics-server pod in kube-system"
	}
	return fmt.Sprintf("The HPA of %s could not read its metrics (%s) — %s", workload, truncateText(why, 200), fix)
}

// degradedWhat names why seconds degraded ("p95 above 4× its baseline").
func degradedWhat(d degradation) string {
	switch {
	case d.latency && d.errors:
		return fmt.Sprintf("p95 above %d× its baseline or errors above %d%%", breakP95Factor, int(breakErrorRate*100))
	case d.errors:
		return fmt.Sprintf("errors above %d%%", int(breakErrorRate*100))
	}
	return fmt.Sprintf("p95 above %d× its baseline", breakP95Factor)
}

func pods(n int) string {
	if n == 1 {
		return "1 pod"
	}
	return fmt.Sprintf("%d pods", n)
}

func newPods(n int) string {
	if n == 1 {
		return "1 new pod"
	}
	return fmt.Sprintf("%d new pods", n)
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// foldScaling makes the autoscaling verdict the run's resource headline
// when it is the main finding: unless a container crashed, a late, maxed
// or blind HPA (or one that scaled) explains the run better than CPU.
func foldScaling(res *model.LoadResources) {
	if res == nil || res.Scaling == nil {
		return
	}
	for _, v := range res.Verdicts {
		if v.Kind == "oom" || v.Kind == "restarted" {
			return
		}
	}
	switch headlineKind(res.Scaling) {
	case model.ScalingLate, model.ScalingMaxed, model.ScalingNoMetrics, model.ScalingScaled:
		res.Verdict = res.Scaling.Verdict
	}
}
