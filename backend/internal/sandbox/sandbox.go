// Package sandbox runs per-workflow Floci sandboxes and Terraform runs in
// Docker, driving the docker CLI.
//
// Layout per workflow wf: network nimbo-<wf>; pod container nimbo-<wf>-pod
// owning the network namespace and publishing emulator ports on 127.0.0.1;
// emulators nimbo-<wf>-<cloud> and Terraform run containers join the pod's
// namespace, so inside the sandbox every emulator is localhost:<native port>.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Config configures a Manager.
type Config struct {
	Home           string                 // ~/.nimbo
	Docker         string                 // docker CLI binary, default "docker"
	Images         map[model.Cloud]string // emulator images, default floci/*:latest
	TerraformImage string                 // default hashicorp/terraform:latest
	AWSRegion      string                 // AWS_REGION of Terraform runs, default us-east-1
	Events         model.Publisher
	// LegacyHomeMigrated is set when this start renamed the pre-Nimbo home
	// directory; it widens the one-time legacy Docker cleanup.
	LegacyHomeMigrated bool
}

// ErrRunActive is returned when an operation conflicts with an active run.
var ErrRunActive = errors.New("a Terraform run is in progress for this workflow")

// Manager owns every workflow sandbox. Sandboxes keep running when the
// daemon exits; Status rediscovers them from container labels.
type Manager struct {
	cfg Config
	// settingsMu guards cfg.Docker, cfg.Images, cfg.TerraformImage and
	// cfg.AWSRegion, which ApplySettings replaces at run time.
	settingsMu sync.RWMutex

	mu         sync.Mutex
	sandboxes  map[string]*sandbox
	runs       map[string]*runState
	loaded     map[string]bool       // workflow run history loaded from disk
	stageCache map[string]stageEntry // "<wf>/<apiID>" -> API Gateway stages
	stateCache map[string]stateEntry // workflow ID -> last `terraform show -json`
	pluginsOK  bool
}

type sandbox struct {
	id        string
	lifecycle sync.Mutex // serializes Start/Stop/Reset/Remove

	// Guarded by Manager.mu.
	ports  map[int]int // container port -> 127.0.0.1 host port
	notice string      // shown in the running status, e.g. ports that moved
	last   *model.SandboxStatus
	busy   *model.SandboxStatus // set while a lifecycle operation runs
	active *runState

	// Docker workflows (compose.go), guarded by Manager.mu.
	composeServices []ComposeService     // services of the files, for status refreshes
	composeLast     *model.ComposeStatus // last published status (UpdatedAt zeroed)
	composeWatch    context.CancelFunc   // stops the `docker events` watcher
	// perf is the docker stats stream, usage history and startup times
	// (compose_stats.go); it has its own lock.
	perf composePerf

	// Kubernetes workflows (kube*.go): cluster clients, informers,
	// port-forwards and the cgroup sampler; it has its own lock.
	kube kubeState
}

// New validates cfg, applies defaults and creates the home directory.
func New(cfg Config) (*Manager, error) {
	if cfg.Home == "" {
		return nil, errors.New("sandbox: Home is required")
	}
	home, err := filepath.Abs(cfg.Home)
	if err != nil {
		return nil, err
	}
	cfg.Home = home
	applyDefaults(&cfg)
	if err := os.MkdirAll(filepath.Join(home, "workflows"), 0o755); err != nil {
		return nil, err
	}
	m := &Manager{
		cfg:        cfg,
		sandboxes:  map[string]*sandbox{},
		runs:       map[string]*runState{},
		loaded:     map[string]bool{},
		stageCache: map[string]stageEntry{},
		stateCache: map[string]stateEntry{},
	}
	m.cleanupLegacy(context.Background(), cfg.LegacyHomeMigrated)
	return m, nil
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,100}$`)

func checkID(wfID string) error {
	if !validID.MatchString(wfID) {
		return fmt.Errorf("invalid workflow id %q", wfID)
	}
	return nil
}

func (m *Manager) wfDir(wfID string) string { return filepath.Join(m.cfg.Home, "workflows", wfID) }

// WorkDir is where Terraform runs for a workflow (its .terraform cache,
// downloaded modules and state live there).
func (m *Manager) WorkDir(wfID string) string {
	return filepath.Join(m.wfDir(wfID), "work")
}
func (m *Manager) emulatorDir(wfID string, c model.Cloud) string {
	return filepath.Join(m.wfDir(wfID), "emulators", string(c))
}

func (m *Manager) sandbox(wfID string) *sandbox {
	m.mu.Lock()
	defer m.mu.Unlock()
	sb, ok := m.sandboxes[wfID]
	if !ok {
		sb = &sandbox{id: wfID}
		m.sandboxes[wfID] = sb
	}
	return sb
}

func (m *Manager) publish(typ, wfID string, data any) {
	if m.cfg.Events != nil {
		m.cfg.Events.Publish(model.Event{Type: typ, WorkflowID: wfID, Data: data})
	}
}

// setStatus records st as the latest status and publishes it when it differs
// from the previous one.
func (m *Manager) setStatus(sb *sandbox, st model.SandboxStatus) {
	m.mu.Lock()
	changed := sb.last == nil || !reflect.DeepEqual(*sb.last, st)
	cp := st
	sb.last = &cp
	m.mu.Unlock()
	if changed {
		m.publish(model.EventSandboxStatus, sb.id, st)
	}
}

// progress marks a lifecycle operation in progress with a user-facing message.
func (m *Manager) progress(sb *sandbox, state model.SandboxState, msg string) {
	m.mu.Lock()
	var st model.SandboxStatus
	if sb.last != nil {
		st = *sb.last
	}
	st.WorkflowID = sb.id
	if state != "" {
		st.State = state
	} else if st.State == "" {
		st.State = model.SandboxAbsent
	}
	st.Message = msg
	if st.Emulators == nil {
		st.Emulators = []model.EmulatorStatus{}
	}
	sb.busy = &st
	m.mu.Unlock()
	m.setStatus(sb, st)
}

func (m *Manager) clearBusy(sb *sandbox) {
	m.mu.Lock()
	sb.busy = nil
	m.mu.Unlock()
}

// DockerAvailable returns nil when the docker daemon answers.
func (m *Manager) DockerAvailable(ctx context.Context) error {
	_, err := m.docker(ctx, "version", "--format", "{{.Server.Version}}")
	return err
}

// Status inspects the sandbox containers of wfID.
func (m *Manager) Status(ctx context.Context, wfID string) (model.SandboxStatus, error) {
	if err := checkID(wfID); err != nil {
		return model.SandboxStatus{}, err
	}
	sb := m.sandbox(wfID)
	m.mu.Lock()
	if sb.busy != nil {
		st := *sb.busy
		m.mu.Unlock()
		return st, nil
	}
	m.mu.Unlock()
	st, err := m.inspect(ctx, sb)
	if err != nil {
		return model.SandboxStatus{}, err
	}
	m.setStatus(sb, st)
	return st, nil
}

// sandboxContainers splits the workflow's labelled containers by role.
func (m *Manager) sandboxContainers(ctx context.Context, wfID string) (pod *container, emus map[model.Cloud]container, tf []container, err error) {
	cs, err := m.ps(ctx, "label="+labelWorkflow+"="+wfID)
	if err != nil {
		return nil, nil, nil, err
	}
	emus = map[model.Cloud]container{}
	for _, c := range cs {
		// Floci-spawned containers may carry the workflow label too (AWS
		// extra labels); only containers we named belong to these roles.
		switch c.label(labelRole) {
		case rolePod:
			if c.Names == podName(wfID) {
				pod = &c
			}
		case roleEmulator:
			cl := model.Cloud(c.label(labelCloud))
			if _, ok := emulators[cl]; ok && c.Names == emulatorName(wfID, string(cl)) {
				emus[cl] = c
			}
		case roleTerraform:
			tf = append(tf, c)
		}
	}
	return pod, emus, tf, nil
}

func (m *Manager) inspect(ctx context.Context, sb *sandbox) (model.SandboxStatus, error) {
	wfID := sb.id
	st := model.SandboxStatus{WorkflowID: wfID, State: model.SandboxAbsent, Emulators: []model.EmulatorStatus{}}
	pod, emus, _, err := m.sandboxContainers(ctx, wfID)
	if err != nil {
		return st, err
	}
	if ok, err := m.networkExists(ctx, networkName(wfID)); err != nil {
		return st, err
	} else if ok {
		st.Network = networkName(wfID)
	}
	// A pod that isn't running publishes no ports: record that as known so
	// EmulatorURL lookups until the next start don't each ask docker.
	ports := map[int]int{}
	if pod != nil && pod.running() {
		if ports, err = m.hostPorts(ctx, pod.Names); err != nil {
			return st, err
		}
	}
	m.mu.Lock()
	sb.ports = ports
	m.mu.Unlock()

	starting := false
	for _, c := range model.AllClouds {
		ct, ok := emus[c]
		if !ok {
			continue
		}
		e := emulators[c]
		es := model.EmulatorStatus{Cloud: c, Container: ct.Names, Image: ct.Image, Port: e.port, State: model.SandboxStopped}
		if hp := ports[e.port]; hp > 0 {
			es.HostPort = hp
			es.URL = hostBase(hp)
		}
		if ct.running() && es.HostPort > 0 {
			es.State = model.SandboxStarting
			if probeReady(ctx, e, es.URL) {
				if v, svcs, ok := probeHealth(ctx, e, es.URL); ok {
					es.State = model.SandboxRunning
					es.Version = v
					es.Services = svcs
				}
			}
			if es.State == model.SandboxStarting {
				starting = true
			}
		}
		st.Emulators = append(st.Emulators, es)
	}

	switch {
	case pod == nil && len(emus) > 0:
		st.State = model.SandboxError
		st.Message = "The sandbox pod container is missing; start the sandbox to recreate it."
	case pod == nil:
		st.State = model.SandboxAbsent
	case !pod.running():
		st.State = model.SandboxStopped
	case starting:
		st.State = model.SandboxStarting
	default:
		st.State = model.SandboxRunning
	}
	if st.State == model.SandboxRunning {
		m.mu.Lock()
		st.Message = sb.notice
		m.mu.Unlock()
	}
	return st, nil
}

// createPod runs the pod container of sb publishing every emulator port on
// fixed 127.0.0.1 host ports (see allocatePorts). When docker reports a
// port taken between the check and the run, it retries on other ports.
func (m *Manager) createPod(ctx context.Context, sb *sandbox, net string) error {
	wfID := sb.id
	before := m.savedPorts(wfID)
	exclude := map[int]bool{}
	for attempt := 0; ; attempt++ {
		ports, err := m.podPorts(wfID, exclude)
		if err != nil {
			return err
		}
		args := []string{"run", "-d", "--init", "--name", podName(wfID), "--network", net,
			"--label", labelWorkflow + "=" + wfID, "--label", labelRole + "=" + rolePod}
		for _, np := range emulatorPorts() {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", ports[np], np))
		}
		args = append(args, podImage, "sleep", "infinity")
		_, err = m.docker(ctx, args...)
		if err == nil {
			m.mu.Lock()
			sb.notice = movedNotice(before, ports)
			m.mu.Unlock()
			return nil
		}
		if !portBusy(err) || attempt == 2 {
			return err
		}
		// The container was created but could not start; drop it.
		if err := m.removeContainers(ctx, podName(wfID)); err != nil {
			return err
		}
		for _, hp := range ports {
			exclude[hp] = true
		}
	}
}

func hostBase(hostPort int) string { return "http://127.0.0.1:" + strconv.Itoa(hostPort) }

// Start creates or resumes the sandbox and the emulators for clouds. With no
// clouds it resumes the emulators that already exist.
func (m *Manager) Start(ctx context.Context, wfID string, clouds []model.Cloud) (model.SandboxStatus, error) {
	if err := checkID(wfID); err != nil {
		return model.SandboxStatus{}, err
	}
	for _, c := range clouds {
		if _, ok := emulators[c]; !ok {
			return model.SandboxStatus{}, fmt.Errorf("unknown cloud %q", c)
		}
	}
	sb := m.sandbox(wfID)
	sb.lifecycle.Lock()
	defer sb.lifecycle.Unlock()
	st, err := m.start(ctx, sb, clouds)
	m.clearBusy(sb)
	if err != nil {
		st = m.failStatus(sb, err)
	}
	return st, err
}

func (m *Manager) failStatus(sb *sandbox, err error) model.SandboxStatus {
	st, ierr := m.inspect(context.Background(), sb)
	if ierr != nil {
		st = model.SandboxStatus{WorkflowID: sb.id, Emulators: []model.EmulatorStatus{}}
	}
	st.State = model.SandboxError
	st.Message = err.Error()
	m.setStatus(sb, st)
	return st
}

func (m *Manager) start(ctx context.Context, sb *sandbox, clouds []model.Cloud) (model.SandboxStatus, error) {
	wfID := sb.id
	say := func(msg string) { m.progress(sb, model.SandboxStarting, msg) }
	say("Preparing sandbox")
	m.mu.Lock()
	sb.notice = ""
	m.mu.Unlock()

	pod, emus, _, err := m.sandboxContainers(ctx, wfID)
	if err != nil {
		return model.SandboxStatus{}, err
	}
	if len(clouds) == 0 {
		for _, c := range model.AllClouds {
			if _, ok := emus[c]; ok {
				clouds = append(clouds, c)
			}
		}
	}
	slices.Sort(clouds)
	clouds = slices.Compact(clouds)

	net := networkName(wfID)
	if ok, err := m.networkExists(ctx, net); err != nil {
		return model.SandboxStatus{}, err
	} else if !ok {
		if _, err := m.docker(ctx, "network", "create", "--label", labelWorkflow+"="+wfID, net); err != nil {
			return model.SandboxStatus{}, err
		}
	}

	// A stopped pod keeps its fixed host ports; if another program took one
	// meanwhile `docker start` would fail, so the pod is re-created on new
	// ports instead. Legacy pods (random ports, nothing saved) just restart.
	if pod != nil && !pod.running() {
		if saved := m.savedPorts(wfID); saved != nil && !savedPortsFree(saved) {
			if err := m.removeContainers(ctx, podName(wfID)); err != nil {
				return model.SandboxStatus{}, err
			}
			pod = nil
		}
	}

	switch {
	case pod == nil:
		// Emulators joined a namespace that no longer exists; recreate them
		// (their state lives in the bind-mounted data directory).
		var orphans []string
		for _, c := range emus {
			orphans = append(orphans, c.Names)
		}
		if err := m.removeContainers(ctx, orphans...); err != nil {
			return model.SandboxStatus{}, err
		}
		emus = map[model.Cloud]container{}
		if err := m.ensureImage(ctx, podImage, say); err != nil {
			return model.SandboxStatus{}, err
		}
		say("Creating sandbox pod")
		if err := m.createPod(ctx, sb, net); err != nil {
			return model.SandboxStatus{}, err
		}
	case !pod.running():
		// Emulators must re-join the pod's fresh namespace after it restarts.
		var running []string
		for _, c := range emus {
			if c.running() {
				running = append(running, c.Names)
			}
		}
		if len(running) > 0 {
			if _, err := m.docker(ctx, append([]string{"stop"}, running...)...); err != nil {
				return model.SandboxStatus{}, err
			}
		}
		say("Starting sandbox pod")
		if _, err := m.docker(ctx, "start", podName(wfID)); err != nil {
			return model.SandboxStatus{}, err
		}
	}

	ports, err := m.hostPorts(ctx, podName(wfID))
	if err != nil {
		return model.SandboxStatus{}, err
	}
	m.mu.Lock()
	sb.ports = ports
	m.mu.Unlock()

	for _, c := range clouds {
		e := emulators[c]
		name := emulatorName(wfID, string(c))
		image := m.emulatorImage(c)
		if ct, ok := emus[c]; ok {
			if ct.running() {
				continue
			}
			if ct.Image == image {
				say("Starting " + string(c) + " emulator")
				if _, err := m.docker(ctx, "start", name); err != nil {
					return model.SandboxStatus{}, err
				}
				continue
			}
			// The image changed in Settings: recreate the container. Emulator
			// state lives in the bind-mounted data dir, so it is kept.
			say("Switching " + string(c) + " emulator to " + image)
			if _, err := m.docker(ctx, "rm", "-f", name); err != nil {
				return model.SandboxStatus{}, err
			}
		}
		if err := m.ensureImage(ctx, image, say); err != nil {
			return model.SandboxStatus{}, err
		}
		dataDir := m.emulatorDir(wfID, c)
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return model.SandboxStatus{}, err
		}
		say("Starting " + string(c) + " emulator")
		args := []string{"run", "-d", "--name", name, "--network", "container:" + podName(wfID),
			"--label", labelWorkflow + "=" + wfID, "--label", labelRole + "=" + roleEmulator,
			"--label", labelCloud + "=" + string(c),
			"-v", "/var/run/docker.sock:/var/run/docker.sock", "-u", "root",
			"-v", dataDir + ":/app/data"}
		for _, kv := range emulatorEnv(e, wfID) {
			args = append(args, "-e", kv)
		}
		args = append(args, image)
		if _, err := m.docker(ctx, args...); err != nil {
			return model.SandboxStatus{}, err
		}
	}

	for _, c := range clouds {
		e := emulators[c]
		say("Waiting for " + string(c) + " emulator")
		if err := m.waitReady(ctx, e, emulatorName(wfID, string(c)), hostBase(ports[e.port])); err != nil {
			return model.SandboxStatus{}, err
		}
	}

	m.clearBusy(sb)
	st, err := m.inspect(ctx, sb)
	if err != nil {
		return st, err
	}
	m.setStatus(sb, st)
	return st, nil
}

const readyTimeout = 3 * time.Minute

func (m *Manager) waitReady(ctx context.Context, e emulator, name, base string) error {
	deadline := time.Now().Add(readyTimeout)
	for i := 0; ; i++ {
		if probeReady(ctx, e, base) {
			return nil
		}
		if i%10 == 9 {
			out, err := m.docker(ctx, "inspect", "--format", "{{.State.Running}}", name)
			if err != nil {
				return err
			}
			if out != "true" {
				logs, _ := m.docker(ctx, "logs", "--tail", "20", name)
				return fmt.Errorf("%s emulator exited during startup: %s", e.cloud, logs)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s emulator did not become ready within %s", e.cloud, readyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (m *Manager) activeRun(sb *sandbox) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return sb.active != nil
}

// spawned lists containers attached to the sandbox network other than the
// pod: the containers Floci started for Lambda, RDS, ...
func (m *Manager) spawned(ctx context.Context, wfID string) ([]container, error) {
	cs, err := m.networkContainers(ctx, networkName(wfID))
	if err != nil {
		return nil, err
	}
	out := cs[:0]
	for _, c := range cs {
		if c.Names != podName(wfID) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Stop stops emulators, spawned containers and the pod; all state is kept.
func (m *Manager) Stop(ctx context.Context, wfID string) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	sb.lifecycle.Lock()
	defer sb.lifecycle.Unlock()
	if m.activeRun(sb) {
		return ErrRunActive
	}
	err := m.stop(ctx, sb)
	m.clearBusy(sb)
	if err != nil {
		m.failStatus(sb, err)
		return err
	}
	_, err = m.Status(ctx, wfID)
	return err
}

func (m *Manager) stop(ctx context.Context, sb *sandbox) error {
	wfID := sb.id
	m.progress(sb, "", "Stopping sandbox")
	pod, emus, _, err := m.sandboxContainers(ctx, wfID)
	if err != nil {
		return err
	}
	var names []string
	for _, c := range emus {
		if c.running() {
			names = append(names, c.Names)
		}
	}
	if len(names) > 0 {
		if _, err := m.docker(ctx, append([]string{"stop", "-t", "20"}, names...)...); err != nil {
			return err
		}
	}
	sp, err := m.spawned(ctx, wfID)
	if err != nil {
		return err
	}
	names = names[:0]
	for _, c := range sp {
		if c.running() {
			names = append(names, c.ID)
		}
	}
	if pod != nil && pod.running() {
		names = append(names, pod.Names)
	}
	if len(names) > 0 {
		if _, err := m.docker(ctx, append([]string{"stop"}, names...)...); err != nil {
			return err
		}
	}
	m.mu.Lock()
	sb.ports = nil
	m.mu.Unlock()
	return nil
}

// Reset wipes emulator state, spawned containers and the Terraform state;
// running emulators keep running.
func (m *Manager) Reset(ctx context.Context, wfID string) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	sb.lifecycle.Lock()
	defer sb.lifecycle.Unlock()
	if m.activeRun(sb) {
		return ErrRunActive
	}
	err := m.reset(ctx, sb)
	m.clearBusy(sb)
	if err != nil {
		m.failStatus(sb, err)
		return err
	}
	m.publish(model.EventStateUpdated, wfID, emptyState(wfID))
	_, err = m.Status(ctx, wfID)
	return err
}

func (m *Manager) reset(ctx context.Context, sb *sandbox) error {
	wfID := sb.id
	m.progress(sb, "", "Resetting sandbox")
	pod, emus, _, err := m.sandboxContainers(ctx, wfID)
	if err != nil {
		return err
	}
	var ports map[int]int
	if pod != nil && pod.running() {
		if ports, err = m.hostPorts(ctx, pod.Names); err != nil {
			return err
		}
	}
	for _, c := range model.AllClouds {
		ct, ok := emus[c]
		if !ok {
			continue
		}
		e := emulators[c]
		if hp := ports[e.port]; ct.running() && hp > 0 {
			if err := httpPost(ctx, hostBase(hp)+e.resetPath); err != nil {
				return fmt.Errorf("reset %s emulator: %w", c, err)
			}
			continue
		}
		if err := m.wipeDir(ctx, m.emulatorDir(wfID, c), true); err != nil {
			return err
		}
	}
	if err := m.removeSpawned(ctx, wfID); err != nil {
		return err
	}
	work := m.WorkDir(wfID)
	for _, name := range []string{"terraform.tfstate", "terraform.tfstate.backup", "terraform.tfstate.d"} {
		if err := os.RemoveAll(filepath.Join(work, name)); err != nil {
			return err
		}
	}
	return nil
}

// removeSpawned removes Floci-spawned containers and volumes of the sandbox.
func (m *Manager) removeSpawned(ctx context.Context, wfID string) error {
	ns := networkName(wfID)
	sp, err := m.spawned(ctx, wfID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(sp))
	for _, c := range sp {
		ids = append(ids, c.ID)
	}
	// Spawned containers of the other emulators carry the namespace label.
	labeled, err := m.ps(ctx, "label=floci_namespace="+ns)
	if err != nil {
		return err
	}
	for _, c := range labeled {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	// Volumes Docker created for a mount (OKE's k3s data) carry no labels:
	// note what the spawned containers mount before they go.
	mounted := m.mountedVolumes(ctx, ids)
	if err := m.removeContainers(ctx, ids...); err != nil {
		return err
	}
	var labeledVols []string
	for _, f := range []string{"label=floci_namespace=" + ns, "label=" + labelWorkflow + "=" + wfID} {
		out, err := m.docker(ctx, "volume", "ls", "-q", "--filter", f)
		if err != nil {
			return err
		}
		labeledVols = append(labeledVols, strings.Fields(out)...)
	}
	out, err := m.docker(ctx, "volume", "ls", "-q", "--filter", "name="+okeVolumePrefix(ns))
	if err != nil {
		return err
	}
	vols := sandboxVolumes(ns, labeledVols, mounted, strings.Fields(out))
	if len(vols) == 0 {
		return nil
	}
	// Containers the emulators' sidecars started without labels or network
	// (fnserver's function containers) still hold the volumes.
	args := []string{"ps", "-aq"}
	for _, v := range vols {
		args = append(args, "--filter", "volume="+v)
	}
	out, err = m.docker(ctx, args...)
	if err != nil {
		return err
	}
	if err := m.removeContainers(ctx, strings.Fields(out)...); err != nil {
		return err
	}
	_, err = m.docker(ctx, append([]string{"volume", "rm", "-f"}, vols...)...)
	return err
}

// mountedVolumes lists the named volumes mounted by containers (missing
// containers are skipped).
func (m *Manager) mountedVolumes(ctx context.Context, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	// inspect exits non-zero when a container is gone but prints the others.
	out, _ := exec.CommandContext(ctx, m.dockerBin(), append([]string{"inspect", "--format",
		`{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{"\n"}}{{end}}{{end}}`}, ids...)...).Output()
	return strings.Fields(string(out))
}

// okeVolumePrefix is the name prefix of floci-oci's k3s data volumes
// ("floci-oci-<namespace>-oke-vol-<cluster OCID>"), which Docker creates
// unlabeled when the k3s container mounts them.
func okeVolumePrefix(ns string) string { return "floci-oci-" + ns + "-oke-vol-" }

// sandboxVolumes picks the volumes of the sandbox whose resource namespace
// is ns: labeled ones, unlabeled ones its spawned containers mounted whose
// name carries the namespace, and k3s volumes of its OKE clusters (cluster
// OCIDs start with "ocid1.", which no workflow ID contains, so a longer
// namespace "<ns>-x" never matches). Anything else is left alone.
func sandboxVolumes(ns string, labeled, mounted, named []string) []string {
	out := slices.Clone(labeled)
	for _, v := range mounted {
		if strings.Contains(v, "-"+ns+"-") {
			out = append(out, v)
		}
	}
	for _, v := range named {
		if strings.HasPrefix(v, okeVolumePrefix(ns)+"ocid1.") {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Remove deletes every container, the network, emulator data and the
// workdir of the sandbox. An active run is canceled first.
func (m *Manager) Remove(ctx context.Context, wfID string) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	m.mu.Lock()
	active := sb.active
	m.mu.Unlock()
	if active != nil {
		_ = m.Cancel(active.run.ID)
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	sb.lifecycle.Lock()
	defer sb.lifecycle.Unlock()
	err := m.remove(ctx, sb)
	m.clearBusy(sb)
	if err != nil {
		m.failStatus(sb, err)
		return err
	}
	m.setStatus(sb, model.SandboxStatus{WorkflowID: wfID, State: model.SandboxAbsent, Emulators: []model.EmulatorStatus{}})
	return nil
}

func (m *Manager) remove(ctx context.Context, sb *sandbox) error {
	wfID := sb.id
	m.progress(sb, "", "Removing sandbox")
	pod, emus, tf, err := m.sandboxContainers(ctx, wfID)
	if err != nil {
		return err
	}
	var names []string
	for _, c := range emus {
		names = append(names, c.Names)
	}
	for _, c := range tf {
		names = append(names, c.ID)
	}
	// Emulators first so they cannot respawn containers while we clean up.
	if err := m.removeContainers(ctx, names...); err != nil {
		return err
	}
	if err := m.removeSpawned(ctx, wfID); err != nil {
		return err
	}
	if pod != nil {
		if err := m.removeContainers(ctx, pod.Names); err != nil {
			return err
		}
	}
	// Floci attaches the pod to the networks it creates (e.g. per-VPC networks
	// of EC2), so they can only go once the pod is gone.
	out, err := m.docker(ctx, "network", "ls", "-q", "--filter", "label=floci_namespace="+networkName(wfID))
	if err != nil {
		return err
	}
	nets := strings.Fields(out)
	if ok, err := m.networkExists(ctx, networkName(wfID)); err != nil {
		return err
	} else if ok {
		nets = append(nets, networkName(wfID))
	}
	for _, n := range nets {
		if err := m.removeNetwork(ctx, n); err != nil {
			return err
		}
	}
	m.mu.Lock()
	sb.ports = nil
	m.mu.Unlock()
	if err := m.wipeDir(ctx, filepath.Join(m.wfDir(wfID), "emulators"), false); err != nil {
		return err
	}
	return m.wipeDir(ctx, m.WorkDir(wfID), false)
}

// removeNetwork deletes a network, retrying while endpoints of just-removed
// containers linger.
func (m *Manager) removeNetwork(ctx context.Context, name string) error {
	for attempt := 0; ; attempt++ {
		_, err := m.docker(ctx, "network", "rm", name)
		if err == nil {
			return nil
		}
		if attempt == 20 || !strings.Contains(err.Error(), "active endpoints") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// wipeDir deletes dir (or only its contents when keep is set). Emulators
// re-own their data directory to their container user, which the host user
// cannot delete on Linux; a throwaway container removes what is left.
func (m *Manager) wipeDir(ctx context.Context, dir string, keep bool) error {
	remove := func() error {
		if !keep {
			return os.RemoveAll(dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := remove(); err == nil {
		return nil
	}
	if _, err := m.docker(ctx, "run", "--rm", "-v", dir+":/wipe", podImage,
		"sh", "-c", "rm -rf /wipe/* /wipe/.[!.]* /wipe/..?*"); err != nil {
		return err
	}
	return remove()
}

// EmulatorURL returns the host base URL of the cloud's emulator.
func (m *Manager) EmulatorURL(wfID string, cloud model.Cloud) (string, bool) {
	e, ok := emulators[cloud]
	if !ok {
		return "", false
	}
	hp := m.hostPort(wfID, e.port)
	if hp == 0 {
		return "", false
	}
	return hostBase(hp), true
}

// hostPort maps a native emulator port to its host port, reading it from
// docker when not cached.
func (m *Manager) hostPort(wfID string, port int) int {
	if checkID(wfID) != nil {
		return 0
	}
	sb := m.sandbox(wfID)
	m.mu.Lock()
	hp, cached := sb.ports[port]
	known := sb.ports != nil
	m.mu.Unlock()
	if cached || known {
		return hp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ports, err := m.hostPorts(ctx, podName(wfID))
	if err != nil && strings.Contains(err.Error(), "No such container") {
		// No pod, no ports until Start publishes them (it refreshes the
		// cache); without caching, every URL lookup would spawn docker.
		ports, err = map[int]int{}, nil
	}
	if err != nil {
		return 0
	}
	m.mu.Lock()
	sb.ports = ports
	m.mu.Unlock()
	return ports[port]
}
