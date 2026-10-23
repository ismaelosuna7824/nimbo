package mcp

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type dockerMetricsIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"docker workflow ID or exact name; omit or pass \"current\" for the workflow open in the Nimbo app"`
	Service  string `json:"service,omitempty" jsonschema:"only this compose service"`
}

type metricsServiceView struct {
	Service    string  `json:"service"`
	State      string  `json:"state"`
	Health     string  `json:"health,omitempty"`
	StartupMs  *int64  `json:"startup_ms,omitempty"`
	Containers int     `json:"containers"`
	CPU        string  `json:"cpu,omitempty"` // "23% of 0.25 CPU"
	CPUPercent float64 `json:"cpu_percent"`   // docker stats: 100 = one core
	CPU60s     string  `json:"cpu_last_60s,omitempty"`
	Memory     string  `json:"memory,omitempty"` // "118 MiB / 256 MiB (46%)"
	NetIO      string  `json:"net_io,omitempty"`
	BlockIO    string  `json:"block_io,omitempty"`
	PIDs       int     `json:"pids,omitempty"`
}

type metricsBuildView struct {
	Service       string               `json:"service"`
	RunID         string               `json:"run_id"`
	At            string               `json:"at"`
	BuildTime     string               `json:"build_time"`
	Cache         string               `json:"cache"` // "7/9 steps cached"
	Image         string               `json:"image,omitempty"`
	Size          string               `json:"size,omitempty"`
	BaseImage     string               `json:"base_image,omitempty"`
	BaseSize      string               `json:"base_size,omitempty"`
	OwnSize       string               `json:"own_size,omitempty"`
	BiggestLayers []string             `json:"biggest_layers,omitempty"`
	SlowestSteps  []string             `json:"slowest_steps,omitempty"`
	Delta         string               `json:"delta_vs_previous,omitempty"`
	Failed        bool                 `json:"failed,omitempty"`
	Findings      []model.BuildFinding `json:"findings,omitempty"`
}

type metricsLoadView struct {
	ID        string   `json:"id"`
	Target    string   `json:"target"`
	Status    string   `json:"status"`
	StartedAt string   `json:"started_at"`
	RPS       float64  `json:"rps,omitempty"`
	P95Ms     float64  `json:"p95_ms,omitempty"`
	ErrorRate float64  `json:"error_rate,omitempty"`
	Verdict   string   `json:"verdict"`
	Services  []string `json:"services"`
}

type dockerMetricsView struct {
	Workflow     string                 `json:"workflow"`
	Project      string                 `json:"project"`
	State        string                 `json:"state"`
	Message      string                 `json:"message,omitempty"`
	HostCPUs     int                    `json:"host_cpus,omitempty"`
	Services     []metricsServiceView   `json:"services"`
	Problems     []model.ComposeProblem `json:"problems,omitempty"`
	Builds       []metricsBuildView     `json:"builds,omitempty"`
	LastLoadTest *metricsLoadView       `json:"last_load_test,omitempty"`
	Hint         string                 `json:"hint,omitempty"`
}

// getDockerMetrics gathers a docker workflow's performance data: live
// container usage, problems, the latest build of every service and the
// bottleneck analysis of the latest load test against it.
func (t *tools) getDockerMetrics(ctx context.Context, in dockerMetricsIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	base := "/workflows/" + pathEscape(wf.ID)
	var st model.ComposeStatus
	if err := t.c.get(ctx, base+"/compose/status", &st); err != nil {
		return nil, err
	}
	if in.Service != "" {
		found := false
		for _, s := range st.Services {
			found = found || s.Name == in.Service
		}
		if !found {
			return nil, fmt.Errorf("service %q is not in %s", in.Service, wf.Name)
		}
	}
	keep := func(name string) bool { return in.Service == "" || name == in.Service }
	var stats model.ComposeStats
	if err := t.c.get(ctx, base+"/compose/stats", &stats); err != nil {
		return nil, err
	}
	var builds model.ComposeBuilds
	if err := t.c.get(ctx, base+"/compose/builds?limit=20", &builds); err != nil {
		return nil, err
	}
	var runs []model.LoadTestRun
	if err := t.c.get(ctx, base+"/loadtests", &runs); err != nil {
		return nil, err
	}

	v := dockerMetricsView{
		Workflow: wf.Name, Project: st.Project, State: string(st.State), Message: st.Message,
		HostCPUs: stats.HostCPUs, Services: []metricsServiceView{},
	}
	series := map[string]model.ServiceStatsSeries{}
	for _, s := range stats.Services {
		series[s.Service] = s
	}
	for _, s := range st.Services {
		if !keep(s.Name) {
			continue
		}
		sv := metricsServiceView{Service: s.Name, State: string(s.State), Health: s.Health, StartupMs: s.StartupMs}
		if cur := series[s.Name].Current; cur != nil {
			sv.Containers = cur.Containers
			sv.CPUPercent = cur.CPUPercent
			sv.CPU = usageCPU(*cur)
			sv.Memory = fmt.Sprintf("%s / %s (%d%%)", binBytes(cur.MemBytes), binBytes(cur.MemLimit), int(math.Round(cur.MemOfLimit()*100)))
			if !cur.MemLimited {
				sv.Memory += " — no limit"
			}
			sv.NetIO = fmt.Sprintf("rx %s / tx %s", decBytes(cur.NetRxBytes), decBytes(cur.NetTxBytes))
			sv.BlockIO = fmt.Sprintf("read %s / write %s", decBytes(cur.BlockRead), decBytes(cur.BlockWrite))
			sv.PIDs = cur.PIDs
		}
		if h := series[s.Name].History; len(h) > 0 {
			from := max(0, len(h)-60)
			var sum, peak float64
			for _, u := range h[from:] {
				sum += u.CPUOfLimit()
				peak = math.Max(peak, u.CPUOfLimit())
			}
			sv.CPU60s = fmt.Sprintf("avg %d%%, max %d%% of limit", int(math.Round(sum/float64(len(h)-from)*100)), int(math.Round(peak*100)))
		}
		v.Services = append(v.Services, sv)
	}
	for _, p := range st.Problems {
		if keep(p.Service) {
			v.Problems = append(v.Problems, p)
		}
	}
	seen := map[string]bool{}
	for _, b := range builds.Builds {
		for _, s := range b.Services {
			if seen[s.Service] || !keep(s.Service) {
				continue
			}
			seen[s.Service] = true
			v.Builds = append(v.Builds, viewServiceBuild(b, s))
		}
	}
	for _, r := range runs {
		if r.Resources == nil {
			continue
		}
		lv := &metricsLoadView{ID: r.ID, Target: r.Spec.Target.Label, Status: string(r.Status), StartedAt: r.StartedAt.Format("2006-01-02 15:04:05Z07:00"), Verdict: r.Resources.Verdict, Services: []string{}}
		if r.Summary != nil {
			lv.RPS, lv.P95Ms, lv.ErrorRate = r.Summary.RPS, r.Summary.Latency.P95, r.Summary.ErrorRate
		}
		for _, vd := range r.Resources.Verdicts {
			if keep(vd.Service) {
				lv.Services = append(lv.Services, vd.Message)
			}
		}
		v.LastLoadTest = lv
		break
	}
	switch {
	case len(v.Builds) == 0 && v.State == string(model.ComposeAbsent):
		v.Hint = "nothing recorded yet: run the project (up) to record build metrics and live usage"
	case v.LastLoadTest == nil:
		v.Hint = "run_load_test against a service of this workflow to get a bottleneck verdict"
	}
	return v, nil
}

func viewServiceBuild(b model.ComposeBuild, s model.ServiceBuildStats) metricsBuildView {
	bv := metricsBuildView{
		Service: s.Service, RunID: b.RunID, At: b.At.Format("2006-01-02 15:04:05Z07:00"),
		BuildTime: seconds(s.DurationMs), Cache: fmt.Sprintf("%d/%d steps cached", s.CacheHits, s.CacheHits+s.CacheMisses),
		Image: s.Image, Failed: s.Failed, Findings: s.Findings,
	}
	if s.SizeBytes > 0 {
		bv.Size = decBytes(s.SizeBytes)
		if s.BaseImage != "" {
			bv.BaseImage, bv.BaseSize, bv.OwnSize = s.BaseImage, decBytes(s.BaseSizeBytes), decBytes(s.SizeBytes-s.BaseSizeBytes)
		}
	}
	for _, l := range s.Layers {
		tag := ""
		if l.Base {
			tag = " (base)"
		}
		bv.BiggestLayers = append(bv.BiggestLayers, fmt.Sprintf("%s %s%s", decBytes(l.SizeBytes), truncateText(l.CreatedBy, 90), tag))
		if len(bv.BiggestLayers) == 3 {
			break
		}
	}
	steps := slices.SortedStableFunc(slices.Values(s.Steps), func(a, b model.BuildStep) int { return int(b.DurationMs - a.DurationMs) })
	for _, st := range steps {
		if st.Internal || st.Cached || st.DurationMs < 500 {
			continue
		}
		bv.SlowestSteps = append(bv.SlowestSteps, fmt.Sprintf("%s %s", seconds(st.DurationMs), truncateText(st.Instruction, 90)))
		if len(bv.SlowestSteps) == 3 {
			break
		}
	}
	if d := s.Delta; d != nil {
		bv.Delta = fmt.Sprintf("time %s, size %s, cached steps %+d", signedSeconds(d.DurationMs), signedBytes(d.SizeBytes), d.CacheHits)
	}
	return bv
}

// usageCPU formats CPU use against the service's limit.
func usageCPU(u model.ServiceUsage) string {
	limit := strconv.FormatFloat(u.CPULimit, 'f', -1, 64) + " CPU"
	if u.CPULimit > 1 {
		limit += "s"
	}
	if !u.CPULimited {
		limit = "the host's " + limit + " (no limit)"
	}
	return fmt.Sprintf("%d%% of %s", int(math.Round(u.CPUOfLimit()*100)), limit)
}

func seconds(ms int64) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + " s"
}

func signedSeconds(ms int64) string {
	return fmt.Sprintf("%+.1f s", float64(ms)/1000)
}

func signedBytes(n int64) string {
	if n < 0 {
		return "-" + decBytes(-n)
	}
	return "+" + decBytes(n)
}

func decBytes(n int64) string {
	switch {
	case n >= 1e9:
		return strconv.FormatFloat(float64(n)/1e9, 'f', 2, 64) + " GB"
	case n >= 1e6:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + " MB"
	case n >= 1e3:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + " kB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

func binBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64), ".0") + " " + string("KMGTPE"[exp]) + "iB"
}

func truncateText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
