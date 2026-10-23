package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker workflows run with `docker compose` on a copy of the workflow's
// files in <home>/workflows/<wf>/compose. Every object the project creates
// belongs to compose project model.ComposeProjectName(wf) and carries the label
// ComposeLabel=<wf> (added through an override file next to the copy), and
// Nimbo only ever lists or removes objects matching both.
const (
	ComposeLabel        = "com.nimbo.workflow"
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
	composeWorkDir      = "compose"
	composeOverride     = ".nimbo/compose.override.yaml"
	composeRemapsFile   = ".nimbo/ports.json"

	// DefaultComposeWait bounds `up --wait` when the request has no timeout.
	DefaultComposeWait = 5 * time.Minute
)

// ErrComposeMissing is returned when the docker CLI has no compose v2 plugin.
var ErrComposeMissing = errors.New("Docker Compose v2 is not installed: the `docker compose` command is missing (install Docker Desktop or OrbStack, or the docker-compose-plugin package)")

// InvalidComposeError is a compose request the workflow's files or the
// request's fields make impossible (invalid files, unknown service, ...).
type InvalidComposeError struct{ Msg string }

func (e *InvalidComposeError) Error() string { return e.Msg }

func invalidCompose(format string, args ...any) error {
	return &InvalidComposeError{Msg: fmt.Sprintf(format, args...)}
}

// PortConflictError is returned by ComposeRun for an up whose fixed host
// ports are taken by other programs, unless ComposeRunRequest.RemapPorts.
type PortConflictError struct{ Conflicts []model.PortConflict }

func (e *PortConflictError) Error() string {
	parts := make([]string, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		s := fmt.Sprintf("%d/%s (%s)", c.HostPort, c.Protocol, c.Service)
		if c.Holder != "" {
			s += " held by " + c.Holder
		}
		parts = append(parts, s)
	}
	return "host ports already in use: " + strings.Join(parts, ", ") + "; free them or start with remapped ports"
}

// ComposeRunRequest describes one docker compose run.
type ComposeRunRequest struct {
	Workflow  *model.Workflow
	Action    model.RunAction // up | down | build | pull | restart
	SourceDir string          // the workflow's files (store.AssetsDir), copied into the work dir
	Files     []string        // compose files in -f order, relative to SourceDir ("" = compose defaults)
	Profiles  []string        // active compose profiles
	Service   string          // up/build/pull/restart: only this service ("" = all)
	// RemoveVolumes makes down delete the project's named volumes.
	RemoveVolumes bool
	// RemapPorts lets up move busy fixed host ports to free ones.
	RemapPorts bool
	Timeout    time.Duration // up --wait-timeout (0 = DefaultComposeWait)
	// BuildHints describe the Dockerfiles of services that build, by
	// service, for the build metrics recorded after build and up runs.
	BuildHints map[string]BuildHint
}

// ComposeService is a service of the workflow's compose files, as the
// status lists it even without containers.
type ComposeService struct {
	Name  string
	Image string
}

var validService = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,100}$`)

func (m *Manager) composeDir(wfID string) string { return filepath.Join(m.wfDir(wfID), composeWorkDir) }

// composeCmd builds docker CLI arguments for one compose project.
type composeCmd struct {
	project  string
	dir      string   // project directory (absolute)
	files    []string // relative to dir
	profiles []string
	override string // relative to dir; "" = none
}

func (c composeCmd) args(sub ...string) []string {
	// The project directory is the first file's folder, docker compose's
	// default: relative paths and .env resolve against it (a workflow may
	// keep its compose file at docker/compose.yaml building ../services/api).
	project := c.dir
	if len(c.files) > 0 {
		project = filepath.Join(c.dir, filepath.Dir(filepath.FromSlash(c.files[0])))
	}
	a := []string{"compose", "--ansi", "never", "--progress", "plain", "-p", c.project, "--project-directory", project}
	for _, f := range c.files {
		a = append(a, "-f", filepath.Join(c.dir, filepath.FromSlash(f)))
	}
	if c.override != "" {
		a = append(a, "-f", filepath.Join(c.dir, filepath.FromSlash(c.override)))
	}
	for _, p := range c.profiles {
		a = append(a, "--profile", p)
	}
	return append(a, sub...)
}

// actionArgs is the compose subcommand of a run action.
func actionArgs(req ComposeRunRequest) ([]string, error) {
	var a []string
	switch req.Action {
	case model.ActionUp:
		wait := req.Timeout
		if wait <= 0 {
			wait = DefaultComposeWait
		}
		a = []string{"up", "--detach", "--build", "--wait", "--wait-timeout", strconv.Itoa(int(wait.Round(time.Second) / time.Second)), "--remove-orphans"}
	case model.ActionDown:
		a = []string{"down", "--remove-orphans"}
		if req.RemoveVolumes {
			a = append(a, "--volumes")
		}
		return a, nil
	case model.ActionBuild:
		a = []string{"build"}
	case model.ActionPull:
		a = []string{"pull", "--ignore-buildable"}
	case model.ActionRestart:
		a = []string{"restart"}
	default:
		return nil, fmt.Errorf("unknown compose action %q", req.Action)
	}
	if req.Service != "" {
		a = append(a, req.Service)
	}
	return a, nil
}

// defaultComposeFiles is what compose picks without -f: the first of the
// canonical names plus its override file.
func defaultComposeFiles(dir string) []string {
	for _, base := range []string{"compose", "docker-compose"} {
		for _, ext := range []string{".yaml", ".yml"} {
			if fileExists(filepath.Join(dir, base+ext)) {
				files := []string{base + ext}
				for _, oext := range []string{".yaml", ".yml"} {
					if o := base + ".override" + oext; fileExists(filepath.Join(dir, o)) {
						files = append(files, o)
						break
					}
				}
				return files
			}
		}
	}
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// composeEnv is the process environment of compose commands: COMPOSE_*
// variables of the daemon's environment would add files, profiles or a
// project name behind Nimbo's back.
func composeEnv() []string {
	env := os.Environ()
	return slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "COMPOSE_") })
}

// composeAvailable checks for the compose v2 plugin once per docker binary.
func (m *Manager) composeAvailable(ctx context.Context) error {
	bin := m.dockerBin()
	composeCheck.Lock()
	defer composeCheck.Unlock()
	if composeCheck.ok[bin] {
		return nil
	}
	if _, err := m.docker(ctx, "compose", "version", "--short"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if derr := m.DockerAvailable(ctx); derr != nil {
			return fmt.Errorf("Docker is not running: %w", derr)
		}
		return ErrComposeMissing
	}
	composeCheck.ok[bin] = true
	return nil
}

var composeCheck = struct {
	sync.Mutex
	ok map[string]bool
}{ok: map[string]bool{}}

// ComposeRun starts a docker compose run asynchronously and returns it
// queued. The files are copied into the work dir and, for up, host ports are
// checked first: busy ones fail with *PortConflictError (no run is created)
// unless RemapPorts. Only one run per workflow may be active.
func (m *Manager) ComposeRun(ctx context.Context, req ComposeRunRequest) (*model.Run, error) {
	if req.Workflow == nil {
		return nil, errors.New("run: workflow is required")
	}
	wfID := req.Workflow.ID
	if err := checkID(wfID); err != nil {
		return nil, err
	}
	sub, err := actionArgs(req)
	if err != nil {
		return nil, err
	}
	if req.Service != "" && (req.Action == model.ActionDown || !validService.MatchString(req.Service)) {
		return nil, invalidCompose("invalid service %q for %s", req.Service, req.Action)
	}
	if err := m.composeAvailable(ctx); err != nil {
		return nil, err
	}
	m.ensureHistory(wfID)
	sb := m.sandbox(wfID)
	rs := &runState{
		run: model.Run{
			ID: newRunID(), WorkflowID: wfID, Action: req.Action, Service: req.Service,
			RemoveVolumes: req.Action == model.ActionDown && req.RemoveVolumes,
			Status:        model.RunQueued, CreatedAt: time.Now().UTC(), Log: []model.LogLine{},
		},
		sb:   sb,
		done: make(chan struct{}),
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel

	// Claim the workflow before touching the work dir; the run is listed
	// only once the preparation succeeded.
	m.mu.Lock()
	if sb.active != nil {
		m.mu.Unlock()
		cancel()
		return nil, ErrRunActive
	}
	sb.active = rs
	m.mu.Unlock()
	prep, err := m.prepareCompose(ctx, req)
	m.mu.Lock()
	if err != nil {
		sb.active = nil
		m.mu.Unlock()
		cancel()
		return nil, err
	}
	m.runs[rs.run.ID] = rs
	snap := m.snapshot(rs, false)
	m.mu.Unlock()

	m.publish(model.EventRunStatus, wfID, snap)
	go m.execute(runCtx, rs, func(ctx context.Context) error {
		for _, r := range prep.newRemaps {
			m.logLine(rs, "system", fmt.Sprintf("Host port %d (%s) is busy: publishing %s:%d/%s on %d instead. Your compose files are unchanged.", r.HostPort, r.Service, r.Service, r.ContainerPort, r.Protocol, r.Port))
		}
		for _, r := range prep.keptRemaps {
			m.logLine(rs, "system", fmt.Sprintf("Host port %d (%s) is still busy: keeping %d.", r.HostPort, r.Service, r.Port))
		}
		if req.Action == model.ActionUp || req.Action == model.ActionRestart {
			m.mu.Lock()
			sb.composeServices = prep.services
			m.mu.Unlock()
			m.watchCompose(sb) // live container states while the run waits
		}
		return m.composeSteps(ctx, rs, prep.cmd, sub)
	}, func(model.Run) {
		sctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if req.Action == model.ActionUp || req.Action == model.ActionBuild {
			m.recordBuild(sctx, rs, prep.built, prep.images, req.BuildHints)
		}
		m.refreshCompose(sctx, sb, prep.services)
	})
	return &snap, nil
}

// composePrep is the outcome of preparing a run's work dir.
type composePrep struct {
	cmd        composeCmd
	services   []ComposeService
	newRemaps  []model.PortRemap
	keptRemaps []model.PortRemap
	// built are the services the run builds; images their image names.
	built  []string
	images map[string]string
}

// prepareCompose copies the files into the work dir, reads the effective
// configuration, plans host ports (up only) and writes the override file.
func (m *Manager) prepareCompose(ctx context.Context, req ComposeRunRequest) (*composePrep, error) {
	wfID := req.Workflow.ID
	work := m.composeDir(wfID)
	if req.SourceDir == "" {
		return nil, invalidCompose("the workflow has no files")
	}
	if _, err := syncWorkdir(work, nil, req.SourceDir); err != nil {
		return nil, fmt.Errorf("copy the workflow's files: %w", err)
	}
	cmd, err := m.composeCommand(wfID, work, req.Files, req.Profiles)
	if err != nil {
		return nil, err
	}
	cfg, err := m.composeConfig(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if req.Service != "" {
		if _, ok := cfg.Services[req.Service]; !ok {
			return nil, invalidCompose("service %q is not in the compose files", req.Service)
		}
	}
	prep := &composePrep{cmd: cmd, services: cfg.services(), images: map[string]string{}}
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[name]
		if len(svc.Build) == 0 || string(svc.Build) == "null" || req.Service != "" && name != req.Service {
			continue
		}
		prep.built = append(prep.built, name)
		prep.images[name] = svc.Image
		if svc.Image == "" {
			prep.images[name] = cmd.project + "-" + name
		}
	}
	saved := loadRemaps(work)
	remaps := saved
	if req.Action == model.ActionUp {
		insp, err := m.composeContainers(ctx, wfID)
		if err != nil {
			return nil, err
		}
		plan := planPorts(cfg, ownPorts(insp), saved, portProber{})
		if len(plan.conflicts) > 0 && !req.RemapPorts {
			return nil, &PortConflictError{Conflicts: plan.conflicts}
		}
		remaps = plan.remaps(req.RemapPorts)
		for _, r := range remaps {
			if slices.Contains(saved, r) {
				prep.keptRemaps = append(prep.keptRemaps, r)
			} else {
				prep.newRemaps = append(prep.newRemaps, r)
			}
		}
		if err := saveRemaps(work, remaps); err != nil {
			return nil, err
		}
	}
	if err := writeFile(filepath.Join(work, filepath.FromSlash(composeOverride)), overrideYAML(wfID, cfg, remaps)); err != nil {
		return nil, err
	}
	prep.cmd.override = composeOverride
	return prep, nil
}

// composeCommand resolves the -f list against the work dir.
func (m *Manager) composeCommand(wfID, dir string, files, profiles []string) (composeCmd, error) {
	for _, f := range files {
		p := filepath.ToSlash(filepath.Clean(filepath.FromSlash(f)))
		if f == "" || filepath.IsAbs(f) || p == ".." || strings.HasPrefix(p, "../") {
			return composeCmd{}, invalidCompose("invalid compose file path %q", f)
		}
	}
	if len(files) == 0 {
		files = defaultComposeFiles(dir)
	}
	if len(files) == 0 {
		return composeCmd{}, invalidCompose("no compose file: add a compose.yaml to the workflow")
	}
	for _, p := range profiles {
		if !validService.MatchString(p) {
			return composeCmd{}, invalidCompose("invalid profile %q", p)
		}
	}
	return composeCmd{project: model.ComposeProjectName(wfID), dir: dir, files: files, profiles: profiles}, nil
}

// composeSteps runs the compose subcommand, streaming its output into the
// run log.
func (m *Manager) composeSteps(ctx context.Context, rs *runState, cmd composeCmd, sub []string) error {
	m.logLine(rs, "system", "$ docker compose -p "+cmd.project+" "+strings.Join(sub, " "))
	code, err := m.composeExec(ctx, rs, cmd.args(sub...))
	if err != nil {
		return err
	}
	m.setExit(rs, code)
	if code != 0 {
		return fmt.Errorf("docker compose %s exited with code %d", sub[0], code)
	}
	return nil
}

// composeExec runs the docker CLI with args in the project directory. A
// canceled ctx interrupts compose (SIGINT, so it stops what it started) and
// kills it 15 s later.
func (m *Manager) composeExec(ctx context.Context, rs *runState, args []string) (int, error) {
	cmd := exec.CommandContext(ctx, m.dockerBin(), args...)
	cmd.Env = composeEnv()
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = 15 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	wg.Go(func() { scanLines(stdout, func(line string) { m.logLine(rs, "stdout", line) }) })
	wg.Go(func() { scanLines(stderr, func(line string) { m.logLine(rs, "stderr", line) }) })
	wg.Wait()
	err = cmd.Wait()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// ---------------------------------------------------------------------------
// Effective configuration (`docker compose config --format json`)

type composeConfig struct {
	Services map[string]configService `json:"services"`
	Networks map[string]configObject  `json:"networks"`
	Volumes  map[string]configObject  `json:"volumes"`
}

type configService struct {
	Image       string             `json:"image"`
	Build       json.RawMessage    `json:"build"`
	Ports       []configPort       `json:"ports"`
	Environment map[string]*string `json:"environment"`
}

type configObject struct {
	External bool `json:"external"`
}

// configPort is a port in compose's normalized long syntax.
type configPort struct {
	Name        string `json:"name,omitempty"`
	Mode        string `json:"mode,omitempty"`
	HostIP      string `json:"host_ip,omitempty"`
	Target      int    `json:"target"`
	Published   string `json:"published,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	AppProtocol string `json:"app_protocol,omitempty"`
}

func (p configPort) proto() string {
	if p.Protocol == "" {
		return "tcp"
	}
	return strings.ToLower(p.Protocol)
}

// fixedPort is the published host port, 0 when Docker chooses it (empty or
// a range).
func (p configPort) fixedPort() int {
	n, err := strconv.Atoi(p.Published)
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}

func (c *composeConfig) services() []ComposeService {
	out := make([]ComposeService, 0, len(c.Services))
	for _, name := range slices.Sorted(maps.Keys(c.Services)) {
		out = append(out, ComposeService{Name: name, Image: c.Services[name].Image})
	}
	return out
}

// composeConfig reads the effective configuration of cmd's files (without
// the override file).
func (m *Manager) composeConfig(ctx context.Context, cmd composeCmd) (*composeConfig, error) {
	cmd.override = ""
	c := exec.CommandContext(ctx, m.dockerBin(), cmd.args("config", "--format", "json")...)
	c.Env = composeEnv()
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, invalidCompose("the compose files are not valid: %s", msg)
	}
	var cfg composeConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, fmt.Errorf("parse docker compose config: %w", err)
	}
	return &cfg, nil
}

// overrideYAML is the override file of Nimbo's runs: the workflow label on
// every service, built image, network and volume the project creates, and
// the remapped ports (the service's whole port list, replaced with
// !override). Values are JSON strings, which YAML reads verbatim.
func overrideYAML(wfID string, cfg *composeConfig, remaps []model.PortRemap) []byte {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	label := "      labels: {" + q(ComposeLabel) + ": " + q(wfID) + "}\n"
	var b strings.Builder
	b.WriteString("# Written by Nimbo for its runs of this project; the workflow's compose files are not changed.\n")
	b.WriteString("services:\n")
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[name]
		b.WriteString("  " + q(name) + ":\n")
		b.WriteString(strings.Replace(label, "      ", "    ", 1))
		if len(svc.Build) > 0 && string(svc.Build) != "null" {
			b.WriteString("    build:\n" + label)
		}
		var mine []model.PortRemap
		for _, r := range remaps {
			if r.Service == name {
				mine = append(mine, r)
			}
		}
		if len(mine) == 0 {
			continue
		}
		b.WriteString("    ports: !override\n")
		for _, p := range svc.Ports {
			for _, r := range mine {
				if r.ContainerPort == p.Target && r.Protocol == p.proto() && r.HostPort == p.fixedPort() {
					p.Published = strconv.Itoa(r.Port)
					break
				}
			}
			data, _ := json.Marshal(p)
			b.WriteString("      - " + string(data) + "\n")
		}
	}
	for _, kind := range []struct {
		key  string
		objs map[string]configObject
	}{{"networks", cfg.Networks}, {"volumes", cfg.Volumes}} {
		var names []string
		for _, n := range slices.Sorted(maps.Keys(kind.objs)) {
			if !kind.objs[n].External {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			continue
		}
		b.WriteString(kind.key + ":\n")
		for _, n := range names {
			b.WriteString("  " + q(n) + ":\n" + strings.Replace(label, "      ", "    ", 1))
		}
	}
	return []byte(b.String())
}

// ---------------------------------------------------------------------------
// Containers and status

// inspected is the subset of `docker inspect` used for the status.
type inspected struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"`
	State struct {
		Status     string `json:"Status"`
		ExitCode   int    `json:"ExitCode"`
		OOMKilled  bool   `json:"OOMKilled"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
			Log    []struct {
				Start    string `json:"Start"`
				End      string `json:"End"`
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	HostConfig struct {
		NanoCpus  int64 `json:"NanoCpus"`
		CPUQuota  int64 `json:"CpuQuota"`
		CPUPeriod int64 `json:"CpuPeriod"`
		Memory    int64 `json:"Memory"`
	} `json:"HostConfig"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func (c inspected) service() string { return c.Config.Labels[composeServiceLabel] }

// container converts c to the status shape: ports deduplicated across the
// IPv4/IPv6 bindings of one host port, sorted.
func (c inspected) container() model.ComposeContainer {
	out := model.ComposeContainer{
		ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), State: c.State.Status, ExitCode: c.State.ExitCode,
		Restarts: c.RestartCount, Image: c.Config.Image, ImageID: c.Image, Ports: []model.ComposePort{},
		OOMKilled: c.State.OOMKilled, CPULimit: c.cpuLimit(), MemLimit: c.HostConfig.Memory,
	}
	if c.State.Status == "exited" || c.State.Status == "dead" {
		if t, err := time.Parse(time.RFC3339Nano, c.State.FinishedAt); err == nil && t.Year() > 1 {
			t = t.UTC()
			out.FinishedAt = &t
		}
	}
	if c.State.Health != nil {
		out.Health = c.State.Health.Status
	}
	if t, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil && t.Year() > 1 {
		t = t.UTC()
		out.StartedAt = &t
	}
	seen := map[string]bool{}
	for key, binds := range c.NetworkSettings.Ports {
		port, proto, _ := strings.Cut(key, "/")
		cp, err := strconv.Atoi(port)
		if err != nil {
			continue
		}
		for _, bnd := range binds {
			hp, err := strconv.Atoi(bnd.HostPort)
			if err != nil || hp == 0 {
				continue
			}
			k := fmt.Sprintf("%d/%s/%d", cp, proto, hp)
			if seen[k] {
				continue
			}
			seen[k] = true
			p := model.ComposePort{Container: cp, Protocol: proto, HostIP: bnd.HostIP, Host: hp}
			if proto == "tcp" && hostReachable(bnd.HostIP) {
				p.URL = fmt.Sprintf("http://127.0.0.1:%d/", hp)
			}
			out.Ports = append(out.Ports, p)
		}
	}
	slices.SortFunc(out.Ports, func(a, b model.ComposePort) int {
		if a.Container != b.Container {
			return a.Container - b.Container
		}
		if a.Protocol != b.Protocol {
			return strings.Compare(a.Protocol, b.Protocol)
		}
		return a.Host - b.Host
	})
	return out
}

// nonHTTPPorts are well-known container ports of protocols other than HTTP;
// every other published TCP port is offered as an HTTP endpoint.
var nonHTTPPorts = map[int]bool{
	22: true, 25: true, 53: true, 465: true, 587: true, 1433: true, 1521: true, 1883: true, 2181: true,
	3306: true, 4222: true, 5432: true, 5671: true, 5672: true, 6379: true, 6380: true, 7000: true,
	7001: true, 9042: true, 9092: true, 9093: true, 11211: true, 26379: true, 27017: true, 61616: true,
}

// ComposeHTTPPort reports whether a published container port is offered as
// an HTTP endpoint (Endpoints cards and load test suggestions).
func ComposeHTTPPort(p model.ComposePort) bool {
	return p.Protocol == "tcp" && p.URL != "" && !nonHTTPPorts[p.Container]
}

// hostReachable reports whether a binding address answers on 127.0.0.1.
func hostReachable(ip string) bool {
	switch ip {
	case "", "0.0.0.0", "::", "127.0.0.1", "localhost":
		return true
	}
	return strings.HasPrefix(ip, "127.")
}

// composeContainers inspects every container of the workflow's project
// (both the project and the workflow label must match).
func (m *Manager) composeContainers(ctx context.Context, wfID string) ([]inspected, error) {
	out, err := m.docker(ctx, "ps", "-aq", "--no-trunc",
		"--filter", "label="+composeProjectLabel+"="+model.ComposeProjectName(wfID),
		"--filter", "label="+ComposeLabel+"="+wfID)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil, nil
	}
	data, err := m.docker(ctx, append([]string{"inspect", "--type", "container"}, ids...)...)
	if err != nil {
		if !isNoSuch(err) {
			return nil, err
		}
		// A container went away between ps and inspect: ask again once.
		return m.composeContainers(ctx, wfID)
	}
	return parseInspect([]byte(data))
}

func parseInspect(data []byte) ([]inspected, error) {
	var cs []inspected
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("parse docker inspect output: %w", err)
	}
	return cs, nil
}

// containerState maps one container to a ComposeState.
func containerState(c model.ComposeContainer) model.ComposeState {
	switch c.State {
	case "running":
		switch c.Health {
		case "starting":
			return model.ComposeStarting
		case "unhealthy":
			return model.ComposeError
		}
		return model.ComposeRunning
	case "created":
		return model.ComposeStarting
	case "restarting":
		if c.Restarts >= 3 {
			return model.ComposeError // crash loop
		}
		return model.ComposeStarting
	case "exited":
		if c.ExitCode != 0 {
			return model.ComposeError
		}
		return model.ComposeStopped
	case "dead":
		return model.ComposeError
	}
	return model.ComposeStopped // paused, removing
}

var stateRank = map[model.ComposeState]int{
	model.ComposeAbsent: 0, model.ComposeStopped: 1, model.ComposeRunning: 2, model.ComposeStarting: 3, model.ComposeError: 4,
}

var healthRank = map[string]int{"": 0, "healthy": 1, "starting": 2, "unhealthy": 3}

// buildStatus groups containers by service. Every service of services is
// listed (absent without containers); containers of other services (e.g.
// removed from the files) are listed too.
func buildStatus(wfID string, services []ComposeService, cs []inspected, remaps []model.PortRemap) model.ComposeStatus {
	st := model.ComposeStatus{
		WorkflowID: wfID, Project: model.ComposeProjectName(wfID), State: model.ComposeAbsent,
		Services: []model.ComposeServiceStatus{}, Remaps: remaps,
	}
	if st.Remaps == nil {
		st.Remaps = []model.PortRemap{}
	}
	idx := map[string]int{}
	add := func(name, image string) int {
		if i, ok := idx[name]; ok {
			return i
		}
		idx[name] = len(st.Services)
		st.Services = append(st.Services, model.ComposeServiceStatus{
			Name: name, NodeID: model.ComposeServiceID(name), State: model.ComposeAbsent, Image: image, Containers: []model.ComposeContainer{},
		})
		return idx[name]
	}
	for _, s := range services {
		add(s.Name, s.Image)
	}
	for _, c := range cs {
		name := c.service()
		if name == "" {
			continue
		}
		i := add(name, "")
		svc := &st.Services[i]
		cc := c.container()
		svc.Containers = append(svc.Containers, cc)
		if s := containerState(cc); stateRank[s] > stateRank[svc.State] {
			svc.State = s
		}
		if healthRank[cc.Health] > healthRank[svc.Health] {
			svc.Health = cc.Health
		}
		if stateRank[svc.State] > stateRank[st.State] {
			st.State = svc.State
		}
	}
	for i := range st.Services {
		slices.SortFunc(st.Services[i].Containers, func(a, b model.ComposeContainer) int { return strings.Compare(a.Name, b.Name) })
	}
	slices.SortFunc(st.Services, func(a, b model.ComposeServiceStatus) int { return strings.Compare(a.Name, b.Name) })
	st.Problems = composeProblems(st, cs)
	return st
}

// ComposeRuntime is the live state of a docker workflow's project plus the
// environment of each service's first container (values in clear: for
// connection snippets, never serialize as is).
type ComposeRuntime struct {
	Status model.ComposeStatus
	Env    map[string]map[string]string // service -> KEY -> value
}

// ComposeInspect reads the project's containers. services are the services
// of the workflow's files (listed even when absent). Without Docker the
// status is absent with a message, not an error.
func (m *Manager) ComposeInspect(ctx context.Context, wfID string, services []ComposeService) (ComposeRuntime, error) {
	if err := checkID(wfID); err != nil {
		return ComposeRuntime{}, err
	}
	sb := m.sandbox(wfID)
	m.mu.Lock()
	sb.composeServices = services
	m.mu.Unlock()
	cs, err := m.composeContainers(ctx, wfID)
	remaps := loadRemaps(m.composeDir(wfID))
	if err != nil {
		if ctx.Err() != nil {
			return ComposeRuntime{}, ctx.Err()
		}
		st := buildStatus(wfID, services, nil, remaps)
		st.Message = "Docker is not available: " + err.Error()
		st.UpdatedAt = time.Now().UTC()
		return ComposeRuntime{Status: st, Env: map[string]map[string]string{}}, nil
	}
	rt := ComposeRuntime{Status: buildStatus(wfID, services, cs, remaps), Env: map[string]map[string]string{}}
	rt.Status.UpdatedAt = time.Now().UTC()
	m.annotateStartup(sb, &rt.Status, cs)
	for _, c := range cs {
		svc := c.service()
		if _, ok := rt.Env[svc]; ok || svc == "" {
			continue
		}
		env := map[string]string{}
		for _, kv := range c.Config.Env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		rt.Env[svc] = env
	}
	m.recordCompose(sb, rt.Status)
	m.syncStats(sb, cs)
	if len(cs) > 0 {
		m.watchCompose(sb)
	}
	return rt, nil
}

// ComposeStatus is ComposeInspect's status.
func (m *Manager) ComposeStatus(ctx context.Context, wfID string, services []ComposeService) (model.ComposeStatus, error) {
	rt, err := m.ComposeInspect(ctx, wfID, services)
	return rt.Status, err
}

// recordCompose stores st and publishes it when it changed.
func (m *Manager) recordCompose(sb *sandbox, st model.ComposeStatus) {
	cmp := st
	cmp.UpdatedAt = time.Time{}
	m.mu.Lock()
	changed := sb.composeLast == nil || !composeEqual(*sb.composeLast, cmp)
	sb.composeLast = &cmp
	m.mu.Unlock()
	if changed {
		m.publish(model.EventComposeStatus, sb.id, st)
	}
}

func composeEqual(a, b model.ComposeStatus) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// refreshCompose re-reads the status after a run or a container event.
func (m *Manager) refreshCompose(ctx context.Context, sb *sandbox, services []ComposeService) {
	if services == nil {
		m.mu.Lock()
		services = sb.composeServices
		m.mu.Unlock()
	}
	_, _ = m.ComposeInspect(ctx, sb.id, services)
}

// watchCompose follows the project's container events (one `docker events`
// per workflow) and publishes compose.status on changes, until the project
// has no containers left or Docker stops answering.
func (m *Manager) watchCompose(sb *sandbox) {
	m.mu.Lock()
	if sb.composeWatch != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sb.composeWatch = cancel
	m.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			sb.composeWatch = nil
			m.mu.Unlock()
		}()
		wfID := sb.id
		cmd := exec.CommandContext(ctx, m.dockerBin(), "events", "--format", "{{json .}}",
			"--filter", "type=container",
			"--filter", "label="+composeProjectLabel+"="+model.ComposeProjectName(wfID),
			"--filter", "label="+ComposeLabel+"="+wfID)
		out, err := cmd.StdoutPipe()
		if err != nil || cmd.Start() != nil {
			return
		}
		events := make(chan struct{}, 1)
		go func() {
			scanLines(out, func(line string) {
				sb.perf.noteEvent(line)
				select {
				case events <- struct{}{}:
				default:
				}
			})
			close(events)
		}()
		for range events {
			// Debounce bursts (create+start+health of every replica).
			time.Sleep(300 * time.Millisecond)
			select {
			case <-events:
			default:
			}
			rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
			m.mu.Lock()
			services := sb.composeServices
			m.mu.Unlock()
			cs, err := m.composeContainers(rctx, wfID)
			if err == nil {
				st := buildStatus(wfID, services, cs, loadRemaps(m.composeDir(wfID)))
				st.UpdatedAt = time.Now().UTC()
				m.annotateStartup(sb, &st, cs)
				m.recordCompose(sb, st)
				m.syncStats(sb, cs)
			} else {
				m.syncStats(sb, nil)
			}
			rcancel()
			if err != nil || len(cs) == 0 {
				break
			}
		}
		cancel()
		_ = cmd.Wait()
	}()
}

func (m *Manager) stopComposeWatch(sb *sandbox) {
	m.mu.Lock()
	cancel := sb.composeWatch
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---------------------------------------------------------------------------
// Down / remove

// ComposeDown removes the project's containers and networks (and named
// volumes with volumes) outside a run, e.g. when the workflow is archived.
// ErrRunActive while a run is active.
func (m *Manager) ComposeDown(ctx context.Context, wfID string, volumes bool) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	if m.activeRun(sb) {
		return ErrRunActive
	}
	if err := m.composeDown(ctx, wfID, volumes, false); err != nil {
		return err
	}
	m.refreshCompose(ctx, sb, nil)
	return nil
}

// ComposeRemove deletes everything of the workflow's project — containers,
// networks, named volumes and the images compose built for it — and the work
// dir, when the workflow is deleted. An active run is canceled first.
func (m *Manager) ComposeRemove(ctx context.Context, wfID string) error {
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
	if err := m.composeDown(ctx, wfID, true, true); err != nil {
		return err
	}
	m.stopComposeWatch(sb)
	m.stopStats(sb)
	if err := m.removePerf(wfID); err != nil {
		return err
	}
	if err := os.RemoveAll(m.composeDir(wfID)); err != nil {
		return err
	}
	m.mu.Lock()
	sb.composeLast = nil
	m.mu.Unlock()
	return nil
}

// composeDown runs `compose down` by project name (compose finds the
// project's containers, networks and volumes by its labels, so the files
// are not needed). With images it then deletes the images built for the
// workflow (labelled through the override's build labels).
func (m *Manager) composeDown(ctx context.Context, wfID string, volumes, images bool) error {
	cs, err := m.composeContainers(ctx, wfID)
	if err != nil {
		return err
	}
	if len(cs) > 0 || m.composeLeftovers(ctx, wfID, volumes) {
		if err := m.composeAvailable(ctx); err != nil {
			return err
		}
		args := []string{"compose", "--ansi", "never", "--progress", "plain", "-p", model.ComposeProjectName(wfID), "down", "--remove-orphans"}
		if volumes {
			args = append(args, "--volumes")
		}
		c := exec.CommandContext(ctx, m.dockerBin(), args...)
		c.Env = composeEnv()
		c.Dir = os.TempDir() // never pick up a compose file from the daemon's cwd
		if out, err := c.CombinedOutput(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("docker compose down: %s", lastLines(string(out), 3))
		}
	}
	if !images {
		return nil
	}
	out, err := m.docker(ctx, "image", "ls", "--format", "{{.ID}} {{.Repository}}:{{.Tag}}", "--filter", "label="+ComposeLabel+"="+wfID)
	if err != nil {
		return err
	}
	// By reference, so an image tagged twice goes with both tags; untagged
	// ones by ID.
	var refs []string
	for line := range strings.Lines(out) {
		id, ref, _ := strings.Cut(strings.TrimSpace(line), " ")
		if strings.Contains(ref, "<none>") {
			ref = id
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	if refs = slices.Compact(slices.Sorted(slices.Values(refs))); len(refs) > 0 {
		if _, err := m.docker(ctx, append([]string{"image", "rm"}, refs...)...); err != nil && !isNoSuch(err) {
			return err
		}
	}
	return nil
}

// composeLeftovers reports labelled networks (and volumes) of the project.
func (m *Manager) composeLeftovers(ctx context.Context, wfID string, volumes bool) bool {
	kinds := []string{"network"}
	if volumes {
		kinds = append(kinds, "volume")
	}
	for _, kind := range kinds {
		out, err := m.docker(ctx, kind, "ls", "-q",
			"--filter", "label="+composeProjectLabel+"="+model.ComposeProjectName(wfID),
			"--filter", "label="+ComposeLabel+"="+wfID)
		if err != nil || strings.TrimSpace(out) != "" {
			return true
		}
	}
	return false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}
