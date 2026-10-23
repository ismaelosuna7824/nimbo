package compose

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Best-practice advice (warning/info dk_* rules) for services and the
// Dockerfiles they build: image size and build speed, runtime reliability
// and performance, security. Every finding names the file and line to
// change and a concrete fix; rules stay quiet when they can't tell (an
// image's own HEALTHCHECK, a remote build context).

// sharedFilesOS is the host OS for the bind-mount performance rule (bind
// mounts are slow only through Docker Desktop's VM: macOS, Windows).
var sharedFilesOS = runtime.GOOS

// practices runs the advice rules on every service and Dockerfile.
func (p *Project) practices() {
	m := p.Model
	names := p.SortedServiceNames()
	dependents := map[string][]types.ServiceDependency{} // service -> how others wait for it
	for _, n := range names {
		for dep, d := range m.Services[n].DependsOn {
			dependents[dep] = append(dependents[dep], d)
		}
	}
	oneShot := func(name string) bool {
		for _, d := range dependents[name] {
			if d.Condition == types.ServiceConditionCompletedSuccessfully {
				return true
			}
		}
		s := m.Services[name]
		return s.Restart == "no" && s.Deploy == nil && len(dependents[name]) == 0 && len(s.Ports) == 0
	}
	seen := map[string]bool{}
	contexts := map[string]bool{}
	for _, name := range names {
		s := m.Services[name]
		p.servicePractices(name, s, dependents[name], oneShot(name))
		if s.Build != nil {
			if df := p.dockerfilePath(s); df != "" && p.Dockerfiles[df] != nil {
				p.dockerfilePractices(name, s, df, seen)
			}
			if ctx := p.contextPath(s); ctx != "" && !strings.HasPrefix(ctx, "..") && !contexts[ctx] && p.isDir(ctx) {
				contexts[ctx] = true
				p.checkDockerignore(name, s, ctx)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Services

func (p *Project) servicePractices(name string, s types.ServiceConfig, dependents []types.ServiceDependency, oneShot bool) {
	id := ServiceID(name)
	svcFile, svcLine := p.ServiceLine(name)
	data := dataServiceOf(s.Image)
	final := (*StageInfo)(nil)
	if s.Build != nil {
		final = p.finalStage(s)
	}

	// Databases published on every interface.
	if data != nil {
		for _, port := range s.Ports {
			if port.Published == "" || !allInterfaces(port.HostIP) {
				continue
			}
			file, line := p.fieldLine(name, "ports")
			p.diag(model.SeverityWarning, "dk_db_published", file, line, id,
				fmt.Sprintf("%s (%s) publishes port %s on every network interface", name, data.label, port.Published),
				fmt.Sprintf("Bind it to this machine only: \"127.0.0.1:%s:%d\" (or drop ports: other services reach it as %s:%d).", port.Published, port.Target, name, port.Target))
			break
		}
	}

	// Data in anonymous volumes.
	if data != nil && data.category != "cache" && len(data.paths) > 0 && len(s.VolumesFrom) == 0 {
		for _, dp := range data.paths {
			var mount *types.ServiceVolumeConfig
			for i, v := range s.Volumes {
				if v.Target == dp || strings.HasPrefix(dp, strings.TrimSuffix(v.Target, "/")+"/") {
					mount = &s.Volumes[i]
				}
			}
			named := strings.ReplaceAll(name, "_", "-") + "-data"
			fix := fmt.Sprintf("Mount a named volume: volumes: [\"%s:%s\"] and declare %s: under the top-level volumes:.", named, dp, named)
			switch {
			case mount == nil:
				p.diag(model.SeverityWarning, "dk_anon_volume", svcFile, svcLine, id,
					fmt.Sprintf("%s (%s) keeps its data in an anonymous volume: nothing is mounted on %s", name, data.label, dp), fix)
			case mount.Type == types.VolumeTypeVolume && mount.Source == "":
				file, line := p.fieldLine(name, "volumes")
				p.diag(model.SeverityWarning, "dk_anon_volume", file, line, id,
					fmt.Sprintf("%s (%s) keeps %s in an anonymous volume", name, data.label, dp), fix)
			}
		}
	}

	// Docker socket, host privileges.
	for _, v := range s.Volumes {
		if v.Type == types.VolumeTypeBind && (path.Base(v.Source) == "docker.sock" || v.Source == "/var/run/docker.sock") {
			file, line := p.refLine(name, "volumes", v.Source)
			p.diag(model.SeverityWarning, "dk_docker_sock", file, line, id,
				fmt.Sprintf("%s mounts the Docker socket (%s): it controls Docker as root on the host", name, v.Source),
				"Remove the mount unless the service manages containers on purpose (then prefer a socket proxy that only allows the API calls it needs, e.g. tecnativa/docker-socket-proxy).")
		}
	}
	host := []struct {
		on          bool
		field, what string
	}{
		{s.Privileged, "privileged", "privileged: true (every device and capability)"},
		{s.NetworkMode == "host", "network_mode", "network_mode: host (no network isolation, port mappings ignored)"},
		{s.Pid == "host", "pid", "pid: host (sees and can signal every host process)"},
	}
	for _, h := range host {
		if h.on {
			file, line := p.fieldLine(name, h.field)
			p.diag(model.SeverityWarning, "dk_privileged", file, line, id, fmt.Sprintf("%s runs with %s", name, h.what),
				fmt.Sprintf("Remove %s; grant only what the service needs (cap_add: one capability, devices: one device, ports: for networking).", h.field))
		}
	}

	// Restart policy, limits, logs.
	if !oneShot && s.Restart == "" && (s.Deploy == nil || s.Deploy.RestartPolicy == nil) {
		p.diag(model.SeverityInfo, "dk_no_restart", svcFile, svcLine, id,
			fmt.Sprintf("%s has no restart policy: it stays down after a crash or a Docker restart", name),
			"Add restart: unless-stopped (or on-failure for workers that should stop when they finish).")
	}
	if !oneShot && s.MemLimit == 0 && (s.Deploy == nil || s.Deploy.Resources.Limits == nil || s.Deploy.Resources.Limits.MemoryBytes == 0) {
		mem := "512M"
		if data != nil || final != nil && isJVM(final.From) || isJVM(s.Image) {
			mem = "1G"
		}
		p.diag(model.SeverityInfo, "dk_no_limits", svcFile, svcLine, id,
			fmt.Sprintf("%s has no memory limit: it can take all of Docker's memory", name),
			fmt.Sprintf("Cap it: deploy: {resources: {limits: {cpus: \"1\", memory: %s}}} (size it from docker stats).", mem))
	}
	if s.Logging != nil && (s.Logging.Driver == "" || s.Logging.Driver == "json-file") && s.Logging.Options["max-size"] == "" {
		file, line := p.fieldLine(name, "logging")
		p.diag(model.SeverityInfo, "dk_log_rotation", file, line, id,
			fmt.Sprintf("%s logs to json-file without max-size: its log grows forever", name),
			"Rotate it: logging: {driver: json-file, options: {max-size: \"10m\", max-file: \"3\"}} (or driver: local).")
	}

	// Waiting for data services; waiting for a server to "complete".
	for _, dep := range sortedKeys(s.DependsOn) {
		target, ok := p.Model.Services[dep]
		if !ok {
			continue
		}
		if s.DependsOn[dep].Condition == types.ServiceConditionCompletedSuccessfully && LongRunning(target, p.hasHealthcheck(target)) {
			file, line := p.refLine(name, "depends_on", dep)
			p.diag(model.SeverityError, "dk_depends_completed", file, line, id,
				fmt.Sprintf("%s waits for %s to complete, but %s is a server that keeps running: %s never starts and docker compose up hangs", name, dep, dep, name),
				fmt.Sprintf("Use depends_on: {%s: {condition: service_healthy}} (or service_started), or drop the dependency if %s doesn't call %s.", dep, name, dep))
			continue
		}
		if s.DependsOn[dep].Condition != types.ServiceConditionStarted {
			continue
		}
		if dd := dataServiceOf(target.Image); dd != nil && p.hasHealthcheck(target) {
			file, line := p.refLine(name, "depends_on", dep)
			p.diag(model.SeverityWarning, "dk_depends_healthy", file, line, id,
				fmt.Sprintf("%s starts as soon as %s's container exists, before the %s accepts connections", name, dep, dd.label),
				fmt.Sprintf("Wait for its healthcheck: depends_on: {%s: {condition: service_healthy}}.", dep))
		}
	}

	// Healthchecks.
	hasHC := p.hasHealthcheck(s)
	switch {
	case hasHC:
	case s.HealthCheck != nil:
		// disabled on purpose
	case s.Build != nil && final != nil && !oneShot && (len(s.Ports) > 0 || len(s.Expose) > 0 || len(final.Expose) > 0 || len(dependents) > 0):
		port := firstPort(s, final)
		test := "[\"CMD-SHELL\", \"wget -qO- http://127.0.0.1:" + port + "/health || exit 1\"]"
		p.diag(model.SeverityInfo, "dk_missing_healthcheck", svcFile, svcLine, id,
			fmt.Sprintf("%s has no healthcheck: Docker can't tell when it is ready or hung", name),
			fmt.Sprintf("Add healthcheck: {test: %s, interval: 10s, timeout: 3s, retries: 5, start_period: 10s} (any command that fails when the app can't serve), or a HEALTHCHECK in the Dockerfile.", test))
	case s.Build == nil && len(dependents) > 0:
		if im, ok := ImageFor(s.Image); ok && im.Healthcheck != nil {
			p.diag(model.SeverityInfo, "dk_missing_healthcheck", svcFile, svcLine, id,
				fmt.Sprintf("%s (%s) has no healthcheck: the services that depend on it can't wait until it is ready", name, im.Label),
				fmt.Sprintf("Add healthcheck: {test: [\"CMD-SHELL\", %q], interval: %s, timeout: 5s, retries: 10} and use condition: service_healthy in depends_on.", strings.TrimPrefix(im.Healthcheck.Test, "CMD-SHELL "), orStr(im.Healthcheck.Interval, "10s")))
		}
	}
	p.checkHealthTiming(name, s, final)

	// PID 1.
	if !oneShot {
		p.checkInit(name, s, final)
	}

	// Published ports the image does not listen on.
	p.checkExpose(name, s, final, data)

	// Dependency folders bind-mounted through Docker Desktop.
	if sharedFilesOS != "linux" {
		p.checkBindDeps(name, s)
	}

	// Opt-in hardening.
	var missing []string
	if !s.ReadOnly {
		missing = append(missing, "read_only: true")
	}
	if !slices.ContainsFunc(s.CapDrop, func(c string) bool { return strings.EqualFold(c, "ALL") }) {
		missing = append(missing, "cap_drop: [ALL]")
	}
	if !slices.ContainsFunc(s.SecurityOpt, func(o string) bool { return strings.HasPrefix(o, "no-new-privileges") }) {
		missing = append(missing, "security_opt: [\"no-new-privileges:true\"]")
	}
	if len(missing) > 0 {
		p.diag(model.SeverityInfo, "dk_hardening", svcFile, svcLine, id,
			fmt.Sprintf("%s could be hardened: %s", name, strings.Join(missing, ", ")),
			"Add "+strings.Join(missing, ", ")+" (with tmpfs: for paths it writes, and cap_add: for the capabilities it really needs).")
	}
}

func allInterfaces(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" || ip == "[::]" }

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func isJVM(image string) bool {
	r := ParseImage(image)
	if r == nil {
		return false
	}
	base := path.Base(r.Repo)
	return slices.Contains([]string{"openjdk", "eclipse-temurin", "amazoncorretto", "maven", "gradle", "elasticsearch", "kafka", "cassandra", "sonarqube", "keycloak", "neo4j", "opensearch"}, base) || strings.Contains(base, "jdk") || strings.Contains(base, "jre")
}

// firstPort is the container port a built service listens on, for a
// healthcheck example.
func firstPort(s types.ServiceConfig, final *StageInfo) string {
	for _, port := range s.Ports {
		if port.Target > 0 {
			return strconv.Itoa(int(port.Target))
		}
	}
	for _, e := range append(slices.Clone(s.Expose), final.Expose...) {
		if n := portNumber(e); n > 0 {
			return strconv.Itoa(n)
		}
	}
	return "8080"
}

// portNumber reads "8080", "8080/tcp" (0 for anything else).
func portNumber(s string) int {
	s, _, _ = strings.Cut(s, "/")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func (p *Project) checkHealthTiming(name string, s types.ServiceConfig, final *StageInfo) {
	var interval, timeout time.Duration
	file, line := "", 0
	switch {
	case s.HealthCheck != nil && !s.HealthCheck.Disable:
		if s.HealthCheck.Interval != nil {
			interval = time.Duration(*s.HealthCheck.Interval)
		}
		if s.HealthCheck.Timeout != nil {
			timeout = time.Duration(*s.HealthCheck.Timeout)
		}
		file, line = p.fieldLine(name, "healthcheck")
	case s.HealthCheck == nil && final != nil && final.Health != nil && !final.Health.Disabled:
		interval, _ = time.ParseDuration(final.Health.Interval)
		timeout, _ = time.ParseDuration(final.Health.Timeout)
		file = p.dockerfilePath(s)
		if in := p.chainInstruction(s, "HEALTHCHECK"); in != nil {
			line = in.StartLine
		}
	default:
		return
	}
	switch {
	case interval > 0 && interval < 5*time.Second:
		p.diag(model.SeverityInfo, "dk_healthcheck_timing", file, line, ServiceID(name),
			fmt.Sprintf("%s's healthcheck runs every %s: a new process in the container %d times a minute", name, interval, int(time.Minute/interval)),
			"Use interval: 10s (or more) and start_interval: 2s with start_period: for a fast first check.")
	case interval > 0 && timeout > interval:
		p.diag(model.SeverityInfo, "dk_healthcheck_timing", file, line, ServiceID(name),
			fmt.Sprintf("%s's healthcheck timeout (%s) is longer than its interval (%s)", name, timeout, interval),
			"Keep timeout below interval (e.g. interval: 10s, timeout: 3s).")
	}
}

var signalAwareServers = []string{"uvicorn", "gunicorn", "hypercorn", "daphne", "granian", "pm2-runtime", "tini", "dumb-init"}

// checkInit flags node/python running as PID 1 without an init.
func (p *Project) checkInit(name string, s types.ServiceConfig, final *StageInfo) {
	if s.Init != nil && *s.Init {
		return
	}
	var entry, cmd []string
	execForm := true
	if len(s.Entrypoint) > 0 {
		entry = s.Entrypoint
		cmd = s.Command
	} else {
		if s.Build != nil {
			if in := p.chainInstruction(s, "ENTRYPOINT"); in != nil {
				entry, execForm = execWords(in)
			}
		}
		if len(s.Command) > 0 {
			cmd = s.Command
		} else if s.Build != nil && len(entry) == 0 {
			if in := p.chainInstruction(s, "CMD"); in != nil {
				var ok bool
				cmd, ok = execWords(in)
				execForm = execForm && ok
			}
		}
	}
	if !execForm {
		return // the shell-form rule covers it
	}
	words := append(slices.Clone(entry), cmd...)
	if len(words) == 0 {
		return
	}
	if s.Build == nil {
		// Images run as is: only node/python images started with an explicit command.
		r := ParseImage(s.Image)
		if r == nil || !slices.Contains([]string{"node", "python", "pypy"}, path.Base(r.Repo)) || len(s.Command) == 0 && len(s.Entrypoint) == 0 {
			return
		}
	}
	for _, w := range words[:min(len(words), 4)] {
		if slices.Contains(signalAwareServers, path.Base(w)) {
			return
		}
	}
	prog := path.Base(words[0])
	if !slices.Contains([]string{"node", "nodejs", "npm", "npx", "yarn", "pnpm", "nodemon", "ts-node", "tsx", "bun"}, prog) && !strings.HasPrefix(prog, "python") && prog != "flask" {
		return
	}
	file, line := p.ServiceLine(name)
	p.diag(model.SeverityInfo, "dk_no_init", file, line, ServiceID(name),
		fmt.Sprintf("%s runs %s as PID 1 without an init: it ignores SIGTERM and docker stop takes 10s", name, prog),
		"Add init: true to the service (Docker's tini forwards signals and reaps zombies), or handle SIGTERM in the app.")
}

// execWords is an instruction's command and whether it is exec form.
func execWords(in *Instruction) ([]string, bool) {
	if in.JSON != nil {
		return in.JSON, true
	}
	return Words(in.Args), false
}

// chainInstruction is the last instruction cmd of the final stage of a
// service, or of the stages it builds FROM.
func (p *Project) chainInstruction(s types.ServiceConfig, cmd string) *Instruction {
	stages, target := p.stages(s)
	if target < 0 {
		return nil
	}
	df := p.Dockerfiles[p.dockerfilePath(s)]
	byRef := map[string]int{}
	for i, st := range stages {
		byRef[stageRef(st.Stage)] = i
	}
	for i := target; ; {
		ins := stages[i].Instructions
		for j := len(ins) - 1; j >= 1; j-- {
			if in := &df.Instructions[ins[j]]; in.Cmd == cmd {
				return in
			}
		}
		next, ok := byRef[stages[i].FromStage]
		if stages[i].FromStage == "" || !ok || next >= i {
			return nil
		}
		i = next
	}
}

// fixedPorts are the ports database images always listen on (unless
// their command changes it).
var fixedPorts = map[string][]int{
	"postgres": {5432}, "postgis": {5432}, "mysql": {3306, 33060}, "mariadb": {3306},
	"mongo": {27017}, "redis": {6379}, "valkey": {6379}, "memcached": {11211},
}

func (p *Project) checkExpose(name string, s types.ServiceConfig, final *StageInfo, data *dataService) {
	if len(s.Command) > 0 || len(s.Entrypoint) > 0 || len(s.Environment) > 0 && s.Environment["PORT"] != nil {
		return // the port may be set there
	}
	var listen []int
	what := ""
	switch {
	case final != nil && len(final.Expose) > 0:
		for _, e := range final.Expose {
			if n := portNumber(e); n > 0 {
				listen = append(listen, n)
			}
		}
		what = "its Dockerfile EXPOSEs"
	case s.Build == nil && data != nil:
		if r := ParseImage(s.Image); r != nil {
			listen = fixedPorts[path.Base(r.Repo)]
		}
		what = data.label + " listens on"
	default:
		return
	}
	if len(listen) == 0 {
		return
	}
	for _, port := range s.Ports {
		if port.Target == 0 || slices.Contains(listen, int(port.Target)) {
			continue
		}
		ls := make([]string, len(listen))
		for i, l := range listen {
			ls[i] = strconv.Itoa(l)
		}
		file, line := p.fieldLine(name, "ports")
		host := port.Published
		if host == "" {
			host = strconv.Itoa(listen[0])
		}
		p.diag(model.SeverityInfo, "dk_expose_mismatch", file, line, ServiceID(name),
			fmt.Sprintf("%s publishes container port %d but %s %s", name, port.Target, what, strings.Join(ls, ", ")),
			fmt.Sprintf("Publish the port the app listens on (\"%s:%d\"), or change the app's port and its EXPOSE together.", host, listen[0]))
		return
	}
}

var depDirs = []string{"node_modules", "vendor", ".venv", "venv"}

func (p *Project) checkBindDeps(name string, s types.ServiceConfig) {
	targets := map[string]bool{}
	for _, v := range s.Volumes {
		targets[path.Clean(v.Target)] = true
	}
	for _, v := range s.Volumes {
		if v.Type != types.VolumeTypeBind {
			continue
		}
		src := p.rel(v.Source)
		var found string
		if slices.Contains(depDirs, path.Base(src)) {
			found = path.Base(src)
		} else if src == "." || inside(src) {
			for _, d := range depDirs {
				if p.isDir(path.Join(src, d)) && !targets[path.Join(path.Clean(v.Target), d)] {
					found = d
					break
				}
			}
		}
		if found == "" {
			continue
		}
		file, line := p.refLine(name, "volumes", v.Source)
		vol := strings.ReplaceAll(name+"-"+strings.TrimPrefix(found, "."), "_", "-")
		dst := path.Join(path.Clean(v.Target), found)
		if path.Base(src) == found {
			dst = path.Clean(v.Target)
		}
		p.diag(model.SeverityWarning, "dk_bind_deps", file, line, ServiceID(name),
			fmt.Sprintf("%s reads %s through a bind mount of %s: on %s every file access crosses Docker Desktop's file sharing", name, found, orStr(src, v.Source), hostOSName()),
			fmt.Sprintf("Keep %s in a named volume: add \"%s:%s\" to volumes (declare %s at the top level) and install dependencies in the image, or use docker compose watch to sync only sources.", found, vol, dst, vol))
	}
}

func hostOSName() string {
	if sharedFilesOS == "windows" {
		return "Windows"
	}
	return "macOS"
}

// ---------------------------------------------------------------------------
// Data services (databases, caches, brokers)

type dataService struct {
	label    string
	category string // database | cache | messaging
	paths    []string
	ports    []int
}

// officialData are data images known by their official name, for the
// images missing from the palette.
var officialData = map[string]dataService{
	"postgres":      {"PostgreSQL", "database", []string{"/var/lib/postgresql/data"}, []int{5432}},
	"postgis":       {"PostGIS", "database", []string{"/var/lib/postgresql/data"}, []int{5432}},
	"mysql":         {"MySQL", "database", []string{"/var/lib/mysql"}, []int{3306}},
	"mariadb":       {"MariaDB", "database", []string{"/var/lib/mysql"}, []int{3306}},
	"mongo":         {"MongoDB", "database", []string{"/data/db"}, []int{27017}},
	"redis":         {"Redis", "cache", []string{"/data"}, []int{6379}},
	"valkey":        {"Valkey", "cache", []string{"/data"}, []int{6379}},
	"memcached":     {"Memcached", "cache", nil, []int{11211}},
	"couchdb":       {"CouchDB", "database", []string{"/opt/couchdb/data"}, []int{5984}},
	"cassandra":     {"Cassandra", "database", []string{"/var/lib/cassandra"}, []int{9042}},
	"elasticsearch": {"Elasticsearch", "database", []string{"/usr/share/elasticsearch/data"}, []int{9200}},
	"influxdb":      {"InfluxDB", "database", []string{"/var/lib/influxdb2"}, []int{8086}},
	"neo4j":         {"Neo4j", "database", []string{"/data"}, nil},
	"rabbitmq":      {"RabbitMQ", "messaging", []string{"/var/lib/rabbitmq"}, []int{5672}},
}

// PublishIP is the host address new services of image publish on:
// databases, caches and brokers only on this machine (127.0.0.1), other
// services on every interface ("").
func PublishIP(image string) string {
	if dataServiceOf(image) != nil {
		return "127.0.0.1"
	}
	return ""
}

// LongRunning tells a server from a one-off job: a restart policy that brings it back after it
// exits, a healthcheck, published ports or a data image all mean it is not meant to exit, so
// service_completed_successfully on it never holds.
func LongRunning(s types.ServiceConfig, healthcheck bool) bool {
	switch s.Restart {
	case types.RestartPolicyAlways, types.RestartPolicyUnlessStopped:
		return true
	}
	if s.Deploy != nil && s.Deploy.RestartPolicy != nil && s.Deploy.RestartPolicy.Condition == "any" {
		return true
	}
	return healthcheck || len(s.Ports) > 0 || dataServiceOf(s.Image) != nil
}

// dataServiceOf recognizes an image that holds data (nil otherwise): the
// palette's databases, caches and brokers, else official images by name.
func dataServiceOf(image string) *dataService {
	r := ParseImage(image)
	if r == nil || strings.Contains(r.Repo, "$") {
		return nil
	}
	if im, ok := ImageFor(image); ok {
		switch im.Category {
		case "database", "cache", "messaging":
			d := &dataService{label: im.Label, category: im.Category, ports: im.Ports}
			for _, v := range im.Volumes {
				d.paths = append(d.paths, v.Target)
			}
			if im.Category == "messaging" && len(d.ports) > 0 {
				d.ports = d.ports[:1]
			}
			return d
		}
		return nil
	}
	repo := strings.TrimPrefix(strings.TrimPrefix(r.Repo, "docker.io/"), "library/")
	if d, ok := officialData[repo]; ok {
		return &d
	}
	return nil
}

// ---------------------------------------------------------------------------
// Dockerfiles

// pkgInstall is a dependency install found in a RUN.
type pkgInstall struct {
	tool      string   // npm, yarn, pnpm, pip, poetry, uv, go, maven, gradle, dotnet, cargo, bundler, composer
	cmd       string   // the command as written
	manifests []string // files that define the dependencies
	cache     string   // cache mount target ("" when none fits)
}

var manifestsOf = map[string][]string{
	"npm":      {"package.json", "package-lock.json", "npm-shrinkwrap.json"},
	"yarn":     {"package.json", "yarn.lock"},
	"pnpm":     {"package.json", "pnpm-lock.yaml"},
	"pip":      {"requirements*.txt", "pyproject.toml", "setup.py", "setup.cfg", "Pipfile", "Pipfile.lock"},
	"poetry":   {"pyproject.toml", "poetry.lock"},
	"uv":       {"pyproject.toml", "uv.lock"},
	"go":       {"go.mod", "go.sum"},
	"maven":    {"pom.xml"},
	"gradle":   {"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle"},
	"dotnet":   {"*.csproj", "*.fsproj", "*.vbproj", "*.sln", "Directory.Packages.props", "nuget.config"},
	"cargo":    {"Cargo.toml", "Cargo.lock"},
	"bundler":  {"Gemfile", "Gemfile.lock"},
	"composer": {"composer.json", "composer.lock"},
}

var cacheOf = map[string]string{
	"npm": "/root/.npm", "yarn": "/usr/local/share/.cache/yarn", "pnpm": "/root/.local/share/pnpm/store",
	"pip": "/root/.cache/pip", "poetry": "/root/.cache/pypoetry", "uv": "/root/.cache/uv",
	"go": "/go/pkg/mod", "maven": "/root/.m2", "gradle": "/root/.gradle", "dotnet": "/root/.nuget/packages",
	"cargo": "/usr/local/cargo/registry",
}

var segmentSplit = regexp.MustCompile(`&&|\|\||;|\||\n`)

// runText is the shell text of a RUN (heredoc bodies included).
func runText(in *Instruction) string {
	t := in.Args
	if in.JSON != nil {
		t = strings.Join(in.JSON, " ")
	}
	for _, h := range in.Heredocs {
		t += "\n" + h.Content
	}
	return t
}

// segments splits a RUN into its commands, each as words without leading
// VAR=value assignments, sudo or exec.
func segments(text string) [][]string {
	var out [][]string
	for _, seg := range segmentSplit.Split(text, -1) {
		w := Words(seg)
		for len(w) > 0 && (strings.Contains(w[0], "=") && !strings.HasPrefix(w[0], "-") || w[0] == "sudo" || w[0] == "exec" || w[0] == "env" || w[0] == "(" || w[0] == "{") {
			w = w[1:]
		}
		if len(w) > 0 {
			out = append(out, w)
		}
	}
	return out
}

// shortCmd is a command for messages (long ones cut).
func shortCmd(w []string) string {
	s := strings.Join(w, " ")
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}

func positional(words []string) []string {
	var out []string
	for _, w := range words {
		if !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// projectInstall recognizes a command installing a project's dependencies
// (not a global tool).
func projectInstall(w []string) *pkgInstall {
	prog := path.Base(w[0])
	arg := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	mk := func(tool string) *pkgInstall {
		return &pkgInstall{tool: tool, cmd: shortCmd(w), manifests: manifestsOf[tool], cache: cacheOf[tool]}
	}
	switch {
	case prog == "npm" && slices.Contains([]string{"ci", "install", "i"}, arg(1)):
		if slices.Contains(w, "-g") || slices.Contains(w, "--global") || len(positional(w[2:])) > 0 {
			return nil
		}
		return mk("npm")
	case prog == "yarn" && (len(w) == 1 || arg(1) == "install" || strings.HasPrefix(arg(1), "-")):
		return mk("yarn")
	case prog == "pnpm" && slices.Contains([]string{"install", "i", "fetch"}, arg(1)) && len(positional(w[2:])) == 0:
		return mk("pnpm")
	case isPip(w):
		i := slices.Index(w, "install")
		rest := w[i+1:]
		if slices.Contains(rest, "-r") || slices.Contains(rest, "--requirement") || slices.ContainsFunc(rest, func(s string) bool {
			return s == "." || s == "./" || strings.HasPrefix(s, "-r") && len(s) > 2 || strings.HasPrefix(s, ".[")
		}) {
			return mk("pip")
		}
	case prog == "poetry" && arg(1) == "install", prog == "pipenv" && arg(1) == "install":
		return mk("poetry")
	case prog == "uv" && (arg(1) == "sync" || arg(1) == "pip" && arg(2) == "install" && (slices.Contains(w, "-r") || slices.Contains(w, "."))):
		return mk("uv")
	case prog == "go" && (arg(1) == "mod" && arg(2) == "download" || arg(1) == "build" || arg(1) == "install" && !strings.Contains(strings.Join(w, " "), "@")):
		return mk("go")
	case prog == "mvn" || prog == "mvnw":
		return mk("maven")
	case prog == "gradle" || prog == "gradlew":
		return mk("gradle")
	case prog == "dotnet" && slices.Contains([]string{"restore", "publish", "build"}, arg(1)):
		return mk("dotnet")
	case prog == "cargo" && slices.Contains([]string{"build", "fetch"}, arg(1)):
		return mk("cargo")
	case prog == "bundle" && arg(1) == "install":
		return mk("bundler")
	case prog == "composer" && arg(1) == "install":
		return mk("composer")
	}
	return nil
}

func isPip(w []string) bool {
	b := path.Base(w[0])
	if strings.HasPrefix(b, "pip") {
		return slices.Contains(w[1:], "install")
	}
	if strings.HasPrefix(b, "python") && len(w) > 3 && w[1] == "-m" && w[2] == "pip" {
		return slices.Contains(w[3:], "install")
	}
	return false
}

// compiles recognizes a build step of a compiled language or a JS bundler
// (and the "run from source" commands that compile at every start).
func compiles(w []string) string {
	prog := path.Base(w[0])
	arg := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	joined := " " + strings.Join(w, " ") + " "
	switch {
	case prog == "go" && (arg(1) == "build" || arg(1) == "run"):
		return "Go"
	case prog == "cargo" && (arg(1) == "build" || arg(1) == "run"):
		return "Rust"
	case (prog == "mvn" || prog == "mvnw") && (strings.Contains(joined, " package ") || strings.Contains(joined, " install ") || strings.Contains(joined, " verify ") || strings.Contains(joined, "spring-boot:run")):
		return "Java (Maven)"
	case (prog == "gradle" || prog == "gradlew") && gradleBuildRe.MatchString(joined):
		return "Java (Gradle)"
	case prog == "dotnet" && slices.Contains([]string{"publish", "build", "run"}, arg(1)):
		return ".NET"
	case (prog == "npm" || prog == "yarn" || prog == "pnpm" || prog == "bun") && (arg(1) == "build" || arg(1) == "run" && strings.HasPrefix(arg(2), "build")):
		return "a JavaScript bundle"
	case prog == "npx" && slices.Contains([]string{"vite", "next", "ng", "tsc", "webpack", "nest"}, arg(1)) && (arg(2) == "build" || arg(1) == "tsc" || arg(1) == "webpack"):
		return "a JavaScript bundle"
	}
	return ""
}

func cacheMounted(in *Instruction, needle string) bool {
	for _, f := range in.Flags {
		if v, ok := strings.CutPrefix(f, "mount="); ok && strings.Contains(v, "type=cache") && (needle == "" || strings.Contains(v, needle)) {
			return true
		}
	}
	return false
}

// matchesManifest reports whether a COPY source names a dependency
// manifest (a glob like package*.json counts).
func matchesManifest(src string) bool {
	base := path.Base(strings.TrimSuffix(src, "/"))
	for _, list := range manifestsOf {
		for _, m := range list {
			if ok, _ := path.Match(m, base); ok {
				return true
			}
			if ok, _ := path.Match(base, strings.ReplaceAll(m, "*", "x")); ok && strings.ContainsAny(base, "*?[") {
				return true
			}
		}
	}
	return false
}

func copySources(in *Instruction) []string {
	words := in.JSON
	if words == nil {
		words = Words(in.Args)
	}
	if len(words) < 2 {
		return nil
	}
	out := make([]string, 0, len(words)-1)
	for _, w := range words[:len(words)-1] {
		out = append(out, unquote(w))
	}
	return out
}

func wholeContext(src string) bool {
	return src == "." || src == "./" || src == "*" || src == "./*"
}

// dockerfilePractices checks the Dockerfile df a service builds (each
// Dockerfile/target once).
func (p *Project) dockerfilePractices(name string, s types.ServiceConfig, rel string, seen map[string]bool) {
	stages, target := p.stages(s)
	if target < 0 {
		return
	}
	df := p.Dockerfiles[rel]
	id := ServiceID(name)
	ctx := p.contextPath(s)
	add := func(sev model.Severity, code string, line int, msg, fix string) {
		k := code + "\x00" + rel + "\x00" + strconv.Itoa(line)
		if seen[k] {
			return
		}
		seen[k] = true
		p.diag(sev, code, rel, line, id, msg, fix)
	}
	reached := reachable(stages, target)
	final := stages[target]
	root := target
	byRef := map[string]int{}
	for i, st := range stages {
		byRef[stageRef(st.Stage)] = i
	}
	for stages[root].FromStage != "" {
		root = byRef[stages[root].FromStage]
	}

	// Unpinned bases.
	for _, i := range reached {
		st := stages[i]
		if st.FromStage != "" || strings.Contains(st.From, "$") || strings.EqualFold(st.From, "scratch") {
			continue
		}
		if img := ParseImage(st.From); img != nil && img.Digest == "" && (img.Tag == "" || img.Tag == "latest") {
			add(model.SeverityInfo, "dk_latest_tag", st.Line, fmt.Sprintf("%s: FROM %s has no version tag", rel, st.From),
				fmt.Sprintf("Pin it, e.g. FROM %s:<version> (a major-minor tag like %s:22-alpine gets security fixes without surprises).", img.Repo, path.Base(img.Repo)))
		}
	}

	// Single-stage builds of compiled code; heavy runtime bases.
	singleStage := false
	if len(reached) == 1 {
		for _, ii := range final.Instructions[1:] {
			in := &df.Instructions[ii]
			var lang string
			switch in.Cmd {
			case "RUN":
				for _, w := range segments(runText(in)) {
					if lang = compiles(w); lang != "" {
						break
					}
				}
			case "CMD", "ENTRYPOINT":
				if w, _ := execWords(in); len(w) > 0 {
					lang = compiles(w)
				}
			}
			if lang == "" {
				continue
			}
			singleStage = true
			if len(df.Stages) > 1 && target < len(df.Stages)-1 {
				// A multi-stage Dockerfile whose build stage is the target.
				add(model.SeverityInfo, "dk_single_stage", in.StartLine,
					fmt.Sprintf("service %s runs build stage %s of %s: the container carries the %s toolchain and sources", name, stageRef(final.Stage), rel, lang),
					fmt.Sprintf("Point build.target at the runtime stage (%s), or remove target to build the last stage.", stageRef(df.Stages[len(df.Stages)-1])))
			} else {
				add(model.SeverityWarning, "dk_single_stage", in.StartLine,
					fmt.Sprintf("%s builds %s in its only stage: the image ships the toolchain, build cache and sources", rel, lang),
					multiStageFix(lang))
			}
			break
		}
	}
	if !singleStage {
		if base := stages[root].From; !strings.Contains(base, "$") {
			if better := slimmerBase(base); better != "" {
				add(model.SeverityInfo, "dk_heavy_base", stages[root].Line,
					fmt.Sprintf("%s: the image runs on %s, a full image with tools the app doesn't need at runtime", rel, base),
					fmt.Sprintf("Run on %s (build in an earlier stage if the app needs compilers to install).", better))
			}
		}
	}

	// Per-instruction checks of the stages the target builds.
	cacheHinted := false
	for _, i := range reached {
		st := stages[i]
		wholeLine, manifestFirst, ordered := 0, false, false
		for _, ii := range st.Instructions[1:] {
			in := &df.Instructions[ii]
			switch in.Cmd {
			case "COPY", "ADD":
				if _, from := in.Flag("from"); from || len(in.Heredocs) > 0 {
					continue
				}
				for _, src := range copySources(in) {
					if wholeContext(src) && wholeLine == 0 {
						wholeLine = in.StartLine
					} else if wholeLine == 0 && matchesManifest(src) {
						manifestFirst = true
					}
				}
				if in.Cmd == "ADD" {
					p.checkAdd(in, rel, add)
				}
			case "RUN":
				text := runText(in)
				segs := segments(text)
				for _, f := range in.Flags {
					if v, ok := strings.CutPrefix(f, "mount="); ok && strings.Contains(v, "type=bind") && matchesManifest(mountSource(v)) && wholeLine == 0 {
						manifestFirst = true
					}
				}
				for _, w := range segs {
					inst := projectInstall(w)
					if inst == nil {
						continue
					}
					if !ordered {
						ordered = true
						if wholeLine > 0 && !manifestFirst && !(inst.tool == "go" && !p.exists(path.Join(ctx, "go.sum"))) {
							add(model.SeverityWarning, "dk_layer_order", wholeLine,
								fmt.Sprintf("%s:%d copies the whole context before %s (line %d): every source change reinstalls all dependencies", rel, wholeLine, inst.cmd, in.StartLine),
								fmt.Sprintf("Copy the manifests first and install, then copy the sources:\nCOPY %s ./\nRUN %s\nCOPY . .", manifestExample(inst.tool), inst.cmd))
						}
					}
					if !cacheHinted && inst.cache != "" && !cacheMounted(in, "") {
						cacheHinted = true
						add(model.SeverityInfo, "dk_cache_mount", in.StartLine,
							fmt.Sprintf("%s:%d: %s downloads every package again whenever this layer rebuilds", rel, in.StartLine, inst.cmd),
							fmt.Sprintf("Keep the download cache between builds: RUN --mount=type=cache,target=%s %s", inst.cache, inst.cmd))
					}
				}
				p.checkRunHygiene(in, text, segs, st, rel, add)
			}
		}
	}

	// Signals: shell-form CMD/ENTRYPOINT of the final image.
	if len(s.Entrypoint) == 0 {
		entry := p.chainInstruction(s, "ENTRYPOINT")
		cmd := p.chainInstruction(s, "CMD")
		switch {
		case entry != nil && entry.JSON == nil && !strings.HasPrefix(strings.TrimSpace(entry.Args), "exec "):
			add(model.SeverityWarning, "dk_shell_form", entry.StartLine,
				fmt.Sprintf("%s: ENTRYPOINT %s runs under /bin/sh -c: the app doesn't receive SIGTERM", rel, entry.Args),
				fmt.Sprintf("Use the exec form: ENTRYPOINT %s", jsonForm(entry.Args)))
		case entry == nil && cmd != nil && cmd.JSON == nil && len(s.Command) == 0 && !strings.HasPrefix(strings.TrimSpace(cmd.Args), "exec "):
			add(model.SeverityWarning, "dk_shell_form", cmd.StartLine,
				fmt.Sprintf("%s: CMD %s runs under /bin/sh -c: the app doesn't receive SIGTERM", rel, cmd.Args),
				fmt.Sprintf("Use the exec form: CMD %s", jsonForm(cmd.Args)))
		}
	}

	// Secrets in ARG/ENV.
	secretArg := func(in *Instruction) {
		switch in.Cmd {
		case "ARG":
			for _, a := range parseArgs(in.Args) {
				if IsSecretKey(a.Name) {
					add(model.SeverityWarning, "dk_secret_build", in.StartLine,
						fmt.Sprintf("%s: ARG %s passes a secret as a build argument: it is stored in the image history", rel, a.Name),
						fmt.Sprintf("Use a build secret: RUN --mount=type=secret,id=%s,env=%s <command>, and in compose build: {secrets: [%s]} with a top-level secrets: %s: {environment: %s}.", strings.ToLower(a.Name), a.Name, strings.ToLower(a.Name), strings.ToLower(a.Name), a.Name))
				}
			}
		case "ENV":
			for _, k := range sortedKeys(parseEnv(in.Args)) {
				v := parseEnv(in.Args)[k]
				if IsSecretKey(k) && strings.TrimSpace(v) != "" && !strings.HasPrefix(v, "$") {
					add(model.SeverityWarning, "dk_secret_build", in.StartLine,
						fmt.Sprintf("%s: ENV %s writes a secret into the image configuration", rel, k),
						fmt.Sprintf("Remove it from the Dockerfile and set %s at runtime (compose environment: %s: ${%s} from .env, or secrets:).", k, k, k))
				}
			}
		}
	}
	for idx := range df.Instructions {
		if in := &df.Instructions[idx]; in.Cmd == "ARG" && (len(df.Stages) == 0 || in.StartLine < df.Stages[0].Line) {
			secretArg(in)
		}
	}
	for _, i := range reached {
		for _, ii := range stages[i].Instructions[1:] {
			secretArg(&df.Instructions[ii])
		}
	}
}

func mountSource(v string) string {
	for _, kv := range strings.Split(v, ",") {
		if src, ok := strings.CutPrefix(kv, "source="); ok {
			return src
		}
		if src, ok := strings.CutPrefix(kv, "src="); ok {
			return src
		}
	}
	return ""
}

func (p *Project) exists(rel string) bool {
	_, err := p.read(rel)
	return err == nil
}

func manifestExample(tool string) string {
	switch tool {
	case "npm":
		return "package.json package-lock.json"
	case "yarn":
		return "package.json yarn.lock"
	case "pnpm":
		return "package.json pnpm-lock.yaml"
	case "pip":
		return "requirements.txt"
	case "poetry":
		return "pyproject.toml poetry.lock"
	case "uv":
		return "pyproject.toml uv.lock"
	case "go":
		return "go.mod go.sum"
	case "maven":
		return "pom.xml"
	case "gradle":
		return "build.gradle settings.gradle"
	case "dotnet":
		return "*.csproj"
	case "cargo":
		return "Cargo.toml Cargo.lock"
	case "bundler":
		return "Gemfile Gemfile.lock"
	case "composer":
		return "composer.json composer.lock"
	}
	return "<manifest>"
}

func multiStageFix(lang string) string {
	switch lang {
	case "Go":
		return "Build in a first stage and copy only the binary:\nFROM golang:1.23-alpine AS build … RUN CGO_ENABLED=0 go build -o /out/app .\nFROM gcr.io/distroless/static-debian12:nonroot\nCOPY --from=build /out/app /app\nENTRYPOINT [\"/app\"]"
	case "Rust":
		return "Build in a first stage and copy only the binary:\nFROM rust:1-slim AS build … RUN cargo build --release\nFROM debian:bookworm-slim (or gcr.io/distroless/cc-debian12)\nCOPY --from=build /src/target/release/app /usr/local/bin/app"
	case "Java (Maven)", "Java (Gradle)":
		return "Build the jar in a first stage and run it on a JRE:\nFROM maven:3.9-eclipse-temurin-21 AS build … RUN mvn -B package\nFROM eclipse-temurin:21-jre-alpine\nCOPY --from=build /src/target/app.jar /app/app.jar\nENTRYPOINT [\"java\", \"-jar\", \"/app/app.jar\"]"
	case ".NET":
		return "Publish in an SDK stage and run on the ASP.NET runtime:\nFROM mcr.microsoft.com/dotnet/sdk:8.0 AS build … RUN dotnet publish -c Release -o /out\nFROM mcr.microsoft.com/dotnet/aspnet:8.0-alpine\nCOPY --from=build /out /app"
	}
	return "Bundle in a first stage and ship only the output:\nFROM node:22-alpine AS build … RUN npm ci && npm run build\nFROM nginx:1.27-alpine (static sites) or node:22-alpine with npm ci --omit=dev (servers)\nCOPY --from=build /app/dist …"
}

// slimmerBase suggests a smaller runtime image for a full language/SDK
// base ("" when the base is already small or unknown).
func slimmerBase(base string) string {
	r := ParseImage(base)
	if r == nil {
		return ""
	}
	repo := strings.TrimPrefix(strings.TrimPrefix(r.Repo, "docker.io/"), "library/")
	tag := strings.ToLower(r.Tag)
	small := strings.Contains(tag, "slim") || strings.Contains(tag, "alpine") || strings.Contains(tag, "distroless") || strings.Contains(tag, "chiseled")
	ver := versionPrefixRe.FindString(tag)
	withVer := func(img, def, suffix string) string {
		if ver == "" {
			ver = def
		}
		return img + ":" + ver + suffix
	}
	switch repo {
	case "node":
		if !small {
			return withVer("node", "22", "-slim") + " or " + withVer("node", "22", "-alpine")
		}
	case "python":
		if !small {
			return withVer("python", "3.12", "-slim")
		}
	case "ruby":
		if !small {
			return withVer("ruby", "3.3", "-slim")
		}
	case "openjdk", "eclipse-temurin", "amazoncorretto":
		if !strings.Contains(tag, "jre") {
			return withVer("eclipse-temurin", "21", "-jre-alpine")
		}
	case "maven", "gradle":
		return "eclipse-temurin:21-jre-alpine (build the jar in an earlier stage)"
	case "golang":
		return "gcr.io/distroless/static-debian12 or alpine:3.20 (build the binary in an earlier stage)"
	case "rust":
		return "debian:bookworm-slim or gcr.io/distroless/cc-debian12 (build in an earlier stage)"
	case "mcr.microsoft.com/dotnet/sdk":
		return "mcr.microsoft.com/dotnet/aspnet:8.0-alpine (publish in an earlier stage)"
	}
	return ""
}

func jsonForm(args string) string {
	words := Words(args)
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = strconv.Quote(unquote(w))
	}
	return "[" + strings.Join(q, ", ") + "]"
}

var archiveExt = []string{".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst"}

func (p *Project) checkAdd(in *Instruction, rel string, add func(model.Severity, string, int, string, string)) {
	_, checksum := in.Flag("checksum")
	for _, src := range copySources(in) {
		low := strings.ToLower(src)
		switch {
		case strings.HasPrefix(low, "git@") || strings.HasSuffix(low, ".git") || strings.Contains(low, ".git#"):
		case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
			if !checksum {
				add(model.SeverityInfo, "dk_add", in.StartLine,
					fmt.Sprintf("%s:%d: ADD downloads %s without verifying it", rel, in.StartLine, src),
					"Pin the download: ADD --checksum=sha256:<digest> <url> <dest>, or RUN curl -fsSL <url> -o <dest> && echo \"<sha256>  <dest>\" | sha256sum -c -.")
				return
			}
		case !slices.ContainsFunc(archiveExt, func(e string) bool { return strings.HasSuffix(low, e) }):
			add(model.SeverityInfo, "dk_add", in.StartLine,
				fmt.Sprintf("%s:%d: ADD copies local files: COPY does the same without ADD's archive extraction and URL magic", rel, in.StartLine),
				"Replace ADD with COPY "+in.Args)
			return
		}
	}
}

var aptInstallRe = regexp.MustCompile(`^apt(-get)?$`)

func (p *Project) checkRunHygiene(in *Instruction, text string, segs [][]string, st StageInfo, rel string, add func(model.Severity, string, int, string, string)) {
	var apt, pip, apk, cd bool
	for i, w := range segs {
		switch {
		case aptInstallRe.MatchString(path.Base(w[0])) && slices.Contains(w, "install"):
			apt = true
		case isPip(w):
			pip = true
		case w[0] == "apk" && slices.Contains(w, "add"):
			apk = true
		case i == 0 && w[0] == "cd" && len(segs) > 1 && in.JSON == nil:
			cd = true
		}
	}
	if apt {
		var missing []string
		if !strings.Contains(text, "--no-install-recommends") && !strings.Contains(text, "Install-Recommends=false") && !strings.Contains(text, "Install-Recommends=0") {
			missing = append(missing, "--no-install-recommends")
		}
		if !strings.Contains(text, "/var/lib/apt/lists") && !cacheMounted(in, "/var/lib/apt") {
			missing = append(missing, "rm -rf /var/lib/apt/lists/*")
		}
		if len(missing) > 0 {
			add(model.SeverityInfo, "dk_apt_install", in.StartLine,
				fmt.Sprintf("%s:%d: apt-get install without %s", rel, in.StartLine, strings.Join(missing, " and ")),
				"In one RUN: apt-get update && apt-get install -y --no-install-recommends <packages> && rm -rf /var/lib/apt/lists/*")
		}
	}
	if pip && !strings.Contains(text, "--no-cache-dir") && st.Env["PIP_NO_CACHE_DIR"] == "" && !cacheMounted(in, "pip") && !cacheMounted(in, "/root/.cache") {
		add(model.SeverityInfo, "dk_pip_cache", in.StartLine,
			fmt.Sprintf("%s:%d: pip install keeps its download cache in the image", rel, in.StartLine),
			"Add --no-cache-dir (pip install --no-cache-dir -r requirements.txt), or use RUN --mount=type=cache,target=/root/.cache/pip.")
	}
	if apk && !strings.Contains(text, "--no-cache") && !strings.Contains(text, "/var/cache/apk") && !cacheMounted(in, "/var/cache/apk") {
		add(model.SeverityInfo, "dk_apk_cache", in.StartLine,
			fmt.Sprintf("%s:%d: apk add keeps the package index in the image", rel, in.StartLine),
			"Use apk add --no-cache <packages> (no apk update needed).")
	}
	if cd {
		dir := segs[0]
		target := "<dir>"
		if len(dir) > 1 {
			target = dir[1]
		}
		add(model.SeverityInfo, "dk_workdir", in.StartLine,
			fmt.Sprintf("%s:%d: RUN cd %s && … only changes directory for this RUN", rel, in.StartLine, target),
			fmt.Sprintf("Use WORKDIR %s before the RUN (and drop the cd).", target))
	}
}

// ---------------------------------------------------------------------------
// .dockerignore

// heavyDirs are folders that should never be sent to the builder.
var heavyDirs = []string{"node_modules", ".git", ".venv", "venv", "target", ".gradle", ".next"}

var (
	gradleBuildRe   = regexp.MustCompile(` (build|bootJar|bootRun|assemble|jar|installDist|shadowJar|run) `)
	versionPrefixRe = regexp.MustCompile(`^\d+(\.\d+)?`)
)

// minHeavy is the size from which a heavy folder is worth a warning.
const minHeavy = 5 << 20

func (p *Project) checkDockerignore(name string, s types.ServiceConfig, ctx string) {
	ignore := ""
	ignoreFile := ""
	if df := p.dockerfilePath(s); df != "" {
		if data, err := p.read(df + ".dockerignore"); err == nil {
			ignore, ignoreFile = string(data), df+".dockerignore"
		}
	}
	if ignoreFile == "" {
		f := path.Join(ctx, ".dockerignore")
		if data, err := p.read(f); err == nil {
			ignore, ignoreFile = string(data), f
		}
	}
	patterns := dockerignorePatterns(ignore)
	type found struct {
		rel  string
		size int64
	}
	var heavy []found
	candidates := slices.Clone(heavyDirs)
	for _, child := range p.list(ctx) {
		if strings.HasPrefix(child, ".") || slices.Contains(heavyDirs, child) || !p.isDir(path.Join(ctx, child)) {
			continue
		}
		candidates = append(candidates, child+"/node_modules", child+"/.venv")
	}
	for _, c := range candidates {
		full := path.Join(ctx, c)
		if !p.isDir(full) || ignored(patterns, c) {
			continue
		}
		size := dirSize(filepath.Join(p.Dir, filepath.FromSlash(full)))
		if size >= minHeavy {
			heavy = append(heavy, found{c, size})
		}
	}
	if len(heavy) == 0 {
		return
	}
	parts := make([]string, len(heavy))
	names := make([]string, len(heavy))
	for i, h := range heavy {
		parts[i] = fmt.Sprintf("%s (%s)", h.rel, humanBytes(h.size))
		names[i] = h.rel
	}
	file, line := p.fieldLine(name, "build")
	what := "has no .dockerignore"
	fix := fmt.Sprintf("Create %s with:\n%s", path.Join(ctx, ".dockerignore"), strings.Join(append(names, "**/*.log", ".env"), "\n"))
	if ignoreFile != "" {
		what = ignoreFile + " doesn't exclude them"
		file, line = ignoreFile, 1
		fix = fmt.Sprintf("Add to %s:\n%s", ignoreFile, strings.Join(names, "\n"))
	}
	p.diag(model.SeverityWarning, "dk_dockerignore", file, line, ServiceID(name),
		fmt.Sprintf("build context %s sends %s to the builder on every build: it %s", ctx, strings.Join(parts, ", "), what), fix)
}

// dockerignorePatterns reads the exclusion patterns of a .dockerignore
// (re-inclusions with ! are kept prefixed).
func dockerignorePatterns(src string) []string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		neg := strings.HasPrefix(l, "!")
		l = strings.TrimPrefix(l, "!")
		l = strings.Trim(path.Clean("/"+l), "/")
		if neg {
			l = "!" + l
		}
		out = append(out, l)
	}
	return out
}

// ignored reports whether a context folder is excluded by the patterns
// (the last matching pattern wins, like docker).
func ignored(patterns []string, rel string) bool {
	match := func(pat, rel string) bool {
		if pat == "" {
			return false
		}
		if pat == "*" || pat == "**" || pat == "**/*" {
			return true
		}
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		if strings.HasSuffix(pat, "/**") && (rel == strings.TrimSuffix(pat, "/**") || strings.HasPrefix(rel, strings.TrimSuffix(pat, "**"))) {
			return true
		}
		if rest, ok := strings.CutPrefix(pat, "**/"); ok {
			parts := strings.Split(rel, "/")
			for i := range parts {
				if m, _ := path.Match(strings.TrimSuffix(rest, "/**"), strings.Join(parts[i:], "/")); m {
					return true
				}
			}
		}
		// A parent folder excluded excludes rel.
		if i := strings.LastIndex(rel, "/"); i > 0 {
			if ok, _ := path.Match(pat, rel[:i]); ok {
				return true
			}
		}
		return false
	}
	out := false
	for _, pat := range patterns {
		if neg, ok := strings.CutPrefix(pat, "!"); ok {
			if match(neg, rel) {
				out = false
			}
			continue
		}
		if match(pat, rel) {
			out = true
		}
	}
	return out
}

// dirSize sums the sizes of a folder's files (capped walk), cached by the
// folder's modification time: Open runs on every save and read.
func dirSize(dir string) int64 {
	fi, err := filepath.Abs(dir)
	if err != nil {
		return 0
	}
	st, err := os.Lstat(fi)
	if err != nil {
		return 0
	}
	key := fi + "\x00" + st.ModTime().String()
	sizeCache.Lock()
	if v, ok := sizeCache.m[key]; ok {
		sizeCache.Unlock()
		return v
	}
	sizeCache.Unlock()
	var total int64
	entries := 0
	_ = filepath.WalkDir(fi, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		entries++
		if entries > 200_000 {
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	sizeCache.Lock()
	if len(sizeCache.m) > 256 {
		sizeCache.m = map[string]int64{}
	}
	sizeCache.m[key] = total
	sizeCache.Unlock()
	return total
}

var sizeCache = struct {
	sync.Mutex
	m map[string]int64
}{m: map[string]int64{}}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
