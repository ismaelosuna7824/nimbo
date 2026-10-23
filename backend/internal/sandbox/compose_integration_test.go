//go:build docker

package sandbox_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/endpoints"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// The compose runtime end to end on real Docker: a busy fixed port is
// reported then remapped, nginx + a tiny built HTTP service come up healthy,
// Endpoints/Try and a load test smoke hit them, logs and events flow, and
// down -v / remove leave nothing labelled behind — while another Nimbo
// compose project (the canary) is never touched.
//
//	go test -tags docker -run TestComposeRuntime -v ./internal/sandbox/

const itCompose = `services:
  web:
    image: nginx:1.27-alpine
    ports: ["${WEB_PORT}:80"]
    depends_on:
      api: {condition: service_healthy}
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1/"]
      interval: 1s
      retries: 30
  api:
    build: ./api
    ports: ["8080"]
    volumes: [data:/data]
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/"]
      interval: 1s
      retries: 30
volumes:
  data: {}
`

const itDockerfile = `FROM busybox:1.36
RUN mkdir -p /www && echo '{"ok":true}' > /www/index.html
EXPOSE 8080
CMD ["httpd", "-f", "-p", "8080", "-h", "/www"]
`

const itCanary = `services:
  idle:
    image: busybox:1.36
    command: ["sleep", "600"]
`

type recorder struct {
	mu     sync.Mutex
	events []model.Event
}

func (r *recorder) Publish(e model.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) composeStates(wfID string) []model.ComposeState {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.ComposeState
	for _, e := range r.events {
		if e.Type == model.EventComposeStatus && e.WorkflowID == wfID {
			out = append(out, e.Data.(model.ComposeStatus).State)
		}
	}
	return out
}

func randomID(prefix string) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func waitRun(t *testing.T, m *sandbox.Manager, run *model.Run) *model.Run {
	t.Helper()
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		r, ok := m.GetRun(run.ID)
		if ok && r.EndedAt != nil {
			if r.Status != model.RunSucceeded {
				var lines []string
				for _, l := range r.Log {
					lines = append(lines, l.Text)
				}
				t.Fatalf("%s run %s: %s\n%s", r.Action, r.Status, r.Error, strings.Join(lines, "\n"))
			}
			return r
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", run.ID)
	return nil
}

func dockerOut(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

// labelled lists the containers, networks, volumes and images carrying
// the workflow label (the only listing the test does).
func labelled(t *testing.T, wfID string) map[string][]string {
	f := "label=" + sandbox.ComposeLabel + "=" + wfID
	return map[string][]string{
		"containers": dockerOut(t, "ps", "-aq", "--filter", f),
		"networks":   dockerOut(t, "network", "ls", "-q", "--filter", f),
		"volumes":    dockerOut(t, "volume", "ls", "-q", "--filter", f),
		"images":     dockerOut(t, "image", "ls", "-q", "--filter", f),
	}
}

func TestComposeRuntime(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx := context.Background()
	home := t.TempDir()
	events := &recorder{}
	m, err := sandbox.New(sandbox.Config{Home: home, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	wf := &model.Workflow{ID: randomID("it-"), Kind: model.WorkflowDocker}
	canary := &model.Workflow{ID: randomID("it-canary-"), Kind: model.WorkflowDocker}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		for _, id := range []string{wf.ID, canary.ID} {
			if err := m.ComposeRemove(cctx, id); err != nil {
				t.Errorf("cleanup %s: %v", id, err)
			}
		}
	})

	// A program already holds the web port.
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	src := filepath.Join(home, "src")
	writeTree(t, src, map[string]string{
		"compose.yaml":   itCompose,
		".env":           "WEB_PORT=" + strconv.Itoa(busyPort) + "\n",
		"api/Dockerfile": itDockerfile,
	})
	canarySrc := filepath.Join(home, "canary")
	writeTree(t, canarySrc, map[string]string{"compose.yaml": itCanary})

	services := []sandbox.ComposeService{{Name: "api"}, {Name: "web", Image: "nginx:1.27-alpine"}}
	req := sandbox.ComposeRunRequest{Workflow: wf, Action: model.ActionUp, SourceDir: src, Files: []string{"compose.yaml"}, Timeout: 3 * time.Minute}

	// Canary: another Nimbo compose project that must survive everything.
	run, err := m.ComposeRun(ctx, sandbox.ComposeRunRequest{Workflow: canary, Action: model.ActionUp, SourceDir: canarySrc})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, run)
	canaryIDs := labelled(t, canary.ID)["containers"]
	if len(canaryIDs) != 1 {
		t.Fatalf("canary containers = %v", canaryIDs)
	}

	// 1. The busy port is reported, no run is created.
	pre, err := m.ComposePorts(ctx, wf.ID, src, req.Files, nil, nil)
	if err != nil || len(pre.Conflicts) != 1 || pre.Conflicts[0].HostPort != busyPort || pre.Conflicts[0].Service != "web" || pre.Conflicts[0].SuggestedPort == 0 {
		t.Fatalf("ports pre-check = %+v, %v", pre, err)
	}
	_, err = m.ComposeRun(ctx, req)
	var conflict *sandbox.PortConflictError
	if !errors.As(err, &conflict) || conflict.Conflicts[0].HostPort != busyPort {
		t.Fatalf("up with a busy port: %v", err)
	}
	if runs := m.Runs(wf.ID); len(runs) != 0 {
		t.Fatalf("conflict created runs: %+v", runs)
	}

	// 2. Up with remapped ports.
	req.RemapPorts = true
	run, err = m.ComposeRun(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, m, run)
	if !slices.ContainsFunc(done.Log, func(l model.LogLine) bool { return strings.Contains(l.Text, "is busy: publishing web:80/tcp on") }) {
		t.Errorf("no remap line in the log")
	}

	// 3. Status: healthy, published ports resolved (remapped + ephemeral).
	rt, err := m.ComposeInspect(ctx, wf.ID, services)
	if err != nil {
		t.Fatal(err)
	}
	st := rt.Status
	if st.State != model.ComposeRunning || len(st.Services) != 2 || len(st.Remaps) != 1 || st.Remaps[0].HostPort != busyPort {
		t.Fatalf("status = %+v", st)
	}
	var webURL, apiURL string
	for _, s := range st.Services {
		if s.Health != "healthy" || len(s.Containers) != 1 || len(s.Containers[0].Ports) != 1 || !strings.HasPrefix(s.Containers[0].ImageID, "sha256:") {
			t.Fatalf("service %s = %+v", s.Name, s)
		}
		p := s.Containers[0].Ports[0]
		switch s.Name {
		case "web":
			webURL = p.URL
			if p.Host != st.Remaps[0].Port || p.Host == busyPort {
				t.Errorf("web host port %d, remap %+v", p.Host, st.Remaps[0])
			}
		case "api":
			apiURL = p.URL
		}
	}
	t.Logf("web %s (remapped from %d), api %s", webURL, busyPort, apiURL)

	// 4. Endpoints + Try.
	info := endpoints.BuildCompose(wf, rt)
	if len(info.Resources) != 2 {
		t.Fatalf("endpoints = %+v", info.Resources)
	}
	for _, node := range []string{"service:web", "service:api"} {
		res, err := endpoints.TryCompose(ctx, rt, model.TryRequest{NodeID: node, Action: endpoints.ActRequest})
		if err != nil || !res.OK || !strings.HasPrefix(res.Status, "200") {
			t.Fatalf("try %s: %+v, %v", node, res, err)
		}
	}

	// 5. Load test smoke on the suggested targets.
	targets := loadtest.SuggestCompose(st)
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	engine := loadtest.NewEngine(loadtest.EngineConfig{Home: home, Resolver: m})
	lrun, err := engine.Start(ctx, wf.ID, model.LoadTestSpec{
		Target:  targets[1], // web
		Profile: model.LoadProfile{Mode: model.ModeRPS, Stages: []model.LoadStage{{DurationSec: 3, Target: 20}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if r, _ := engine.Get(lrun.ID); r.EndedAt != nil {
			if r.Status != model.LoadPassed || r.Summary == nil || r.Summary.Requests < 30 || r.Summary.Errors != 0 {
				t.Fatalf("load test = %s %s %+v", r.Status, r.Error, r.Summary)
			}
			t.Logf("load test: %d requests, %.1f rps, p50 %.1f ms", r.Summary.Requests, r.Summary.RPS, r.Summary.Latency.P50)
			lrun = nil
			break
		}
	}
	if lrun != nil {
		t.Fatal("load test did not finish")
	}

	// 6. Logs of web show the load test's requests.
	var lines []model.ComposeLogLine
	if err := m.ComposeLogs(ctx, wf.ID, "web", 50, false, func(l model.ComposeLogLine) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || lines[0].Service != "web" || !slices.ContainsFunc(lines, func(l model.ComposeLogLine) bool { return strings.Contains(l.Line.Text, "GET / HTTP/1.1") }) {
		t.Errorf("web logs = %+v", lines)
	}
	followCtx, cancelFollow := context.WithTimeout(ctx, 2*time.Second)
	followed := 0
	_ = m.ComposeLogs(followCtx, wf.ID, "web", 5, true, func(model.ComposeLogLine) { followed++ })
	cancelFollow()
	if followed == 0 {
		t.Errorf("follow emitted nothing")
	}

	// 7. Live events reached the hub.
	if states := events.composeStates(wf.ID); !slices.Contains(states, model.ComposeRunning) {
		t.Errorf("compose.status events = %v", states)
	}

	// 8. Labels on everything the project created.
	got := labelled(t, wf.ID)
	if len(got["containers"]) != 2 || len(got["networks"]) != 1 || len(got["volumes"]) != 1 || len(got["images"]) != 1 {
		t.Errorf("labelled objects = %v", got)
	}

	// 9. Restart one service.
	run, err = m.ComposeRun(ctx, sandbox.ComposeRunRequest{Workflow: wf, Action: model.ActionRestart, SourceDir: src, Files: req.Files, Service: "api"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, run)

	// 10. Down -v: containers, network and volume gone; the image stays.
	run, err = m.ComposeRun(ctx, sandbox.ComposeRunRequest{Workflow: wf, Action: model.ActionDown, SourceDir: src, Files: req.Files, RemoveVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, run)
	if st, _ := m.ComposeStatus(ctx, wf.ID, services); st.State != model.ComposeAbsent {
		t.Errorf("after down: %+v", st)
	}
	got = labelled(t, wf.ID)
	if len(got["containers"])+len(got["networks"])+len(got["volumes"]) != 0 || len(got["images"]) != 1 {
		t.Errorf("after down -v = %v", got)
	}

	// 11. Up again without remapPorts: the saved remap is reused.
	req.RemapPorts = false
	run, err = m.ComposeRun(ctx, req)
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	waitRun(t, m, run)

	// 12. Remove: nothing labelled remains, the canary is untouched.
	if err := m.ComposeRemove(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	got = labelled(t, wf.ID)
	for kind, ids := range got {
		if len(ids) != 0 {
			t.Errorf("%s left after remove: %v", kind, ids)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "workflows", wf.ID, "compose")); !os.IsNotExist(err) {
		t.Errorf("work dir left: %v", err)
	}
	if after := labelled(t, canary.ID)["containers"]; !slices.Equal(after, canaryIDs) {
		t.Errorf("canary changed: %v → %v", canaryIDs, after)
	}
	if st, _ := m.ComposeStatus(ctx, canary.ID, nil); st.State != model.ComposeRunning {
		t.Errorf("canary state = %s", st.State)
	}
}
