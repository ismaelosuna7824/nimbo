//go:build docker

package sandbox_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"sigs.k8s.io/yaml"

	"github.com/ismaelosuna7824/nimbo/backend/internal/endpoints"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// The Kubernetes runtime end to end on real Docker: up creates the
// workflow's own k3s cluster, builds an image from the workflow's files and
// loads it, applies a manifests source (Deployment + Service + Ingress)
// and a Helm chart, and waits for the rollout; the Service answers 200
// through the daemon's port-forward and the Ingress through Traefik, a
// smoke load test gets a resource verdict from the pods' cgroups, logs and
// events flow, a restart rolls out, down removes the workloads and
// cluster-delete the cluster — while the user's kubeconfig, KUBECONFIG
// and every other container stay untouched.
//
//	go test -tags docker -run TestKubeRuntime -v -timeout 20m ./internal/sandbox/

const itKubeManifests = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels: {app: web}
spec:
  replicas: 2
  selector:
    matchLabels: {app: web}
  template:
    metadata:
      labels: {app: web}
    spec:
      containers:
        - name: web
          image: itweb:dev
          ports: [{name: http, containerPort: 8080}]
          readinessProbe:
            httpGet: {path: /, port: http}
            periodSeconds: 1
          resources:
            requests: {cpu: 50m, memory: 16Mi}
            limits: {cpu: 250m, memory: 64Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector: {app: web}
  ports: [{name: http, port: 80, targetPort: http}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
spec:
  rules:
    - host: web.it.localhost
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: web, port: {number: 80}}
`

const itKubeDockerfile = `FROM busybox:1.36
RUN mkdir -p /www && echo '{"ok":true}' > /www/index.html
EXPOSE 8080
CMD ["httpd", "-f", "-v", "-p", "8080", "-h", "/www"]
`

var itKubeChart = map[string]string{
	"chart/Chart.yaml":  "apiVersion: v2\nname: hello\nversion: 0.1.0\n",
	"chart/values.yaml": "replicas: 1\nmessage: hi\n",
	"chart/templates/deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-hello
spec:
  replicas: {{ .Values.replicas }}
  selector:
    matchLabels: {app: hello}
  template:
    metadata:
      labels: {app: hello}
    spec:
      containers:
        - name: hello
          image: itweb:dev
          env: [{name: MESSAGE, value: {{ .Values.message | quote }}}]
          ports: [{containerPort: 8080}]
`,
}

// hostFingerprint is what the cluster must never touch on this machine:
// the user's kubeconfig files (content hash or absence) and every Docker
// container that is not the workflow's node.
func hostFingerprint(t *testing.T, node string, files ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		switch {
		case os.IsNotExist(err):
			out[f] = "absent"
		case err != nil:
			out[f] = "error " + err.Error()
		default:
			sum := sha256.Sum256(data)
			out[f] = hex.EncodeToString(sum[:])
		}
	}
	ids, err := exec.Command("docker", "ps", "-a", "--no-trunc", "--format", "{{.ID}} {{.Names}} {{.State}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	for _, l := range strings.Split(strings.TrimSpace(string(ids)), "\n") {
		if l != "" && !strings.Contains(l, " "+node+" ") {
			others = append(others, l)
		}
	}
	slices.Sort(others)
	out["containers"] = strings.Join(others, "\n")
	return out
}

func parseDocs(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, doc := range strings.Split(s, "\n---\n") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			t.Fatal(err)
		}
		out = append(out, obj)
	}
	return out
}

func httpGet(t *testing.T, url, host string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if host != "" {
		req.Host = host
	}
	var last error
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			return res.StatusCode, string(body)
		}
		last = nil
		t.Logf("GET %s (Host %q): %d", url, host, res.StatusCode)
	}
	t.Fatalf("GET %s (Host %q) never answered 200: %v", url, host, last)
	return 0, ""
}

func TestKubeRuntime(t *testing.T) {
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("docker not available")
	}
	ctx := context.Background()
	home := t.TempDir()
	events := &recorder{}
	m, err := sandbox.New(sandbox.Config{Home: home, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	wf := &model.Workflow{ID: randomID("it-"), Kind: model.WorkflowKubernetes}
	node := model.KubeClusterName(wf.ID)

	// Isolation: a KUBECONFIG pointing at a sentinel file and the user's
	// ~/.kube must be left exactly as they are.
	userHome, _ := os.UserHomeDir()
	sentinel := filepath.Join(t.TempDir(), "sentinel-kubeconfig")
	if err := os.WriteFile(sentinel, []byte("apiVersion: v1\nkind: Config\ncurrent-context: do-not-touch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", sentinel)
	watched := []string{sentinel, filepath.Join(userHome, ".kube", "config")}
	before := hostFingerprint(t, node, watched...)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := m.KubeRemove(cctx, wf.ID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	src := filepath.Join(home, "src")
	files := map[string]string{"k8s/app.yaml": itKubeManifests, "itweb/Dockerfile": itKubeDockerfile}
	for k, v := range itKubeChart {
		files[k] = v
	}
	writeTree(t, src, files)
	objs := parseDocs(t, itKubeManifests)
	var images []string
	for _, o := range objs {
		images = append(images, sandbox.KubeObjectImages(o)...)
	}
	builds := sandbox.KubeBuildPlan(src, nil, images)
	if len(builds) != 1 || builds[0].Image != "itweb:dev" || builds[0].Context != "itweb" {
		t.Fatalf("build plan = %+v", builds)
	}
	ch, err := loader.LoadDir(filepath.Join(src, "chart"))
	if err != nil {
		t.Fatal(err)
	}
	up := sandbox.KubeRunRequest{
		Workflow: wf, Action: model.ActionUp, SourceDir: src, Builds: builds, Timeout: 4 * time.Minute,
		Sources: []sandbox.KubeApplySource{
			{Index: 0, Kind: model.KubeManifests, Path: "k8s", Objects: objs},
			{Index: 1, Kind: model.KubeHelm, Path: "chart", Namespace: "hello", Helm: &sandbox.KubeHelmRelease{Release: "greet", Chart: ch, Values: map[string]any{"message": "from values"}}},
		},
	}

	// 1. Up: cluster created, image built + loaded, both sources applied,
	// rollout waited for.
	start := time.Now()
	run, err := m.KubeRun(ctx, up)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, m, run)
	t.Logf("first up (cluster create + build + apply + rollout): %s", time.Since(start).Round(time.Second))
	for _, want := range []string{"Creating the local Kubernetes cluster", "Built itweb:dev", "Deployment/default/web applied", "helm install greet", "All workloads are rolled out."} {
		if !slices.ContainsFunc(done.Log, func(l model.LogLine) bool { return strings.Contains(l.Text, want) }) {
			t.Errorf("up log lacks %q", want)
		}
	}

	// 2. Status: both workloads running, forwards and ingress resolved.
	st, err := m.KubeStatus(ctx, wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Cluster.State != model.ClusterRunning || st.Cluster.Name != node || !strings.HasPrefix(st.Cluster.Version, "v1.36.") || st.Cluster.IngressPort == 0 {
		t.Fatalf("cluster = %+v", st.Cluster)
	}
	// client-go/kubectl/Helm with default settings cache discovery in
	// ~/.kube/cache/discovery/<host>_<port>: none may exist for this cluster.
	discoveryCache := filepath.Join(userHome, ".kube", "cache", "discovery", fmt.Sprintf("127.0.0.1_%d", st.Cluster.APIPort))
	if st.Cluster.APIPort == 0 {
		t.Fatalf("cluster = %+v", st.Cluster)
	}
	if !strings.HasPrefix(st.Cluster.Kubeconfig, home) {
		t.Errorf("kubeconfig %s is outside the Nimbo home", st.Cluster.Kubeconfig)
	}
	byID := map[string]model.KubeWorkloadStatus{}
	for _, w := range st.Workloads {
		byID[w.NodeID] = w
	}
	web, hello := byID["Deployment/default/web"], byID["Deployment/hello/greet-hello"]
	if web.State != model.KubeRunning || web.Ready != 2 || len(web.Pods) != 2 || hello.State != model.KubeRunning {
		t.Fatalf("workloads = %+v", st.Workloads)
	}
	if len(st.Forwards) != 1 || st.Forwards[0].NodeID != "Service/default/web" || st.Forwards[0].URL == "" || !st.Forwards[0].Ready ||
		!slices.Equal(st.Forwards[0].Workloads, []string{"Deployment/default/web"}) {
		t.Fatalf("forwards = %+v", st.Forwards)
	}
	if len(st.Ingresses) != 1 || st.Ingresses[0].Host != "web.it.localhost" || st.Ingresses[0].Address == "" {
		t.Fatalf("ingresses = %+v", st.Ingresses)
	}
	t.Logf("forward %s, ingress %s (address %s)", st.Forwards[0].URL, st.Ingresses[0].URL, st.Ingresses[0].Address)

	// 3. Endpoint 200 through the port-forward and through the Ingress
	// (plain HTTP and Try).
	if code, body := httpGet(t, st.Forwards[0].URL, ""); code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("forward: %d %q", code, body)
	}
	if code, _ := httpGet(t, st.Ingresses[0].Address, "web.it.localhost"); code != 200 {
		t.Fatalf("ingress: %d", code)
	}
	info := endpoints.BuildKube(wf, st)
	if len(info.Resources) != 3 { // cluster, Service, Ingress
		t.Fatalf("endpoints = %+v", info.Resources)
	}
	for _, id := range []string{"Service/default/web", "Ingress/default/web"} {
		res, err := endpoints.TryKube(ctx, st, model.TryRequest{NodeID: id, Action: endpoints.ActRequest})
		if err != nil || !res.OK || !strings.HasPrefix(res.Status, "200") {
			t.Fatalf("try %s: %+v, %v", id, res, err)
		}
	}

	// 4. Smoke load test on the Service with a resource verdict.
	targets := loadtest.SuggestKube(st)
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	engine := loadtest.NewEngine(loadtest.EngineConfig{Home: home, Resolver: m, Monitor: m})
	lrun, err := engine.Start(ctx, wf.ID, model.LoadTestSpec{
		Target:  targets[0],
		Profile: model.LoadProfile{Mode: model.ModeRPS, Stages: []model.LoadStage{{DurationSec: 5, Target: 50}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var lt *model.LoadTestRun
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline) && lt == nil; time.Sleep(300 * time.Millisecond) {
		// EndedAt is set before the resource analysis; the saved run has both.
		if r, _ := engine.Get(lrun.ID); r != nil && r.EndedAt != nil && (r.Resources != nil || r.Status != model.LoadPassed) {
			lt = r
		}
	}
	if lt == nil {
		t.Fatal("load test did not finish")
	}
	if lt.Status != model.LoadPassed || lt.Summary == nil || lt.Summary.Requests < 100 || lt.Summary.Errors != 0 {
		t.Fatalf("load test = %s %s %+v", lt.Status, lt.Error, lt.Summary)
	}
	res := lt.Resources
	if res == nil || res.Service != "web" || res.Verdict == "" || !strings.Contains(res.Note, "cgroups") {
		t.Fatalf("load test resources = %+v", res)
	}
	i := slices.IndexFunc(res.Services, func(s model.LoadServiceResources) bool { return s.Service == "web" })
	if i < 0 || res.Services[i].NodeID != "Deployment/default/web" || !res.Services[i].CPULimited || res.Services[i].CPULimit != 0.5 || res.Services[i].PeakCPU <= 0 {
		t.Fatalf("web resources = %+v", res.Services)
	}
	t.Logf("load test: %d requests, %.1f rps, p50 %.1f ms; verdict: %s (peak CPU %.1f%% of %.2f cores)",
		lt.Summary.Requests, lt.Summary.RPS, lt.Summary.Latency.P50, res.Verdict, res.Services[i].PeakCPU, res.Services[i].CPULimit)

	// 5. Logs of the workload show the load test's requests; follow streams.
	var lines []model.KubeLogLine
	if err := m.KubeLogs(ctx, wf.ID, sandbox.KubeLogSelector{Workload: "Deployment/default/web"}, 50, false, func(l model.KubeLogLine) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || lines[0].NodeID != "Deployment/default/web" || lines[0].Container != "web" {
		t.Errorf("logs = %+v", lines)
	}
	followCtx, cancelFollow := context.WithTimeout(ctx, 3*time.Second)
	followed := 0
	go func() { _, _ = http.Get(st.Forwards[0].URL) }()
	_ = m.KubeLogs(followCtx, wf.ID, sandbox.KubeLogSelector{Workload: "Deployment/default/web"}, 1, true, func(model.KubeLogLine) { followed++ })
	cancelFollow()
	if followed == 0 {
		t.Errorf("follow emitted nothing")
	}

	// 6. Live usage from the cgroup sampler.
	stats, err := m.KubeStats(ctx, wf.ID)
	if err != nil || !stats.Streaming || !slices.ContainsFunc(stats.Services, func(s model.ServiceStatsSeries) bool {
		return s.NodeID == "Deployment/default/web" && len(s.History) > 0
	}) {
		t.Errorf("stats = %+v, %v", stats, err)
	}

	// 7. Restart one workload: a new rollout, the forward keeps its port.
	hostPort := st.Forwards[0].HostPort
	run, err = m.KubeRun(ctx, sandbox.KubeRunRequest{Workflow: wf, Action: model.ActionRestart, Workload: "Deployment/default/web", Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, run)
	st, _ = m.KubeStatus(ctx, wf.ID)
	if len(st.Forwards) != 1 || st.Forwards[0].HostPort != hostPort {
		t.Errorf("forward after restart = %+v (was port %d)", st.Forwards, hostPort)
	}
	httpGet(t, st.Forwards[0].URL, "")

	// 8. Events reached the hub.
	var sawRunning bool
	events.mu.Lock()
	for _, e := range events.events {
		if s, ok := e.Data.(model.KubeStatus); ok && e.Type == model.EventKubeStatus && e.WorkflowID == wf.ID && s.State == model.KubeRunning {
			sawRunning = true
		}
	}
	events.mu.Unlock()
	if !sawRunning {
		t.Errorf("no kube.status event with running workloads")
	}

	// 9. Down: workloads, the Helm release and the namespace Nimbo
	// created are gone; the cluster keeps running.
	run, err = m.KubeRun(ctx, sandbox.KubeRunRequest{Workflow: wf, Action: model.ActionDown})
	if err != nil {
		t.Fatal(err)
	}
	done = waitRun(t, m, run)
	if !slices.ContainsFunc(done.Log, func(l model.LogLine) bool { return strings.Contains(l.Text, "Helm release greet uninstalled") }) {
		t.Errorf("down log lacks the helm uninstall")
	}
	st, _ = m.KubeStatus(ctx, wf.ID)
	if st.Cluster.State != model.ClusterRunning || len(st.Workloads) != 0 || len(st.Forwards) != 0 {
		t.Errorf("after down: %+v", st)
	}

	// 10. Cluster delete: the node container and the built image are gone.
	run, err = m.KubeRun(ctx, sandbox.KubeRunRequest{Workflow: wf, Action: model.ActionClusterDelete})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, m, run)
	if out, _ := exec.Command("docker", "ps", "-a", "-q", "--filter", "label="+sandbox.ComposeLabel+"="+wf.ID).Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("containers left: %s", out)
	}
	if out, _ := exec.Command("docker", "image", "ls", "-q", "--filter", "label="+sandbox.ComposeLabel+"="+wf.ID).Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("images left: %s", out)
	}
	if st, _ := m.KubeStatus(ctx, wf.ID); st.Cluster.State != model.ClusterAbsent {
		t.Errorf("after delete: %+v", st.Cluster)
	}

	// 11. Isolation.
	after := hostFingerprint(t, node, watched...)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed:\nbefore %s\nafter  %s", k, v, after[k])
		}
	}
	if _, err := os.Stat(discoveryCache); !os.IsNotExist(err) {
		t.Errorf("%s exists: something used the default kube cache (%v)", discoveryCache, err)
	}
	t.Logf("untouched: %s=%s, ~/.kube/config=%s, no %s, %d other containers", sentinel, before[sentinel][:12],
		before[watched[1]], discoveryCache, strings.Count(before["containers"], "\n")+1)
}
