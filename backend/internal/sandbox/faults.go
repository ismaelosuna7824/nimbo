package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

// Incident scenarios (load test faults) act only on what Nimbo created for
// the workflow: the containers of its compose project (both project and
// workflow labels match) or the workloads of its own k3s cluster (node
// container labelled for the workflow, user namespaces only). Every fault
// writes a revert recipe to <home>/faults/<id>.json BEFORE acting and
// deletes it once reverted; RecoverFaults replays leftovers (a daemon that
// crashed mid-scenario) at startup.
//
// Tools that the targets' images lack (tc, nsenter, kill from the host PID
// namespace) run in short-lived helper containers of ChaosToolsImage, built
// locally once from a digest-pinned Alpine with a pinned iproute2-tc.

// ChaosToolsImage is the helper image of fault injection.
const ChaosToolsImage = "nimbo-chaos-tools:alpine3.20.3-iproute2-6.9.0"

const chaosToolsDockerfile = "FROM alpine:3.20.3@sha256:1e42bbe2508154c9126d48c2b8a75420c3544343bf86fd041fb7527e017a4b4a\n" +
	"RUN apk add --no-cache iproute2-tc=6.9.0-r0\n" +
	"LABEL " + kubeRoleLabel + "=chaos-tools\n"

// roleChaos labels the helper containers.
const roleChaos = "chaos"

// Revert actions of a pending fault.
const (
	revertStart     = "start"      // docker: start containers that are not running
	revertUnpause   = "unpause"    // docker: unpause containers
	revertNetem     = "netem"      // docker: remove netem qdiscs in the containers' netns
	revertScale     = "scale"      // kubernetes: scale the workload back
	revertKubeNetem = "kube-netem" // kubernetes: remove netem qdiscs in the workload's pods
)

// faultWait bounds waiting for replicas to come back when reverting.
const faultWait = 2 * time.Minute

// pendingFault is the recipe that reverts an injected fault.
type pendingFault struct {
	ID         string    `json:"id"`
	Workflow   string    `json:"workflow"`
	Scenario   string    `json:"scenario"`
	Action     string    `json:"action"`
	Containers []string  `json:"containers,omitempty"` // docker container IDs
	Node       string    `json:"node,omitempty"`       // k3s node container
	Namespace  string    `json:"namespace,omitempty"`
	Workload   string    `json:"workload,omitempty"` // "deployment/web"
	Selector   string    `json:"selector,omitempty"` // pod label selector "app=web"
	Replicas   int       `json:"replicas,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// faultOps is how faults reach the runtime (the docker CLI; faked in
// tests).
type faultOps struct {
	docker func(ctx context.Context, args ...string) (string, error)
	// tools ensures ChaosToolsImage exists.
	tools func(ctx context.Context) error
	dir   string // pending recipes
	poll  time.Duration
	wait  time.Duration // replicas coming back on revert
}

func (m *Manager) faultOps() *faultOps {
	return &faultOps{docker: m.docker, tools: m.ensureChaosTools, dir: filepath.Join(m.cfg.Home, "faults"), poll: 500 * time.Millisecond, wait: faultWait}
}

var chaosToolsMu sync.Mutex

// ensureChaosTools builds ChaosToolsImage when missing.
func (m *Manager) ensureChaosTools(ctx context.Context) error {
	chaosToolsMu.Lock()
	defer chaosToolsMu.Unlock()
	if _, err := m.docker(ctx, "image", "inspect", "--format", "{{.Id}}", ChaosToolsImage); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, m.dockerBin(), "build", "-q", "-t", ChaosToolsImage, "-")
	cmd.Stdin = strings.NewReader(chaosToolsDockerfile)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build the fault injection helper image %s: %s", ChaosToolsImage, strings.TrimSpace(string(out)))
	}
	return nil
}

// PrepareFaults builds the helper image fault injection uses when missing
// (loadtest.FaultInjector).
func (m *Manager) PrepareFaults(ctx context.Context) error { return m.ensureChaosTools(ctx) }

// InjectFault injects spec into the workflow's runtime; target is the
// load test's target (kill_replica prefers the replica it hits). The
// caller must Revert the fault, also when the run is canceled.
func (m *Manager) InjectFault(ctx context.Context, wfID string, spec model.ScenarioSpec, target model.LoadTarget) (scenario.Fault, error) {
	if err := checkID(wfID); err != nil {
		return nil, err
	}
	ops := m.faultOps()
	// A recipe a previous daemon left (e.g. its node was stopped then)
	// is replayed before anything new is injected.
	if _, err := ops.recover(ctx, wfID); err != nil {
		return nil, fmt.Errorf("revert an earlier fault first: %w", err)
	}
	if name, ok := strings.CutPrefix(spec.Target, "service:"); ok {
		return ops.injectCompose(ctx, wfID, name, spec, target)
	}
	kind, ns, name, ok := model.ParseKubeNodeID(spec.Target)
	if !ok || (kind != "Deployment" && kind != "StatefulSet") || !userNamespace(ns) {
		return nil, fmt.Errorf("scenario target %q is not a compose service or a Deployment/StatefulSet", spec.Target)
	}
	node, err := m.inspectKubeNode(ctx, wfID)
	if err != nil {
		return nil, err
	}
	if node == nil || !node.running() {
		return nil, errors.New("the workflow's cluster is not running")
	}
	return ops.injectKube(ctx, wfID, node.Name, strings.ToLower(kind)+"/"+name, ns, spec)
}

// RecoverFaults reverts every fault left pending by a previous daemon
// (crash or kill mid-scenario). It returns how many were reverted.
func (m *Manager) RecoverFaults(ctx context.Context) (int, error) {
	return m.faultOps().recover(ctx, "")
}

// activeFaults are the recipes of faults injected by this process and not
// reverted yet: recovery leaves them to their run.
var activeFaults sync.Map

// recover replays the pending recipes of workflow wfID ("" = all).
func (o *faultOps) recover(ctx context.Context, wfID string) (int, error) {
	files, _ := filepath.Glob(filepath.Join(o.dir, "*.json"))
	n := 0
	var errs []error
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var p pendingFault
		if err := json.Unmarshal(data, &p); err != nil {
			os.Remove(f) // unreadable recipe: nothing to replay
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f), err))
			continue
		}
		if _, active := activeFaults.Load(p.ID); active || (wfID != "" && p.Workflow != wfID) {
			continue
		}
		if _, err := o.revertPending(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("revert %s of workflow %s: %w", p.Scenario, p.Workflow, err))
			continue
		}
		os.Remove(f)
		n++
	}
	return n, errors.Join(errs...)
}

func (o *faultOps) save(p pendingFault) error {
	if err := os.MkdirAll(o.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(o.dir, p.ID+".json"), data); err != nil {
		return err
	}
	activeFaults.Store(p.ID, true)
	return nil
}

// forget deletes a reverted recipe; done false keeps it for recovery.
func (o *faultOps) forget(p pendingFault, done bool) {
	if done {
		os.Remove(filepath.Join(o.dir, p.ID+".json"))
	}
	activeFaults.Delete(p.ID)
}

func newFaultID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "f-" + hex.EncodeToString(b)
}

// fault is an injected fault.
type fault struct {
	ops      *faultOps
	rec      *pendingFault // nil: nothing to revert
	target   string
	detail   string
	injected time.Time
	keys     int

	// revert undoes the fault (beyond the recipe) and fills obs.
	revert func(ctx context.Context, obs *scenario.Observation) error

	watchStop context.CancelFunc
	watchDone chan struct{}
	mu        sync.Mutex
	back      time.Duration // kill_replica: back after the injection
	isBack    bool

	once sync.Once
	obs  scenario.Observation
	err  error
}

func (f *fault) TargetName() string { return f.target }
func (f *fault) Detail() string     { return f.detail }

func (f *fault) Revert(ctx context.Context) (scenario.Observation, error) {
	f.once.Do(func() {
		if f.watchStop != nil {
			f.watchStop()
			<-f.watchDone
		}
		f.obs.Keys = f.keys
		f.mu.Lock()
		f.obs.Back, f.obs.BackAfter = f.isBack, f.back
		f.mu.Unlock()
		switch {
		case f.revert != nil:
			f.err = f.revert(ctx, &f.obs)
		case f.rec != nil: // injection failed before the fault was complete
			_, f.err = f.ops.revertPending(ctx, *f.rec)
		}
		if f.rec != nil {
			f.ops.forget(*f.rec, f.err == nil)
		}
	})
	return f.obs, f.err
}

// watch polls back() until it reports true or Revert stops it.
func (f *fault) watch(back func(ctx context.Context) bool) {
	ctx, cancel := context.WithCancel(context.Background())
	f.watchStop, f.watchDone = cancel, make(chan struct{})
	go func() {
		defer close(f.watchDone)
		t := time.NewTicker(f.ops.poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if back(ctx) {
				f.mu.Lock()
				f.isBack, f.back = true, time.Since(f.injected)
				f.mu.Unlock()
				return
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Docker

// serviceContainers lists the service's containers in the workflow's
// project, sorted by name.
func (o *faultOps) serviceContainers(ctx context.Context, wfID, service string) ([]inspected, error) {
	out, err := o.docker(ctx, "ps", "-aq", "--no-trunc",
		"--filter", "label="+composeProjectLabel+"="+model.ComposeProjectName(wfID),
		"--filter", "label="+ComposeLabel+"="+wfID,
		"--filter", "label="+composeServiceLabel+"="+service)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil, nil
	}
	data, err := o.docker(ctx, append([]string{"inspect", "--type", "container"}, ids...)...)
	if err != nil {
		return nil, err
	}
	cs, err := parseInspect([]byte(data))
	if err != nil {
		return nil, err
	}
	// Belt and braces: only containers carrying both labels.
	cs = slices.DeleteFunc(cs, func(c inspected) bool {
		return c.Config.Labels[composeProjectLabel] != model.ComposeProjectName(wfID) || c.Config.Labels[ComposeLabel] != wfID || c.service() != service
	})
	slices.SortFunc(cs, func(a, b inspected) int { return strings.Compare(a.Name, b.Name) })
	return cs, nil
}

func containerName(c inspected) string { return strings.TrimPrefix(c.Name, "/") }

func running(cs []inspected) []inspected {
	return slices.DeleteFunc(slices.Clone(cs), func(c inspected) bool { return c.State.Status != "running" })
}

func ids(cs []inspected) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func names(cs []inspected) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = containerName(c)
	}
	return strings.Join(out, ", ")
}

// helper runs a ChaosToolsImage container with args before the image and
// cmd after it.
func (o *faultOps) helper(ctx context.Context, wfID string, args []string, cmd ...string) (string, error) {
	if err := o.tools(ctx); err != nil {
		return "", err
	}
	a := append([]string{"run", "--rm", "--label", ComposeLabel + "=" + wfID, "--label", kubeRoleLabel + "=" + roleChaos}, args...)
	a = append(a, ChaosToolsImage)
	return o.docker(ctx, append(a, cmd...)...)
}

// netemScript replaces the root qdisc of every interface but lo.
func netemScript(params map[string]string) (script, rule string) {
	delay := int(scenario.Param(params, "delayMs", scenario.DefaultDelayMs))
	jitter := int(scenario.Param(params, "jitterMs", scenario.DefaultJitterMs))
	loss := scenario.Param(params, "lossPct", scenario.DefaultLossPct)
	rule = fmt.Sprintf("delay %dms", delay)
	if jitter > 0 {
		rule += fmt.Sprintf(" %dms", jitter)
	}
	if loss > 0 {
		rule += " loss " + strconv.FormatFloat(loss, 'f', -1, 64) + "%"
	}
	return `set -e; for i in $(ls /sys/class/net); do [ "$i" = lo ] && continue; tc qdisc replace dev "$i" root netem ` + rule + `; done; tc qdisc show | grep -q netem`, rule
}

// netemClearScript removes the root qdiscs and fails while one is netem.
const netemClearScript = `for i in $(ls /sys/class/net); do [ "$i" = lo ] && continue; tc qdisc del dev "$i" root 2>/dev/null; done; ! tc qdisc show | grep -q netem`

// redisFlushScript prints the key count then flushes (FLUSHALL).
const redisFlushScript = `cli=$(command -v redis-cli || command -v valkey-cli || command -v keydb-cli); [ -n "$cli" ] || { echo "no redis-cli in the container" >&2; exit 1; }
auth=""; [ -n "$REDIS_PASSWORD" ] && auth="--no-auth-warning -a $REDIS_PASSWORD"
$cli $auth DBSIZE; $cli $auth FLUSHALL >/dev/null`

// memcachedFlushScript prints curr_items then flushes (flush_all).
const memcachedFlushScript = `printf 'stats\r\nquit\r\n' | nc 127.0.0.1 11211 | awk '/STAT curr_items/ {print $3}'; printf 'flush_all\r\nquit\r\n' | nc 127.0.0.1 11211 | grep -q OK`

func (o *faultOps) injectCompose(ctx context.Context, wfID, service string, spec model.ScenarioSpec, target model.LoadTarget) (scenario.Fault, error) {
	all, err := o.serviceContainers(ctx, wfID, service)
	if err != nil {
		return nil, err
	}
	live := running(all)
	if len(live) == 0 {
		return nil, fmt.Errorf("service %s has no running container", service)
	}
	f := &fault{ops: o, target: service, keys: -1}
	switch spec.ID {
	case model.ScenarioKillReplica:
		return o.killContainer(ctx, wfID, f, pickReplica(live, target), spec)
	case model.ScenarioDependencyDown:
		rec := pendingFault{ID: newFaultID(), Workflow: wfID, Scenario: spec.ID, Action: revertUnpause, Containers: ids(live), CreatedAt: time.Now().UTC()}
		if err := o.save(rec); err != nil {
			return nil, err
		}
		f.rec = &rec
		f.injected = time.Now()
		if _, err := o.docker(ctx, append([]string{"pause"}, rec.Containers...)...); err != nil {
			_, _ = f.Revert(context.WithoutCancel(ctx))
			return nil, err
		}
		f.detail = "paused " + names(live)
		f.revert = func(ctx context.Context, obs *scenario.Observation) error {
			back := time.Now()
			ok, err := o.revertPending(ctx, rec)
			obs.Back, obs.BackAfter = ok, back.Sub(f.injected)
			obs.Verified = ok
			obs.VerifyDetail = map[bool]string{true: "Unpaused " + names(live) + "; all running.", false: "Some containers of " + service + " are still paused."}[ok]
			return err
		}
		return f, nil
	case model.ScenarioDependencyLatency:
		rec := pendingFault{ID: newFaultID(), Workflow: wfID, Scenario: spec.ID, Action: revertNetem, Containers: ids(live), CreatedAt: time.Now().UTC()}
		if err := o.tools(ctx); err != nil {
			return nil, err
		}
		if err := o.save(rec); err != nil {
			return nil, err
		}
		f.rec = &rec
		f.injected = time.Now()
		script, rule := netemScript(spec.Params)
		for _, c := range live {
			if _, err := o.helper(ctx, wfID, []string{"--network", "container:" + c.ID, "--cap-add", "NET_ADMIN"}, "sh", "-c", script); err != nil {
				_, _ = f.Revert(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("add latency to %s: %w", containerName(c), err)
			}
		}
		f.detail = "tc netem " + rule + " on " + names(live)
		f.revert = func(ctx context.Context, obs *scenario.Observation) error {
			ok, err := o.revertPending(ctx, rec)
			obs.Back, obs.BackAfter = ok, time.Since(f.injected)
			obs.Verified = ok
			obs.VerifyDetail = map[bool]string{true: "netem qdisc removed from " + names(live) + " (tc qdisc show has no netem).", false: "A netem qdisc is still present on " + service + "."}[ok]
			return err
		}
		return f, nil
	case model.ScenarioCacheFlush:
		f.injected = time.Now()
		total := 0
		kind := scenario.CacheKind(live[0].Config.Image)
		for _, c := range live {
			var out string
			if kind == "memcached" {
				out, err = o.helper(ctx, wfID, []string{"--network", "container:" + c.ID}, "sh", "-c", memcachedFlushScript)
			} else {
				out, err = o.docker(ctx, "exec", c.ID, "sh", "-c", redisFlushScript)
			}
			if err != nil {
				return nil, fmt.Errorf("flush %s: %w", containerName(c), err)
			}
			n, _ := strconv.Atoi(strings.TrimSpace(firstLine(out)))
			total += n
		}
		f.keys = total
		f.detail = fmt.Sprintf("flushed %s (%d keys)", names(live), total)
		return f, nil
	}
	return nil, fmt.Errorf("scenario %s does not inject into containers", spec.ID)
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// pickReplica is the running replica the load target hits through its
// published port, else the first by name.
func pickReplica(live []inspected, target model.LoadTarget) inspected {
	if target.HTTP != nil {
		if u, err := url.Parse(target.HTTP.URL); err == nil {
			if port, _ := strconv.Atoi(u.Port()); port > 0 {
				for _, c := range live {
					for _, binds := range c.NetworkSettings.Ports {
						for _, b := range binds {
							if b.HostPort == strconv.Itoa(port) {
								return c
							}
						}
					}
				}
			}
		}
	}
	return live[0]
}

// killContainer crashes c: SIGKILL of its main process from the host PID
// namespace, so Docker sees a crash and applies the restart policy
// (`docker kill` would count as a manual stop and skip it).
func (o *faultOps) killContainer(ctx context.Context, wfID string, f *fault, c inspected, spec model.ScenarioSpec) (scenario.Fault, error) {
	out, err := o.docker(ctx, "inspect", "--type", "container", "--format", "{{.State.Pid}} {{.State.StartedAt}}", c.ID)
	if err != nil {
		return nil, err
	}
	pidStr, startedAt, _ := strings.Cut(strings.TrimSpace(out), " ")
	pid, _ := strconv.Atoi(pidStr)
	if pid <= 0 {
		return nil, fmt.Errorf("%s has no main process", containerName(c))
	}
	if err := o.tools(ctx); err != nil {
		return nil, err
	}
	rec := pendingFault{ID: newFaultID(), Workflow: wfID, Scenario: spec.ID, Action: revertStart, Containers: []string{c.ID}, CreatedAt: time.Now().UTC()}
	if err := o.save(rec); err != nil {
		return nil, err
	}
	f.rec = &rec
	f.injected = time.Now()
	if _, err := o.helper(ctx, wfID, []string{"--pid", "host", "--privileged", "--network", "none"}, "kill", "-9", strconv.Itoa(pid)); err != nil {
		_, _ = f.Revert(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("kill %s: %w", containerName(c), err)
	}
	f.detail = "crashed " + containerName(c) + " (SIGKILL of its main process)"
	f.watch(func(ctx context.Context) bool {
		out, err := o.docker(ctx, "inspect", "--type", "container", "--format", "{{.State.Status}} {{.State.StartedAt}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", c.ID)
		if err != nil {
			return false
		}
		fs := strings.Fields(out)
		return len(fs) >= 2 && fs[0] == "running" && fs[1] != startedAt
	})
	f.revert = func(ctx context.Context, obs *scenario.Observation) error {
		obs.SelfHealed = obs.Back
		if !obs.Back {
			// Nobody brought it back (no restart policy, or still
			// restarting): start it so the workflow is as before.
			if _, err := o.revertPending(ctx, rec); err != nil {
				return err
			}
		}
		st, _ := o.docker(ctx, "inspect", "--type", "container", "--format", "{{.State.Status}}", c.ID)
		obs.Verified = strings.TrimSpace(st) == "running"
		switch {
		case obs.Back:
			obs.VerifyDetail = containerName(c) + " was restarted by its restart policy."
		case obs.Verified:
			obs.VerifyDetail = containerName(c) + " was started again by Nimbo."
		default:
			obs.VerifyDetail = containerName(c) + " is not running: " + strings.TrimSpace(st)
		}
		return nil
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// Kubernetes (kubectl and crictl inside the workflow's own k3s node)

type kubeObj struct {
	Metadata struct {
		Name              string  `json:"name"`
		DeletionTimestamp *string `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int `json:"replicas"`
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int `json:"readyReplicas"`
		Conditions    []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Name        string `json:"name"`
			ContainerID string `json:"containerID"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func (p kubeObj) ready() bool {
	if p.Metadata.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}

func (o *faultOps) kubectl(ctx context.Context, node string, args ...string) (string, error) {
	return o.docker(ctx, append([]string{"exec", node, "kubectl"}, args...)...)
}

func (o *faultOps) kubeGet(ctx context.Context, node, ns, workload string) (kubeObj, error) {
	var w kubeObj
	out, err := o.kubectl(ctx, node, "-n", ns, "get", workload, "-o", "json")
	if err != nil {
		return w, err
	}
	return w, json.Unmarshal([]byte(out), &w)
}

func (o *faultOps) kubePods(ctx context.Context, node, ns, selector string) ([]kubeObj, error) {
	out, err := o.kubectl(ctx, node, "-n", ns, "get", "pods", "-l", selector, "-o", "json")
	if err != nil {
		return nil, err
	}
	var l struct {
		Items []kubeObj `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		return nil, err
	}
	slices.SortFunc(l.Items, func(a, b kubeObj) int { return strings.Compare(a.Metadata.Name, b.Metadata.Name) })
	return l.Items, nil
}

func readyPods(ps []kubeObj) []kubeObj {
	return slices.DeleteFunc(slices.Clone(ps), func(p kubeObj) bool { return !p.ready() })
}

func selectorString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ",")
}

// podPids are the host-of-node PIDs of the pods' first containers (for
// nsenter into their network namespace).
func (o *faultOps) podPids(ctx context.Context, node string, pods []kubeObj) []string {
	var out []string
	for _, p := range pods {
		if len(p.Status.ContainerStatuses) == 0 {
			continue
		}
		_, cid, ok := strings.Cut(p.Status.ContainerStatuses[0].ContainerID, "://")
		if !ok {
			continue
		}
		pid, err := o.docker(ctx, "exec", node, "crictl", "inspect", "--output", "go-template", "--template", "{{.info.pid}}", cid)
		if err == nil && strings.TrimSpace(pid) != "" && strings.TrimSpace(pid) != "0" {
			out = append(out, strings.TrimSpace(pid))
		}
	}
	return out
}

func podNames(ps []kubeObj) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Metadata.Name
	}
	return strings.Join(out, ", ")
}

func (o *faultOps) injectKube(ctx context.Context, wfID, node, workload, ns string, spec model.ScenarioSpec) (scenario.Fault, error) {
	w, err := o.kubeGet(ctx, node, ns, workload)
	if err != nil {
		return nil, err
	}
	selector := selectorString(w.Spec.Selector.MatchLabels)
	if selector == "" {
		return nil, fmt.Errorf("%s has no matchLabels selector", workload)
	}
	pods, err := o.kubePods(ctx, node, ns, selector)
	if err != nil {
		return nil, err
	}
	ready := readyPods(pods)
	if len(ready) == 0 {
		return nil, fmt.Errorf("%s has no Ready pod", workload)
	}
	name := w.Metadata.Name
	f := &fault{ops: o, target: name, keys: -1}
	switch spec.ID {
	case model.ScenarioKillReplica:
		victim := ready[0]
		want := len(ready)
		f.injected = time.Now()
		if _, err := o.kubectl(ctx, node, "-n", ns, "delete", "pod", victim.Metadata.Name, "--grace-period=0", "--force", "--wait=false"); err != nil {
			return nil, fmt.Errorf("delete pod %s: %w", victim.Metadata.Name, err)
		}
		f.detail = "deleted pod " + victim.Metadata.Name + " (no grace period)"
		f.watch(func(ctx context.Context) bool {
			ps, err := o.kubePods(ctx, node, ns, selector)
			if err != nil {
				return false
			}
			n := 0
			for _, p := range readyPods(ps) {
				if p.Metadata.Name != victim.Metadata.Name {
					n++
				}
			}
			return n >= want
		})
		f.revert = func(ctx context.Context, obs *scenario.Observation) error {
			obs.SelfHealed = obs.Back
			if !obs.Back {
				ok := o.waitReady(ctx, node, ns, selector, want)
				obs.Verified = ok
				obs.VerifyDetail = map[bool]string{true: fmt.Sprintf("%s is back to %d Ready pods (after the window).", name, want), false: fmt.Sprintf("%s did not get back to %d Ready pods.", name, want)}[ok]
				return nil
			}
			obs.Verified = true
			obs.VerifyDetail = fmt.Sprintf("The controller replaced the pod: %d Ready pods again.", want)
			return nil
		}
		return f, nil
	case model.ScenarioDependencyDown:
		replicas := len(ready)
		if w.Spec.Replicas != nil {
			replicas = *w.Spec.Replicas
		}
		rec := pendingFault{ID: newFaultID(), Workflow: wfID, Scenario: spec.ID, Action: revertScale, Node: node, Namespace: ns, Workload: workload, Selector: selector, Replicas: replicas, CreatedAt: time.Now().UTC()}
		if err := o.save(rec); err != nil {
			return nil, err
		}
		f.rec = &rec
		f.injected = time.Now()
		if _, err := o.kubectl(ctx, node, "-n", ns, "scale", workload, "--replicas=0"); err != nil {
			_, _ = f.Revert(context.WithoutCancel(ctx))
			return nil, err
		}
		f.detail = fmt.Sprintf("scaled %s from %d to 0 replicas", workload, replicas)
		f.revert = func(ctx context.Context, obs *scenario.Observation) error {
			ok, err := o.revertPending(ctx, rec)
			obs.Back, obs.BackAfter = ok, time.Since(f.injected)
			obs.Verified = ok
			obs.VerifyDetail = map[bool]string{true: fmt.Sprintf("Scaled %s back to %d replicas, all Ready.", workload, replicas), false: fmt.Sprintf("%s is not back to %d Ready replicas.", workload, replicas)}[ok]
			return err
		}
		return f, nil
	case model.ScenarioDependencyLatency:
		if err := o.tools(ctx); err != nil {
			return nil, err
		}
		rec := pendingFault{ID: newFaultID(), Workflow: wfID, Scenario: spec.ID, Action: revertKubeNetem, Node: node, Namespace: ns, Workload: workload, Selector: selector, CreatedAt: time.Now().UTC()}
		if err := o.save(rec); err != nil {
			return nil, err
		}
		f.rec = &rec
		f.injected = time.Now()
		script, rule := netemScript(spec.Params)
		pids := o.podPids(ctx, node, ready)
		if len(pids) == 0 {
			_, _ = f.Revert(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("could not find the processes of %s's pods", workload)
		}
		for _, pid := range pids {
			if _, err := o.helper(ctx, wfID, []string{"--pid", "container:" + node, "--privileged", "--network", "none"}, "nsenter", "-t", pid, "-n", "sh", "-c", script); err != nil {
				_, _ = f.Revert(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("add latency to %s: %w", workload, err)
			}
		}
		f.detail = "tc netem " + rule + " on pods " + podNames(ready)
		f.revert = func(ctx context.Context, obs *scenario.Observation) error {
			ok, err := o.revertPending(ctx, rec)
			obs.Back, obs.BackAfter = ok, time.Since(f.injected)
			obs.Verified = ok
			obs.VerifyDetail = map[bool]string{true: "netem qdisc removed from " + podNames(ready) + " (tc qdisc show has no netem).", false: "A netem qdisc is still present in " + workload + "'s pods."}[ok]
			return err
		}
		return f, nil
	case model.ScenarioCacheFlush:
		f.injected = time.Now()
		total := 0
		for _, p := range ready {
			kind, cname := "", ""
			for _, c := range p.Spec.Containers {
				if k := scenario.CacheKind(c.Image); k != "" {
					kind, cname = k, c.Name
					break
				}
			}
			var out string
			if kind == "memcached" {
				pids := o.podPids(ctx, node, []kubeObj{p})
				if len(pids) == 0 {
					return nil, fmt.Errorf("could not find the process of pod %s", p.Metadata.Name)
				}
				out, err = o.helper(ctx, wfID, []string{"--pid", "container:" + node, "--privileged", "--network", "none"}, "nsenter", "-t", pids[0], "-n", "sh", "-c", memcachedFlushScript)
			} else {
				args := []string{"-n", ns, "exec", p.Metadata.Name}
				if cname != "" {
					args = append(args, "-c", cname)
				}
				out, err = o.kubectl(ctx, node, append(args, "--", "sh", "-c", redisFlushScript)...)
			}
			if err != nil {
				return nil, fmt.Errorf("flush pod %s: %w", p.Metadata.Name, err)
			}
			n, _ := strconv.Atoi(strings.TrimSpace(firstLine(out)))
			total += n
		}
		f.keys = total
		f.detail = fmt.Sprintf("flushed pods %s (%d keys)", podNames(ready), total)
		return f, nil
	}
	return nil, fmt.Errorf("scenario %s does not inject into workloads", spec.ID)
}

// waitReady waits until the selector has at least want Ready pods.
func (o *faultOps) waitReady(ctx context.Context, node, ns, selector string, want int) bool {
	ctx, cancel := context.WithTimeout(ctx, o.wait)
	defer cancel()
	for {
		if ps, err := o.kubePods(ctx, node, ns, selector); err == nil && len(readyPods(ps)) >= want {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(o.poll):
		}
	}
}

// revertPending applies a recipe; ok reports the runtime is verifiably
// back to its pre-fault state. Containers or pods that are gone (removed
// since) have nothing left to revert.
func (o *faultOps) revertPending(ctx context.Context, p pendingFault) (bool, error) {
	switch p.Action {
	case revertStart, revertUnpause, revertNetem:
		ok := true
		var errs []error
		for _, id := range p.Containers {
			st, err := o.docker(ctx, "inspect", "--type", "container", "--format", "{{.State.Status}} {{index .Config.Labels \""+ComposeLabel+"\"}}", id)
			if err != nil {
				if isNoSuch(err) {
					continue
				}
				errs = append(errs, err)
				ok = false
				continue
			}
			fs := strings.Fields(st)
			if len(fs) < 2 || fs[1] != p.Workflow {
				errs = append(errs, fmt.Errorf("container %s does not belong to workflow %s", id, p.Workflow))
				ok = false
				continue
			}
			switch p.Action {
			case revertStart:
				if fs[0] != "running" && fs[0] != "restarting" {
					if _, err := o.docker(ctx, "start", id); err != nil {
						errs = append(errs, err)
						ok = false
					}
				}
			case revertUnpause:
				if fs[0] == "paused" {
					if _, err := o.docker(ctx, "unpause", id); err != nil {
						errs = append(errs, err)
						ok = false
					}
				}
				if st, err := o.docker(ctx, "inspect", "--type", "container", "--format", "{{.State.Status}}", id); err != nil || strings.TrimSpace(st) == "paused" {
					ok = false
				}
			case revertNetem:
				if fs[0] != "running" {
					continue // no live network namespace: nothing applied
				}
				if _, err := o.helper(ctx, p.Workflow, []string{"--network", "container:" + id, "--cap-add", "NET_ADMIN"}, "sh", "-c", netemClearScript); err != nil {
					errs = append(errs, err)
					ok = false
				}
			}
		}
		return ok && len(errs) == 0, errors.Join(errs...)
	case revertScale:
		if _, err := o.kubectl(ctx, p.Node, "-n", p.Namespace, "scale", p.Workload, "--replicas="+strconv.Itoa(p.Replicas)); err != nil {
			if isNoSuch(err) {
				return true, nil
			}
			return false, err
		}
		return o.waitReady(ctx, p.Node, p.Namespace, p.Selector, p.Replicas), nil
	case revertKubeNetem:
		pods, err := o.kubePods(ctx, p.Node, p.Namespace, p.Selector)
		if err != nil {
			if isNoSuch(err) {
				return true, nil
			}
			return false, err
		}
		var errs []error
		for _, pid := range o.podPids(ctx, p.Node, pods) {
			if _, err := o.helper(ctx, p.Workflow, []string{"--pid", "container:" + p.Node, "--privileged", "--network", "none"}, "nsenter", "-t", pid, "-n", "sh", "-c", netemClearScript); err != nil {
				errs = append(errs, err)
			}
		}
		return len(errs) == 0, errors.Join(errs...)
	}
	return false, fmt.Errorf("unknown revert action %q", p.Action)
}
