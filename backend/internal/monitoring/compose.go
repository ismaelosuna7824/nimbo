package monitoring

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker monitoring is a compose file of its own (monitoring/compose.yaml
// next to the first compose file, listed after it in Compose.Files) with
// Prometheus, Grafana and cAdvisor, plus their configuration under
// monitoring/. Keeping it in its own file lets Update monitoring rewrite
// it without touching the user's services.

// Service names of the stack.
const (
	PrometheusService = "prometheus"
	GrafanaService    = "grafana"
	CadvisorService   = "cadvisor"
)

// GrafanaPasswordVar is the .env variable with the Grafana admin password.
const GrafanaPasswordVar = "GRAFANA_ADMIN_PASSWORD"

// ComposeDir is the folder of the generated files, relative to the
// compose project directory.
const ComposeDir = "monitoring"

// ComposeFile is the generated compose file of a project whose first
// compose file is in projectDir (workflow-relative slash path).
func ComposeFile(projectDir string) string {
	return path.Join(projectDir, ComposeDir, "compose.yaml")
}

// ComposeAlertsFile is the generated alert rules file (it keeps the HTTP
// thresholds).
func ComposeAlertsFile(projectDir string) string {
	return path.Join(projectDir, ComposeDir, "prometheus", "alerts.yml")
}

// ComposeInput is what the docker generator reads.
type ComposeInput struct {
	// Project is the workflow's compose project (with the monitoring file
	// when it was added before).
	Project *compose.Project
	// ProjectName is the compose project name the containers run under
	// (cAdvisor's metrics are filtered on it).
	ProjectName string
	// Password is the Grafana admin password (.env); "" generates one.
	Password string
	// Thresholds tune the HTTP alerts (zero fields: defaults).
	Thresholds HTTPThresholds
}

// ComposeOutput is the generated stack.
type ComposeOutput struct {
	// File is the compose file to list in Compose.Files.
	File string
	// Files are the generated files (workflow-relative).
	Files map[string][]byte
	// Password is the Grafana admin password; NewPassword is set when it
	// was generated (the caller appends it to .env).
	Password    string
	NewPassword bool
	// Ports are the host ports of Grafana and Prometheus.
	GrafanaPort, PrometheusPort int
	Summary                     model.MonitoringSummary
}

// ConflictError is a service of the user's files named like a service of
// the stack.
type ConflictError struct{ Service, File string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s already defines a service named %s: rename or remove it, then add monitoring again", e.File, e.Service)
}

// stack is the pinned image of each service (the image palette's pins).
func stackImage(id string) string {
	im, ok := compose.Image(id)
	if !ok {
		panic("monitoring: image palette has no " + id) // the palette is static
	}
	return im.Image + ":" + im.Tag
}

// composeCost is what the stack reserves (deploy.resources.reservations)
// and Prometheus' retention size.
var composeCost = model.MonitoringCost{CPUMillis: 100 + 50 + 50, MemoryBytes: (256 + 128 + 64) << 20, DiskBytes: 2 << 30}

type scrapeTarget struct {
	service, port, path string
}

// Compose generates the docker monitoring stack.
func Compose(in ComposeInput) (*ComposeOutput, error) {
	p := in.Project
	if p.Model == nil {
		return nil, fmt.Errorf("the compose files don't load: fix the errors in Problems first")
	}
	file := ComposeFile(p.ProjectDir)
	ours := map[string]bool{PrometheusService: true, GrafanaService: true, CadvisorService: true}
	for name := range ours {
		for _, rel := range p.Order {
			if rel == file {
				continue
			}
			if _, v := p.Docs[rel].Get("services", name); v != nil {
				return nil, &ConflictError{Service: name, File: rel}
			}
		}
	}
	// The user's services, what they publish and how Prometheus reaches them.
	var services []string
	published := map[int]bool{}
	networks := map[string]bool{}
	var scrapes []scrapeTarget
	dependents := map[string][]types.ServiceDependency{}
	for _, s := range p.Model.Services {
		for dep, d := range s.DependsOn {
			dependents[dep] = append(dependents[dep], d)
		}
	}
	for _, name := range p.SortedServiceNames() {
		if ours[name] || p.Inactive[name] {
			continue
		}
		s := p.Model.Services[name]
		for _, port := range s.Ports {
			if n, err := strconv.Atoi(port.Published); err == nil {
				published[n] = true
			}
		}
		if oneShot(s, dependents[name]) {
			continue
		}
		services = append(services, name)
		if s.Labels["prometheus.io/scrape"] != "true" {
			continue
		}
		port := s.Labels["prometheus.io/port"]
		if port == "" {
			if len(s.Ports) > 0 {
				port = strconv.Itoa(int(s.Ports[0].Target))
			} else if len(s.Expose) > 0 {
				port = s.Expose[0]
			}
		}
		if _, err := strconv.Atoi(port); err != nil {
			continue // no port to scrape
		}
		scrapes = append(scrapes, scrapeTarget{service: name, port: port, path: orDefault(s.Labels["prometheus.io/path"], "/metrics")})
		if len(s.Networks) == 0 {
			networks["default"] = true
		}
		for n := range s.Networks {
			networks[n] = true
		}
	}
	hostPort := func(svc string, port int) int {
		if prev, ok := p.Model.Services[svc]; ok && ours[svc] {
			for _, pp := range prev.Ports {
				if n, err := strconv.Atoi(pp.Published); err == nil && !published[n] {
					published[n] = true
					return n
				}
			}
		}
		for published[port] {
			port++
		}
		published[port] = true
		return port
	}
	out := &ComposeOutput{File: file, Files: map[string][]byte{}, Password: in.Password}
	if out.Password == "" {
		out.Password, out.NewPassword = NewPassword(), true
	}
	out.PrometheusPort = hostPort(PrometheusService, 9090)
	out.GrafanaPort = hostPort(GrafanaService, 3000)

	dir := path.Join(p.ProjectDir, ComposeDir)
	out.Files[file] = composeYAML(out.PrometheusPort, out.GrafanaPort, slices.Sorted(maps.Keys(networks)))
	out.Files[dir+"/prometheus/prometheus.yml"] = prometheusYAML(in.ProjectName, scrapes)
	t, err := ValidThresholds(in.Thresholds)
	if err != nil {
		return nil, err
	}
	alerts, rules := composeAlerts(services, scrapes, t)
	out.Files[ComposeAlertsFile(p.ProjectDir)] = rules
	out.Files[dir+"/grafana/provisioning/datasources/prometheus.yml"] = []byte(`# ` + generatedMarker + `.
apiVersion: 1
datasources:
  - name: Prometheus
    uid: ` + datasourceUID + `
    type: prometheus
    access: proxy
    url: http://` + PrometheusService + `:9090
    isDefault: true
    editable: false
    jsonData:
      timeInterval: 15s
`)
	out.Files[dir+"/grafana/provisioning/dashboards/nimbo.yml"] = []byte(`# ` + generatedMarker + `: loads the dashboards of monitoring/grafana/dashboards.
apiVersion: 1
providers:
  - name: nimbo
    folder: Nimbo
    type: file
    disableDeletion: true
    allowUiUpdates: false
    options:
      path: /etc/grafana/dashboards
`)
	out.Files[dir+"/grafana/dashboards/containers.json"] = composeDashboard(len(scrapes) > 0)

	files := slices.Sorted(maps.Keys(out.Files))
	sc := []model.MonitoringScrape{}
	for _, s := range scrapes {
		sc = append(sc, model.MonitoringScrape{Target: s.service, Port: s.port, Path: s.path, Via: "Prometheus scrape config (prometheus.io labels)"})
	}
	var notes []string
	if len(scrapes) == 0 {
		notes = append(notes, `No service is labelled prometheus.io/scrape: "true": Prometheus collects container metrics (cAdvisor) only, so there are no HTTP alerts. Label a service that serves /metrics and run Update monitoring.`)
	}
	httpSum := []model.MonitoringHTTP{}
	for _, s := range services {
		h := model.MonitoringHTTP{Target: s, Sources: []string{}}
		if slices.ContainsFunc(scrapes, func(sc scrapeTarget) bool { return sc.service == s }) {
			h.Sources = append(h.Sources, SourceOTel)
		}
		httpSum = append(httpSum, h)
	}
	out.Summary = model.MonitoringSummary{
		Kind: model.WorkflowDocker,
		Components: []model.MonitoringComponent{
			{Name: PrometheusService, Image: stackImage("prometheus"), Purpose: "Scrapes cAdvisor and labelled services every 15s, evaluates the alert rules (7d / 2GB retention)"},
			{Name: GrafanaService, Image: stackImage("grafana"), Purpose: "Containers dashboard: CPU and memory vs limits, network and restarts per service"},
			{Name: CadvisorService, Image: stackImage("cadvisor"), Purpose: "Per-container CPU, memory, network and restart metrics (reads the Docker host read-only)"},
		},
		Files: files, Alerts: alerts, Scrapes: sc, Dashboards: []string{"Containers"}, HTTP: httpSum, Thresholds: t,
		Links: []model.MonitoringLink{
			{Name: "Grafana", URL: fmt.Sprintf("http://127.0.0.1:%d", out.GrafanaPort)},
			{Name: "Prometheus", URL: fmt.Sprintf("http://127.0.0.1:%d", out.PrometheusPort)},
		},
		Credentials: &model.MonitoringCredentials{User: grafanaUser, Password: out.Password, Source: ".env " + GrafanaPasswordVar},
		Cost:        composeCost, Notes: notes,
	}
	return out, nil
}

// oneShot: a service that runs to completion (migrations, seeders) is not
// expected to keep a container running.
func oneShot(s types.ServiceConfig, dependents []types.ServiceDependency) bool {
	for _, d := range dependents {
		if d.Condition == types.ServiceConditionCompletedSuccessfully {
			return true
		}
	}
	return s.Restart == "no" && s.Deploy == nil && len(dependents) == 0 && len(s.Ports) == 0
}

const hardening = `    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
`

func composeYAML(promPort, grafanaPort int, networks []string) []byte {
	nets := ""
	if len(networks) > 0 && !(len(networks) == 1 && networks[0] == "default") {
		nets = "    networks: [" + strings.Join(networks, ", ") + "]\n"
	}
	return []byte(`# ` + generatedMarker + `. Update monitoring regenerates this file and
# monitoring/prometheus, monitoring/grafana from the services.
#
# Prometheus scrapes cAdvisor (CPU, memory, network and restarts of every
# container of the project) and the services labelled
#   prometheus.io/scrape: "true"
#   prometheus.io/port: "8080"     (container port; default: the first port)
#   prometheus.io/path: /metrics   (default)
# Grafana signs in as admin with ` + GrafanaPasswordVar + ` from .env.
services:
  ` + PrometheusService + `:
    image: ` + stackImage("prometheus") + `
    command:
      - --config.file=/etc/prometheus/prometheus.yml
      - --storage.tsdb.path=/prometheus
      - --storage.tsdb.retention.time=7d
      - --storage.tsdb.retention.size=2GB
    volumes:
      - ./` + ComposeDir + `/prometheus:/etc/prometheus:ro
      - prometheus-data:/prometheus
    ports:
      - "127.0.0.1:` + strconv.Itoa(promPort) + `:9090"
` + nets + `    depends_on:
      ` + CadvisorService + `:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:9090/-/ready >/dev/null || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 10s
` + hardening + `    deploy:
      resources:
        limits:
          cpus: "1"
          memory: 512M
        reservations:
          cpus: "0.1"
          memory: 256M

  ` + GrafanaService + `:
    image: ` + stackImage("grafana") + `
    environment:
      GF_SECURITY_ADMIN_USER: ` + grafanaUser + `
      GF_SECURITY_ADMIN_PASSWORD: ${` + GrafanaPasswordVar + `:?set ` + GrafanaPasswordVar + ` in .env}
      GF_USERS_ALLOW_SIGN_UP: "false"
      GF_ANALYTICS_REPORTING_ENABLED: "false"
      GF_ANALYTICS_CHECK_FOR_UPDATES: "false"
      GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH: /etc/grafana/dashboards/containers.json
    volumes:
      - ./` + ComposeDir + `/grafana/provisioning:/etc/grafana/provisioning:ro
      - ./` + ComposeDir + `/grafana/dashboards:/etc/grafana/dashboards:ro
      - grafana-data:/var/lib/grafana
    tmpfs:
      - /tmp
    ports:
      - "127.0.0.1:` + strconv.Itoa(grafanaPort) + `:3000"
    depends_on:
      ` + PrometheusService + `:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:3000/api/health >/dev/null || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 20s
` + hardening + `    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 256M
        reservations:
          cpus: "0.05"
          memory: 128M

  # cAdvisor reads the host's cgroups and Docker state read-only, as its
  # docs run it (https://github.com/google/cadvisor#quick-start); on Docker
  # Desktop these are the Linux VM's paths. It only lists containers
  # (--docker_only) and keeps the compose project/service labels.
  ` + CadvisorService + `:
    image: ` + stackImage("cadvisor") + `
    command:
      - --docker_only=true
      - --housekeeping_interval=10s
      - --store_container_labels=false
      - --whitelisted_container_labels=com.docker.compose.project,com.docker.compose.service
      - --disable_metrics=advtcp,cpu_topology,cpuset,hugetlb,memory_numa,percpu,referenced_memory,resctrl,sched,tcp,udp,process,disk,diskIO
    volumes:
      - /:/rootfs:ro
      - /var/run:/var/run:ro
      - /sys:/sys:ro
      - /var/lib/docker/:/var/lib/docker:ro
    expose:
      - "8080"
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 10s
` + hardening + `    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 256M
        reservations:
          cpus: "0.05"
          memory: 64M

volumes:
  prometheus-data:
  grafana-data:
`)
}

func prometheusYAML(project string, scrapes []scrapeTarget) []byte {
	var b strings.Builder
	b.WriteString(`# ` + generatedMarker + `. Update monitoring regenerates it.
global:
  scrape_interval: 15s
  evaluation_interval: 15s
rule_files:
  - /etc/prometheus/alerts.yml
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["localhost:9090"]
  - job_name: cadvisor
    static_configs:
      - targets: ["` + CadvisorService + `:8080"]
    # Keep the containers of this compose project only (Nimbo runs it as
    # ` + project + `; change the regex when you run it under another name).
    metric_relabel_configs:
      - source_labels: [container_label_com_docker_compose_project]
        regex: ` + project + `
        action: keep
`)
	for _, s := range scrapes {
		// DNS service discovery finds every replica of the service.
		fmt.Fprintf(&b, `  - job_name: %s
    metrics_path: %s
    dns_sd_configs:
      - names: [%s]
        type: A
        port: %s
`, s.service, s.path, s.service, s.port)
	}
	return []byte(b.String())
}

// composeAlerts are the container alerts (one set for every service of
// the project), a ContainerDown alert per long-running service, and HTTP
// alerts on the OpenTelemetry HTTP metrics of the scraped services.
func composeAlerts(services []string, scrapes []scrapeTarget, t HTTPThresholds) ([]model.MonitoringAlert, []byte) {
	const svc = `container_label_com_docker_compose_service!=""`
	common := []alert{
		{name: "ContainerMemoryNearLimit", severity: "warning", forDur: "5m",
			expr:    fmt.Sprintf("container_memory_working_set_bytes{%s}\n  / (container_spec_memory_limit_bytes{%s} > 0)\n  > 0.9", svc, svc),
			short:   "Memory above 90% of the limit for 5m",
			summary: "{{ $labels.container_label_com_docker_compose_service }} uses {{ $value | humanizePercentage }} of its memory limit",
			description: "Docker kills the container (OOM) when it reaches deploy.resources.limits.memory. " +
				"What to do: compare with the peak of a load test and raise the limit with headroom (~30%), or look for a leak if memory only grows."},
		{name: "ContainerCPUThrottled", severity: "warning", forDur: "10m",
			expr:    fmt.Sprintf("rate(container_cpu_cfs_throttled_periods_total{%s}[5m])\n  / rate(container_cpu_cfs_periods_total{%s}[5m])\n  > 0.5", svc, svc),
			short:   "CPU throttled in over 50% of periods for 10m",
			summary: "{{ $labels.container_label_com_docker_compose_service }} is CPU-throttled in {{ $value | humanizePercentage }} of its periods",
			description: "The container hits its CPU limit (deploy.resources.limits.cpus) most of the time: requests queue and latency grows. " +
				"What to do: raise the CPU limit or run more replicas behind a load balancer."},
		{name: "ContainerOOMKilled", severity: "warning",
			expr:    fmt.Sprintf("increase(container_oom_events_total{%s}[10m]) > 0", svc),
			short:   "Out-of-memory kill in the last 10m",
			summary: "{{ $labels.container_label_com_docker_compose_service }} had an out-of-memory kill",
			description: "A process of the container went over the memory limit and was killed. " +
				"What to do: raise deploy.resources.limits.memory from the memory peak of a load test, or cap the app's heap/cache below the limit."},
		{name: "ContainerRestarting", severity: "warning",
			expr:    fmt.Sprintf("changes(container_start_time_seconds{%s}[15m]) > 2", svc),
			short:   "More than 2 restarts in 15m",
			summary: "{{ $labels.container_label_com_docker_compose_service }} restarted {{ $value | humanize }} times in 15 minutes",
			description: "The container keeps crashing and Docker restarts it (restart policy). " +
				"What to do: read its logs from before the restart (docker compose logs), check the healthcheck and the exit code."},
		{name: "ScrapeTargetDown", severity: "warning", forDur: "2m",
			expr:        "up == 0",
			short:       "A metrics endpoint is unreachable for 2m",
			summary:     "Prometheus can't scrape {{ $labels.job }} ({{ $labels.instance }})",
			description: "Its metrics are missing from the dashboards and alerts. What to do: check the service is running and serves the metrics path on the labelled port."},
	}
	var alerts []model.MonitoringAlert
	group := promGroup{Name: "containers"}
	for _, a := range common {
		group.Rules = append(group.Rules, a.rule(nil))
		alerts = append(alerts, model.MonitoringAlert{Name: a.name, Severity: a.severity, Target: "every service", Summary: a.short})
	}
	groups := []promGroup{group}
	if len(services) > 0 {
		down := promGroup{Name: "services"}
		for _, s := range services {
			a := alert{name: "ContainerDown", severity: "critical", forDur: "1m",
				expr:    fmt.Sprintf("absent(container_last_seen{container_label_com_docker_compose_service=%q})", s),
				short:   "No running container for 1m",
				summary: s + " has no running container",
				description: "cAdvisor sees no container of the service: it stopped or keeps failing to start. " +
					"What to do: check docker compose ps and its logs; a failing healthcheck dependency can keep it from starting."}
			down.Rules = append(down.Rules, a.rule(map[string]string{"service": s}))
			alerts = append(alerts, model.MonitoringAlert{Name: a.name, Severity: a.severity, Target: s, Summary: a.short})
		}
		groups = append(groups, down)
	}
	if len(scrapes) > 0 {
		http := promGroup{Name: "http"}
		for _, s := range scrapes {
			http.Rules = append(http.Rules, otelServiceRecordings(s.service)...)
			for _, a := range httpAlerts(serviceRecs, "Service", fmt.Sprintf("service=%q", s.service), s.service, t, SourceOTel) {
				http.Rules = append(http.Rules, a.rule(map[string]string{"service": s.service}))
				alerts = append(alerts, model.MonitoringAlert{Name: a.name, Severity: a.severity, Target: s.service, Summary: a.short})
			}
		}
		groups = append(groups, http)
	}
	return alerts, encodeYAML("# "+generatedMarker+". Update monitoring regenerates it.\n"+thresholdsLine(t), map[string]any{"groups": groups})
}

// composeDashboard is the per-service containers dashboard (cAdvisor).
func composeDashboard(http bool) []byte {
	sel := `container_label_com_docker_compose_service="$service"`
	panels := []map[string]any{row(1, "$service", "service", 0)}
	panels = append(panels,
		timeseries(2, "CPU vs limit", "CPU cores used by the service's containers against their limit (deploy.resources.limits.cpus).", "short", gridPos{0, 1, 12, 8},
			map[string]string{"limit": "red"},
			target{"used", fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{%s}[$__rate_interval]))`, sel)},
			target{"limit", fmt.Sprintf(`sum(container_spec_cpu_quota{%s} / container_spec_cpu_period{%s})`, sel, sel)}),
		timeseries(3, "Memory vs limit", "Working set against the memory limit (Docker kills the container at the limit).", "bytes", gridPos{12, 1, 12, 8},
			map[string]string{"limit": "red"},
			target{"working set", fmt.Sprintf(`sum(container_memory_working_set_bytes{%s})`, sel)},
			target{"limit", fmt.Sprintf(`sum(container_spec_memory_limit_bytes{%s} > 0)`, sel)}),
		timeseries(4, "Network", "Bytes per second received and sent.", "Bps", gridPos{0, 9, 12, 8}, nil,
			target{"received", fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{%s}[$__rate_interval]))`, sel)},
			target{"sent", fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{%s}[$__rate_interval]))`, sel)}),
		timeseries(5, "Containers and restarts", "Running containers (replicas) and restarts per interval.", "short", gridPos{12, 9, 12, 8}, nil,
			target{"containers", fmt.Sprintf(`count(container_last_seen{%s})`, sel)},
			target{"restarts", fmt.Sprintf(`sum(changes(container_start_time_seconds{%s}[$__rate_interval]))`, sel)}),
	)
	if http {
		// OpenTelemetry HTTP metrics of the scraped services (recorded).
		rec := func(name string) string { return fmt.Sprintf(`%s{service="$service"}`, name) }
		panels = append(panels,
			timeseries(6, "Requests/s", "Requests per second (the app's OpenTelemetry HTTP metrics; empty for services that don't export them).", "reqps", gridPos{0, 17, 8, 8}, nil,
				target{"{{source}}", rec(serviceRecs.requests)}),
			timeseries(7, "5xx ratio", "Share of requests answered 5xx (5m rate).", "percentunit", gridPos{8, 17, 8, 8}, nil,
				target{"{{source}}", rec(serviceRecs.errors) + " / " + rec(serviceRecs.requests)}),
			timeseries(8, "p95 latency", "95th percentile of the request duration (5m window).", "s", gridPos{16, 17, 8, 8}, nil,
				target{"{{source}}", rec(serviceRecs.p95)}),
		)
	}
	vars := []map[string]any{queryVar("service", "Service", `label_values(container_last_seen{container_label_com_docker_compose_service!=""}, container_label_com_docker_compose_service)`, "")}
	return dashboard("nimbo-containers", "Containers", "Per-service CPU and memory vs limits, network and restarts (cAdvisor, generated by Nimbo).",
		[]string{"nimbo", "docker"}, vars, panels)
}
