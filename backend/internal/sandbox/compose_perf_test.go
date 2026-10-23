package sandbox

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"0B": 0, "676KiB": 676 * 1024, "64MiB": 64 << 20, "1.5GiB": 3 << 29, "1.48kB": 1480,
		"126B": 126, "79.1MB": 79_100_000, "2GB": 2_000_000_000, " 12 MiB ": 12 << 20, "--": 0, "": 0, "3 parsecs": 0,
	} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

// stats-stream.txt is 2 s of `docker stats --no-trunc --format '{{json .}}'`
// on the node+postgres+redis template (ANSI redraw codes included).
func TestParseStatsStream(t *testing.T) {
	raw, err := os.ReadFile("testdata/perf/stats-stream.txt")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	latest := map[string]statsLine{}
	for line := range strings.Lines(string(raw)) {
		if id, st, ok := parseStatsLine(line, at); ok {
			latest[id] = st
		}
	}
	if len(latest) != 3 {
		t.Fatalf("containers parsed: %d", len(latest))
	}
	for id, st := range latest {
		if len(id) != 64 {
			t.Errorf("id %q is not a full container id", id)
		}
		if st.memBytes <= 0 || st.memLimit < st.memBytes || st.pids <= 0 || st.cpuPercent < 0 {
			t.Errorf("implausible stats for %s: %+v", id[:12], st)
		}
	}
	id, st, ok := parseStatsLine("\x1b[J\x1b[H"+`{"BlockIO":"79.1MB / 0B","CPUPerc":"26.12%","ID":"abc","MemUsage":"31.08MiB / 256MiB","NetIO":"1.48kB / 126B","PIDs":"12"}`+"\x1b[K", at)
	want := statsLine{at: at, cpuPercent: 26.12, memBytes: 32589742, memLimit: 256 << 20, netRx: 1480, netTx: 126, blockRead: 79_100_000, pids: 12}
	if !ok || id != "abc" || st != want {
		t.Errorf("parseStatsLine = %q %+v %v", id, st, ok)
	}
	for _, bad := range []string{"\x1b[K", "", `{"ID":"x","CPUPerc":"--","MemUsage":"-- / --"}`, "not json"} {
		if _, _, ok := parseStatsLine(bad, at); ok {
			t.Errorf("parseStatsLine(%q) accepted", bad)
		}
	}
}

func TestAggregateUsage(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	info := map[string]containerInfo{
		"a1": {service: "api", cpuLimit: 0.25, memLimit: 256 << 20},
		"a2": {service: "api", cpuLimit: 0.25, memLimit: 256 << 20},
		"d1": {service: "db"},
		"s1": {service: "stale"},
	}
	latest := map[string]statsLine{
		"a1": {at: now, cpuPercent: 20, memBytes: 100 << 20, memLimit: 256 << 20, netRx: 10, pids: 5},
		"a2": {at: now.Add(-time.Second), cpuPercent: 24.5, memBytes: 50 << 20, memLimit: 256 << 20, netRx: 5, pids: 6},
		"d1": {at: now, cpuPercent: 150, memBytes: 1 << 30, memLimit: 8 << 30},
		"s1": {at: now.Add(-time.Minute), cpuPercent: 99},
	}
	got := aggregateUsage(info, latest, hostInfo{cpus: 14, memory: 16 << 30}, now)
	if len(got) != 2 || got[0].Service != "api" || got[1].Service != "db" {
		t.Fatalf("services: %+v", got)
	}
	api, db := got[0], got[1]
	if api.Containers != 2 || api.CPUPercent != 44.5 || api.CPULimit != 0.5 || !api.CPULimited || api.MemBytes != 150<<20 || api.MemLimit != 512<<20 || !api.MemLimited || api.NetRxBytes != 15 || api.PIDs != 11 {
		t.Errorf("api: %+v", api)
	}
	if of := api.CPUOfLimit(); of != 0.89 {
		t.Errorf("api CPU of limit %v", of)
	}
	if db.CPULimited || db.CPULimit != 14 || db.MemLimited || db.MemLimit != 16<<30 {
		t.Errorf("db (no limits) gets the host's capacity: %+v", db)
	}
}

func TestInspectLimitsAndProblems(t *testing.T) {
	cs, err := parseInspect([]byte(`[
	 {"Id":"c1","Name":"/p-api-1","State":{"Status":"running","OOMKilled":false,"StartedAt":"2026-10-22T21:00:00Z","Health":{"Status":"unhealthy","Log":[{"Start":"2026-10-22T21:00:10Z","End":"2026-10-22T21:00:11Z","ExitCode":1,"Output":"wget: can't connect to remote host\n"}]}},
	  "HostConfig":{"NanoCpus":250000000,"Memory":268435456},"RestartCount":2,"Config":{"Labels":{"com.docker.compose.service":"api"}}},
	 {"Id":"c2","Name":"/p-db-1","State":{"Status":"exited","ExitCode":137,"OOMKilled":true,"FinishedAt":"2026-10-22T21:05:00Z"},
	  "HostConfig":{"CpuQuota":50000,"CpuPeriod":100000,"Memory":536870912},"Config":{"Labels":{"com.docker.compose.service":"db"}}},
	 {"Id":"c3","Name":"/p-job-1","State":{"Status":"exited","ExitCode":1},"Config":{"Labels":{"com.docker.compose.service":"job"}}},
	 {"Id":"c4","Name":"/p-web-1","State":{"Status":"exited","ExitCode":143},"Config":{"Labels":{"com.docker.compose.service":"web"}}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].cpuLimit() != 0.25 || cs[1].cpuLimit() != 0.5 || cs[2].cpuLimit() != 0 {
		t.Errorf("cpu limits %v %v %v", cs[0].cpuLimit(), cs[1].cpuLimit(), cs[2].cpuLimit())
	}
	st := buildStatus("wf", nil, cs, nil)
	db := st.Services[slices.IndexFunc(st.Services, func(s model.ComposeServiceStatus) bool { return s.Name == "db" })].Containers[0]
	if !db.OOMKilled || db.MemLimit != 512<<20 || db.CPULimit != 0.5 || db.FinishedAt == nil {
		t.Errorf("db container: %+v", db)
	}
	kinds := map[string]model.ComposeProblem{}
	for _, p := range st.Problems {
		kinds[p.Service+":"+p.Kind] = p
	}
	if len(st.Problems) != 5 {
		t.Errorf("problems: %+v", st.Problems)
	}
	if p := kinds["db:oom"]; p.Severity != "error" || !strings.Contains(p.Message, "exceeding its memory limit (512 MiB)") || p.Fix == "" {
		t.Errorf("oom: %+v", p)
	}
	if _, ok := kinds["db:exit"]; ok {
		t.Error("an OOM kill is not also reported as an exit")
	}
	if p := kinds["api:restarts"]; p.Severity != "warning" || p.Restarts != 2 {
		t.Errorf("restarts: %+v", p)
	}
	if p := kinds["api:unhealthy"]; !strings.Contains(p.Message, `last output: "wget: can't connect to remote host"`) {
		t.Errorf("unhealthy: %+v", p)
	}
	if p := kinds["job:exit"]; p.Severity != "error" || !strings.Contains(p.Message, "code 1: application error") {
		t.Errorf("exit: %+v", p)
	}
	if p := kinds["web:exit"]; p.Severity != "warning" || !strings.Contains(p.Message, "SIGTERM") {
		t.Errorf("SIGTERM exit: %+v", p)
	}
}

func TestStartupTimes(t *testing.T) {
	m := &Manager{sandboxes: map[string]*sandbox{}}
	sb := m.sandbox("wf")
	started := time.Date(2026, 10, 22, 21, 0, 0, 0, time.UTC)
	// Docker 29 puts the container ID in Actor.ID only; older engines also
	// send a top-level "id".
	ev := func(id, action string, at time.Time) string {
		b, _ := json.Marshal(map[string]any{"Type": "container", "Action": action, "Actor": map[string]any{"ID": id}, "timeNano": at.UnixNano()})
		return string(b)
	}
	legacy := func(id, action string, at time.Time) string {
		b, _ := json.Marshal(map[string]any{"id": id, "status": action, "Action": action, "timeNano": at.UnixNano()})
		return string(b)
	}
	sb.perf.noteEvent(ev("c1", "start", started))
	sb.perf.noteEvent(ev("c1", "health_status: healthy", started.Add(10365*time.Millisecond)))
	sb.perf.noteEvent(legacy("c1", "health_status: healthy", started.Add(40*time.Second))) // later checks keep the first
	sb.perf.noteEvent("garbage")

	cs, _ := parseInspect([]byte(`[
	 {"Id":"c1","State":{"Status":"running","StartedAt":"2026-10-22T21:00:00Z","Health":{"Status":"healthy"}},"Config":{"Labels":{"com.docker.compose.service":"api"}}},
	 {"Id":"c2","State":{"Status":"running","StartedAt":"2026-10-22T21:00:00Z","Health":{"Status":"healthy","Log":[
	   {"Start":"2026-10-22T20:59:00Z","End":"2026-10-22T20:59:01Z","ExitCode":0},
	   {"Start":"2026-10-22T21:00:05Z","End":"2026-10-22T21:00:05.150Z","ExitCode":1},
	   {"Start":"2026-10-22T21:00:10Z","End":"2026-10-22T21:00:10.200Z","ExitCode":0}]}},"Config":{"Labels":{"com.docker.compose.service":"db"}}},
	 {"Id":"c3","State":{"Status":"running","StartedAt":"2026-10-22T21:00:00Z","Health":{"Status":"healthy","Log":[{},{},{},{},{"Start":"2026-10-22T21:00:10Z","End":"2026-10-22T21:00:11Z"}]}},"Config":{"Labels":{"com.docker.compose.service":"cache"}}}
	]`))
	st := buildStatus("wf", nil, cs, nil)
	m.annotateStartup(sb, &st, cs)
	got := map[string]*int64{}
	for _, s := range st.Services {
		got[s.Name] = s.StartupMs
	}
	if got["api"] == nil || *got["api"] != 10365 {
		t.Errorf("api (event) startup %v", got["api"])
	}
	if got["db"] == nil || *got["db"] != 10200 {
		t.Errorf("db (health log) startup %v", got["db"])
	}
	if got["cache"] != nil {
		t.Errorf("cache: a full health log may have lost the first success, got %d", *got["cache"])
	}
	// A restart clears the recorded time until the next healthy event.
	sb.perf.noteEvent(ev("c1", "start", started.Add(time.Hour)))
	if _, ok := sb.perf.startup["c1"]; ok {
		t.Error("start event keeps the old startup time")
	}
}

func TestTargetService(t *testing.T) {
	st := model.ComposeStatus{Services: []model.ComposeServiceStatus{
		{Name: "api", Containers: []model.ComposeContainer{{Ports: []model.ComposePort{{Container: 3000, Protocol: "tcp", Host: 3100}}}}},
		{Name: "web", Containers: []model.ComposeContainer{{Ports: []model.ComposePort{{Container: 80, Protocol: "tcp", Host: 8080}}}}},
	}}
	for _, c := range []struct {
		target model.LoadTarget
		want   string
	}{
		{model.LoadTarget{NodeID: "service:web"}, "web"},
		{model.LoadTarget{NodeID: "service:gone", HTTP: &model.HTTPTarget{URL: "http://127.0.0.1:3100/notes"}}, "api"},
		{model.LoadTarget{HTTP: &model.HTTPTarget{URL: "http://localhost:8080/"}}, "web"},
		{model.LoadTarget{HTTP: &model.HTTPTarget{URL: "https://example.com:3100/"}}, ""},
		{model.LoadTarget{HTTP: &model.HTTPTarget{URL: "http://127.0.0.1:9999/"}}, ""},
	} {
		if got := targetService(st, c.target); got != c.want {
			t.Errorf("targetService(%+v) = %q, want %q", c.target, got, c.want)
		}
	}
}

// up-node-postgres-redis.log.json is the run log of `compose up --build` of
// the node+postgres+redis template (BuildKit plain progress, one built
// service, two cached steps).
func TestParseBuildLogRecorded(t *testing.T) {
	var lines []model.LogLine
	data, err := os.ReadFile("testdata/perf/up-node-postgres-redis.log.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &lines); err != nil {
		t.Fatal(err)
	}
	pb := parseBuildLog(lines, []string{"api"})
	stats := pb.serviceBuilds()
	api := stats["api"]
	if len(stats) != 1 || api == nil {
		t.Fatalf("services: %v", stats)
	}
	if api.CacheHits != 2 || api.CacheMisses != 2 || api.Failed {
		t.Errorf("cache %d/%d failed %v", api.CacheHits, api.CacheMisses, api.Failed)
	}
	if pb.images["api"] != "docker.io/library/nimbo-dk-perf-node-47etxq-api" {
		t.Errorf("image %q", pb.images["api"])
	}
	var npm *model.BuildStep
	for i, s := range api.Steps {
		if strings.Contains(s.Instruction, "npm install") {
			npm = &api.Steps[i]
		}
	}
	if npm == nil || npm.Cached || npm.Stage != "stage-0" || npm.DurationMs != 5800 || npm.Internal {
		t.Errorf("npm step %+v", npm)
	}
	if api.DurationMs < 5800 || api.DurationMs > pb.end.Sub(pb.start).Milliseconds() {
		t.Errorf("service build %d ms, whole build %v", api.DurationMs, pb.end.Sub(pb.start))
	}
}

// Captured from `docker compose --progress plain build` of two services
// sharing a Dockerfile (bake prefixes vertices with the service; shared
// vertices appear once).
const multiBuild = `#1 [internal] load local bake definitions
#1 DONE 0.0s
#2 [app2 internal] load build definition from Dockerfile
#2 DONE 0.0s
#5 [app2 base 1/3] FROM docker.io/library/busybox:latest
#5 DONE 0.0s
#7 [app base 2/3] WORKDIR /app
#7 CACHED
#8 [app base 3/3] RUN echo hello > a.txt
#8 CACHED
#9 [app stage-1 1/2] COPY x.txt /app/
#9 DONE 0.1s
#10 [app stage-1 2/2] RUN sleep 1 && dd if=/dev/zero of=/app/big bs=1M count=5 2>/dev/null
#10 DONE 1.2s
#11 [app2] exporting to image
#11 exporting layers 0.0s done
#11 naming to docker.io/library/p-app2 done
#11 DONE 0.1s
#12 [app] exporting to image
#12 naming to docker.io/library/p-app done
#12 DONE 0.1s
#13 [web 1/2] RUN npm ci
#13 ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`

func TestParseBuildLogMultiService(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	var lines []model.LogLine
	for i, l := range strings.Split(multiBuild, "\n") {
		lines = append(lines, model.LogLine{T: t0.Add(time.Duration(i) * 100 * time.Millisecond), Stream: "stdout", Text: l})
	}
	stats := parseBuildLog(lines, []string{"app", "app2", "web"}).serviceBuilds()
	app, app2, web := stats["app"], stats["app2"], stats["web"]
	if app == nil || app2 == nil || web == nil || len(stats) != 3 {
		t.Fatalf("services: %v", stats)
	}
	if app.CacheHits != 2 || app.CacheMisses != 2 || len(app.Steps) != 5 {
		t.Errorf("app: hits %d misses %d steps %d", app.CacheHits, app.CacheMisses, len(app.Steps))
	}
	if app.Steps[2].Stage != "stage-1" || app.Steps[2].Instruction != "COPY x.txt /app/" || app.Steps[2].Name != "[app stage-1 1/2] COPY x.txt /app/" {
		t.Errorf("step %+v", app.Steps[2])
	}
	// app2's FROM does not count as a cache hit or miss.
	if app2.CacheHits != 0 || app2.CacheMisses != 0 || len(app2.Steps) != 3 {
		t.Errorf("app2: %+v", app2)
	}
	if !web.Failed || !web.Steps[0].Error || web.CacheMisses != 0 {
		t.Errorf("web: %+v", web)
	}
}

func TestImageHistorySplit(t *testing.T) {
	data, err := os.ReadFile("testdata/perf/history-node-api.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	hist := parseHistory(string(data))
	if len(hist) != 17 {
		t.Fatalf("history entries %d", len(hist))
	}
	var total int64
	for _, l := range hist {
		total += l.SizeBytes
	}
	// The template's Dockerfile has 8 instructions after FROM.
	base, ok := splitLayers(hist, 8)
	if !ok || base <= 0 || base >= total || total-base < 5_000_000 {
		t.Fatalf("base %d of %d", base, total)
	}
	if hist[7].Base || !hist[8].Base {
		t.Error("the 8 newest entries are the Dockerfile's own")
	}
	if hist[4].CreatedBy != "RUN npm install --omit=dev --no-audit --no-fund" {
		t.Errorf("createdBy %q", hist[4].CreatedBy)
	}
	top := biggestLayers(hist, 3)
	if len(top) != 3 || top[0].SizeBytes < top[1].SizeBytes || top[1].SizeBytes < top[2].SizeBytes || !top[0].Base {
		t.Errorf("biggest %+v", top)
	}
	if _, ok := splitLayers(hist, 0); ok {
		t.Error("unknown own layer count splits")
	}
	if _, ok := splitLayers(hist, 40); ok {
		t.Error("more own layers than history splits")
	}
}

func TestBuildFindings(t *testing.T) {
	find := func(s model.ServiceBuildStats) map[string]model.BuildFinding {
		out := map[string]model.BuildFinding{}
		for _, f := range buildFindings(&s) {
			out[f.Code] = f
		}
		return out
	}
	f := find(model.ServiceBuildStats{Service: "api", Stack: "node", SizeBytes: 1_210_000_000, BaseImage: "node:22", BaseSizeBytes: 1_100_000_000})
	if big := f["image_too_big"]; !strings.Contains(big.Message, "1.21 GB — big for a Node.js service") || !slices.Contains(big.Rules, "dk_heavy_base") || !slices.Contains(big.Rules, "dk_single_stage") {
		t.Errorf("node: %+v", big)
	}
	if b := f["base_dominates"]; !strings.Contains(b.Message, "node:22 is 1.10 GB of the 1.21 GB image (90%)") {
		t.Errorf("base: %+v", b)
	}
	if f := find(model.ServiceBuildStats{Service: "api", Stack: "node", SizeBytes: 170_000_000}); len(f) != 0 {
		t.Errorf("lean node image flagged: %v", f)
	}
	if f := find(model.ServiceBuildStats{Service: "svc", Stack: "go", SizeBytes: 320_000_000}); f["image_too_big"].Message == "" {
		t.Error("a 320 MB Go image is flagged")
	}
	if f := find(model.ServiceBuildStats{Service: "svc", Stack: "go", SizeBytes: 18_000_000}); len(f) != 0 {
		t.Errorf("small Go image flagged: %v", f)
	}
	slow := model.BuildStep{Instruction: "RUN npm ci", DurationMs: 42_000}
	if f := find(model.ServiceBuildStats{Service: "api", CacheHits: 2, Steps: []model.BuildStep{slow}}); !slices.Equal(f["slow_uncached_step"].Rules, []string{"dk_layer_order", "dk_cache_mount"}) {
		t.Errorf("slow step: %v", f)
	}
	if f := find(model.ServiceBuildStats{Service: "api", Steps: []model.BuildStep{slow}}); len(f) != 0 {
		t.Errorf("a cold build's install is expected to be slow: %v", f)
	}
}

func TestImageStack(t *testing.T) {
	for _, c := range []struct {
		base  string
		hints []string
		want  string
	}{
		{"node:22-alpine", nil, "node"},
		{"gcr.io/distroless/static-debian12", []string{"go"}, "go"},
		{"docker.io/library/golang:1.23", nil, "go"},
		{"python:3.12-slim", nil, "python"},
		{"eclipse-temurin:21-jre", nil, "java"},
		{"mcr.microsoft.com/dotnet/aspnet:8.0", nil, "dotnet"},
		{"nginx:alpine", nil, "static"},
		{"alpine:3.20", []string{"fastapi"}, "python"},
		{"alpine:3.20", nil, ""},
	} {
		if got := imageStack(c.base, c.hints); got != c.want {
			t.Errorf("imageStack(%q, %v) = %q, want %q", c.base, c.hints, got, c.want)
		}
	}
}

func TestBuildRecordsAndDelta(t *testing.T) {
	m := &Manager{cfg: Config{Home: t.TempDir()}}
	prev := []model.ComposeBuild{
		{RunID: "r2", Services: []model.ServiceBuildStats{{Service: "api", Failed: true}}},
		{RunID: "r1", Services: []model.ServiceBuildStats{{Service: "api", DurationMs: 9000, SizeBytes: 170_000_000, CacheHits: 2, CacheMisses: 2}}},
	}
	d := buildDelta(model.ServiceBuildStats{Service: "api", DurationMs: 1500, SizeBytes: 171_000_000, CacheHits: 4}, prev)
	if d == nil || *d != (model.BuildDelta{PrevRunID: "r1", DurationMs: -7500, SizeBytes: 1_000_000, CacheHits: 2, CacheMisses: -2}) {
		t.Errorf("delta %+v", d)
	}
	if buildDelta(model.ServiceBuildStats{Service: "web", SizeBytes: 1}, prev) != nil {
		t.Error("delta without a previous build")
	}
	if err := m.saveBuilds("wf", prev); err != nil {
		t.Fatal(err)
	}
	got, err := m.ComposeBuilds("wf", 1)
	if err != nil || len(got.Builds) != 1 || got.Builds[0].RunID != "r2" {
		t.Errorf("ComposeBuilds: %+v %v", got, err)
	}
	if err := m.removePerf("wf"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ComposeBuilds("wf", 0); len(got.Builds) != 0 {
		t.Errorf("after removal: %+v", got)
	}
}
