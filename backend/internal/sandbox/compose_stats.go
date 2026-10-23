package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Live resource usage of docker workflows: while a project has running
// containers, one `docker stats --no-trunc <ids…>` process streams their
// usage (the CLI redraws every 500 ms; the daemon refreshes each container
// once a second). Every second the newest values are summed per service,
// kept for the last statsHistory seconds and published as compose.stats.
// `docker stats` takes explicit container IDs (it has no label filter), so
// only the project's own containers are ever sampled.

const (
	statsInterval = time.Second
	statsHistory  = 120
	// statsFresh is how old a container's last stats line may be to count.
	statsFresh = 5 * time.Second
)

// composePerf is the performance state of one docker workflow.
type composePerf struct {
	mu      sync.Mutex
	ids     []string // containers streamed (sorted)
	gen     int      // increments with every new stream
	cancel  context.CancelFunc
	info    map[string]containerInfo // running container ID -> service, limits
	latest  map[string]statsLine     // container ID -> newest stats line
	history map[string][]model.ServiceUsage

	healthyAt map[string]time.Time // container ID -> first "healthy" event since its start
	startup   map[string]startupMs // container ID -> start→healthy
}

type startupMs struct {
	started time.Time
	ms      int64
}

// containerInfo is what aggregation needs about a running container.
type containerInfo struct {
	service  string
	cpuLimit float64 // cores; 0 = none
	memLimit int64   // bytes; 0 = none
}

// statsLine is one container of a `docker stats --format '{{json .}}'`
// frame, parsed.
type statsLine struct {
	at         time.Time
	cpuPercent float64
	memBytes   int64
	memLimit   int64
	netRx      int64
	netTx      int64
	blockRead  int64
	blockWrite int64
	pids       int
}

// cpuLimit reads a container's CPU limit in cores (deploy.resources.limits
// .cpus / cpus: set NanoCpus; cpu_quota/cpu_period the CFS quota).
func (c inspected) cpuLimit() float64 {
	switch {
	case c.HostConfig.NanoCpus > 0:
		return float64(c.HostConfig.NanoCpus) / 1e9
	case c.HostConfig.CPUQuota > 0 && c.HostConfig.CPUPeriod > 0:
		return float64(c.HostConfig.CPUQuota) / float64(c.HostConfig.CPUPeriod)
	}
	return 0
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// parseStatsLine parses one output line of `docker stats --format
// '{{json .}}'` (ANSI cursor codes of the redraws stripped). ok is false for
// blank lines and for containers without data ("--").
func parseStatsLine(line string, at time.Time) (string, statsLine, bool) {
	line = strings.TrimSpace(ansiRe.ReplaceAllString(line, ""))
	if !strings.HasPrefix(line, "{") {
		return "", statsLine{}, false
	}
	var raw struct {
		ID       string `json:"ID"`
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
		NetIO    string `json:"NetIO"`
		BlockIO  string `json:"BlockIO"`
		PIDs     string `json:"PIDs"`
	}
	if json.Unmarshal([]byte(line), &raw) != nil || raw.ID == "" {
		return "", statsLine{}, false
	}
	cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(raw.CPUPerc), "%"), 64)
	if err != nil {
		return "", statsLine{}, false
	}
	s := statsLine{at: at, cpuPercent: cpu}
	s.memBytes, s.memLimit = parseSizePair(raw.MemUsage)
	s.netRx, s.netTx = parseSizePair(raw.NetIO)
	s.blockRead, s.blockWrite = parseSizePair(raw.BlockIO)
	s.pids, _ = strconv.Atoi(strings.TrimSpace(raw.PIDs))
	return raw.ID, s, true
}

// parseSizePair parses "12.5MiB / 1GiB".
func parseSizePair(s string) (int64, int64) {
	a, b, _ := strings.Cut(s, "/")
	return parseSize(a), parseSize(b)
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z]*)$`)

// sizeUnits covers both docker CLI styles: binary units for memory
// (KiB, MiB, GiB) and decimal ones for network and block I/O (kB, MB, GB).
var sizeUnits = map[string]float64{
	"": 1, "b": 1,
	"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50,
}

// parseSize parses a docker CLI size ("676KiB", "1.48kB", "0B"); 0 when
// unreadable ("--").
func parseSize(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	mult, ok := sizeUnits[strings.ToLower(m[2])]
	if err != nil || !ok {
		return 0
	}
	return int64(v*mult + 0.5)
}

// aggregateUsage sums the fresh stats lines of each service's running
// containers. Services without a fresh line are left out.
func aggregateUsage(info map[string]containerInfo, latest map[string]statsLine, host hostInfo, now time.Time) []model.ServiceUsage {
	type acc struct {
		u                    model.ServiceUsage
		cpuLimited, memLimit bool
		cpuSum               float64
		memSum               int64
		reportedMem          int64
	}
	by := map[string]*acc{}
	for _, id := range slices.Sorted(maps.Keys(info)) {
		ci := info[id]
		st, ok := latest[id]
		if !ok || now.Sub(st.at) > statsFresh {
			continue
		}
		a := by[ci.service]
		if a == nil {
			a = &acc{u: model.ServiceUsage{Service: ci.service, T: now.UTC()}, cpuLimited: true, memLimit: true}
			by[ci.service] = a
		}
		a.u.Containers++
		a.u.CPUPercent += st.cpuPercent
		a.u.MemBytes += st.memBytes
		a.u.NetRxBytes += st.netRx
		a.u.NetTxBytes += st.netTx
		a.u.BlockRead += st.blockRead
		a.u.BlockWrite += st.blockWrite
		a.u.PIDs += st.pids
		a.cpuSum += ci.cpuLimit
		a.memSum += ci.memLimit
		a.cpuLimited = a.cpuLimited && ci.cpuLimit > 0
		a.memLimit = a.memLimit && ci.memLimit > 0
		a.reportedMem = max(a.reportedMem, st.memLimit)
	}
	out := make([]model.ServiceUsage, 0, len(by))
	for _, name := range slices.Sorted(maps.Keys(by)) {
		a := by[name]
		u := a.u
		u.CPUPercent = round2(u.CPUPercent)
		u.CPULimited, u.MemLimited = a.cpuLimited, a.memLimit
		switch {
		case a.cpuLimited:
			u.CPULimit = round2(a.cpuSum)
		case host.cpus > 0:
			u.CPULimit = float64(host.cpus)
		}
		switch {
		case a.memLimit:
			u.MemLimit = a.memSum
		case host.memory > 0:
			u.MemLimit = host.memory
		default:
			u.MemLimit = a.reportedMem
		}
		out = append(out, u)
	}
	return out
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// hostInfo is the Docker host's capacity (`docker info`).
type hostInfo struct {
	cpus   int
	memory int64
}

var hostInfos = struct {
	sync.Mutex
	byBin map[string]hostInfo
}{byBin: map[string]hostInfo{}}

func (m *Manager) hostInfo(ctx context.Context) hostInfo {
	bin := m.dockerBin()
	hostInfos.Lock()
	h, ok := hostInfos.byBin[bin]
	hostInfos.Unlock()
	if ok {
		return h
	}
	out, err := m.docker(ctx, "info", "--format", "{{.NCPU}} {{.MemTotal}}")
	if err != nil {
		return hostInfo{}
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		h.cpus, _ = strconv.Atoi(f[0])
		h.memory, _ = strconv.ParseInt(f[1], 10, 64)
	}
	if h.cpus > 0 {
		hostInfos.Lock()
		hostInfos.byBin[bin] = h
		hostInfos.Unlock()
	}
	return h
}

// syncStats points the stats stream at the project's running containers:
// it restarts `docker stats` when the set changed and stops it when none
// runs. Limits are refreshed on every call.
func (m *Manager) syncStats(sb *sandbox, cs []inspected) {
	info := map[string]containerInfo{}
	for _, c := range cs {
		if c.State.Status == "running" && c.service() != "" {
			info[c.ID] = containerInfo{service: c.service(), cpuLimit: c.cpuLimit(), memLimit: c.HostConfig.Memory}
		}
	}
	ids := slices.Sorted(maps.Keys(info))
	p := &sb.perf
	p.mu.Lock()
	defer p.mu.Unlock()
	p.info = info
	if slices.Equal(ids, p.ids) && (p.cancel != nil || len(ids) == 0) {
		return
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.ids = ids
	for id := range p.latest {
		if _, ok := info[id]; !ok {
			delete(p.latest, id)
		}
	}
	if len(ids) == 0 {
		p.history = nil
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.gen++
	go m.streamStats(ctx, sb, ids, p.gen)
}

// stopStats ends the stats stream (workflow removed).
func (m *Manager) stopStats(sb *sandbox) {
	m.syncStats(sb, nil)
}

// streamStats runs one `docker stats` stream until ctx is canceled or the
// process ends, publishing one aggregated sample per second.
func (m *Manager) streamStats(ctx context.Context, sb *sandbox, ids []string, gen int) {
	p := &sb.perf
	defer func() {
		p.mu.Lock()
		if p.gen == gen && p.cancel != nil {
			// The process ended on its own (Docker stopped, a container
			// vanished): the next status refresh starts a new stream.
			p.cancel()
			p.cancel = nil
			p.ids = nil
		}
		p.mu.Unlock()
	}()
	host := m.hostInfo(ctx)
	cmd := exec.CommandContext(ctx, m.dockerBin(), append([]string{"stats", "--no-trunc", "--format", "{{json .}}"}, ids...)...)
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanLines(out, func(line string) {
			id, st, ok := parseStatsLine(line, time.Now())
			if !ok {
				return
			}
			p.mu.Lock()
			if p.latest == nil {
				p.latest = map[string]statsLine{}
			}
			p.latest[id] = st
			p.mu.Unlock()
		})
	}()
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-done:
			break loop
		case now := <-ticker.C:
			p.mu.Lock()
			usage := aggregateUsage(p.info, p.latest, host, now)
			if p.history == nil {
				p.history = map[string][]model.ServiceUsage{}
			}
			for _, u := range usage {
				h := append(p.history[u.Service], u)
				if len(h) > statsHistory {
					h = slices.Clone(h[len(h)-statsHistory:])
				}
				p.history[u.Service] = h
			}
			p.mu.Unlock()
			if len(usage) > 0 {
				m.publish(model.EventComposeStats, sb.id, model.ComposeStatsEvent{WorkflowID: sb.id, Services: usage})
			}
		}
	}
	if ctx.Err() == nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// usageNow aggregates the newest stats of the workflow's containers.
func (m *Manager) usageNow(sb *sandbox) []model.ServiceUsage {
	host := m.hostInfo(context.Background())
	p := &sb.perf
	p.mu.Lock()
	defer p.mu.Unlock()
	return aggregateUsage(p.info, p.latest, host, time.Now())
}

// ComposeStats returns the live usage of a docker workflow's services
// (services: those of the workflow's files) with the last two minutes of
// history. Reading it refreshes the status, which starts the stream when
// containers run.
func (m *Manager) ComposeStats(ctx context.Context, wfID string, services []ComposeService) (model.ComposeStats, error) {
	rt, err := m.ComposeInspect(ctx, wfID, services)
	if err != nil {
		return model.ComposeStats{}, err
	}
	host := m.hostInfo(ctx)
	sb := m.sandbox(wfID)
	out := model.ComposeStats{
		WorkflowID: wfID, HostCPUs: host.cpus, HostMemory: host.memory,
		IntervalMs: int(statsInterval / time.Millisecond), Services: []model.ServiceStatsSeries{},
		Message: rt.Status.Message, UpdatedAt: time.Now().UTC(),
	}
	current := map[string]model.ServiceUsage{}
	for _, u := range m.usageNow(sb) {
		current[u.Service] = u
	}
	p := &sb.perf
	p.mu.Lock()
	out.Streaming = p.cancel != nil
	for _, s := range rt.Status.Services {
		series := model.ServiceStatsSeries{Service: s.Name, NodeID: s.NodeID, History: slices.Clone(p.history[s.Name])}
		if series.History == nil {
			series.History = []model.ServiceUsage{}
		}
		if u, ok := current[s.Name]; ok {
			series.Current = &u
		}
		out.Services = append(out.Services, series)
	}
	p.mu.Unlock()
	return out, nil
}

// ---------------------------------------------------------------------------
// Startup times (container start → healthy)

// composeEvent is the subset of a `docker events --format '{{json .}}'` line
// used for startup times (Docker 29 drops the legacy top-level "id": the
// container ID is Actor.ID).
type composeEvent struct {
	ID    string `json:"id"`
	Actor struct {
		ID string `json:"ID"`
	} `json:"Actor"`
	Action   string `json:"Action"`
	TimeNano int64  `json:"timeNano"`
}

// noteEvent records start and first-healthy times of the project's
// containers from its event stream.
func (p *composePerf) noteEvent(line string) {
	var ev composeEvent
	if json.Unmarshal([]byte(line), &ev) != nil || ev.TimeNano == 0 {
		return
	}
	id := ev.Actor.ID
	if id == "" {
		id = ev.ID
	}
	if id == "" {
		return
	}
	at := time.Unix(0, ev.TimeNano).UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev.Action {
	case "start":
		delete(p.healthyAt, id)
		delete(p.startup, id)
	case "health_status: healthy":
		if p.healthyAt == nil {
			p.healthyAt = map[string]time.Time{}
		}
		if _, ok := p.healthyAt[id]; !ok {
			p.healthyAt[id] = at
		}
	}
}

// startupFromHealth derives start→healthy from the container's health log
// when no event was seen (e.g. the daemon restarted): only while the log
// still holds every check since the start (Docker keeps the last five).
func startupFromHealth(c inspected, started time.Time) (time.Time, bool) {
	h := c.State.Health
	if h == nil || h.Status != "healthy" || len(h.Log) == 0 || len(h.Log) >= 5 {
		return time.Time{}, false
	}
	for _, e := range h.Log {
		start, err1 := time.Parse(time.RFC3339Nano, e.Start)
		end, err2 := time.Parse(time.RFC3339Nano, e.End)
		if err1 != nil || err2 != nil || start.Before(started) {
			continue
		}
		if e.ExitCode == 0 {
			return end, true
		}
	}
	return time.Time{}, false
}

// annotateStartup sets the startup time of every healthy container (and the
// slowest per service) on st.
func (m *Manager) annotateStartup(sb *sandbox, st *model.ComposeStatus, cs []inspected) {
	byID := map[string]inspected{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	p := &sb.perf
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.startup == nil {
		p.startup = map[string]startupMs{}
	}
	for i := range st.Services {
		svc := &st.Services[i]
		for j := range svc.Containers {
			c := &svc.Containers[j]
			if c.StartedAt == nil {
				continue
			}
			started := *c.StartedAt
			if s, ok := p.startup[c.ID]; ok && s.started.Equal(started) {
				ms := s.ms
				c.StartupMs = &ms
			} else {
				healthy, ok := p.healthyAt[c.ID]
				if !ok || healthy.Before(started) {
					healthy, ok = startupFromHealth(byID[c.ID], started)
				}
				if !ok {
					continue
				}
				ms := healthy.Sub(started).Milliseconds()
				p.startup[c.ID] = startupMs{started: started, ms: ms}
				c.StartupMs = &ms
			}
			if svc.StartupMs == nil || *c.StartupMs > *svc.StartupMs {
				ms := *c.StartupMs
				svc.StartupMs = &ms
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Problems

// exitMeaning explains common container exit codes.
func exitMeaning(code int) string {
	switch code {
	case 1:
		return "application error"
	case 2:
		return "misuse of a shell builtin or invalid arguments"
	case 125:
		return "the container could not be run (docker error)"
	case 126:
		return "the command is not executable"
	case 127:
		return "the command was not found"
	case 130:
		return "interrupted (SIGINT)"
	case 134:
		return "aborted (SIGABRT)"
	case 137:
		return "killed (SIGKILL: out of memory, or it did not stop within the timeout)"
	case 139:
		return "segmentation fault (SIGSEGV)"
	case 143:
		return "terminated (SIGTERM)"
	}
	if code > 128 && code < 160 {
		return fmt.Sprintf("killed by signal %d", code-128)
	}
	return "error"
}

// composeProblems lists OOM kills, restarts, failed exits and unhealthy
// containers of st. cs supplies the last health check output.
func composeProblems(st model.ComposeStatus, cs []inspected) []model.ComposeProblem {
	healthOut := map[string]string{}
	for _, c := range cs {
		if h := c.State.Health; h != nil && len(h.Log) > 0 {
			healthOut[c.ID] = strings.TrimSpace(h.Log[len(h.Log)-1].Output)
		}
	}
	out := []model.ComposeProblem{}
	for _, svc := range st.Services {
		for _, c := range svc.Containers {
			base := model.ComposeProblem{Service: svc.Name, NodeID: svc.NodeID, Container: c.Name, ExitCode: c.ExitCode, Restarts: c.Restarts}
			if c.OOMKilled {
				p := base
				p.Kind, p.Severity = "oom", "error"
				if c.MemLimit > 0 {
					p.Message = fmt.Sprintf("%s was killed for exceeding its memory limit (%s)", c.Name, humanBytes(c.MemLimit))
					p.Fix = "Raise deploy.resources.limits.memory, or lower the app's memory use (heap size flags such as NODE_OPTIONS=--max-old-space-size or -Xmx, caches, leaks)."
				} else {
					p.Message = c.Name + " was killed by the kernel's OOM killer: the Docker host ran out of memory"
					p.Fix = "Give Docker more memory or set deploy.resources.limits.memory so one service cannot starve the others."
				}
				out = append(out, p)
			}
			if c.Restarts > 0 {
				p := base
				p.Kind, p.Severity = "restarts", "warning"
				if c.State == "restarting" && c.Restarts >= 3 {
					p.Severity = "error"
				}
				p.Message = fmt.Sprintf("%s restarted %d time(s)", c.Name, c.Restarts)
				if c.ExitCode != 0 {
					p.Message += fmt.Sprintf("; last exit code %d (%s)", c.ExitCode, exitMeaning(c.ExitCode))
				}
				p.Fix = "Read the service's logs around the restarts; a crash loop usually means a missing dependency, configuration or a failing startup command."
				out = append(out, p)
			}
			if (c.State == "exited" || c.State == "dead") && c.ExitCode != 0 && !c.OOMKilled {
				p := base
				p.Kind, p.Severity = "exit", "error"
				if c.ExitCode == 143 || c.ExitCode == 130 {
					p.Severity = "warning"
				}
				p.Message = fmt.Sprintf("%s exited with code %d: %s", c.Name, c.ExitCode, exitMeaning(c.ExitCode))
				p.Fix = "Check the service's logs for the error before the exit."
				out = append(out, p)
			}
			if c.State == "running" && c.Health == "unhealthy" {
				p := base
				p.Kind, p.Severity = "unhealthy", "error"
				p.Message = c.Name + " is unhealthy: its healthcheck keeps failing"
				if o := healthOut[c.ID]; o != "" {
					p.Message += ` (last output: "` + truncate(o, 160) + `")`
				}
				p.Fix = "Run the healthcheck command inside the container, and check the port/path it probes and its timeout."
				out = append(out, p)
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// humanBytes formats a size with binary units ("256 MiB").
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return s + " " + string("KMGTPE"[exp]) + "iB"
}

// ---------------------------------------------------------------------------
// Load test probes

// resourceProbe samples a docker workflow's containers during a load test.
type resourceProbe struct {
	m      *Manager
	sb     *sandbox
	target string
}

func (p *resourceProbe) Target() string               { return p.target }
func (p *resourceProbe) Sample() []model.ServiceUsage { return p.m.usageNow(p.sb) }

func (p *resourceProbe) Containers() map[string]model.ContainerFacts {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out := map[string]model.ContainerFacts{}
	cs, err := p.m.composeContainers(ctx, p.sb.id)
	if err != nil {
		return out
	}
	p.m.mu.Lock()
	services := p.sb.composeServices
	p.m.mu.Unlock()
	st := buildStatus(p.sb.id, services, cs, nil)
	p.m.annotateStartup(p.sb, &st, cs)
	for _, svc := range st.Services {
		f := model.ContainerFacts{StartupMs: svc.StartupMs}
		for _, c := range svc.Containers {
			f.Restarts += c.Restarts
			f.OOMKilled = f.OOMKilled || c.OOMKilled
		}
		out[svc.Name] = f
	}
	return out
}

// MonitorResources returns a probe of the workflow's containers for a load
// test against target, or nil when nothing runs: the pods of a Kubernetes
// workflow's cluster (kubeMonitor), else the compose containers. The
// compose target service is the one of target.NodeID ("service:<name>"),
// else the one publishing the HTTP target's port. It waits up to
// probeWarmup for the first stats sample.
func (m *Manager) MonitorResources(ctx context.Context, wfID string, target model.LoadTarget) model.ResourceProbe {
	if checkID(wfID) != nil {
		return nil
	}
	sb := m.sandbox(wfID)
	if p := m.kubeMonitor(ctx, sb, target); p != nil {
		return p
	}
	m.mu.Lock()
	services := sb.composeServices
	m.mu.Unlock()
	rt, err := m.ComposeInspect(ctx, wfID, services)
	if err != nil {
		return nil
	}
	running := false
	for _, s := range rt.Status.Services {
		for _, c := range s.Containers {
			running = running || c.State == "running"
		}
	}
	if !running {
		return nil
	}
	probe := &resourceProbe{m: m, sb: sb, target: targetService(rt.Status, target)}
	deadline := time.Now().Add(probeWarmup)
	for len(probe.Sample()) == 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return probe
		case <-time.After(200 * time.Millisecond):
		}
	}
	return probe
}

const probeWarmup = 4 * time.Second

// targetService finds the compose service a load target hits.
func targetService(st model.ComposeStatus, target model.LoadTarget) string {
	if name, ok := strings.CutPrefix(target.NodeID, "service:"); ok {
		for _, s := range st.Services {
			if s.Name == name {
				return name
			}
		}
	}
	if target.HTTP == nil {
		return ""
	}
	u, err := url.Parse(target.HTTP.URL)
	if err != nil {
		return ""
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 || !hostReachable(u.Hostname()) && u.Hostname() != "localhost" {
		return ""
	}
	for _, s := range st.Services {
		for _, c := range s.Containers {
			for _, p := range c.Ports {
				if p.Host == port && p.Protocol == "tcp" {
					return s.Name
				}
			}
		}
	}
	return ""
}
