package sandbox

import (
	"context"
	"maps"
	"net/url"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Live usage of a Kubernetes workflow's pods comes from their cgroups,
// read once a second inside the node by one long-lived `docker exec`: the
// same counters kubelet and metrics-server report, but metrics-server only
// refreshes every 15 s (kubelet's resource metrics every ~10-15 s), too
// coarse for a load test's per-second timeline. CPU is the usage_usec
// delta (100% = one core), memory memory.current minus inactive_file (the
// working set kubectl top shows). metrics-server stays installed for HPAs.

// kubeSamplerScript prints "@ <uptime seconds>" then one line per pod
// cgroup ("<dir> usage_usec <n> <memory.current> inactive_file <n>") every
// second, for both the cgroupfs and the systemd cgroup layouts.
const kubeSamplerScript = `while :; do
echo "@ $(cut -d' ' -f1 /proc/uptime)"
for d in /sys/fs/cgroup/kubepods/pod* /sys/fs/cgroup/kubepods/*/pod* /sys/fs/cgroup/kubepods.slice/kubepods-*pod*.slice /sys/fs/cgroup/kubepods.slice/*/kubepods-*pod*.slice; do
[ -d "$d" ] || continue
echo "$d $(head -n1 "$d/cpu.stat") $(cat "$d/memory.current") $(grep -w inactive_file "$d/memory.stat")"
done
sleep 1
done`

// podUID extracts the pod UID from a pod cgroup directory
// (".../pod<uid>" or ".../kubepods-burstable-pod<uid_with_underscores>.slice").
func podUID(dir string) string {
	base := path.Base(dir)
	i := strings.LastIndex(base, "pod")
	if i < 0 {
		return ""
	}
	uid := strings.TrimSuffix(base[i+3:], ".slice")
	return strings.ReplaceAll(uid, "_", "-")
}

// cgroupSample is one pod's counters at one frame.
type cgroupSample struct {
	usec    int64
	mem     int64
	inFrame float64 // uptime seconds of the frame
}

// cgroupParser turns the sampler's output into per-pod stats lines.
type cgroupParser struct {
	frame float64
	prev  map[string]cgroupSample
}

// line parses one output line; for a pod line seen in two frames it
// returns the pod UID and its usage since the previous frame.
func (p *cgroupParser) line(s string, now time.Time) (string, statsLine, bool) {
	f := strings.Fields(s)
	if len(f) == 2 && f[0] == "@" {
		p.frame, _ = strconv.ParseFloat(f[1], 64)
		return "", statsLine{}, false
	}
	if len(f) != 6 || f[1] != "usage_usec" || f[4] != "inactive_file" {
		return "", statsLine{}, false
	}
	uid := podUID(f[0])
	usec, err1 := strconv.ParseInt(f[2], 10, 64)
	cur, err2 := strconv.ParseInt(f[3], 10, 64)
	inactive, err3 := strconv.ParseInt(f[5], 10, 64)
	if uid == "" || err1 != nil || err2 != nil || err3 != nil {
		return "", statsLine{}, false
	}
	if p.prev == nil {
		p.prev = map[string]cgroupSample{}
	}
	cs := cgroupSample{usec: usec, mem: max(cur-inactive, 0), inFrame: p.frame}
	prev, ok := p.prev[uid]
	p.prev[uid] = cs
	if !ok || cs.inFrame <= prev.inFrame || usec < prev.usec {
		return "", statsLine{}, false
	}
	cpu := float64(usec-prev.usec) / ((cs.inFrame - prev.inFrame) * 1e6) * 100
	return uid, statsLine{at: now, cpuPercent: cpu, memBytes: cs.mem}, true
}

// syncKubeStats points the sampler at the running pods (UID -> workload
// and limits): it starts while pods run and stops when none does.
func (m *Manager) syncKubeStats(sb *sandbox, pods map[string]containerInfo) {
	k := &sb.kube
	k.mu.Lock()
	node := k.node.Name
	k.mu.Unlock()
	p := &k.perf
	p.mu.Lock()
	defer p.mu.Unlock()
	p.info = pods
	for uid := range p.latest {
		if _, ok := pods[uid]; !ok {
			delete(p.latest, uid)
		}
	}
	if len(pods) == 0 {
		if p.cancel != nil {
			p.cancel()
			p.cancel = nil
		}
		p.history = nil
		return
	}
	if p.cancel != nil || node == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.gen++
	go m.sampleKube(ctx, sb, node, p.gen)
}

// sampleKube runs the cgroup sampler until ctx ends or the process exits,
// publishing one aggregated sample per second.
func (m *Manager) sampleKube(ctx context.Context, sb *sandbox, node string, gen int) {
	p := &sb.kube.perf
	defer func() {
		p.mu.Lock()
		if p.gen == gen && p.cancel != nil {
			p.cancel()
			p.cancel = nil // the next status refresh starts it again
		}
		p.mu.Unlock()
	}()
	host := m.hostInfo(ctx)
	cmd := exec.CommandContext(ctx, m.dockerBin(), "exec", node, "sh", "-c", kubeSamplerScript)
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var parser cgroupParser
		scanLines(out, func(line string) {
			uid, st, ok := parser.line(line, time.Now())
			if !ok {
				return
			}
			p.mu.Lock()
			if p.latest == nil {
				p.latest = map[string]statsLine{}
			}
			p.latest[uid] = st
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
				m.publish(model.EventKubeStats, sb.id, model.ComposeStatsEvent{WorkflowID: sb.id, Services: usage})
			}
		}
	}
	if ctx.Err() == nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// kubeUsageNow aggregates the newest samples per workload node ID.
func (m *Manager) kubeUsageNow(sb *sandbox) []model.ServiceUsage {
	host := m.hostInfo(context.Background())
	p := &sb.kube.perf
	p.mu.Lock()
	defer p.mu.Unlock()
	return aggregateUsage(p.info, p.latest, host, time.Now())
}

// KubeStats returns the live usage of a Kubernetes workflow's workloads
// (Service and NodeID are the workload node ID) with the last two minutes
// of history. Reading it refreshes the status, which starts the sampler
// when pods run.
func (m *Manager) KubeStats(ctx context.Context, wfID string) (model.ComposeStats, error) {
	st, err := m.KubeStatus(ctx, wfID)
	if err != nil {
		return model.ComposeStats{}, err
	}
	host := m.hostInfo(ctx)
	sb := m.sandbox(wfID)
	out := model.ComposeStats{
		WorkflowID: wfID, HostCPUs: host.cpus, HostMemory: host.memory,
		IntervalMs: int(statsInterval / time.Millisecond), Services: []model.ServiceStatsSeries{},
		Message: st.Cluster.Message, UpdatedAt: time.Now().UTC(),
	}
	current := map[string]model.ServiceUsage{}
	for _, u := range m.kubeUsageNow(sb) {
		current[u.Service] = u
	}
	p := &sb.kube.perf
	p.mu.Lock()
	out.Streaming = p.cancel != nil
	for _, w := range st.Workloads {
		series := model.ServiceStatsSeries{Service: w.NodeID, NodeID: w.NodeID, History: slices.Clone(p.history[w.NodeID])}
		if series.History == nil {
			series.History = []model.ServiceUsage{}
		}
		if u, ok := current[w.NodeID]; ok {
			series.Current = &u
		}
		out.Services = append(out.Services, series)
	}
	p.mu.Unlock()
	return out, nil
}

// ---------------------------------------------------------------------------
// Load test probe

// kubeProbe samples a Kubernetes workflow's workloads during a load test.
// Services are labelled with the workload name (namespace/name, or the
// node ID, when names repeat) so verdicts read naturally; NodeID maps a
// label back.
type kubeProbe struct {
	m      *Manager
	sb     *sandbox
	target string            // label
	labels map[string]string // node ID -> label
	ids    map[string]string // label -> node ID
}

const kubeResourcesNote = "CPU and memory come from the pods' cgroups inside the local cluster, sampled once a second at the same instants as the latency timeline (100% CPU = one core; replicas summed; limits are the pods' resources.limits). Peaks are 2-second averages so a single spike does not count as saturation."

func (p *kubeProbe) Target() string { return p.target }
func (p *kubeProbe) Note() string   { return kubeResourcesNote }

func (p *kubeProbe) NodeID(service string) string {
	if id, ok := p.ids[service]; ok {
		return id
	}
	return service
}

func (p *kubeProbe) label(nodeID string) string {
	if l, ok := p.labels[nodeID]; ok {
		return l
	}
	return nodeID
}

func (p *kubeProbe) Sample() []model.ServiceUsage {
	us := p.m.kubeUsageNow(p.sb)
	for i := range us {
		us[i].Service = p.label(us[i].Service)
	}
	return us
}

func (p *kubeProbe) Containers() map[string]model.ContainerFacts {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out := map[string]model.ContainerFacts{}
	st, err := p.m.KubeStatus(ctx, p.sb.id)
	if err != nil {
		return out
	}
	for _, w := range st.Workloads {
		f := model.ContainerFacts{StartupMs: w.StartupMs}
		for _, pod := range w.Pods {
			f.Restarts += pod.Restarts
			for _, c := range pod.Containers {
				f.OOMKilled = f.OOMKilled || c.OOMKilled
			}
		}
		out[p.label(w.NodeID)] = f
	}
	return out
}

// Scaling samples the replicas of the targeted workload and of every
// workload an HPA scales, with the HPA's bounds, CPU target, current CPU
// utilization and conditions (from the informers' caches).
func (p *kubeProbe) Scaling() []model.WorkloadScale {
	k := &p.sb.kube
	k.mu.Lock()
	st, l := k.last, k.listers
	k.mu.Unlock()
	if st == nil {
		return []model.WorkloadScale{}
	}
	byWorkload := map[string]*autoscalingv2.HorizontalPodAutoscaler{}
	if l != nil {
		for _, h := range l.hpas() {
			ref := h.Spec.ScaleTargetRef
			byWorkload[model.KubeNodeID(ref.Kind, h.Namespace, ref.Name)] = h
		}
	}
	out := []model.WorkloadScale{}
	for _, w := range st.Workloads {
		h := byWorkload[w.NodeID]
		label := p.label(w.NodeID)
		if h == nil && (p.target == "" || label != p.target) {
			continue
		}
		ws := model.WorkloadScale{NodeID: w.NodeID, Workload: label, Desired: w.Desired, Ready: w.Ready}
		if h != nil {
			ws.HPA = hpaStatus(h)
		}
		out = append(out, ws)
	}
	return out
}

// hpaStatus reads an autoscaling/v2 HPA's bounds, CPU target and status.
func hpaStatus(h *autoscalingv2.HorizontalPodAutoscaler) *model.HPAStatus {
	s := &model.HPAStatus{
		HPAPolicy:       model.HPAPolicy{Name: h.Name, Min: int(ptrOr(h.Spec.MinReplicas, 1)), Max: int(h.Spec.MaxReplicas)},
		DesiredReplicas: int(h.Status.DesiredReplicas),
	}
	for _, m := range h.Spec.Metrics {
		if m.Type == autoscalingv2.ResourceMetricSourceType && m.Resource != nil && m.Resource.Name == corev1.ResourceCPU && m.Resource.Target.AverageUtilization != nil {
			s.TargetCPU = int(*m.Resource.Target.AverageUtilization)
		}
	}
	for _, m := range h.Status.CurrentMetrics {
		if m.Type == autoscalingv2.ResourceMetricSourceType && m.Resource != nil && m.Resource.Name == corev1.ResourceCPU && m.Resource.Current.AverageUtilization != nil {
			v := int(*m.Resource.Current.AverageUtilization)
			s.CPUUtilization = &v
		}
	}
	for _, c := range h.Status.Conditions {
		s.Conditions = append(s.Conditions, model.HPACondition{Type: string(c.Type), Status: string(c.Status), Reason: c.Reason, Message: c.Message})
	}
	return s
}

// workloadLabels names workloads by name, else namespace/name, else node
// ID, whichever is unique.
func workloadLabels(ws []model.KubeWorkloadStatus) map[string]string {
	count := func(f func(model.KubeWorkloadStatus) string) map[string]int {
		c := map[string]int{}
		for _, w := range ws {
			c[f(w)]++
		}
		return c
	}
	byName := count(func(w model.KubeWorkloadStatus) string { return w.Name })
	byNS := count(func(w model.KubeWorkloadStatus) string { return w.Namespace + "/" + w.Name })
	out := map[string]string{}
	for _, w := range ws {
		switch {
		case byName[w.Name] == 1:
			out[w.NodeID] = w.Name
		case byNS[w.Namespace+"/"+w.Name] == 1:
			out[w.NodeID] = w.Namespace + "/" + w.Name
		default:
			out[w.NodeID] = w.NodeID
		}
	}
	return out
}

// kubeMonitor returns a probe of a Kubernetes workflow's pods for a load
// test against target, or nil when the workflow has no running cluster
// with running pods.
func (m *Manager) kubeMonitor(ctx context.Context, sb *sandbox, target model.LoadTarget) model.ResourceProbe {
	node, err := m.inspectKubeNode(ctx, sb.id)
	if err != nil || !node.running() {
		return nil
	}
	st, err := m.KubeStatus(ctx, sb.id)
	if err != nil || st.Cluster.State != model.ClusterRunning {
		return nil
	}
	sb.kube.mu.Lock()
	running := len(sb.kube.pods) > 0
	links := maps.Clone(sb.kube.links)
	sb.kube.mu.Unlock()
	if !running {
		return nil
	}
	labels := workloadLabels(st.Workloads)
	probe := &kubeProbe{m: m, sb: sb, labels: labels, ids: map[string]string{}}
	for id, l := range labels {
		probe.ids[l] = id
	}
	if w := kubeTargetWorkload(st, links, target); w != "" {
		probe.target = probe.label(w)
	}
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

// kubeTargetWorkload finds the workload a load target hits: by node ID
// (workload, Service, Ingress), else by the forward or ingress port of its
// URL.
func kubeTargetWorkload(st model.KubeStatus, links map[string][]string, target model.LoadTarget) string {
	first := func(svc string) string {
		if ws := links[svc]; len(ws) > 0 {
			return ws[0]
		}
		return ""
	}
	for _, w := range st.Workloads {
		if w.NodeID == target.NodeID {
			return w.NodeID
		}
	}
	if w := first(target.NodeID); w != "" {
		return w
	}
	var u *url.URL
	if target.HTTP != nil {
		u, _ = url.Parse(target.HTTP.URL)
	}
	for _, ing := range st.Ingresses {
		if ing.NodeID == target.NodeID && (u == nil || strings.HasPrefix(u.Path, ing.Path)) {
			if w := first(ing.ServiceNodeID); w != "" {
				return w
			}
		}
	}
	if u == nil {
		return ""
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		return ""
	}
	for _, f := range st.Forwards {
		if f.HostPort == port {
			return first(f.NodeID)
		}
	}
	if port == st.Cluster.IngressPort {
		host := u.Hostname()
		for k, v := range target.HTTP.Headers {
			if strings.EqualFold(k, "Host") {
				host = v
			}
		}
		for _, ing := range st.Ingresses {
			if (ing.Host == "" || ing.Host == host) && strings.HasPrefix(u.Path, ing.Path) {
				if w := first(ing.ServiceNodeID); w != "" {
					return w
				}
			}
		}
	}
	return ""
}
