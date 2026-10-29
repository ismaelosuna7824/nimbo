package advisor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker Compose recommendations: a rate-limiting reverse proxy (and the
// OWASP Core Rule Set in production) in front of services that publish
// ports, secrets as files, private and persistent data services, resource
// limits and health checks, and per-environment overlays. They read the
// saved compose graph (Workflow.Compose.Graph).

// dataImage describes an official database, cache or broker image.
type dataImage struct {
	label  string
	dir    string // data directory ("" = keeps no data worth persisting)
	health string // CMD-SHELL health check
}

var dataImages = map[string]dataImage{
	"postgres":      {"PostgreSQL", "/var/lib/postgresql/data", "pg_isready -U postgres"},
	"postgis":       {"PostGIS", "/var/lib/postgresql/data", "pg_isready -U postgres"},
	"mysql":         {"MySQL", "/var/lib/mysql", "mysqladmin ping -h 127.0.0.1"},
	"mariadb":       {"MariaDB", "/var/lib/mysql", "healthcheck.sh --connect --innodb_initialized"},
	"mongo":         {"MongoDB", "/data/db", "mongosh --quiet --eval 'db.runCommand({ ping: 1 })'"},
	"redis":         {"Redis", "", "redis-cli ping"},
	"valkey":        {"Valkey", "", "valkey-cli ping"},
	"memcached":     {"Memcached", "", ""},
	"rabbitmq":      {"RabbitMQ", "/var/lib/rabbitmq", "rabbitmq-diagnostics -q ping"},
	"elasticsearch": {"Elasticsearch", "/usr/share/elasticsearch/data", ""},
	"opensearch":    {"OpenSearch", "/usr/share/opensearch/data", ""},
	"cassandra":     {"Cassandra", "/var/lib/cassandra", ""},
	"influxdb":      {"InfluxDB", "/var/lib/influxdb2", ""},
}

// proxyImages are reverse proxies and gateways (last repository part).
var proxyImages = []string{"nginx", "openresty", "traefik", "caddy", "haproxy", "envoy", "kong", "modsecurity-crs", "nginx-proxy", "nginx-proxy-manager", "httpd"}

// imageName is the last path part of a service's image repository ("" for
// built images).
func imageName(s *model.ServiceFacts) string {
	if s.Image == nil {
		return ""
	}
	repo := s.Image.Repo
	return repo[strings.LastIndex(repo, "/")+1:]
}

// official reports an image of Docker Hub's official library.
func official(s *model.ServiceFacts) bool {
	return s.Image != nil && (!strings.Contains(s.Image.Repo, "/") || strings.HasPrefix(s.Image.Repo, "library/"))
}

func dataOf(s *model.ServiceFacts) (dataImage, bool) {
	d, ok := dataImages[imageName(s)]
	return d, ok
}

func isProxy(s *model.ServiceFacts) bool { return slices.Contains(proxyImages, imageName(s)) }

// allInterfaces reports a published port bound to every interface.
func allInterfaces(ip string) bool { return ip == "" || ip == "0.0.0.0" || ip == "::" || ip == "[::]" }

// service is one active compose service.
type service struct {
	name string
	*model.ServiceFacts
}

func dockerRecs(wf *model.Workflow) ([]rec, bool) {
	c := wf.Compose
	if c == nil {
		return nil, false
	}
	var svcs []service
	taken := map[string]bool{}
	for _, n := range c.Graph.Nodes {
		taken[n.Kind+":"+n.Name] = true
		if n.Kind == model.ComposeService && n.Service != nil && !n.Inactive {
			svcs = append(svcs, service{n.Name, n.Service})
		}
	}
	slices.SortFunc(svcs, func(a, b service) int { return strings.Compare(a.name, b.name) })
	envs := []string{c.Project, c.Name, c.Environment}
	for _, e := range c.Environments {
		envs = append(envs, e.Name)
	}
	production := prodLike(wf, envs...)
	free := func(kind, name string) string {
		for i := 2; taken[kind+":"+name]; i++ {
			name = fmt.Sprintf("%s-%d", strings.TrimSuffix(name, fmt.Sprintf("-%d", i-1)), i)
		}
		taken[kind+":"+name] = true
		return name
	}
	var out []rec
	add := func(priority int, r Recommendation) {
		if r.Local == "" {
			r.Local = LocalRuns
		}
		out = append(out, rec{Recommendation: r, priority: priority})
	}
	dockerEdge(svcs, free, add)
	dockerSecrets(svcs, add)
	dockerPrivateData(c, svcs, add)
	dockerPersistence(svcs, free, add)
	dockerRuntime(svcs, add)
	dockerSmells(c, svcs, production, free, add)
	if production && len(c.Environments) < 2 && len(svcs) > 0 {
		dockerEnvironments(c, svcs, add)
	}
	return out, production
}

// publishedPort is one port a service publishes on the host.
type publishedPort struct {
	ip, host, container string
}

// httpEntries are the non-proxy, non-data services that publish TCP ports,
// with those ports.
func httpEntries(svcs []service) ([]service, map[string][]publishedPort) {
	var out []service
	ports := map[string][]publishedPort{}
	for _, s := range svcs {
		if _, data := dataOf(s.ServiceFacts); data || isProxy(s.ServiceFacts) {
			continue
		}
		for _, p := range s.Ports {
			if !p.Published || p.Protocol == "udp" || strings.Contains(p.Host, "-") || strings.Contains(p.Container, "-") {
				continue
			}
			ports[s.name] = append(ports[s.name], publishedPort{p.HostIP, p.Host, p.Container})
		}
		if len(ports[s.name]) > 0 {
			out = append(out, s)
		}
	}
	return out, ports
}

func yamlPort(ip, host, container string) string {
	if ip != "" && !allInterfaces(ip) {
		return fmt.Sprintf("%q", ip+":"+host+":"+container)
	}
	return fmt.Sprintf("%q", host+":"+container)
}

func dockerEdge(svcs []service, free func(kind, name string) string, add func(int, Recommendation)) {
	entries, ports := httpEntries(svcs)
	var proxies []service
	crs := false
	for _, s := range svcs {
		if isProxy(s.ServiceFacts) {
			proxies = append(proxies, s)
			crs = crs || imageName(s.ServiceFacts) == "modsecurity-crs"
		}
	}
	if len(entries) > 0 {
		add(prioEdge, dockerProxy(entries, ports, proxies, free))
	}
	if crs || len(entries) == 0 && len(proxies) == 0 {
		return
	}
	front, port := "", publishedPort{}
	if len(proxies) > 0 {
		front = proxies[0].name
		for _, p := range proxies[0].Ports {
			if p.Published && p.Protocol != "udp" {
				port = publishedPort{p.HostIP, p.Host, p.Container}
				break
			}
		}
	} else {
		front = entries[0].name
		port = ports[front][0]
	}
	if port.host == "" || strings.Contains(port.host, "-") {
		return
	}
	waf := free("service", "waf")
	snippet := fmt.Sprintf(`# Merge into the production overlay: %s publishes %s and forwards to %s (remove that port from %s).
services:
  %s:
    image: owasp/modsecurity-crs:nginx-alpine
    restart: unless-stopped
    ports:
      - %s
    environment:
      BACKEND: http://%s:%s
      PORT: "8080"
      PARANOIA: "1"
      BLOCKING_PARANOIA: "1"
    depends_on:
      - %s
`, waf, port.host, front, front, waf, yamlPort(port.ip, port.host, "8080"), front, port.container, front)
	add(prioEdge+1, Recommendation{
		ID:      "docker_waf_crs",
		Pattern: "gateway-offloading",
		Title:   "Filter attacks with the OWASP Core Rule Set in front of the entry point",
		Why:     "A rate limit stops floods, not SQL injection, XSS or path traversal. ModSecurity with the OWASP Core Rule Set (the official owasp/modsecurity-crs nginx image) blocks the common attack classes before they reach the app; start at paranoia level 1 and tune false positives.",
		Pillars: pillarsEdge,
		Nodes:   []string{model.ComposeServiceID(front)},
		Add:     []Addition{{Type: "services." + waf, Purpose: "owasp/modsecurity-crs nginx image forwarding to " + front}},
		Snippet: snippet,
		Scope:   ScopeProduction,
		Skip:    "Local development and internal tools: the rule set adds latency and false positives to tune.",
		Docs:    "https://coreruleset.org/docs/",
	})
}

// dockerProxy routes the services that publish their own ports through
// one rate-limiting proxy (a new nginx, or the project's own proxy).
func dockerProxy(entries []service, ports map[string][]publishedPort, proxies []service, free func(kind, name string) string) Recommendation {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = model.ComposeServiceID(e.name)
	}
	var b strings.Builder
	why := "Services that publish their own ports take every request directly: nothing limits a client that floods them, terminates TLS or adds security headers. " +
		"One reverse proxy in front (nginx here; Traefik or Caddy work too) rate-limits per client IP and is the single place to add TLS, while the apps only expose their ports on the compose network."
	adds := []Addition{}
	conf := ""
	if len(proxies) > 0 {
		why = fmt.Sprintf("%s already runs a reverse proxy, but these services still publish their own ports, so clients can bypass it and its rate limits. Route them through the proxy and stop publishing them.", proxies[0].name)
		fmt.Fprintf(&b, "# Route these services through %s and remove their ports: entries.\nservices:\n", proxies[0].name)
	} else {
		proxy := free("service", "proxy")
		conf = free("config", "proxy_conf")
		adds = append(adds,
			Addition{Type: "services." + proxy, Purpose: "nginx reverse proxy publishing the same host ports, with per-client-IP rate limiting (limit_req)"},
			Addition{Type: "configs." + conf, Purpose: "the proxy's nginx configuration, inline"})
		fmt.Fprintf(&b, "# Merge into compose.yaml: %s publishes the ports instead of the apps (remove their ports: entries).\nservices:\n  %s:\n    image: nginx:1.27-alpine\n    restart: unless-stopped\n    ports:\n", proxy, proxy)
		for _, e := range entries {
			for _, p := range ports[e.name] {
				fmt.Fprintf(&b, "      - %s\n", yamlPort(p.ip, p.host, p.host))
			}
		}
		fmt.Fprintf(&b, "    configs:\n      - source: %s\n        target: /etc/nginx/conf.d/default.conf\n    depends_on:\n", conf)
		for _, e := range entries {
			fmt.Fprintf(&b, "      - %s\n", e.name)
		}
	}
	adds = append(adds, Addition{Type: "expose", Purpose: "the apps keep their ports on the compose network only (remove their ports: entries)"})
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s:\n    expose:\n", e.name)
		for _, p := range ports[e.name] {
			fmt.Fprintf(&b, "      - %q\n", p.container)
		}
	}
	if conf != "" {
		fmt.Fprintf(&b, "configs:\n  %s:\n    # $$ escapes $ from compose interpolation.\n    content: |\n      limit_req_zone $$binary_remote_addr zone=per_ip:10m rate=10r/s;\n", conf)
		for _, e := range entries {
			for _, p := range ports[e.name] {
				fmt.Fprintf(&b, "      server {\n        listen %s;\n        location / {\n          limit_req zone=per_ip burst=20 nodelay;\n          proxy_set_header Host $$host;\n          proxy_set_header X-Forwarded-For $$proxy_add_x_forwarded_for;\n          proxy_pass http://%s:%s;\n        }\n      }\n", p.host, e.name, p.container)
			}
		}
	}
	// One service behind the proxy offloads rate limiting and TLS; several
	// get a single entry point that routes to them.
	pattern := "gateway-offloading"
	if len(entries) > 1 {
		pattern = "api-gateway"
	}
	return Recommendation{
		ID:      "docker_reverse_proxy",
		Pattern: pattern,
		Title:   "Put a rate-limiting reverse proxy in front of the published services",
		Why:     why,
		Pillars: pillarsEdge,
		Nodes:   names,
		Add:     adds,
		Snippet: b.String(),
		Scope:   ScopeRecommended,
		Skip:    "A single service you only open from this machine (publish it on 127.0.0.1 instead).",
		Docs:    "https://nginx.org/en/docs/http/ngx_http_limit_req_module.html",
	}
}

func dockerSecrets(svcs []service, add func(int, Recommendation)) {
	var nodes []string
	var b strings.Builder
	var files []string
	for _, s := range svcs {
		if len(s.Secrets) > 0 {
			continue
		}
		var keys []string
		for _, e := range s.Environment {
			if e.Secret && !strings.HasSuffix(e.Key, "_FILE") {
				keys = append(keys, e.Key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		nodes = append(nodes, model.ComposeServiceID(s.name))
		fmt.Fprintf(&b, "  %s:\n    # Replace %s in environment: with:\n    environment:\n", s.name, strings.Join(keys, ", "))
		for _, k := range keys {
			fmt.Fprintf(&b, "      %s_FILE: /run/secrets/%s\n", k, secretName(s.name, k))
		}
		b.WriteString("    secrets:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "      - %s\n", secretName(s.name, k))
			files = append(files, secretName(s.name, k))
		}
	}
	if len(nodes) == 0 {
		return
	}
	var top strings.Builder
	top.WriteString("secrets:\n")
	for _, f := range files {
		fmt.Fprintf(&top, "  %s:\n    file: ./secrets/%s.txt\n", f, f)
	}
	add(prioSecrets, Recommendation{
		ID:      "docker_secrets",
		Pattern: "external-configuration-store",
		Title:   "Pass credentials as compose secrets, not environment variables",
		Why: "Environment variables are visible to anyone who can run docker inspect, are copied into child processes and crash reports, and often end up committed. " +
			"Compose secrets are mounted as files under /run/secrets; official images (postgres, mysql, mariadb) read <VAR>_FILE, other apps read the file. Keep ./secrets/ out of git.",
		Pillars: pillarsSecret,
		Nodes:   nodes,
		Add: []Addition{
			{Type: "secrets", Purpose: "file-based secrets under ./secrets/ (gitignored)"},
			{Type: "services.<name>.secrets", Purpose: "mounts each secret into the services that need it"},
		},
		Snippet: "# Merge into compose.yaml.\nservices:\n" + b.String() + top.String(),
		Scope:   ScopeRecommended,
		Skip:    "Throwaway local passwords for a database nobody else can reach.",
		Rules:   []string{"dk_secret_env"},
		Docs:    "https://docs.docker.com/compose/how-tos/use-secrets/",
	})
}

func secretName(service, key string) string {
	return strings.ToLower(strings.ReplaceAll(service, "-", "_") + "_" + key)
}

// consumers are the services that use s (depends_on, links or an inferred
// host in their environment).
func consumers(c *model.ComposeModel, s string) []string {
	var out []string
	id := model.ComposeServiceID(s)
	for _, e := range c.Graph.Edges {
		if e.Source == id && strings.HasPrefix(e.Target, "service:") && !slices.Contains(out, strings.TrimPrefix(e.Target, "service:")) {
			switch e.Kind {
			case model.ComposeEdgeDependsOn, model.ComposeEdgeLinks, model.ComposeEdgeInferred:
				out = append(out, strings.TrimPrefix(e.Target, "service:"))
			}
		}
	}
	slices.Sort(out)
	return out
}

func dockerPrivateData(c *model.ComposeModel, svcs []service, add func(int, Recommendation)) {
	var nodes []string
	var b strings.Builder
	var apps []string
	for _, s := range svcs {
		d, ok := dataOf(s.ServiceFacts)
		if !ok {
			continue
		}
		exposed := false
		for _, p := range s.Ports {
			exposed = exposed || p.Published && allInterfaces(p.HostIP)
		}
		if !exposed {
			continue
		}
		nodes = append(nodes, model.ComposeServiceID(s.name))
		fmt.Fprintf(&b, "  %s:\n    # Remove ports: (other services reach %s as %s:<port>; for a local client publish on 127.0.0.1 only).\n    networks:\n      - backend\n", s.name, d.label, s.name)
		for _, a := range consumers(c, s.name) {
			if !slices.Contains(apps, a) {
				apps = append(apps, a)
			}
		}
	}
	if len(nodes) == 0 {
		return
	}
	for _, a := range apps {
		fmt.Fprintf(&b, "  %s:\n    networks:\n      - default\n      - backend\n", a)
	}
	add(prioPrivate, Recommendation{
		ID:    "docker_private_data",
		Title: "Keep databases and caches off the published ports, on an internal network",
		Why: "A data service published on every interface is reachable from the LAN (and, on Linux, Docker's iptables rules bypass the host firewall) with its development password. " +
			"An internal network has no route out or in: only the services attached to it reach the database.",
		Pillars: pillarsSecret,
		Nodes:   nodes,
		Add:     []Addition{{Type: "networks.backend", Purpose: "internal: true network shared by the data services and their consumers only"}},
		Snippet: "# Merge into compose.yaml.\nservices:\n" + b.String() + "networks:\n  backend:\n    internal: true\n",
		Scope:   ScopeRecommended,
		Rules:   []string{"dk_db_published", "dk_network_internal"},
		Docs:    "https://docs.docker.com/reference/compose-file/networks/#internal",
	})
}

func dockerPersistence(svcs []service, free func(kind, name string) string, add func(int, Recommendation)) {
	var nodes []string
	var b, vols strings.Builder
	for _, s := range svcs {
		d, ok := dataOf(s.ServiceFacts)
		if !ok || d.dir == "" || !official(s.ServiceFacts) {
			continue
		}
		mounted := false
		for _, m := range s.Volumes {
			mounted = mounted || m.Target == d.dir || strings.HasPrefix(d.dir, strings.TrimSuffix(m.Target, "/")+"/")
		}
		if mounted {
			continue
		}
		vol := free("volume", s.name+"-data")
		nodes = append(nodes, model.ComposeServiceID(s.name))
		fmt.Fprintf(&b, "  %s:\n    volumes:\n      - %s:%s\n", s.name, vol, d.dir)
		fmt.Fprintf(&vols, "  %s: {}\n", vol)
	}
	if len(nodes) == 0 {
		return
	}
	add(prioData, Recommendation{
		ID:    "docker_data_volumes",
		Title: "Keep database data in named volumes",
		Why: "Without a named volume the data lives in an anonymous volume that docker compose down -v, a prune or a recreate silently drops. " +
			"A named volume survives recreation and is what you back up (docker run --rm -v <volume>:/data … tar, or the database's own dump on a schedule in production).",
		Pillars: pillarsData,
		Nodes:   nodes,
		Add:     []Addition{{Type: "volumes", Purpose: "one named volume per data service, mounted on its data directory"}},
		Snippet: "# Merge into compose.yaml.\nservices:\n" + b.String() + "volumes:\n" + vols.String(),
		Scope:   ScopeRecommended,
		Skip:    "Caches and test databases you reseed on every start.",
		Rules:   []string{"dk_anon_volume"},
		Docs:    "https://docs.docker.com/engine/storage/volumes/",
	})
}

func dockerRuntime(svcs []service, add func(int, Recommendation)) {
	var nodes []string
	var b strings.Builder
	shown := 0
	for _, s := range svcs {
		limited := s.Resources != nil && s.Resources.Memory != ""
		checked := s.Healthcheck != nil && !s.Healthcheck.Disabled || s.HealthcheckFrom != ""
		if limited && checked {
			continue
		}
		nodes = append(nodes, model.ComposeServiceID(s.name))
		if shown == 3 {
			continue
		}
		shown++
		fmt.Fprintf(&b, "  %s:\n", s.name)
		if !limited {
			b.WriteString("    deploy:\n      resources:\n        limits:\n          cpus: \"1.0\"\n          memory: 512M\n")
		}
		if !checked {
			test := ""
			if d, ok := dataOf(s.ServiceFacts); ok && d.health != "" {
				test = d.health
			} else if port := firstPort(s); port != "" {
				test = fmt.Sprintf("wget -qO- http://127.0.0.1:%s/ || exit 1", port)
			}
			if test != "" {
				fmt.Fprintf(&b, "    healthcheck:\n      # Use a command the image has (curl, wget or the app's own health endpoint).\n      test: [\"CMD-SHELL\", %q]\n      interval: 10s\n      timeout: 3s\n      retries: 3\n      start_period: 20s\n", test)
			}
		}
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > shown {
		fmt.Fprintf(&b, "  # Same for: %s\n", strings.Join(nodes[shown:], ", "))
	}
	add(prioRuntime, Recommendation{
		ID:      "docker_limits_healthchecks",
		Pattern: "health-endpoint-monitoring",
		Title:   "Give each service memory and CPU limits and a health check",
		Why: "Without limits one leaking or busy container starves the others and the host. Without a health check Docker only knows the process is alive, not that it serves: " +
			"depends_on: condition: service_healthy and restarts can't react to a hung app.",
		Pillars: []model.Pillar{model.PillarReliability, model.PillarPerformance},
		Nodes:   nodes,
		Add: []Addition{
			{Type: "deploy.resources.limits", Purpose: "memory and CPU ceilings sized from docker stats under load"},
			{Type: "healthcheck", Purpose: "probe that the service answers, used by depends_on and restarts"},
		},
		Snippet: "# Merge into compose.yaml.\nservices:\n" + b.String(),
		Scope:   ScopeRecommended,
		Rules:   []string{"dk_no_limits", "dk_no_healthcheck"},
		Docs:    "https://docs.docker.com/reference/compose-file/deploy/#resources",
	})
}

// firstPort is the first container port a service publishes or exposes.
func firstPort(s service) string {
	for _, p := range s.Ports {
		if p.Container != "" && !strings.Contains(p.Container, "-") {
			return p.Container
		}
	}
	for _, e := range s.Expose {
		if !strings.Contains(e, "-") {
			return strings.Split(e, "/")[0]
		}
	}
	return ""
}

func dockerEnvironments(c *model.ComposeModel, svcs []service, add func(int, Recommendation)) {
	var b strings.Builder
	for i, s := range svcs {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "  %s:\n    restart: always\n    logging:\n      options:\n        max-size: 10m\n        max-file: \"3\"\n", s.name)
	}
	have := "no environments"
	if len(c.Environments) == 1 {
		have = fmt.Sprintf("only the %q environment", c.Environments[0].Name)
	}
	add(prioEnvironment, Recommendation{
		ID:    "docker_environments",
		Title: "Split dev and prod into compose overlays",
		Why: fmt.Sprintf("The project looks production-bound but has %s. Keep compose.yaml as the shared base and put per-environment differences in compose.<env>.yaml (with .env.<env> for its variables), "+
			"so production gets restart policies, bounded logs and no development bind mounts or debug ports, and Nimbo runs each environment from the same base.", have),
		Pillars: []model.Pillar{model.PillarOperations, model.PillarReliability},
		Add: []Addition{
			{Type: "compose.prod.yaml", Purpose: "production overlay: restart policies, bounded logs, no code bind mounts"},
			{Type: "compose.dev.yaml", Purpose: "development overlay: bind mounts, debug ports"},
		},
		Snippet: "# compose.prod.yaml (docker compose -f compose.yaml -f compose.prod.yaml up -d)\nservices:\n" + b.String(),
		Scope:   ScopeProduction,
		Skip:    "Projects that only ever run on a developer machine.",
		Docs:    "https://docs.docker.com/compose/how-tos/production/",
	})
}
