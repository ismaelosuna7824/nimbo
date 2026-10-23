package sandbox

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestComposeCmdArgs(t *testing.T) {
	c := composeCmd{project: "nimbo-dk-shop-ab12cd", dir: "/h/wf/compose", files: []string{"compose.yaml", "compose.prod.yaml"}, envFiles: []string{".env", ".env.prod"}, profiles: []string{"debug"}, override: composeOverride}
	got := strings.Join(c.args("up", "--detach"), " ")
	// An environment: the base files, its override last (before Nimbo's own
	// port override), then .env and .env.<env> so the environment's values win.
	want := "compose --ansi never --progress plain -p nimbo-dk-shop-ab12cd --project-directory /h/wf/compose" +
		" -f " + filepath.Join("/h/wf/compose", "compose.yaml") +
		" -f " + filepath.Join("/h/wf/compose", "compose.prod.yaml") +
		" -f " + filepath.Join("/h/wf/compose", ".nimbo", "compose.override.yaml") +
		" --env-file " + filepath.Join("/h/wf/compose", ".env") +
		" --env-file " + filepath.Join("/h/wf/compose", ".env.prod") +
		" --profile debug up --detach"
	if got != want {
		t.Errorf("args =\n%s\nwant\n%s", got, want)
	}
	// A compose file in a subfolder is its project directory (docker
	// compose's default), so ../services/api in docker/compose.yaml resolves.
	c = composeCmd{project: "nimbo-dk-x", dir: "/h/wf/compose", files: []string{"docker/compose.yaml"}}
	got = strings.Join(c.args("config"), " ")
	want = "compose --ansi never --progress plain -p nimbo-dk-x --project-directory " + filepath.Join("/h/wf/compose", "docker") +
		" -f " + filepath.Join("/h/wf/compose", "docker", "compose.yaml") + " config"
	if got != want {
		t.Errorf("nested args =\n%s\nwant\n%s", got, want)
	}
}

func TestActionArgs(t *testing.T) {
	cases := []struct {
		req  ComposeRunRequest
		want string
	}{
		{ComposeRunRequest{Action: model.ActionUp}, "up --detach --build --wait --wait-timeout 300 --remove-orphans"},
		{ComposeRunRequest{Action: model.ActionUp, Service: "web", Timeout: 90 * time.Second}, "up --detach --build --wait --wait-timeout 90 --remove-orphans web"},
		{ComposeRunRequest{Action: model.ActionDown}, "down --remove-orphans"},
		{ComposeRunRequest{Action: model.ActionDown, RemoveVolumes: true}, "down --remove-orphans --volumes"},
		{ComposeRunRequest{Action: model.ActionBuild, Service: "api"}, "build api"},
		{ComposeRunRequest{Action: model.ActionPull}, "pull --ignore-buildable"},
		{ComposeRunRequest{Action: model.ActionRestart, Service: "db"}, "restart db"},
	}
	for _, c := range cases {
		got, err := actionArgs(c.req)
		if err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%+v: %q, %v; want %q", c.req, got, err, c.want)
		}
	}
	if _, err := actionArgs(ComposeRunRequest{Action: model.ActionApply}); err == nil {
		t.Error("apply accepted as a compose action")
	}
}

func TestComposeCommandValidation(t *testing.T) {
	m := &Manager{}
	dir := t.TempDir()
	for _, files := range [][]string{{"../compose.yaml"}, {"/etc/compose.yaml"}, {""}} {
		_, err := m.composeCommand("wf", dir, files, nil, nil)
		var inv *InvalidComposeError
		if !errors.As(err, &inv) {
			t.Errorf("files %q: err = %v, want InvalidComposeError", files, err)
		}
	}
	if _, err := m.composeCommand("wf", dir, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "no compose file") {
		t.Errorf("no files: %v", err)
	}
	if _, err := m.composeCommand("wf", dir, []string{"compose.yaml"}, nil, []string{"bad profile"}); err == nil {
		t.Error("invalid profile accepted")
	}
	if _, err := m.composeCommand("wf", dir, []string{"compose.yaml"}, []string{"../.env"}, nil); err == nil {
		t.Error("env file outside the project accepted")
	}
	for _, f := range []string{"docker-compose.yml", "docker-compose.override.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := m.composeCommand("wf", dir, nil, nil, nil)
	if err != nil || !slices.Equal(c.files, []string{"docker-compose.yml", "docker-compose.override.yaml"}) || c.project != "nimbo-dk-wf" {
		t.Errorf("defaults = %+v, %v", c, err)
	}
}

func TestComposeEnvDropsComposeVariables(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "evil")
	t.Setenv("COMPOSE_FILE", "/tmp/other.yaml")
	t.Setenv("NIMBO_KEEP", "1")
	env := composeEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "COMPOSE_") {
			t.Errorf("kept %s", kv)
		}
	}
	if !slices.Contains(env, "NIMBO_KEEP=1") {
		t.Error("dropped an unrelated variable")
	}
}

const configJSON = `{
  "name": "x",
  "services": {
    "web": {"image": "nginx:alpine", "ports": [
      {"mode": "ingress", "target": 80, "published": "8080", "protocol": "tcp"},
      {"mode": "ingress", "host_ip": "127.0.0.1", "target": 443, "published": "8443", "protocol": "tcp"},
      {"mode": "ingress", "target": 9000, "protocol": "tcp"}]},
    "api": {"build": {"context": "/x/api", "dockerfile": "Dockerfile"}, "ports": [
      {"mode": "ingress", "target": 3000, "published": "3000", "protocol": "tcp"},
      {"mode": "ingress", "target": 5353, "published": "5353", "protocol": "udp"},
      {"mode": "ingress", "target": 7000, "published": "7000-7005", "protocol": "tcp"}]},
    "db": {"image": "postgres:16", "environment": {"POSTGRES_PASSWORD": "pw"}}
  },
  "networks": {"default": {"name": "x_default"}, "shared": {"name": "shared", "external": true}},
  "volumes": {"data": {"name": "x_data"}, "ext": {"external": true}}
}`

func testConfig(t *testing.T) *composeConfig {
	t.Helper()
	var cfg composeConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func TestOverrideYAML(t *testing.T) {
	cfg := testConfig(t)
	got := string(overrideYAML("shop-ab12cd", cfg, []model.PortRemap{{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, Port: 49001}}))
	label := `labels: {"com.nimbo.workflow": "shop-ab12cd"}`
	for _, want := range []string{
		"services:\n",
		"  \"api\":\n    " + label + "\n    build:\n      " + label + "\n",
		"  \"db\":\n    " + label + "\n",
		"  \"web\":\n    " + label + "\n    ports: !override\n",
		`      - {"mode":"ingress","target":80,"published":"49001","protocol":"tcp"}`,
		`      - {"mode":"ingress","host_ip":"127.0.0.1","target":443,"published":"8443","protocol":"tcp"}`,
		`      - {"mode":"ingress","target":9000,"protocol":"tcp"}`,
		"networks:\n  \"default\":\n    " + label + "\n",
		"volumes:\n  \"data\":\n    " + label + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("override lacks %q:\n%s", want, got)
		}
	}
	for _, external := range []string{`"shared"`, `"ext"`} {
		if strings.Contains(got, external) {
			t.Errorf("external %s labelled:\n%s", external, got)
		}
	}
	// Services without remaps keep their ports untouched (no !override).
	if strings.Count(got, "!override") != 1 {
		t.Errorf("unexpected port overrides:\n%s", got)
	}
}

// fakeProber marks busy ports ("tcp/8080") and hands out ports from next.
type fakeProber struct {
	busy    map[string]bool
	next    int
	holders map[string]string
}

func (f *fakeProber) free(_ string, port int, proto string) bool {
	return !f.busy[portKey(proto, port)]
}
func (f *fakeProber) pick(_ string, proto string, used map[string]bool) (int, error) {
	for {
		f.next++
		if !used[portKey(proto, f.next)] && !f.busy[portKey(proto, f.next)] {
			return f.next, nil
		}
	}
}
func (f *fakeProber) holder(port int, proto string) string { return f.holders[portKey(proto, port)] }

func TestPlanPorts(t *testing.T) {
	cfg := testConfig(t)
	pr := &fakeProber{
		busy:    map[string]bool{"tcp/8080": true, "tcp/3000": true, "udp/5353": true, "tcp/8443": true, "tcp/49500": true},
		next:    49499,
		holders: map[string]string{"tcp/8080": "node"},
	}
	own := map[string]bool{"tcp/8443": true} // our own running web publishes 8443
	saved := []model.PortRemap{
		{Service: "api", ContainerPort: 3000, Protocol: "tcp", HostPort: 3000, Port: 41000}, // still usable: kept
		{Service: "api", ContainerPort: 5353, Protocol: "udp", HostPort: 5353, Port: 49500}, // busy (tcp only, so usable for udp): kept
	}
	plan := planPorts(cfg, own, saved, pr)
	if !slices.Equal(plan.kept, saved) {
		t.Errorf("kept = %+v", plan.kept)
	}
	if len(plan.conflicts) != 1 {
		t.Fatalf("conflicts = %+v", plan.conflicts)
	}
	c := plan.conflicts[0]
	if c.Service != "web" || c.HostPort != 8080 || c.ContainerPort != 80 || c.Protocol != "tcp" || c.Holder != "node" || c.SuggestedPort != 49501 {
		t.Errorf("conflict = %+v", c)
	}
	if got := plan.remaps(false); len(got) != 2 {
		t.Errorf("remaps(false) = %+v", got)
	}
	got := plan.remaps(true)
	if len(got) != 3 || got[2] != (model.PortRemap{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, Port: 49501}) {
		t.Errorf("remaps(true) = %+v", got)
	}

	// Once the original port is free again the remap is dropped.
	pr.busy = map[string]bool{}
	if plan := planPorts(cfg, nil, saved, pr); len(plan.kept) != 0 || len(plan.conflicts) != 0 {
		t.Errorf("free ports: %+v", plan)
	}
	// A saved remap whose port got taken becomes a conflict with a new port.
	pr.busy = map[string]bool{"tcp/3000": true, "tcp/41000": true}
	plan = planPorts(cfg, nil, saved, pr)
	if len(plan.kept) != 0 || len(plan.conflicts) != 1 || plan.conflicts[0].HostPort != 3000 || plan.conflicts[0].SuggestedPort == 41000 {
		t.Errorf("taken remap: %+v", plan)
	}
}

func TestRemapsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := loadRemaps(dir); got == nil || len(got) != 0 {
		t.Errorf("missing file = %#v", got)
	}
	want := []model.PortRemap{{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, Port: 49001}}
	if err := saveRemaps(dir, want); err != nil {
		t.Fatal(err)
	}
	if got := loadRemaps(dir); !slices.Equal(got, want) {
		t.Errorf("loaded %+v", got)
	}
}

func TestPortProberSeesListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	pr := portProber{}
	// Wildcard publishing (compose's default) must notice a loopback-only
	// listener, which BSD lets a wildcard bind ignore.
	for _, ip := range []string{"", "0.0.0.0", "127.0.0.1"} {
		if pr.free(ip, port, "tcp") {
			t.Errorf("free(%q, %d) with a listener", ip, port)
		}
	}
	ln.Close()
	if !pr.free("127.0.0.1", port, "tcp") {
		t.Errorf("port %d busy after close", port)
	}
	used := map[string]bool{}
	p, err := pr.pick("", "tcp", used)
	if err != nil || p == 0 || !pr.free("", p, "tcp") {
		t.Errorf("pick = %d, %v", p, err)
	}
}

func TestDescribeHolder(t *testing.T) {
	cases := map[string]string{
		"p123\ncnode\n":                 "node",
		"p1\ncOrbStack Helper\n":        "a Docker container (OrbStack Helper)",
		"p1\nccom.docker.backend\n":     "a Docker container (com.docker.backend)",
		"":                              "",
		"p1\nfcwd\ncpostgres\ncother\n": "postgres",
	}
	for in, want := range cases {
		if got := describeHolder(in); got != want {
			t.Errorf("describeHolder(%q) = %q, want %q", in, got, want)
		}
	}
}

const inspectJSON = `[
 {"Id": "c1", "Name": "/nimbo-dk-wf-web-1", "Image": "sha256:aaa",
  "State": {"Status": "running", "ExitCode": 0, "StartedAt": "2026-10-22T18:21:19.5Z", "Health": {"Status": "healthy"}},
  "RestartCount": 0,
  "Config": {"Image": "nginx:alpine", "Labels": {"com.docker.compose.service": "web"}, "Env": ["A=1"]},
  "NetworkSettings": {"Ports": {"80/tcp": [{"HostIp": "0.0.0.0", "HostPort": "55001"}, {"HostIp": "::", "HostPort": "55001"}], "443/tcp": null}}},
 {"Id": "c2", "Name": "/nimbo-dk-wf-web-2", "Image": "sha256:aaa",
  "State": {"Status": "running", "ExitCode": 0, "StartedAt": "2026-10-22T18:21:19.5Z", "Health": {"Status": "starting"}},
  "RestartCount": 0,
  "Config": {"Image": "nginx:alpine", "Labels": {"com.docker.compose.service": "web"}},
  "NetworkSettings": {"Ports": {"80/tcp": [{"HostIp": "127.0.0.1", "HostPort": "55002"}], "53/udp": [{"HostIp": "0.0.0.0", "HostPort": "55053"}]}}},
 {"Id": "c3", "Name": "/nimbo-dk-wf-migrate-1", "Image": "sha256:bbb",
  "State": {"Status": "exited", "ExitCode": 0, "StartedAt": "2026-10-22T18:21:10Z"},
  "RestartCount": 0,
  "Config": {"Image": "nimbo-dk-wf-migrate", "Labels": {"com.docker.compose.service": "migrate"}},
  "NetworkSettings": {"Ports": {}}},
 {"Id": "c4", "Name": "/nimbo-dk-wf-worker-1", "Image": "sha256:ccc",
  "State": {"Status": "restarting", "ExitCode": 1, "StartedAt": "2026-10-22T18:21:10Z"},
  "RestartCount": 5,
  "Config": {"Image": "busybox", "Labels": {"com.docker.compose.service": "worker"}},
  "NetworkSettings": {"Ports": {}}}
]`

func TestBuildStatus(t *testing.T) {
	cs, err := parseInspect([]byte(inspectJSON))
	if err != nil {
		t.Fatal(err)
	}
	remaps := []model.PortRemap{{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, Port: 55001}}
	st := buildStatus("wf", []ComposeService{{Name: "web", Image: "nginx:alpine"}, {Name: "db", Image: "postgres:16"}, {Name: "migrate"}}, cs, remaps)
	if st.Project != "nimbo-dk-wf" || st.State != model.ComposeError || len(st.Remaps) != 1 {
		t.Errorf("status = %+v", st)
	}
	names := []string{}
	for _, s := range st.Services {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"db", "migrate", "web", "worker"}) {
		t.Fatalf("services = %v", names)
	}
	db, migrate, web, worker := st.Services[0], st.Services[1], st.Services[2], st.Services[3]
	if db.State != model.ComposeAbsent || len(db.Containers) != 0 || db.NodeID != "service:db" || db.Image != "postgres:16" {
		t.Errorf("db = %+v", db)
	}
	if migrate.State != model.ComposeStopped {
		t.Errorf("migrate = %+v", migrate)
	}
	if web.State != model.ComposeStarting || web.Health != "starting" || len(web.Containers) != 2 {
		t.Errorf("web = %+v", web)
	}
	w1 := web.Containers[0]
	if w1.Name != "nimbo-dk-wf-web-1" || w1.ImageID != "sha256:aaa" || w1.Image != "nginx:alpine" || w1.Health != "healthy" || w1.StartedAt == nil {
		t.Errorf("web-1 = %+v", w1)
	}
	if len(w1.Ports) != 1 || w1.Ports[0] != (model.ComposePort{Container: 80, Protocol: "tcp", HostIP: "0.0.0.0", Host: 55001, URL: "http://127.0.0.1:55001/"}) {
		t.Errorf("web-1 ports = %+v", w1.Ports)
	}
	w2 := web.Containers[1].Ports
	if len(w2) != 2 || w2[0].Container != 53 || w2[0].URL != "" || w2[1].URL != "http://127.0.0.1:55002/" {
		t.Errorf("web-2 ports = %+v", w2)
	}
	if worker.State != model.ComposeError || worker.Containers[0].Restarts != 5 {
		t.Errorf("worker = %+v", worker)
	}

	if st := buildStatus("wf", []ComposeService{{Name: "web"}}, nil, nil); st.State != model.ComposeAbsent || st.Remaps == nil || len(st.Services) != 1 {
		t.Errorf("no containers: %+v", st)
	}
}

func TestContainerState(t *testing.T) {
	cases := []struct {
		c    model.ComposeContainer
		want model.ComposeState
	}{
		{model.ComposeContainer{State: "running"}, model.ComposeRunning},
		{model.ComposeContainer{State: "running", Health: "healthy"}, model.ComposeRunning},
		{model.ComposeContainer{State: "running", Health: "starting"}, model.ComposeStarting},
		{model.ComposeContainer{State: "running", Health: "unhealthy"}, model.ComposeError},
		{model.ComposeContainer{State: "created"}, model.ComposeStarting},
		{model.ComposeContainer{State: "restarting", Restarts: 1}, model.ComposeStarting},
		{model.ComposeContainer{State: "restarting", Restarts: 3}, model.ComposeError},
		{model.ComposeContainer{State: "exited"}, model.ComposeStopped},
		{model.ComposeContainer{State: "exited", ExitCode: 137}, model.ComposeError},
		{model.ComposeContainer{State: "dead"}, model.ComposeError},
		{model.ComposeContainer{State: "paused"}, model.ComposeStopped},
	}
	for _, c := range cases {
		if got := containerState(c.c); got != c.want {
			t.Errorf("%+v: %s, want %s", c.c, got, c.want)
		}
	}
}

func TestParseLogLine(t *testing.T) {
	l := parseLogLine("stderr", "2026-10-22T18:21:19.809038492Z hello world\r")
	if l.Stream != "stderr" || l.Text != "hello world" || l.T.Format(time.RFC3339Nano) != "2026-10-22T18:21:19.809038492Z" {
		t.Errorf("parsed %+v", l)
	}
	if l := parseLogLine("stdout", "no timestamp"); l.Text != "no timestamp" || l.T.IsZero() {
		t.Errorf("fallback %+v", l)
	}
}

func TestPortConflictError(t *testing.T) {
	err := &PortConflictError{Conflicts: []model.PortConflict{{Service: "web", HostPort: 8080, Protocol: "tcp", Holder: "node"}}}
	if !strings.Contains(err.Error(), "8080/tcp (web) held by node") {
		t.Error(err)
	}
}

func TestComposeHTTPPort(t *testing.T) {
	for _, c := range []struct {
		p    model.ComposePort
		want bool
	}{
		{model.ComposePort{Container: 80, Protocol: "tcp", URL: "http://127.0.0.1:1/"}, true},
		{model.ComposePort{Container: 5432, Protocol: "tcp", URL: "http://127.0.0.1:1/"}, false},
		{model.ComposePort{Container: 80, Protocol: "tcp"}, false},
		{model.ComposePort{Container: 80, Protocol: "udp", URL: "x"}, false},
	} {
		if got := ComposeHTTPPort(c.p); got != c.want {
			t.Errorf("%+v: %v", c.p, got)
		}
	}
}
