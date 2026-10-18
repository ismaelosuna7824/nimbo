package sandbox

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// RunRequest describes one Terraform invocation.
type RunRequest struct {
	Workflow    *model.Workflow
	Action      model.RunAction
	Files       map[string][]byte // hclcodec.Export output
	AssetsDir   string            // copied into the workdir before the run ("" = none)
	Environment *model.Environment
}

type runState struct {
	run       model.Run // guarded by Manager.mu
	sb        *sandbox
	cancel    context.CancelFunc
	container string // current Terraform container
	canceled  bool
	secrets   []string
	done      chan struct{}
}

func newRunID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b[:])
}

var validRunID = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func (m *Manager) runsDir(wfID string) string { return filepath.Join(m.wfDir(wfID), "runs") }

// Run starts a Terraform run asynchronously and returns it in queued state.
// Only one run per workflow may be active.
func (m *Manager) Run(ctx context.Context, req RunRequest) (*model.Run, error) {
	if req.Workflow == nil {
		return nil, errors.New("run: workflow is required")
	}
	wfID := req.Workflow.ID
	if err := checkID(wfID); err != nil {
		return nil, err
	}
	switch req.Action {
	case model.ActionValidate, model.ActionPlan, model.ActionApply, model.ActionDestroy:
	default:
		return nil, fmt.Errorf("unknown run action %q", req.Action)
	}
	for _, c := range req.Workflow.Clouds {
		if _, ok := emulators[c]; !ok {
			return nil, fmt.Errorf("unknown cloud %q", c)
		}
	}
	m.ensureHistory(wfID)
	sb := m.sandbox(wfID)
	rs := &runState{
		run: model.Run{
			ID: newRunID(), WorkflowID: wfID, Action: req.Action,
			Status: model.RunQueued, CreatedAt: time.Now().UTC(), Log: []model.LogLine{},
		},
		sb:   sb,
		done: make(chan struct{}),
	}
	if req.Environment != nil {
		rs.run.Environment = req.Environment.Name
		for _, ev := range req.Environment.EnvVars {
			if ev.Secret && len(ev.Value) >= 4 {
				rs.secrets = append(rs.secrets, ev.Value)
			}
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel

	m.mu.Lock()
	if sb.active != nil {
		m.mu.Unlock()
		cancel()
		return nil, ErrRunActive
	}
	sb.active = rs
	m.runs[rs.run.ID] = rs
	snap := m.snapshot(rs, false)
	m.mu.Unlock()

	m.publish(model.EventRunStatus, wfID, snap)
	go m.execute(runCtx, rs, req)
	return &snap, nil
}

// snapshot copies the run; callers hold m.mu.
func (m *Manager) snapshot(rs *runState, withLog bool) model.Run {
	r := rs.run
	if withLog {
		r.Log = slices.Clone(rs.run.Log)
	} else {
		r.Log = nil
	}
	if r.Plan != nil {
		p := *r.Plan
		r.Plan = &p
	}
	return r
}

func (m *Manager) execute(ctx context.Context, rs *runState, req RunRequest) {
	defer close(rs.done)
	defer rs.cancel()

	m.mu.Lock()
	now := time.Now().UTC()
	rs.run.Status = model.RunRunning
	rs.run.StartedAt = &now
	snap := m.snapshot(rs, false)
	m.mu.Unlock()
	m.publish(model.EventRunStatus, rs.run.WorkflowID, snap)

	err := m.runSteps(ctx, rs, req)

	m.mu.Lock()
	end := time.Now().UTC()
	rs.run.EndedAt = &end
	var final string
	switch {
	case rs.canceled:
		rs.run.Status = model.RunCanceled
		final = "Run canceled."
	case err != nil:
		rs.run.Status = model.RunFailed
		rs.run.Error = err.Error()
		final = "Run failed: " + err.Error()
	default:
		rs.run.Status = model.RunSucceeded
		final = "Run succeeded."
	}
	m.mu.Unlock()
	m.logLine(rs, "system", final)

	m.mu.Lock()
	rs.sb.active = nil
	full := m.snapshot(rs, true)
	snap = m.snapshot(rs, false)
	m.mu.Unlock()
	if perr := m.persistRun(full); perr != nil {
		m.logLine(rs, "system", "Could not save run history: "+perr.Error())
	}
	m.publish(model.EventRunStatus, rs.run.WorkflowID, snap)

	if snap.Status == model.RunSucceeded && (snap.Action == model.ActionApply || snap.Action == model.ActionDestroy) {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if st, err := m.State(sctx, req.Workflow); err == nil {
			m.publish(model.EventStateUpdated, rs.run.WorkflowID, st)
		}
	}
}

func (m *Manager) runSteps(ctx context.Context, rs *runState, req RunRequest) error {
	wf := req.Workflow
	clouds := slices.Clone(wf.Clouds)
	slices.Sort(clouds)
	clouds = slices.Compact(clouds)

	if len(clouds) > 0 {
		names := make([]string, len(clouds))
		for i, c := range clouds {
			names[i] = string(c)
		}
		m.logLine(rs, "system", "Starting sandbox ("+strings.Join(names, ", ")+")")
	} else {
		m.logLine(rs, "system", "Starting sandbox")
	}
	if _, err := m.Start(ctx, wf.ID, clouds); err != nil {
		return fmt.Errorf("start sandbox: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	work := m.workDir(wf.ID)
	versions := readLockVersions(work)
	if err := syncWorkdir(work, runFiles(req, versions), req.AssetsDir); err != nil {
		return fmt.Errorf("prepare workdir: %w", err)
	}
	if slices.Contains(clouds, model.CloudAzure) {
		if err := m.writeAzureCert(ctx, wf.ID, work); err != nil {
			return err
		}
	}
	if slices.Contains(clouds, model.CloudOCI) {
		if err := writeOCIKey(work); err != nil {
			return err
		}
	}

	if err := m.ensureImage(ctx, m.terraformImage(), func(s string) { m.logLine(rs, "system", s) }); err != nil {
		return err
	}
	if err := m.ensurePluginCache(ctx); err != nil {
		return err
	}

	env, userEnv := runEnv(clouds, req.Environment, m.awsRegion())
	if len(userEnv) > 0 {
		keys := make([]string, len(userEnv))
		for i, kv := range userEnv {
			keys[i], _, _ = strings.Cut(kv, "=")
		}
		m.logLine(rs, "system", "Environment variables: "+strings.Join(keys, ", "))
	}

	m.logLine(rs, "system", "$ terraform init")
	// The shared plugin cache is not safe for concurrent writers.
	pluginCacheMu.Lock()
	code, err := m.terraform(ctx, rs, "init", env, userEnv, "init", "-input=false", "-no-color", "-upgrade=false")
	pluginCacheMu.Unlock()
	if err != nil {
		return err
	}
	if code != 0 {
		m.setExit(rs, code)
		return fmt.Errorf("terraform init exited with code %d", code)
	}
	if after := readLockVersions(work); after["azurerm"] != versions["azurerm"] {
		// The wiring file set depends only on the workflow, so rewriting in
		// place keeps the workdir manifest valid.
		for name, content := range wiringFiles(wf, after) {
			if err := writeFile(filepath.Join(work, name), content); err != nil {
				return err
			}
		}
	}

	var args []string
	switch req.Action {
	case model.ActionValidate:
		args = []string{"validate", "-no-color"}
	case model.ActionPlan:
		args = []string{"plan", "-input=false", "-no-color"}
	case model.ActionApply:
		args = []string{"apply", "-auto-approve", "-input=false", "-no-color"}
	case model.ActionDestroy:
		args = []string{"destroy", "-auto-approve", "-input=false", "-no-color"}
	}
	m.logLine(rs, "system", "$ terraform "+strings.Join(args, " "))
	code, err = m.terraform(ctx, rs, string(req.Action), env, userEnv, args...)
	if err != nil {
		return err
	}
	m.setExit(rs, code)
	if code != 0 {
		return fmt.Errorf("terraform %s exited with code %d", req.Action, code)
	}
	return nil
}

func (m *Manager) setExit(rs *runState, code int) {
	m.mu.Lock()
	rs.run.ExitCode = code
	m.mu.Unlock()
}

func readLockVersions(work string) map[string]string {
	src, err := os.ReadFile(filepath.Join(work, ".terraform.lock.hcl"))
	if err != nil {
		return map[string]string{}
	}
	return lockVersions(src)
}

// runFiles is the complete file set of a run: the exported Terraform, the
// environment's variable values and the Floci wiring.
func runFiles(req RunRequest, versions map[string]string) map[string][]byte {
	files := make(map[string][]byte, len(req.Files)+3)
	for name, content := range req.Files {
		files[name] = content
	}
	if b := tfvarsFileContent(req.Environment); len(b) > 0 {
		files[tfvarsFile] = b
	}
	for name, content := range wiringFiles(req.Workflow, versions) {
		files[name] = content
	}
	return files
}

// writeAzureCert stores floci-az's self-signed CA where SSL_CERT_DIR points
// inside the Terraform container.
func (m *Manager) writeAzureCert(ctx context.Context, wfID, work string) error {
	base, ok := m.EmulatorURL(wfID, model.CloudAzure)
	if !ok {
		return errors.New("azure emulator port is not published")
	}
	code, body, err := httpGet(ctx, base+"/_floci/tls-cert")
	if err != nil {
		return fmt.Errorf("fetch azure emulator certificate: %w", err)
	}
	if code != 200 || !strings.Contains(string(body), "BEGIN CERTIFICATE") {
		return fmt.Errorf("fetch azure emulator certificate: HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	return writeFile(filepath.Join(work, filepath.FromSlash(certDir), "floci-az.pem"), body)
}

// runEnv returns the emulator environment (KEY=VALUE, safe to pass as
// arguments) and the user's environment variables, which are handed to the
// docker CLI through its own environment so values never appear in process
// arguments. User variables replace emulator defaults with the same key.
func runEnv(clouds []model.Cloud, env *model.Environment, awsRegion string) (base, user []string) {
	userKeys := map[string]bool{}
	if env != nil {
		for _, ev := range env.EnvVars {
			if ev.Key == "" || strings.ContainsAny(ev.Key, "= \t\n") {
				continue
			}
			userKeys[ev.Key] = true
			user = append(user, ev.Key+"="+ev.Value)
		}
	}
	for _, kv := range cloudEnv(clouds, awsRegion) {
		k, _, _ := strings.Cut(kv, "=")
		if !userKeys[k] {
			base = append(base, kv)
		}
	}
	return base, user
}

// pluginCacheMu guards the shared plugin cache volume within this process:
// `terraform init` writes to it (exclusive), `terraform show` only reads
// links into it but must not observe a half-written provider.
var pluginCacheMu sync.RWMutex

// terraformArgs builds the docker run arguments shared by every Terraform
// container.
func (m *Manager) terraformArgs(name, wfID, network string) []string {
	args := []string{"run", "--rm", "--name", name,
		"--label", labelWorkflow + "=" + wfID, "--label", labelRole + "=" + roleTerraform,
		"--network", network,
		"-v", m.workDir(wfID) + ":/work", "-w", "/work",
		"-v", pluginCacheVolume + ":/plugins",
		"-e", "TF_PLUGIN_CACHE_DIR=/plugins",
		"-e", "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=true",
		"-e", "TF_IN_AUTOMATION=1",
		"-e", "TF_INPUT=0",
		"-e", "CHECKPOINT_DISABLE=1",
	}
	if runtime.GOOS == "linux" {
		// Keep workdir files owned by the host user.
		args = append(args, "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "-e", "HOME=/tmp")
	}
	return args
}

// ensurePluginCache makes the shared plugin cache writable for non-root
// Terraform containers (Linux hosts).
func (m *Manager) ensurePluginCache(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	m.mu.Lock()
	ok := m.pluginsOK
	m.mu.Unlock()
	if ok {
		return nil
	}
	if err := m.ensureImage(ctx, podImage, func(string) {}); err != nil {
		return err
	}
	if _, err := m.docker(ctx, "run", "--rm", "-v", pluginCacheVolume+":/plugins", podImage, "chmod", "0777", "/plugins"); err != nil {
		return err
	}
	m.mu.Lock()
	m.pluginsOK = true
	m.mu.Unlock()
	return nil
}

// terraform runs one Terraform command in a container joined to the pod and
// streams its output into the run log. It returns the exit code.
func (m *Manager) terraform(ctx context.Context, rs *runState, step string, env, userEnv []string, tfArgs ...string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	wfID := rs.run.WorkflowID
	name := "nimbo-" + wfID + "-tf-" + rs.run.ID + "-" + step
	args := m.terraformArgs(name, wfID, "container:"+podName(wfID))
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	for _, kv := range userEnv {
		k, _, _ := strings.Cut(kv, "=")
		args = append(args, "-e", k)
	}
	args = append(args, m.terraformImage())
	args = append(args, tfArgs...)

	cmd := exec.Command(m.dockerBin(), args...)
	cmd.Env = append(os.Environ(), userEnv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	rs.container = name
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		rs.container = ""
		m.mu.Unlock()
	}()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			kctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = m.docker(kctx, "kill", name)
		case <-stop:
		}
	}()

	var wg sync.WaitGroup
	for stream, r := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanLines(r, func(line string) { m.logLine(rs, stream, line) })
		}()
	}
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

func scanLines(r io.Reader, fn func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
	// Drain anything left after an over-long line so the writer never blocks.
	_, _ = io.Copy(io.Discard, r)
}

// logLine appends a line to the run log (secret values masked), updates the
// change summary and publishes the line.
func (m *Manager) logLine(rs *runState, stream, text string) {
	text = strings.TrimRight(text, "\r")
	for _, s := range rs.secrets {
		text = strings.ReplaceAll(text, s, "****")
	}
	line := model.LogLine{T: time.Now().UTC(), Stream: stream, Text: text}
	m.mu.Lock()
	rs.run.Log = append(rs.run.Log, line)
	if stream == "stdout" {
		if p, ok := parseSummary(text); ok {
			rs.run.Plan = &p
		}
	}
	id, wfID := rs.run.ID, rs.run.WorkflowID
	m.mu.Unlock()
	m.publish(model.EventRunLog, wfID, map[string]any{"runId": id, "line": line})
}

var (
	planRe    = regexp.MustCompile(`^Plan: (?:(\d+) to import, )?(\d+) to add, (\d+) to change, (\d+) to destroy\.`)
	applyRe   = regexp.MustCompile(`^Apply complete! Resources: (?:(\d+) imported, )?(\d+) added, (\d+) changed, (\d+) destroyed\.`)
	destroyRe = regexp.MustCompile(`^Destroy complete! Resources: (\d+) destroyed\.`)
)

// parseSummary extracts change counts from a Terraform output line.
func parseSummary(line string) (model.PlanSummary, bool) {
	line = strings.TrimSpace(line)
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	if m := planRe.FindStringSubmatch(line); m != nil {
		return model.PlanSummary{Add: atoi(m[2]), Change: atoi(m[3]), Destroy: atoi(m[4])}, true
	}
	if m := applyRe.FindStringSubmatch(line); m != nil {
		return model.PlanSummary{Add: atoi(m[2]), Change: atoi(m[3]), Destroy: atoi(m[4])}, true
	}
	if m := destroyRe.FindStringSubmatch(line); m != nil {
		return model.PlanSummary{Destroy: atoi(m[1])}, true
	}
	if strings.HasPrefix(line, "No changes.") {
		return model.PlanSummary{}, true
	}
	return model.PlanSummary{}, false
}

// Cancel kills the run's Terraform container; the run ends as canceled.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	rs, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %q not found", id)
	}
	if rs.run.EndedAt != nil {
		m.mu.Unlock()
		return fmt.Errorf("run %q already finished", id)
	}
	rs.canceled = true
	name := rs.container
	m.mu.Unlock()
	if name != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := m.docker(ctx, "kill", name); err != nil && !isNoSuch(err) && !strings.Contains(err.Error(), "is not running") {
			rs.cancel()
			return err
		}
	}
	rs.cancel()
	return nil
}

func (m *Manager) persistRun(r model.Run) error {
	dir := m.runsDir(r.WorkflowID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".run-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, r.ID+".json"))
}

func loadRunFile(p string) (model.Run, bool) {
	data, err := os.ReadFile(p)
	if err != nil {
		return model.Run{}, false
	}
	var r model.Run
	if json.Unmarshal(data, &r) != nil || r.ID == "" {
		return model.Run{}, false
	}
	if r.Status == model.RunQueued || r.Status == model.RunRunning {
		r.Status = model.RunFailed
		r.Error = "Interrupted: the daemon stopped during this run."
	}
	return r, true
}

// ensureHistory loads the persisted runs of wfID once.
func (m *Manager) ensureHistory(wfID string) {
	m.mu.Lock()
	loaded := m.loaded[wfID]
	m.mu.Unlock()
	if loaded {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(m.runsDir(wfID), "*.json"))
	var runs []model.Run
	for _, p := range paths {
		if r, ok := loadRunFile(p); ok && r.WorkflowID == wfID {
			runs = append(runs, r)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded[wfID] {
		return
	}
	m.loaded[wfID] = true
	for _, r := range runs {
		if _, ok := m.runs[r.ID]; !ok {
			done := make(chan struct{})
			close(done)
			m.runs[r.ID] = &runState{run: r, done: done, cancel: func() {}}
		}
	}
}

// Runs lists the runs of wfID newest first, without logs.
func (m *Manager) Runs(wfID string) []model.Run {
	if checkID(wfID) != nil {
		return []model.Run{}
	}
	m.ensureHistory(wfID)
	m.mu.Lock()
	out := []model.Run{}
	for _, rs := range m.runs {
		if rs.run.WorkflowID == wfID {
			out = append(out, m.snapshot(rs, false))
		}
	}
	m.mu.Unlock()
	slices.SortFunc(out, func(a, b model.Run) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}

// GetRun returns a run with its log.
func (m *Manager) GetRun(id string) (*model.Run, bool) {
	m.mu.Lock()
	rs, ok := m.runs[id]
	if ok {
		r := m.snapshot(rs, true)
		m.mu.Unlock()
		return &r, true
	}
	m.mu.Unlock()
	if !validRunID.MatchString(id) {
		return nil, false
	}
	paths, _ := filepath.Glob(filepath.Join(m.cfg.Home, "workflows", "*", "runs", id+".json"))
	for _, p := range paths {
		if r, ok := loadRunFile(p); ok && r.ID == id {
			m.ensureHistory(r.WorkflowID)
			return &r, true
		}
	}
	return nil, false
}
