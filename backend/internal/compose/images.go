package compose

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The curated image palette (GET /catalog/docker/images): popular images
// with a pinned tag, their icon, ports, environment, data paths, a
// healthcheck that works with the tools inside the image, and the
// variables a client service sets to reach them. Images without a shell
// (distroless, scratch) carry no healthcheck: compose runs tests with
// CMD-SHELL.

// ImageCategories are the palette categories, in palette order.
var ImageCategories = []string{
	"web", "gateway", "language", "database", "cache", "search", "messaging", "storage",
	"observability", "auth", "workflow", "ai", "devtools", "apps", "cms", "network", "tools",
}

func hc(test, interval string, retries int) *model.ServiceHealthcheck {
	return hcs(test, interval, retries, "10s")
}

// hcs is hc for images that take a while to boot (JVMs, migrations).
func hcs(test, interval string, retries int, startPeriod string) *model.ServiceHealthcheck {
	return &model.ServiceHealthcheck{Test: test, Interval: interval, Timeout: "5s", Retries: &retries, StartPeriod: startPeriod}
}

// secretKeys are credentials whose names don't say so (IsSecretKey misses them).
var secretKeys = map[string]bool{
	"NEO4J_AUTH":         true,
	"MEILI_MASTER_KEY":   true,
	"LITELLM_MASTER_KEY": true,
	"ZITADEL_MASTERKEY":  true,
}

// credURLRe matches a URL carrying a password (postgres://user:pass@host).
var credURLRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@`)

// isSecretEnv reports whether a catalog variable holds a credential: its
// key says so, it is a known credential, or its value is a URL with a password.
func isSecretEnv(k, v string) bool {
	return IsSecretKey(k) || secretKeys[k] || credURLRe.MatchString(v)
}

func env(kv ...string) []model.DockerImageEnv {
	var out []model.DockerImageEnv
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, model.DockerImageEnv{Key: kv[i], Value: kv[i+1], Secret: isSecretEnv(kv[i], kv[i+1])})
	}
	return out
}

func vol(target, name string) []model.DockerImageVolume {
	return []model.DockerImageVolume{{Target: target, Name: name}}
}

// vols lists several data paths: target, name, target, name…
func vols(kv ...string) []model.DockerImageVolume {
	var out []model.DockerImageVolume
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, model.DockerImageVolume{Target: kv[i], Name: kv[i+1]})
	}
	return out
}

func conn(kv ...string) []model.DockerImageConnect {
	var out []model.DockerImageConnect
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, model.DockerImageConnect{Key: kv[i], Template: kv[i+1]})
	}
	return out
}

// Shared recipes.
var (
	pgEnv = func() []model.DockerImageEnv {
		return env("POSTGRES_USER", "app", "POSTGRES_PASSWORD", "app", "POSTGRES_DB", "app")
	}
	pgHealth = func() *model.ServiceHealthcheck {
		return hc("pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}", "5s", 10)
	}
	pgConnect = func() []model.DockerImageConnect {
		return conn("DATABASE_URL", "postgres://{{env.POSTGRES_USER}}:{{env.POSTGRES_PASSWORD}}@{{host}}:{{port}}/{{env.POSTGRES_DB}}")
	}
	redisConnect = func() []model.DockerImageConnect { return conn("REDIS_URL", "redis://{{host}}:{{port}}") }
)

var images = []model.DockerImage{
	// Web servers and proxies
	{ID: "nginx", Image: "nginx", Tag: "1.27-alpine", Label: "NGINX", Description: "Web server and reverse proxy", Category: "web", Icon: "nginx", Ports: []int{80},
		Volumes: vol("/usr/share/nginx/html", ""), Healthcheck: hc("wget -qO- http://127.0.0.1/ >/dev/null || exit 1", "10s", 3)},
	{ID: "nginx-unprivileged", Image: "nginxinc/nginx-unprivileged", Tag: "1.30-alpine", Label: "NGINX (unprivileged)", Description: "NGINX running as a non-root user on port 8080", Category: "web", Icon: "nginx", Ports: []int{8080},
		Volumes: vol("/usr/share/nginx/html", ""), Healthcheck: hc("wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1", "10s", 3)},
	{ID: "httpd", Image: "httpd", Tag: "2.4-alpine", Label: "Apache HTTP Server", Description: "The Apache web server", Category: "web", Icon: "apache", Ports: []int{80},
		Volumes: vol("/usr/local/apache2/htdocs", ""), Healthcheck: hc("wget -qO- http://127.0.0.1/ >/dev/null || exit 1", "10s", 3)},
	{ID: "caddy", Image: "caddy", Tag: "2-alpine", Label: "Caddy", Description: "Web server with automatic HTTPS", Category: "web", Icon: "caddy", Ports: []int{80, 443},
		Volumes: vols("/data", "data", "/config", "config")},
	{ID: "traefik", Image: "traefik", Tag: "v3.1", Label: "Traefik", Description: "Cloud-native reverse proxy reading Docker labels", Category: "web", Icon: "traefik", Ports: []int{80, 8080},
		Command: "--api.insecure=true --providers.docker=true --entrypoints.web.address=:80"},
	{ID: "haproxy", Image: "haproxy", Tag: "3.0-alpine", Label: "HAProxy", Description: "TCP/HTTP load balancer", Category: "web", Icon: "haproxy", Ports: []int{80}},
	{ID: "envoy", Image: "envoyproxy/envoy", Tag: "v1.39.4", Label: "Envoy", Description: "L7 proxy and service mesh data plane", Category: "web", Icon: "envoy", Ports: []int{10000, 9901}},
	{ID: "varnish", Image: "varnish", Tag: "7.7", Label: "Varnish Cache", Description: "HTTP accelerator in front of a backend (set VARNISH_BACKEND_HOST/PORT)", Category: "web", Icon: "varnish", Ports: []int{80},
		Env: env("VARNISH_BACKEND_HOST", "localhost", "VARNISH_BACKEND_PORT", "8080", "VARNISH_SIZE", "100M"), Healthcheck: hc("varnishadm ping >/dev/null || exit 1", "10s", 5)},
	{ID: "squid", Image: "ubuntu/squid", Tag: "6.6-24.04_edge", Label: "Squid", Description: "Forward HTTP proxy and cache", Category: "web", Icon: "squid", Ports: []int{3128},
		Volumes: vol("/var/spool/squid", "squidcache"), Healthcheck: hc("squid -k check || exit 1", "10s", 5),
		Connect: conn("HTTP_PROXY", "http://{{host}}:{{port}}", "HTTPS_PROXY", "http://{{host}}:{{port}}")},
	// API gateways
	{ID: "kong", Image: "kong", Tag: "3.9.4", Label: "Kong Gateway", Description: "API gateway, DB-less (proxy 8000, admin API 8001)", Category: "gateway", Icon: "kong", Ports: []int{8000, 8001},
		Env:         env("KONG_DATABASE", "off", "KONG_PROXY_LISTEN", "0.0.0.0:8000", "KONG_ADMIN_LISTEN", "0.0.0.0:8001", "KONG_PROXY_ACCESS_LOG", "/dev/stdout", "KONG_ADMIN_ACCESS_LOG", "/dev/stdout"),
		Healthcheck: hc("kong health", "10s", 10), Connect: conn("KONG_ADMIN_URL", "http://{{host}}:8001")},
	{ID: "apisix", Image: "apache/apisix", Tag: "3.19.0-debian", Label: "Apache APISIX", Description: "API gateway in standalone mode (routes in /usr/local/apisix/conf/apisix.yaml)", Category: "gateway", Icon: "apisix", Ports: []int{9080, 9443},
		Env: env("APISIX_STAND_ALONE", "true"), Command: `bash -c 'printf "deployment:\n  role: data_plane\n  role_data_plane:\n    config_provider: yaml\n" > /usr/local/apisix/conf/config.yaml && exec /docker-entrypoint.sh docker-start'`,
		Healthcheck: hc("bash -c ':> /dev/tcp/127.0.0.1/9080' || exit 1", "10s", 10)},
	{ID: "krakend", Image: "devopsfaith/krakend", Tag: "2.9.4", Label: "KrakenD", Description: "Stateless API gateway (config in /etc/krakend/krakend.json)", Category: "gateway", Icon: "krakend", Ports: []int{8080},
		Healthcheck: hc("wget -qO- http://127.0.0.1:8080/__health >/dev/null || exit 1", "10s", 5)},
	{ID: "oauth2-proxy", Image: "quay.io/oauth2-proxy/oauth2-proxy", Tag: "v7.14.2-alpine", Label: "OAuth2 Proxy", Description: "Puts an OAuth/OIDC login in front of an upstream", Category: "gateway", Icon: "oauth", Ports: []int{4180},
		Env: env("OAUTH2_PROXY_PROVIDER", "github", "OAUTH2_PROXY_CLIENT_ID", "change-me", "OAUTH2_PROXY_CLIENT_SECRET", "change-me", "OAUTH2_PROXY_COOKIE_SECRET", "0123456789abcdef0123456789abcdef",
			"OAUTH2_PROXY_EMAIL_DOMAINS", "*", "OAUTH2_PROXY_UPSTREAMS", "http://app:8080", "OAUTH2_PROXY_HTTP_ADDRESS", "0.0.0.0:4180"),
		Healthcheck: hc("wget -qO- http://127.0.0.1:4180/ping >/dev/null || exit 1", "10s", 5)},
	// Languages, runtimes and app servers
	{ID: "node", Image: "node", Tag: "22-alpine", Label: "Node.js", Description: "JavaScript runtime", Category: "language", Icon: "node", Ports: []int{3000}, Command: "node server.js"},
	{ID: "node-slim", Image: "node", Tag: "22-slim", Label: "Node.js (Debian slim)", Description: "JavaScript runtime on glibc, for native modules", Category: "language", Icon: "node", Ports: []int{3000}, Command: "node server.js"},
	{ID: "node-24", Image: "node", Tag: "24-alpine", Label: "Node.js 24", Description: "Current Node.js LTS line", Category: "language", Icon: "node", Ports: []int{3000}, Command: "node server.js"},
	{ID: "bun", Image: "oven/bun", Tag: "1-alpine", Label: "Bun", Description: "Fast JavaScript runtime", Category: "language", Icon: "bun", Ports: []int{3000}},
	{ID: "deno", Image: "denoland/deno", Tag: "alpine-2.0.0", Label: "Deno", Description: "Secure TypeScript runtime", Category: "language", Icon: "deno", Ports: []int{8000}},
	{ID: "python", Image: "python", Tag: "3.12-slim", Label: "Python", Description: "Python runtime", Category: "language", Icon: "python", Ports: []int{8000}},
	{ID: "python-3.14", Image: "python", Tag: "3.14-slim", Label: "Python 3.14", Description: "Latest Python on Debian slim", Category: "language", Icon: "python", Ports: []int{8000}},
	{ID: "python-alpine", Image: "python", Tag: "3.13-alpine", Label: "Python (Alpine)", Description: "Small Python image on musl", Category: "language", Icon: "python", Ports: []int{8000}},
	{ID: "golang", Image: "golang", Tag: "1.23-alpine", Label: "Go", Description: "Go toolchain (build stage)", Category: "language", Icon: "go"},
	{ID: "rust", Image: "rust", Tag: "1.81-slim", Label: "Rust", Description: "Rust toolchain (build stage)", Category: "language", Icon: "rust"},
	{ID: "openjdk", Image: "eclipse-temurin", Tag: "21-jre-alpine", Label: "Java (Temurin JRE)", Description: "Java runtime", Category: "language", Icon: "java", Ports: []int{8080}},
	{ID: "temurin-jdk", Image: "eclipse-temurin", Tag: "21-jdk", Label: "Java (Temurin JDK)", Description: "Java development kit (build stage)", Category: "language", Icon: "java"},
	{ID: "corretto", Image: "amazoncorretto", Tag: "21-alpine", Label: "Amazon Corretto", Description: "Amazon's OpenJDK distribution", Category: "language", Icon: "java", Ports: []int{8080}},
	{ID: "tomcat", Image: "tomcat", Tag: "11.0-jdk21-temurin-noble", Label: "Apache Tomcat", Description: "Servlet container (deploy WARs to webapps/)", Category: "language", Icon: "tomcat", Ports: []int{8080},
		Volumes: vol("/usr/local/tomcat/webapps", ""), Healthcheck: hcs("curl -s -o /dev/null http://127.0.0.1:8080/ || exit 1", "10s", 10, "20s")},
	{ID: "jetty", Image: "jetty", Tag: "12.1-jdk21-eclipse-temurin", Label: "Eclipse Jetty", Description: "Lightweight servlet container", Category: "language", Icon: "jetty", Ports: []int{8080},
		Volumes: vol("/var/lib/jetty/webapps", ""), Healthcheck: hcs("curl -s -o /dev/null http://127.0.0.1:8080/ || exit 1", "10s", 10, "20s")},
	{ID: "wildfly", Image: "quay.io/wildfly/wildfly", Tag: "39.0.0.Final-jdk21", Label: "WildFly", Description: "Jakarta EE application server (management on 9990)", Category: "language", Icon: "wildfly", Ports: []int{8080, 9990},
		Command: "/opt/jboss/wildfly/bin/standalone.sh -b 0.0.0.0 -bmanagement 0.0.0.0", Healthcheck: hcs("curl -s -o /dev/null http://127.0.0.1:8080/ || exit 1", "10s", 10, "30s")},
	{ID: "dotnet-aspnet", Image: "mcr.microsoft.com/dotnet/aspnet", Tag: "8.0", Label: ".NET ASP.NET Core", Description: ".NET runtime for web apps", Category: "language", Icon: "dotnet", Ports: []int{8080}},
	{ID: "dotnet-sdk", Image: "mcr.microsoft.com/dotnet/sdk", Tag: "10.0", Label: ".NET SDK", Description: ".NET SDK (build stage)", Category: "language", Icon: "dotnet"},
	{ID: "php", Image: "php", Tag: "8.3-apache", Label: "PHP + Apache", Description: "PHP with the Apache web server", Category: "language", Icon: "php", Ports: []int{80}, Volumes: vol("/var/www/html", "")},
	{ID: "php-fpm", Image: "php", Tag: "8.4-fpm-alpine", Label: "PHP-FPM", Description: "PHP FastCGI process manager (pair with NGINX)", Category: "language", Icon: "php", Ports: []int{9000}, Volumes: vol("/var/www/html", "")},
	{ID: "ruby", Image: "ruby", Tag: "3.3-slim", Label: "Ruby", Description: "Ruby runtime", Category: "language", Icon: "ruby", Ports: []int{3000}},
	{ID: "elixir", Image: "elixir", Tag: "1.17-alpine", Label: "Elixir", Description: "Elixir runtime", Category: "language", Icon: "elixir", Ports: []int{4000}},
	{ID: "distroless-static", Image: "gcr.io/distroless/static-debian12", Tag: "nonroot", Label: "Distroless static", Description: "Runtime stage for static binaries (Go, Rust): no shell, non-root", Category: "language", Icon: "distroless"},
	{ID: "distroless-base", Image: "gcr.io/distroless/base-debian12", Tag: "nonroot", Label: "Distroless base", Description: "Runtime stage with glibc and OpenSSL: no shell, non-root", Category: "language", Icon: "distroless"},
	{ID: "distroless-java", Image: "gcr.io/distroless/java21-debian12", Tag: "nonroot", Label: "Distroless Java 21", Description: "Runtime stage for Java apps: no shell, non-root", Category: "language", Icon: "distroless"},
	{ID: "distroless-node", Image: "gcr.io/distroless/nodejs22-debian12", Tag: "nonroot", Label: "Distroless Node.js 22", Description: "Runtime stage for Node.js apps: no shell, non-root", Category: "language", Icon: "distroless"},
	{ID: "distroless-python", Image: "gcr.io/distroless/python3-debian12", Tag: "nonroot", Label: "Distroless Python 3", Description: "Runtime stage for Python apps: no shell, non-root", Category: "language", Icon: "distroless"},
	// Databases
	{ID: "postgres", Image: "postgres", Tag: "16-alpine", Label: "PostgreSQL", Description: "Relational database", Category: "database", Icon: "postgres", Ports: []int{5432},
		Env: pgEnv(), Volumes: vol("/var/lib/postgresql/data", "pgdata"), Healthcheck: pgHealth(), Connect: pgConnect()},
	{ID: "postgis", Image: "postgis/postgis", Tag: "17-3.6-alpine", Label: "PostGIS", Description: "PostgreSQL with spatial types and functions (amd64 only)", Category: "database", Icon: "postgres", Ports: []int{5432},
		Env: pgEnv(), Volumes: vol("/var/lib/postgresql/data", "pgdata"), Healthcheck: pgHealth(), Connect: pgConnect()},
	{ID: "timescaledb", Image: "timescale/timescaledb", Tag: "2.31.0-pg17", Label: "TimescaleDB", Description: "PostgreSQL for time series", Category: "database", Icon: "timescale", Ports: []int{5432},
		Env: pgEnv(), Volumes: vol("/var/lib/postgresql/data", "tsdata"), Healthcheck: pgHealth(), Connect: pgConnect()},
	{ID: "cockroachdb", Image: "cockroachdb/cockroach", Tag: "v25.4.18", Label: "CockroachDB", Description: "Distributed SQL, PostgreSQL wire protocol (single node, insecure; UI on 8080)", Category: "database", Icon: "cockroachdb", Ports: []int{26257, 8080},
		Command: "start-single-node --insecure", Volumes: vol("/cockroach/cockroach-data", "crdbdata"),
		Healthcheck: hcs("curl -fs 'http://127.0.0.1:8080/health?ready=1' >/dev/null || exit 1", "10s", 10, "20s"),
		Connect:     conn("DATABASE_URL", "postgresql://root@{{host}}:{{port}}/defaultdb?sslmode=disable")},
	{ID: "mysql", Image: "mysql", Tag: "8.4", Label: "MySQL", Description: "Relational database", Category: "database", Icon: "mysql", Ports: []int{3306},
		Env: env("MYSQL_ROOT_PASSWORD", "root", "MYSQL_DATABASE", "app", "MYSQL_USER", "app", "MYSQL_PASSWORD", "app"), Volumes: vol("/var/lib/mysql", "mysqldata"),
		Healthcheck: hc("mysqladmin ping -h 127.0.0.1 -u root -p$${MYSQL_ROOT_PASSWORD} --silent", "5s", 20),
		Connect:     conn("DATABASE_URL", "mysql://{{env.MYSQL_USER}}:{{env.MYSQL_PASSWORD}}@{{host}}:{{port}}/{{env.MYSQL_DATABASE}}")},
	{ID: "mariadb", Image: "mariadb", Tag: "11", Label: "MariaDB", Description: "MySQL-compatible database", Category: "database", Icon: "mariadb", Ports: []int{3306},
		Env: env("MARIADB_ROOT_PASSWORD", "root", "MARIADB_DATABASE", "app", "MARIADB_USER", "app", "MARIADB_PASSWORD", "app"), Volumes: vol("/var/lib/mysql", "mariadbdata"),
		Healthcheck: hc("healthcheck.sh --connect --innodb_initialized", "5s", 20),
		Connect:     conn("DATABASE_URL", "mysql://{{env.MARIADB_USER}}:{{env.MARIADB_PASSWORD}}@{{host}}:{{port}}/{{env.MARIADB_DATABASE}}")},
	{ID: "mongo", Image: "mongo", Tag: "7", Label: "MongoDB", Description: "Document database", Category: "database", Icon: "mongodb", Ports: []int{27017},
		Env: env("MONGO_INITDB_ROOT_USERNAME", "root", "MONGO_INITDB_ROOT_PASSWORD", "root"), Volumes: vol("/data/db", "mongodata"),
		Healthcheck: hc("mongosh --quiet --eval 'db.runCommand({ping:1}).ok' | grep 1", "10s", 10),
		Connect:     conn("MONGO_URL", "mongodb://{{env.MONGO_INITDB_ROOT_USERNAME}}:{{env.MONGO_INITDB_ROOT_PASSWORD}}@{{host}}:{{port}}")},
	{ID: "sqlserver", Image: "mcr.microsoft.com/mssql/server", Tag: "2022-CU27-ubuntu-22.04", Label: "SQL Server", Description: "Microsoft SQL Server (amd64 only)", Category: "database", Icon: "sqlserver", Ports: []int{1433},
		Env: env("ACCEPT_EULA", "Y", "MSSQL_SA_PASSWORD", "Nimbo_Passw0rd!"), Volumes: vol("/var/opt/mssql", "mssqldata"),
		Connect: conn("ConnectionStrings__Default", "Server={{host}},{{port}};User Id=sa;Password={{env.MSSQL_SA_PASSWORD}};TrustServerCertificate=true")},
	{ID: "cassandra", Image: "cassandra", Tag: "5.0", Label: "Cassandra", Description: "Wide-column database", Category: "database", Icon: "cassandra", Ports: []int{9042},
		Volumes: vol("/var/lib/cassandra", "cassandradata"), Healthcheck: hc("cqlsh -e 'describe keyspaces' >/dev/null", "20s", 15)},
	{ID: "scylladb", Image: "scylladb/scylla", Tag: "2026.1", Label: "ScyllaDB", Description: "Cassandra-compatible wide-column database (developer mode, 1 core)", Category: "database", Icon: "scylladb", Ports: []int{9042},
		Command: "--smp 1 --memory 750M --overprovisioned 1 --developer-mode 1", Volumes: vol("/var/lib/scylla", "scylladata"),
		Healthcheck: hcs("cqlsh -e 'describe keyspaces' >/dev/null", "10s", 20, "30s"), Connect: conn("CASSANDRA_CONTACT_POINTS", "{{host}}:{{port}}")},
	{ID: "clickhouse", Image: "clickhouse/clickhouse-server", Tag: "24.8-alpine", Label: "ClickHouse", Description: "Columnar analytics database", Category: "database", Icon: "clickhouse", Ports: []int{8123, 9000},
		Env: env("CLICKHOUSE_USER", "app", "CLICKHOUSE_PASSWORD", "app"), Volumes: vol("/var/lib/clickhouse", "clickhousedata"),
		Healthcheck: hc("wget -qO- http://127.0.0.1:8123/ping || exit 1", "10s", 5)},
	{ID: "questdb", Image: "questdb/questdb", Tag: "9.3.5", Label: "QuestDB", Description: "Time-series SQL database (console 9000, PostgreSQL wire 8812, ILP 9009)", Category: "database", Icon: "questdb", Ports: []int{9000, 8812, 9009},
		Volumes: vol("/var/lib/questdb", "questdbdata"), Healthcheck: hc("curl -fs http://127.0.0.1:9003/status >/dev/null || exit 1", "10s", 10),
		Connect: conn("DATABASE_URL", "postgres://admin:quest@{{host}}:8812/qdb")},
	{ID: "influxdb", Image: "influxdb", Tag: "2.8-alpine", Label: "InfluxDB", Description: "Time-series database (initial org, bucket and token set up on first start)", Category: "database", Icon: "influxdb", Ports: []int{8086},
		Env: env("DOCKER_INFLUXDB_INIT_MODE", "setup", "DOCKER_INFLUXDB_INIT_USERNAME", "admin", "DOCKER_INFLUXDB_INIT_PASSWORD", "nimbo-password", "DOCKER_INFLUXDB_INIT_ORG", "app",
			"DOCKER_INFLUXDB_INIT_BUCKET", "app", "DOCKER_INFLUXDB_INIT_ADMIN_TOKEN", "nimbo-admin-token"),
		Volumes: vols("/var/lib/influxdb2", "data", "/etc/influxdb2", "config"), Healthcheck: hc("influx ping", "10s", 10),
		Connect: conn("INFLUX_URL", "http://{{host}}:{{port}}", "INFLUX_TOKEN", "{{env.DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}}", "INFLUX_ORG", "{{env.DOCKER_INFLUXDB_INIT_ORG}}")},
	{ID: "neo4j", Image: "neo4j", Tag: "5", Label: "Neo4j", Description: "Graph database", Category: "database", Icon: "neo4j", Ports: []int{7474, 7687},
		Env: env("NEO4J_AUTH", "neo4j/nimbo-password"), Volumes: vol("/data", "neo4jdata")},
	{ID: "arangodb", Image: "arangodb", Tag: "3.12.12", Label: "ArangoDB", Description: "Multi-model database: documents, graphs, key-value", Category: "database", Icon: "arangodb", Ports: []int{8529},
		Env: env("ARANGO_ROOT_PASSWORD", "nimbo-password"), Volumes: vols("/var/lib/arangodb3", "data", "/var/lib/arangodb3-apps", "apps"),
		Healthcheck: hcs("wget -qO- http://127.0.0.1:8529/_admin/server/availability >/dev/null || exit 1", "10s", 10, "20s"),
		Connect:     conn("ARANGO_URL", "http://{{host}}:{{port}}", "ARANGO_PASSWORD", "{{env.ARANGO_ROOT_PASSWORD}}")},
	{ID: "surrealdb", Image: "surrealdb/surrealdb", Tag: "v3.3.4", Label: "SurrealDB", Description: "Multi-model database with SurrealQL (in-memory; pass a storage path to persist)", Category: "database", Icon: "surrealdb", Ports: []int{8000},
		Env: env("SURREAL_USER", "root", "SURREAL_PASS", "nimbo-password"), Command: "start",
		Connect: conn("SURREAL_URL", "ws://{{host}}:{{port}}/rpc")},
	{ID: "couchdb", Image: "couchdb", Tag: "3", Label: "CouchDB", Description: "Document database with HTTP API", Category: "database", Icon: "couchdb", Ports: []int{5984},
		Env: env("COUCHDB_USER", "admin", "COUCHDB_PASSWORD", "admin"), Volumes: vol("/opt/couchdb/data", "couchdata")},
	{ID: "couchbase", Image: "couchbase/server", Tag: "community-8.0.0", Label: "Couchbase Server", Description: "Document database with SQL++ (set up the cluster in the UI on 8091)", Category: "database", Icon: "couchbase", Ports: []int{8091, 11210},
		Volumes: vol("/opt/couchbase/var", "couchbasedata"), Healthcheck: hcs("curl -fs http://127.0.0.1:8091/ui/index.html >/dev/null || exit 1", "10s", 10, "30s"),
		Connect: conn("COUCHBASE_URL", "couchbase://{{host}}")},
	{ID: "etcd", Image: "gcr.io/etcd-development/etcd", Tag: "v3.6.15", Label: "etcd", Description: "Distributed key-value store (single member)", Category: "database", Icon: "etcd", Ports: []int{2379},
		Command: "etcd --name etcd --data-dir /etcd-data --listen-client-urls http://0.0.0.0:2379 --advertise-client-urls http://{{host}}:2379", Volumes: vol("/etcd-data", "etcddata"),
		Connect: conn("ETCD_ENDPOINTS", "http://{{host}}:{{port}}")},
	// Caches
	{ID: "redis", Image: "redis", Tag: "7-alpine", Label: "Redis", Description: "In-memory data store", Category: "cache", Icon: "redis", Ports: []int{6379},
		Volumes: vol("/data", "redisdata"), Healthcheck: hc("redis-cli ping | grep PONG", "5s", 10), Connect: redisConnect()},
	{ID: "valkey", Image: "valkey/valkey", Tag: "8-alpine", Label: "Valkey", Description: "Open-source Redis fork", Category: "cache", Icon: "valkey", Ports: []int{6379},
		Volumes: vol("/data", "valkeydata"), Healthcheck: hc("valkey-cli ping | grep PONG", "5s", 10), Connect: redisConnect()},
	{ID: "dragonfly", Image: "docker.dragonflydb.io/dragonflydb/dragonfly", Tag: "v1.40.2", Label: "Dragonfly", Description: "Redis- and Memcached-compatible in-memory store", Category: "cache", Icon: "dragonfly", Ports: []int{6379},
		Volumes: vol("/data", "dragonflydata"), Healthcheck: hc("healthcheck.sh", "10s", 5), Connect: redisConnect()},
	{ID: "memcached", Image: "memcached", Tag: "1.6-alpine", Label: "Memcached", Description: "Distributed memory cache", Category: "cache", Icon: "memcached", Ports: []int{11211},
		Connect: conn("MEMCACHED_SERVERS", "{{host}}:{{port}}")},
	// Search and vector databases
	{ID: "elasticsearch", Image: "docker.elastic.co/elasticsearch/elasticsearch", Tag: "8.15.0", Label: "Elasticsearch", Description: "Search and analytics engine", Category: "search", Icon: "elasticsearch", Ports: []int{9200},
		Env: env("discovery.type", "single-node", "xpack.security.enabled", "false", "ES_JAVA_OPTS", "-Xms512m -Xmx512m"), Volumes: vol("/usr/share/elasticsearch/data", "esdata"),
		Healthcheck: hc("curl -fs http://127.0.0.1:9200/_cluster/health || exit 1", "10s", 20), Connect: conn("ELASTICSEARCH_URL", "http://{{host}}:{{port}}")},
	{ID: "opensearch", Image: "opensearchproject/opensearch", Tag: "2", Label: "OpenSearch", Description: "Open-source search engine", Category: "search", Icon: "opensearch", Ports: []int{9200},
		Env: env("discovery.type", "single-node", "DISABLE_SECURITY_PLUGIN", "true"), Volumes: vol("/usr/share/opensearch/data", "osdata"), Connect: conn("OPENSEARCH_URL", "http://{{host}}:{{port}}")},
	{ID: "meilisearch", Image: "getmeili/meilisearch", Tag: "v1.55.0", Label: "Meilisearch", Description: "Typo-tolerant search engine", Category: "search", Icon: "meilisearch", Ports: []int{7700},
		Env: env("MEILI_MASTER_KEY", "nimbo-master-key-change-me", "MEILI_ENV", "development"), Volumes: vol("/meili_data", "meilidata"),
		Healthcheck: hc("curl -fs http://127.0.0.1:7700/health >/dev/null || exit 1", "10s", 5),
		Connect:     conn("MEILI_HOST", "http://{{host}}:{{port}}", "MEILI_MASTER_KEY", "{{env.MEILI_MASTER_KEY}}")},
	{ID: "typesense", Image: "typesense/typesense", Tag: "30.2", Label: "Typesense", Description: "Fast typo-tolerant search engine", Category: "search", Icon: "typesense", Ports: []int{8108},
		Env: env("TYPESENSE_API_KEY", "nimbo-api-key", "TYPESENSE_DATA_DIR", "/data", "TYPESENSE_ENABLE_CORS", "true"), Volumes: vol("/data", "typesensedata"),
		Healthcheck: hc("bash -c ':> /dev/tcp/127.0.0.1/8108' || exit 1", "10s", 10),
		Connect:     conn("TYPESENSE_URL", "http://{{host}}:{{port}}", "TYPESENSE_API_KEY", "{{env.TYPESENSE_API_KEY}}")},
	{ID: "solr", Image: "solr", Tag: "9.11", Label: "Apache Solr", Description: "Search platform (creates the core \"app\")", Category: "search", Icon: "solr", Ports: []int{8983},
		Command: "solr-precreate app", Volumes: vol("/var/solr", "solrdata"), Healthcheck: hcs("curl -fs http://127.0.0.1:8983/solr/app/admin/ping >/dev/null || exit 1", "10s", 10, "20s"),
		Connect: conn("SOLR_URL", "http://{{host}}:{{port}}/solr/app")},
	{ID: "qdrant", Image: "qdrant/qdrant", Tag: "v1.20.0", Label: "Qdrant", Description: "Vector database (REST 6333, gRPC 6334)", Category: "search", Icon: "qdrant", Ports: []int{6333, 6334},
		Volumes: vol("/qdrant/storage", "qdrantdata"), Healthcheck: hc("bash -c ':> /dev/tcp/127.0.0.1/6333' || exit 1", "10s", 5),
		Connect: conn("QDRANT_URL", "http://{{host}}:{{port}}")},
	{ID: "weaviate", Image: "cr.weaviate.io/semitechnologies/weaviate", Tag: "1.40.2", Label: "Weaviate", Description: "Vector database (anonymous access, no vectorizer module)", Category: "search", Icon: "weaviate", Ports: []int{8080, 50051},
		Env:     env("AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED", "true", "PERSISTENCE_DATA_PATH", "/var/lib/weaviate", "DEFAULT_VECTORIZER_MODULE", "none", "CLUSTER_HOSTNAME", "node1", "QUERY_DEFAULTS_LIMIT", "25"),
		Command: "--host 0.0.0.0 --port 8080 --scheme http", Volumes: vol("/var/lib/weaviate", "weaviatedata"),
		Healthcheck: hc("wget -qO- http://127.0.0.1:8080/v1/.well-known/ready >/dev/null || exit 1", "10s", 10), Connect: conn("WEAVIATE_URL", "http://{{host}}:{{port}}")},
	{ID: "chroma", Image: "chromadb/chroma", Tag: "1.5.9", Label: "Chroma", Description: "Embedding database for AI apps", Category: "search", Icon: "chroma", Ports: []int{8000},
		Volumes: vol("/data", "chromadata"), Healthcheck: hc("bash -c ':> /dev/tcp/127.0.0.1/8000' || exit 1", "10s", 5), Connect: conn("CHROMA_URL", "http://{{host}}:{{port}}")},
	{ID: "milvus", Image: "milvusdb/milvus", Tag: "v2.6.25", Label: "Milvus", Description: "Vector database (standalone, embedded etcd, local storage)", Category: "search", Icon: "milvus", Ports: []int{19530, 9091},
		Env: env("ETCD_USE_EMBED", "true", "ETCD_DATA_DIR", "/var/lib/milvus/etcd", "COMMON_STORAGETYPE", "local", "DEPLOY_MODE", "STANDALONE"), Command: "milvus run standalone",
		Volumes: vol("/var/lib/milvus", "milvusdata"), Healthcheck: hcs("curl -fs http://127.0.0.1:9091/healthz >/dev/null || exit 1", "10s", 10, "60s"),
		Connect: conn("MILVUS_URI", "http://{{host}}:{{port}}")},
	{ID: "pgvector", Image: "pgvector/pgvector", Tag: "0.8.7-pg17-trixie", Label: "pgvector", Description: "PostgreSQL with vector similarity search", Category: "search", Icon: "postgres", Ports: []int{5432},
		Env: pgEnv(), Volumes: vol("/var/lib/postgresql/data", "pgdata"), Healthcheck: pgHealth(), Connect: pgConnect()},
	// Messaging and streaming
	{ID: "rabbitmq", Image: "rabbitmq", Tag: "3.13-management-alpine", Label: "RabbitMQ", Description: "Message broker (with management UI on 15672)", Category: "messaging", Icon: "rabbitmq", Ports: []int{5672, 15672},
		Env: env("RABBITMQ_DEFAULT_USER", "app", "RABBITMQ_DEFAULT_PASS", "app"), Volumes: vol("/var/lib/rabbitmq", "rabbitmqdata"),
		Healthcheck: hc("rabbitmq-diagnostics -q ping", "10s", 10),
		Connect:     conn("AMQP_URL", "amqp://{{env.RABBITMQ_DEFAULT_USER}}:{{env.RABBITMQ_DEFAULT_PASS}}@{{host}}:{{port}}/")},
	{ID: "kafka", Image: "apache/kafka", Tag: "3.8.0", Label: "Kafka", Description: "Event streaming (KRaft, single node)", Category: "messaging", Icon: "kafka", Ports: []int{9092},
		Volumes: vol("/var/lib/kafka/data", "kafkadata"), Connect: conn("KAFKA_BROKERS", "{{host}}:{{port}}")},
	{ID: "redpanda", Image: "redpandadata/redpanda", Tag: "v24.2.4", Label: "Redpanda", Description: "Kafka-compatible streaming", Category: "messaging", Icon: "kafka", Ports: []int{9092},
		Command: "redpanda start --overprovisioned --smp 1 --memory 512M --kafka-addr 0.0.0.0:9092 --advertise-kafka-addr {{host}}:9092"},
	{ID: "kafka-ui", Image: "kafbat/kafka-ui", Tag: "v1.5.0", Label: "Kafka UI", Description: "Web UI for Kafka clusters (points at kafka:9092)", Category: "messaging", Icon: "kafka", Ports: []int{8080},
		Env:         env("KAFKA_CLUSTERS_0_NAME", "local", "KAFKA_CLUSTERS_0_BOOTSTRAPSERVERS", "kafka:9092", "DYNAMIC_CONFIG_ENABLED", "true"),
		Healthcheck: hcs("wget -qO- http://127.0.0.1:8080/actuator/health >/dev/null || exit 1", "10s", 10, "30s")},
	{ID: "zookeeper", Image: "zookeeper", Tag: "3.9", Label: "ZooKeeper", Description: "Coordination service", Category: "messaging", Icon: "zookeeper", Ports: []int{2181},
		Volumes: vols("/data", "data", "/datalog", "datalog"), Healthcheck: hcs("zkServer.sh status >/dev/null || exit 1", "10s", 10, "20s"),
		Connect: conn("ZOOKEEPER_CONNECT", "{{host}}:{{port}}")},
	{ID: "pulsar", Image: "apachepulsar/pulsar", Tag: "4.0.14", Label: "Apache Pulsar", Description: "Messaging and streaming (standalone; admin API on 8080)", Category: "messaging", Icon: "pulsar", Ports: []int{6650, 8080},
		Command: "bin/pulsar standalone", Volumes: vol("/pulsar/data", "pulsardata"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:8080/admin/v2/brokers/health >/dev/null || exit 1", "10s", 12, "60s"),
		Connect:     conn("PULSAR_URL", "pulsar://{{host}}:{{port}}")},
	{ID: "artemis", Image: "apache/activemq-artemis", Tag: "2.44.0-alpine", Label: "ActiveMQ Artemis", Description: "JMS/AMQP/STOMP/MQTT broker (console on 8161)", Category: "messaging", Icon: "activemq", Ports: []int{61616, 8161},
		Env: env("ARTEMIS_USER", "artemis", "ARTEMIS_PASSWORD", "artemis"), Volumes: vol("/var/lib/artemis-instance", "artemisdata"),
		Healthcheck: hcs("wget -qO- http://127.0.0.1:8161/ >/dev/null || exit 1", "10s", 10, "20s"),
		Connect:     conn("BROKER_URL", "tcp://{{host}}:{{port}}", "AMQP_URL", "amqp://{{env.ARTEMIS_USER}}:{{env.ARTEMIS_PASSWORD}}@{{host}}:{{port}}")},
	{ID: "activemq", Image: "apache/activemq-classic", Tag: "6.2.0", Label: "ActiveMQ Classic", Description: "JMS broker (console on 8161)", Category: "messaging", Icon: "activemq", Ports: []int{61616, 8161},
		Env: env("ACTIVEMQ_CONNECTION_USER", "app", "ACTIVEMQ_CONNECTION_PASSWORD", "app"), Volumes: vol("/opt/apache-activemq/data", "activemqdata"),
		Healthcheck: hcs("bash -c ':> /dev/tcp/127.0.0.1/61616' || exit 1", "10s", 10, "20s"),
		Connect:     conn("BROKER_URL", "tcp://{{host}}:{{port}}")},
	{ID: "nats", Image: "nats", Tag: "2-alpine", Label: "NATS", Description: "Lightweight messaging (JetStream with -js)", Category: "messaging", Icon: "nats", Ports: []int{4222, 8222},
		Command: "-js -m 8222", Connect: conn("NATS_URL", "nats://{{host}}:{{port}}")},
	{ID: "nsq", Image: "nsqio/nsq", Tag: "v1.3.0", Label: "NSQ", Description: "Realtime distributed messaging (nsqd: TCP 4150, HTTP 4151)", Category: "messaging", Icon: "nsq", Ports: []int{4150, 4151},
		Command: "/nsqd --data-path=/data", Volumes: vol("/data", "nsqdata"), Healthcheck: hc("wget -qO- http://127.0.0.1:4151/ping >/dev/null || exit 1", "10s", 5),
		Connect: conn("NSQD_ADDRESS", "{{host}}:{{port}}")},
	{ID: "mosquitto", Image: "eclipse-mosquitto", Tag: "2", Label: "Mosquitto", Description: "MQTT broker", Category: "messaging", Icon: "mosquitto", Ports: []int{1883}},
	{ID: "emqx", Image: "emqx/emqx", Tag: "5.8.9", Label: "EMQX", Description: "MQTT broker at scale (dashboard on 18083)", Category: "messaging", Icon: "emqx", Ports: []int{1883, 8083, 18083},
		Env: env("EMQX_DASHBOARD__DEFAULT_PASSWORD", "nimbo-password"), Volumes: vol("/opt/emqx/data", "emqxdata"),
		Healthcheck: hcs("/opt/emqx/bin/emqx ctl status", "10s", 10, "20s"), Connect: conn("MQTT_URL", "mqtt://{{host}}:{{port}}")},
	// Storage
	{ID: "minio", Image: "pgsty/minio", Tag: "RELEASE.2026-08-04T00-00-00Z", Label: "MinIO", Description: "S3-compatible object storage, community build (MinIO stopped publishing images; console on 9001)", Category: "storage", Icon: "minio", Ports: []int{9000, 9001},
		Env: env("MINIO_ROOT_USER", "minioadmin", "MINIO_ROOT_PASSWORD", "minioadmin"), Command: "server /data --console-address :9001", Volumes: vol("/data", "miniodata"),
		Healthcheck: hc("mc ready local", "10s", 5), Connect: conn("S3_ENDPOINT", "http://{{host}}:{{port}}")},
	{ID: "rustfs", Image: "rustfs/rustfs", Tag: "1.0.3", Label: "RustFS", Description: "S3-compatible object storage (console on 9001)", Category: "storage", Icon: "rustfs", Ports: []int{9000, 9001},
		Env: env("RUSTFS_ACCESS_KEY", "rustfsadmin", "RUSTFS_SECRET_KEY", "rustfsadmin", "RUSTFS_CONSOLE_ENABLE", "true"), Volumes: vol("/data", "rustfsdata"),
		Healthcheck: hc("curl -fs http://127.0.0.1:9000/health >/dev/null || exit 1", "10s", 5),
		Connect:     conn("S3_ENDPOINT", "http://{{host}}:{{port}}", "AWS_ACCESS_KEY_ID", "{{env.RUSTFS_ACCESS_KEY}}", "AWS_SECRET_ACCESS_KEY", "{{env.RUSTFS_SECRET_KEY}}")},
	{ID: "localstack", Image: "localstack/localstack", Tag: "3.7", Label: "LocalStack", Description: "Local AWS cloud emulator", Category: "storage", Icon: "localstack", Ports: []int{4566},
		Connect: conn("AWS_ENDPOINT_URL", "http://{{host}}:{{port}}")},
	{ID: "azurite", Image: "mcr.microsoft.com/azure-storage/azurite", Tag: "3.32.0", Label: "Azurite", Description: "Azure Storage emulator", Category: "storage", Icon: "azure", Ports: []int{10000, 10001, 10002}},
	// Observability
	{ID: "prometheus", Image: "prom/prometheus", Tag: "v2.54.1", Label: "Prometheus", Description: "Metrics and alerting", Category: "observability", Icon: "prometheus", Ports: []int{9090},
		Volumes: vol("/prometheus", "promdata"), Healthcheck: hc("wget -qO- http://127.0.0.1:9090/-/healthy || exit 1", "10s", 5)},
	{ID: "alertmanager", Image: "prom/alertmanager", Tag: "v0.34.1", Label: "Alertmanager", Description: "Routes Prometheus alerts", Category: "observability", Icon: "prometheus", Ports: []int{9093},
		Volumes: vol("/alertmanager", "alertmanagerdata"), Healthcheck: hc("wget -qO- http://127.0.0.1:9093/-/healthy >/dev/null || exit 1", "10s", 5)},
	{ID: "node-exporter", Image: "prom/node-exporter", Tag: "v1.12.1", Label: "Node Exporter", Description: "Host metrics for Prometheus (sees the container unless you mount the host)", Category: "observability", Icon: "prometheus", Ports: []int{9100},
		Healthcheck: hc("wget -qO- http://127.0.0.1:9100/metrics >/dev/null || exit 1", "10s", 5)},
	{ID: "pushgateway", Image: "prom/pushgateway", Tag: "v1.11.5", Label: "Pushgateway", Description: "Metrics push target for batch jobs", Category: "observability", Icon: "prometheus", Ports: []int{9091},
		Healthcheck: hc("wget -qO- http://127.0.0.1:9091/-/healthy >/dev/null || exit 1", "10s", 5)},
	{ID: "victoria-metrics", Image: "victoriametrics/victoria-metrics", Tag: "v1.154.1", Label: "VictoriaMetrics", Description: "Prometheus-compatible time-series database", Category: "observability", Icon: "victoriametrics", Ports: []int{8428},
		Command: "-storageDataPath=/victoria-metrics-data", Volumes: vol("/victoria-metrics-data", "vmdata"),
		Healthcheck: hc("wget -qO- http://127.0.0.1:8428/health >/dev/null || exit 1", "10s", 5), Connect: conn("PROMETHEUS_REMOTE_WRITE_URL", "http://{{host}}:{{port}}/api/v1/write")},
	{ID: "grafana", Image: "grafana/grafana", Tag: "11.2.0", Label: "Grafana", Description: "Dashboards", Category: "observability", Icon: "grafana", Ports: []int{3000},
		Env: env("GF_SECURITY_ADMIN_PASSWORD", "admin"), Volumes: vol("/var/lib/grafana", "grafanadata"),
		Healthcheck: hc("wget -qO- http://127.0.0.1:3000/api/health || exit 1", "10s", 5)},
	{ID: "loki", Image: "grafana/loki", Tag: "3.1.1", Label: "Loki", Description: "Log aggregation", Category: "observability", Icon: "loki", Ports: []int{3100}},
	{ID: "tempo", Image: "grafana/tempo", Tag: "2.10.10", Label: "Grafana Tempo", Description: "Trace storage (query API on 3200; mount a tempo.yaml with OTLP receivers to ingest)", Category: "observability", Icon: "grafana", Ports: []int{3200},
		Command: "-target=all -storage.trace.backend=local -storage.trace.local.path=/var/tempo/traces -storage.trace.wal.path=/var/tempo/wal", Volumes: vol("/var/tempo", "tempodata")},
	{ID: "alloy", Image: "grafana/alloy", Tag: "v1.21.1", Label: "Grafana Alloy", Description: "OpenTelemetry collector distribution (UI on 12345)", Category: "observability", Icon: "grafana", Ports: []int{12345},
		Command: "run --server.http.listen-addr=0.0.0.0:12345 --storage.path=/var/lib/alloy/data /etc/alloy/config.alloy", Volumes: vol("/var/lib/alloy/data", "alloydata")},
	{ID: "jaeger", Image: "jaegertracing/all-in-one", Tag: "1.60", Label: "Jaeger", Description: "Distributed tracing (UI on 16686, OTLP 4317/4318)", Category: "observability", Icon: "jaeger", Ports: []int{16686, 4317, 4318},
		Connect: conn("OTEL_EXPORTER_OTLP_ENDPOINT", "http://{{host}}:4318")},
	{ID: "zipkin", Image: "openzipkin/zipkin", Tag: "3.6.1", Label: "Zipkin", Description: "Distributed tracing", Category: "observability", Icon: "zipkin", Ports: []int{9411},
		Healthcheck: hcs("wget -qO- http://127.0.0.1:9411/health >/dev/null || exit 1", "10s", 10, "20s"), Connect: conn("ZIPKIN_ENDPOINT", "http://{{host}}:{{port}}/api/v2/spans")},
	{ID: "otel-collector", Image: "otel/opentelemetry-collector-contrib", Tag: "0.109.0", Label: "OpenTelemetry Collector", Description: "Receives and exports telemetry", Category: "observability", Icon: "otel", Ports: []int{4317, 4318},
		Connect: conn("OTEL_EXPORTER_OTLP_ENDPOINT", "http://{{host}}:4318")},
	{ID: "openobserve", Image: "openobserve/openobserve", Tag: "v1.1.2", Label: "OpenObserve", Description: "Logs, metrics and traces in one store (OTLP and UI on 5080)", Category: "observability", Icon: "openobserve", Ports: []int{5080},
		Env: env("ZO_ROOT_USER_EMAIL", "admin@example.com", "ZO_ROOT_USER_PASSWORD", "Nimbo-Passw0rd!", "ZO_DATA_DIR", "/data"), Volumes: vol("/data", "openobservedata"),
		Connect: conn("OTEL_EXPORTER_OTLP_ENDPOINT", "http://{{host}}:{{port}}/api/default")},
	{ID: "netdata", Image: "netdata/netdata", Tag: "v2.12.1", Label: "Netdata", Description: "Real-time system and container monitoring", Category: "observability", Icon: "netdata", Ports: []int{19999},
		Volumes:     vols("/var/lib/netdata", "lib", "/var/cache/netdata", "cache", "/etc/netdata", "config"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:19999/api/v1/info >/dev/null || exit 1", "10s", 10, "30s")},
	{ID: "cadvisor", Image: "ghcr.io/google/cadvisor", Tag: "0.60.6", Label: "cAdvisor", Description: "Container resource metrics (mount /, /sys, /var/run and /var/lib/docker read-only)", Category: "observability", Icon: "cadvisor", Ports: []int{8080},
		Healthcheck: hc("wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1", "10s", 5)},
	{ID: "uptime-kuma", Image: "louislam/uptime-kuma", Tag: "2.5.6", Label: "Uptime Kuma", Description: "Uptime monitoring and status pages", Category: "observability", Icon: "uptimekuma", Ports: []int{3001},
		Volumes: vol("/app/data", "kumadata"), Healthcheck: hcs("curl -fs http://127.0.0.1:3001/ >/dev/null || exit 1", "10s", 10, "30s")},
	{ID: "kibana", Image: "docker.elastic.co/kibana/kibana", Tag: "8.15.0", Label: "Kibana", Description: "Elasticsearch UI", Category: "observability", Icon: "kibana", Ports: []int{5601}},
	// Auth and secrets
	{ID: "keycloak", Image: "quay.io/keycloak/keycloak", Tag: "25.0", Label: "Keycloak", Description: "Identity and access management", Category: "auth", Icon: "keycloak", Ports: []int{8080},
		Env: env("KC_BOOTSTRAP_ADMIN_USERNAME", "admin", "KC_BOOTSTRAP_ADMIN_PASSWORD", "admin"), Command: "start-dev"},
	{ID: "authentik", Image: "ghcr.io/goauthentik/server", Tag: "2026.8.5", Label: "authentik", Description: "Identity provider: SSO, OIDC, SAML (needs PostgreSQL; run a second container with command worker)", Category: "auth", Icon: "authentik", Ports: []int{9000, 9443},
		Env: env("AUTHENTIK_SECRET_KEY", "change-me-to-a-long-random-string", "AUTHENTIK_POSTGRESQL__HOST", "postgres", "AUTHENTIK_POSTGRESQL__USER", "app",
			"AUTHENTIK_POSTGRESQL__PASSWORD", "app", "AUTHENTIK_POSTGRESQL__NAME", "app"),
		Command: "server", Volumes: vol("/data", "authentikdata"), Healthcheck: hcs("ak healthcheck", "15s", 10, "60s")},
	{ID: "kratos", Image: "oryd/kratos", Tag: "v26.2.0", Label: "Ory Kratos", Description: "Headless identity and user management (in-memory SQLite, dev mode)", Category: "auth", Icon: "ory", Ports: []int{4433, 4434},
		Env: env("DSN", "memory", "SERVE_PUBLIC_BASE_URL", "http://localhost:4433/", "SERVE_ADMIN_BASE_URL", "http://localhost:4434/",
			"SELFSERVICE_DEFAULT_BROWSER_RETURN_URL", "http://localhost:3000/", "IDENTITY_DEFAULT_SCHEMA_ID", "default",
			"IDENTITY_SCHEMAS", `[{"id":"default","url":"base64://`+kratosSchema+`"}]`, "COURIER_SMTP_CONNECTION_URI", "smtp://mailpit:1025/?disable_starttls=true"),
		Command: "serve --dev --watch-courier", Connect: conn("KRATOS_PUBLIC_URL", "http://{{host}}:4433", "KRATOS_ADMIN_URL", "http://{{host}}:4434")},
	{ID: "zitadel", Image: "ghcr.io/zitadel/zitadel", Tag: "v4.20.0", Label: "ZITADEL", Description: "Identity platform: OIDC, SAML, passkeys (needs PostgreSQL)", Category: "auth", Icon: "zitadel", Ports: []int{8080},
		Env: env("ZITADEL_MASTERKEY", "MasterkeyNeedsToHave32Characters", "ZITADEL_EXTERNALSECURE", "false", "ZITADEL_EXTERNALDOMAIN", "localhost",
			"ZITADEL_DATABASE_POSTGRES_HOST", "postgres", "ZITADEL_DATABASE_POSTGRES_PORT", "5432", "ZITADEL_DATABASE_POSTGRES_DATABASE", "zitadel",
			"ZITADEL_DATABASE_POSTGRES_USER_USERNAME", "zitadel", "ZITADEL_DATABASE_POSTGRES_USER_PASSWORD", "zitadel", "ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE", "disable",
			"ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME", "app", "ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD", "app", "ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE", "disable"),
		Command: "start-from-init --masterkeyFromEnv --tlsMode disabled"},
	{ID: "dex", Image: "ghcr.io/dexidp/dex", Tag: "v2.46.1-alpine", Label: "Dex", Description: "Federated OpenID Connect provider", Category: "auth", Icon: "dex", Ports: []int{5556},
		Env: env("DEX_ISSUER", "http://localhost:5556/dex"), Command: "dex serve /etc/dex/config.docker.yaml",
		Healthcheck: hc("wget -qO- http://127.0.0.1:5556/dex/.well-known/openid-configuration >/dev/null || exit 1", "10s", 5), Connect: conn("OIDC_ISSUER_URL", "{{env.DEX_ISSUER}}")},
	{ID: "logto", Image: "svhd/logto", Tag: "1.45.0", Label: "Logto", Description: "Auth for apps: OIDC, social sign-in, MFA (needs PostgreSQL; admin console on 3002)", Category: "auth", Icon: "logto", Ports: []int{3001, 3002},
		Env:        env("DB_URL", "postgres://app:app@postgres:5432/app", "TRUST_PROXY_HEADER", "1", "ENDPOINT", "http://localhost:3001", "ADMIN_ENDPOINT", "http://localhost:3002"),
		Entrypoint: "sh -c", Command: `"npm run cli db seed -- --swe && npm start"`},
	{ID: "vaultwarden", Image: "vaultwarden/server", Tag: "1.37.4-alpine", Label: "Vaultwarden", Description: "Bitwarden-compatible password manager server", Category: "auth", Icon: "vaultwarden", Ports: []int{80},
		Env: env("DOMAIN", "http://localhost", "SIGNUPS_ALLOWED", "true"), Volumes: vol("/data", "vwdata"), Healthcheck: hc("/healthcheck.sh", "10s", 5)},
	{ID: "vault", Image: "hashicorp/vault", Tag: "1.17", Label: "Vault", Description: "Secrets management (dev mode)", Category: "auth", Icon: "vault", Ports: []int{8200},
		Env: env("VAULT_DEV_ROOT_TOKEN_ID", "root"), Connect: conn("VAULT_ADDR", "http://{{host}}:{{port}}")},
	{ID: "consul", Image: "hashicorp/consul", Tag: "1.19", Label: "Consul", Description: "Service discovery and KV (dev mode)", Category: "auth", Icon: "consul", Ports: []int{8500}, Command: "agent -dev -client=0.0.0.0"},
	// Workflow and data
	{ID: "temporal", Image: "temporalio/temporal", Tag: "1.9.1", Label: "Temporal", Description: "Durable workflow engine (dev server with SQLite; UI on 8233)", Category: "workflow", Icon: "temporal", Ports: []int{7233, 8233},
		Command: "server start-dev --ip 0.0.0.0 --ui-ip 0.0.0.0 --db-filename /home/temporal/temporal.db", Volumes: vol("/home/temporal", "temporaldata"),
		Connect: conn("TEMPORAL_ADDRESS", "{{host}}:{{port}}")},
	{ID: "temporal-ui", Image: "temporalio/ui", Tag: "2.57.0", Label: "Temporal UI", Description: "Web UI for a Temporal server", Category: "workflow", Icon: "temporal", Ports: []int{8080},
		Env: env("TEMPORAL_ADDRESS", "temporal:7233", "TEMPORAL_CORS_ORIGINS", "http://localhost:3000")},
	{ID: "airflow", Image: "apache/airflow", Tag: "3.3.2", Label: "Apache Airflow", Description: "Workflow scheduler (standalone: SQLite, admin password in the logs)", Category: "workflow", Icon: "airflow", Ports: []int{8080},
		Env: env("AIRFLOW__CORE__LOAD_EXAMPLES", "false"), Command: "standalone", Volumes: vol("/opt/airflow", "airflowhome"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:8080/api/v2/monitor/health >/dev/null || exit 1", "15s", 10, "90s")},
	{ID: "prefect", Image: "prefecthq/prefect", Tag: "3.9.0-python3.12", Label: "Prefect", Description: "Workflow orchestration server and UI", Category: "workflow", Icon: "prefect", Ports: []int{4200},
		Command: "prefect server start --host 0.0.0.0", Volumes: vol("/root/.prefect", "prefectdata"),
		Healthcheck: hcs("python -c \"import urllib.request; urllib.request.urlopen('http://127.0.0.1:4200/api/health')\"", "10s", 10, "30s"),
		Connect:     conn("PREFECT_API_URL", "http://{{host}}:{{port}}/api")},
	{ID: "n8n", Image: "n8nio/n8n", Tag: "1.60.0", Label: "n8n", Description: "Workflow automation", Category: "workflow", Icon: "n8n", Ports: []int{5678}, Volumes: vol("/home/node/.n8n", "n8ndata")},
	{ID: "node-red", Image: "nodered/node-red", Tag: "4.1.17-22", Label: "Node-RED", Description: "Low-code flows for events and IoT", Category: "workflow", Icon: "nodered", Ports: []int{1880},
		Volumes: vol("/data", "noderreddata"), Healthcheck: hc("node /healthcheck.js", "10s", 5)},
	{ID: "metabase", Image: "metabase/metabase", Tag: "v0.56.25", Label: "Metabase", Description: "Business intelligence dashboards (H2 app database)", Category: "workflow", Icon: "metabase", Ports: []int{3000},
		Env: env("MB_DB_FILE", "/metabase-data/metabase.db"), Volumes: vol("/metabase-data", "metabasedata"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:3000/api/health >/dev/null || exit 1", "15s", 10, "120s")},
	{ID: "superset", Image: "apache/superset", Tag: "6.0.0", Label: "Apache Superset", Description: "Data exploration and dashboards (run superset db upgrade and fab create-admin once)", Category: "workflow", Icon: "superset", Ports: []int{8088},
		Env: env("SUPERSET_SECRET_KEY", "change-me-to-a-long-random-string"), Volumes: vol("/app/superset_home", "supersetdata"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:8088/health >/dev/null || exit 1", "15s", 10, "60s")},
	{ID: "nifi", Image: "apache/nifi", Tag: "2.12.0", Label: "Apache NiFi", Description: "Dataflow automation (HTTPS UI on 8443)", Category: "workflow", Icon: "nifi", Ports: []int{8443},
		Env:         env("SINGLE_USER_CREDENTIALS_USERNAME", "admin", "SINGLE_USER_CREDENTIALS_PASSWORD", "nimbo-password-123", "NIFI_WEB_HTTPS_HOST", "0.0.0.0", "NIFI_WEB_PROXY_HOST", "localhost:8443"),
		Volumes:     vols("/opt/nifi/nifi-current/conf", "conf", "/opt/nifi/nifi-current/database_repository", "db", "/opt/nifi/nifi-current/flowfile_repository", "flowfile", "/opt/nifi/nifi-current/content_repository", "content"),
		Healthcheck: hcs("curl -kfs https://localhost:8443/nifi/ >/dev/null || exit 1", "15s", 10, "120s")},
	// AI
	{ID: "ollama", Image: "ollama/ollama", Tag: "0.40.8", Label: "Ollama", Description: "Run open LLMs locally (ollama pull <model> inside the container)", Category: "ai", Icon: "ollama", Ports: []int{11434},
		Volumes: vol("/root/.ollama", "ollamadata"), Healthcheck: hc("ollama list >/dev/null || exit 1", "10s", 5),
		Connect: conn("OLLAMA_BASE_URL", "http://{{host}}:{{port}}", "OLLAMA_HOST", "http://{{host}}:{{port}}")},
	{ID: "open-webui", Image: "ghcr.io/open-webui/open-webui", Tag: "v0.12.3", Label: "Open WebUI", Description: "Chat UI for Ollama and OpenAI-compatible APIs", Category: "ai", Icon: "openwebui", Ports: []int{8080},
		Env: env("OLLAMA_BASE_URL", "http://ollama:11434", "WEBUI_SECRET_KEY", "change-me"), Volumes: vol("/app/backend/data", "openwebuidata"),
		Healthcheck: hcs("curl -fs http://127.0.0.1:8080/health >/dev/null || exit 1", "15s", 10, "60s")},
	{ID: "litellm", Image: "ghcr.io/berriai/litellm", Tag: "v1.106.2", Label: "LiteLLM", Description: "OpenAI-compatible proxy in front of 100+ LLM providers", Category: "ai", Icon: "litellm", Ports: []int{4000},
		Env: env("LITELLM_MASTER_KEY", "sk-nimbo-change-me"), Command: "--port 4000",
		Healthcheck: hcs("python -c \"import urllib.request; urllib.request.urlopen('http://127.0.0.1:4000/health/liveliness')\"", "15s", 10, "30s"),
		Connect:     conn("OPENAI_BASE_URL", "http://{{host}}:{{port}}", "OPENAI_API_KEY", "{{env.LITELLM_MASTER_KEY}}")},
	{ID: "flowise", Image: "flowiseai/flowise", Tag: "3.1.3", Label: "Flowise", Description: "Visual builder for LLM apps and agents", Category: "ai", Icon: "flowise", Ports: []int{3000},
		Volumes: vol("/root/.flowise", "flowisedata"), Healthcheck: hcs("wget -qO- http://127.0.0.1:3000/api/v1/ping >/dev/null || exit 1", "15s", 10, "30s")},
	// Dev tools
	{ID: "gitea", Image: "gitea/gitea", Tag: "1.22", Label: "Gitea", Description: "Self-hosted Git service", Category: "devtools", Icon: "gitea", Ports: []int{3000, 22}, Volumes: vol("/data", "giteadata")},
	{ID: "gitlab", Image: "gitlab/gitlab-ce", Tag: "19.5.1-ce.0", Label: "GitLab CE", Description: "Git hosting, CI/CD and registry (needs ~4 GB RAM; root password in /etc/gitlab/initial_root_password)", Category: "devtools", Icon: "gitlab", Ports: []int{80, 22},
		Volumes:     vols("/etc/gitlab", "config", "/var/log/gitlab", "logs", "/var/opt/gitlab", "data"),
		Healthcheck: hcs("/opt/gitlab/bin/gitlab-healthcheck --fail --max-time 10", "30s", 10, "300s")},
	{ID: "woodpecker", Image: "woodpeckerci/woodpecker-server", Tag: "v3.19.0-alpine", Label: "Woodpecker CI", Description: "CI server for Gitea/Forgejo/GitHub (add woodpecker-agent with the same agent secret)", Category: "devtools", Icon: "woodpecker", Ports: []int{8000, 9000},
		Env: env("WOODPECKER_HOST", "http://localhost:8000", "WOODPECKER_AGENT_SECRET", "change-me", "WOODPECKER_OPEN", "true",
			"WOODPECKER_GITEA", "true", "WOODPECKER_GITEA_URL", "http://gitea:3000", "WOODPECKER_GITEA_CLIENT", "change-me", "WOODPECKER_GITEA_SECRET", "change-me"),
		Volumes: vol("/var/lib/woodpecker", "woodpeckerdata"), Healthcheck: hc("/bin/woodpecker-server ping", "10s", 5)},
	{ID: "jenkins", Image: "jenkins/jenkins", Tag: "lts-jdk17", Label: "Jenkins", Description: "CI server", Category: "devtools", Icon: "jenkins", Ports: []int{8080, 50000}, Volumes: vol("/var/jenkins_home", "jenkinshome")},
	{ID: "sonarqube", Image: "sonarqube", Tag: "10-community", Label: "SonarQube", Description: "Code quality", Category: "devtools", Icon: "sonarqube", Ports: []int{9000}},
	{ID: "registry", Image: "registry", Tag: "2", Label: "Docker Registry", Description: "Private image registry", Category: "devtools", Icon: "docker", Ports: []int{5000}, Volumes: vol("/var/lib/registry", "registrydata")},
	{ID: "zot", Image: "ghcr.io/project-zot/zot", Tag: "v2.1.22", Label: "zot", Description: "OCI-native image registry", Category: "devtools", Icon: "zot", Ports: []int{5000},
		Volumes: vol("/var/lib/registry", "zotdata"), Connect: conn("REGISTRY", "{{host}}:{{port}}")},
	{ID: "verdaccio", Image: "verdaccio/verdaccio", Tag: "6.10.7", Label: "Verdaccio", Description: "Private npm registry and proxy", Category: "devtools", Icon: "verdaccio", Ports: []int{4873},
		Volumes: vol("/verdaccio/storage", "verdacciodata"), Healthcheck: hc("wget -qO- http://127.0.0.1:4873/-/ping >/dev/null || exit 1", "10s", 5),
		Connect: conn("NPM_CONFIG_REGISTRY", "http://{{host}}:{{port}}/")},
	{ID: "nexus", Image: "sonatype/nexus3", Tag: "3.97.1", Label: "Sonatype Nexus", Description: "Artifact repository: Maven, npm, Docker… (admin password in /nexus-data/admin.password)", Category: "devtools", Icon: "sonatype", Ports: []int{8081},
		Volumes: vol("/nexus-data", "nexusdata"), Healthcheck: hcs("curl -fs http://127.0.0.1:8081/service/rest/v1/status >/dev/null || exit 1", "15s", 10, "120s")},
	{ID: "code-server", Image: "codercom/code-server", Tag: "4.141.0", Label: "code-server", Description: "VS Code in the browser", Category: "devtools", Icon: "vscode", Ports: []int{8080},
		Env: env("PASSWORD", "nimbo-password"), Volumes: vol("/home/coder", "coderhome"), Healthcheck: hc("curl -fs http://127.0.0.1:8080/healthz >/dev/null || exit 1", "10s", 5)},
	{ID: "wiremock", Image: "wiremock/wiremock", Tag: "3.13.2", Label: "WireMock", Description: "HTTP API mocking (stubs in /home/wiremock/mappings)", Category: "devtools", Icon: "wiremock", Ports: []int{8080},
		Volumes: vol("/home/wiremock", "wiremockdata"), Healthcheck: hcs("wget -qO- http://127.0.0.1:8080/__admin/health >/dev/null || exit 1", "10s", 10, "20s")},
	{ID: "swagger-ui", Image: "swaggerapi/swagger-ui", Tag: "v5.34.0", Label: "Swagger UI", Description: "Interactive docs for an OpenAPI document (SWAGGER_JSON_URL)", Category: "devtools", Icon: "swagger", Ports: []int{8080},
		Env: env("SWAGGER_JSON_URL", "http://localhost:8080/openapi.json"), Healthcheck: hc("wget -qO- http://127.0.0.1:8080/ >/dev/null || exit 1", "10s", 5)},
	{ID: "selenium", Image: "selenium/standalone-chromium", Tag: "4.51.0", Label: "Selenium Chromium", Description: "WebDriver browser for end-to-end tests (noVNC on 7900)", Category: "devtools", Icon: "selenium", Ports: []int{4444, 7900},
		Healthcheck: hcs("curl -fs http://127.0.0.1:4444/status >/dev/null || exit 1", "10s", 10, "20s"), Connect: conn("SELENIUM_REMOTE_URL", "http://{{host}}:{{port}}/wd/hub")},
	// Apps and backends
	{ID: "nextcloud", Image: "nextcloud", Tag: "32-apache", Label: "Nextcloud", Description: "File sync and collaboration (SQLite by default)", Category: "apps", Icon: "nextcloud", Ports: []int{80},
		Env:     env("SQLITE_DATABASE", "nextcloud", "NEXTCLOUD_ADMIN_USER", "admin", "NEXTCLOUD_ADMIN_PASSWORD", "nimbo-password", "NEXTCLOUD_TRUSTED_DOMAINS", "localhost"),
		Volumes: vol("/var/www/html", "nextclouddata"), Healthcheck: hcs("curl -fs http://127.0.0.1/status.php >/dev/null || exit 1", "15s", 10, "60s")},
	{ID: "mattermost", Image: "mattermost/mattermost-team-edition", Tag: "11.11.3", Label: "Mattermost", Description: "Team chat (needs PostgreSQL; amd64 only, no shell for a healthcheck)", Category: "apps", Icon: "mattermost", Ports: []int{8065},
		Env:     env("MM_SQLSETTINGS_DRIVERNAME", "postgres", "MM_SQLSETTINGS_DATASOURCE", "postgres://app:app@postgres:5432/app?sslmode=disable", "MM_SERVICESETTINGS_SITEURL", "http://localhost:8065"),
		Volumes: vol("/mattermost/data", "mattermostdata")},
	{ID: "outline", Image: "outlinewiki/outline", Tag: "1.11.0", Label: "Outline", Description: "Team knowledge base (needs PostgreSQL and Redis; generate SECRET_KEY/UTILS_SECRET with openssl rand -hex 32)", Category: "apps", Icon: "outline", Ports: []int{3000},
		Env: env("SECRET_KEY", "2cade82cd90df00cae258793810bdbdc267114e4a0109fbb7e373d3ca412791a", "UTILS_SECRET", "496700ae3d71fd0134053ed2fac2ef98e58f3a9d7f2121c52dbc04b20f4d7efd", "DATABASE_URL", "postgres://app:app@postgres:5432/app", "PGSSLMODE", "disable",
			"REDIS_URL", "redis://redis:6379", "URL", "http://localhost:3000", "FILE_STORAGE", "local", "FILE_STORAGE_LOCAL_ROOT_DIR", "/var/lib/outline/data"),
		Volumes: vol("/var/lib/outline/data", "outlinedata")},
	{ID: "plausible", Image: "ghcr.io/plausible/community-edition", Tag: "v3.2.1", Label: "Plausible Analytics", Description: "Privacy-friendly web analytics (needs PostgreSQL and ClickHouse)", Category: "apps", Icon: "plausible", Ports: []int{8000},
		Env: env("BASE_URL", "http://localhost:8000", "SECRET_KEY_BASE", "change-me-to-a-64-byte-random-string", "DATABASE_URL", "postgres://app:app@postgres:5432/plausible",
			"CLICKHOUSE_DATABASE_URL", "http://app:app@clickhouse:8123/plausible_events_db"),
		Command: "sh -c '/entrypoint.sh db createdb && /entrypoint.sh db migrate && /entrypoint.sh run'", Volumes: vol("/var/lib/plausible", "plausibledata")},
	{ID: "umami", Image: "ghcr.io/umami-software/umami", Tag: "3.4.0", Label: "Umami", Description: "Simple web analytics (needs PostgreSQL)", Category: "apps", Icon: "umami", Ports: []int{3000},
		Env: env("DATABASE_URL", "postgres://app:app@postgres:5432/app", "APP_SECRET", "change-me"), Healthcheck: hcs("curl -fs http://127.0.0.1:3000/api/heartbeat >/dev/null || exit 1", "10s", 10, "30s")},
	{ID: "matomo", Image: "matomo", Tag: "5.14.0-apache", Label: "Matomo", Description: "Web analytics (needs MySQL/MariaDB)", Category: "apps", Icon: "matomo", Ports: []int{80},
		Env: env("MATOMO_DATABASE_HOST", "db", "MATOMO_DATABASE_USERNAME", "app", "MATOMO_DATABASE_PASSWORD", "app", "MATOMO_DATABASE_DBNAME", "app"), Volumes: vol("/var/www/html", "matomodata")},
	{ID: "appsmith", Image: "appsmith/appsmith-ce", Tag: "v2.5.1", Label: "Appsmith", Description: "Low-code internal tools builder", Category: "apps", Icon: "appsmith", Ports: []int{80},
		Volumes: vol("/appsmith-stacks", "appsmithdata"), Healthcheck: hcs("curl -fs http://127.0.0.1/api/v1/health >/dev/null || exit 1", "15s", 10, "120s")},
	{ID: "hasura", Image: "hasura/graphql-engine", Tag: "v2.48.16", Label: "Hasura GraphQL Engine", Description: "Instant GraphQL over PostgreSQL", Category: "apps", Icon: "hasura", Ports: []int{8080},
		Env:     env("HASURA_GRAPHQL_DATABASE_URL", "postgres://app:app@postgres:5432/app", "HASURA_GRAPHQL_ADMIN_SECRET", "change-me", "HASURA_GRAPHQL_ENABLE_CONSOLE", "true"),
		Connect: conn("GRAPHQL_URL", "http://{{host}}:{{port}}/v1/graphql")},
	{ID: "postgrest", Image: "postgrest/postgrest", Tag: "v14.18", Label: "PostgREST", Description: "REST API straight from a PostgreSQL schema", Category: "apps", Icon: "postgres", Ports: []int{3000},
		Env:     env("PGRST_DB_URI", "postgres://app:app@postgres:5432/app", "PGRST_DB_SCHEMAS", "public", "PGRST_DB_ANON_ROLE", "app"),
		Connect: conn("API_URL", "http://{{host}}:{{port}}")},
	{ID: "pocketbase", Image: "ghcr.io/muchobien/pocketbase", Tag: "0.40.5", Label: "PocketBase", Description: "Backend in one file: SQLite, auth, files, realtime", Category: "apps", Icon: "pocketbase", Ports: []int{8090},
		Volumes: vol("/pb_data", "pbdata"), Healthcheck: hc("wget -qO- http://127.0.0.1:8090/api/health >/dev/null || exit 1", "10s", 5),
		Connect: conn("POCKETBASE_URL", "http://{{host}}:{{port}}")},
	{ID: "nocodb", Image: "nocodb/nocodb", Tag: "2026.09.1", Label: "NocoDB", Description: "Spreadsheet UI over a database (SQLite by default)", Category: "apps", Icon: "nocodb", Ports: []int{8080},
		Volumes: vol("/usr/app/data", "nocodbdata"), Healthcheck: hcs("wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null || exit 1", "10s", 10, "30s")},
	// CMS
	{ID: "wordpress", Image: "wordpress", Tag: "6-apache", Label: "WordPress", Description: "Blog and CMS (needs MySQL/MariaDB)", Category: "cms", Icon: "wordpress", Ports: []int{80},
		Env: env("WORDPRESS_DB_HOST", "db", "WORDPRESS_DB_USER", "wordpress", "WORDPRESS_DB_PASSWORD", "wordpress", "WORDPRESS_DB_NAME", "wordpress"), Volumes: vol("/var/www/html", "wpdata")},
	{ID: "ghost", Image: "ghost", Tag: "5-alpine", Label: "Ghost", Description: "Publishing platform", Category: "cms", Icon: "ghost", Ports: []int{2368},
		Env: env("url", "http://localhost:2368", "NODE_ENV", "development"), Volumes: vol("/var/lib/ghost/content", "ghostcontent")},
	{ID: "directus", Image: "directus/directus", Tag: "11", Label: "Directus", Description: "Headless CMS over any SQL database", Category: "cms", Icon: "directus", Ports: []int{8055},
		Env: env("SECRET", "change-me", "ADMIN_EMAIL", "admin@example.com", "ADMIN_PASSWORD", "admin")},
	{ID: "drupal", Image: "drupal", Tag: "11-apache", Label: "Drupal", Description: "CMS", Category: "cms", Icon: "drupal", Ports: []int{80}},
	// DNS and networking
	{ID: "coredns", Image: "coredns/coredns", Tag: "1.15.0", Label: "CoreDNS", Description: "DNS server (whoami answers until you mount a Corefile)", Category: "network", Icon: "coredns", Ports: []int{53}},
	{ID: "pihole", Image: "pihole/pihole", Tag: "2026.10.0", Label: "Pi-hole", Description: "DNS sinkhole with a web admin (/admin)", Category: "network", Icon: "pihole", Ports: []int{80, 53},
		Env: env("TZ", "UTC", "FTLCONF_webserver_api_password", "nimbo-password"), Volumes: vol("/etc/pihole", "piholedata"),
		Healthcheck: hcs("dig +short +norecurse +retry=0 @127.0.0.1 pi.hole >/dev/null || exit 1", "10s", 10, "30s")},
	// Tools
	{ID: "adminer", Image: "adminer", Tag: "4", Label: "Adminer", Description: "Database admin UI", Category: "tools", Icon: "adminer", Ports: []int{8080}},
	{ID: "pgadmin", Image: "dpage/pgadmin4", Tag: "8", Label: "pgAdmin", Description: "PostgreSQL admin UI", Category: "tools", Icon: "postgres", Ports: []int{80},
		Env: env("PGADMIN_DEFAULT_EMAIL", "admin@example.com", "PGADMIN_DEFAULT_PASSWORD", "admin")},
	{ID: "phpmyadmin", Image: "phpmyadmin", Tag: "5", Label: "phpMyAdmin", Description: "MySQL admin UI", Category: "tools", Icon: "mysql", Ports: []int{80}, Env: env("PMA_HOST", "db")},
	{ID: "mongo-express", Image: "mongo-express", Tag: "1", Label: "Mongo Express", Description: "MongoDB admin UI", Category: "tools", Icon: "mongodb", Ports: []int{8081},
		Env: env("ME_CONFIG_MONGODB_URL", "mongodb://root:root@mongo:27017/", "ME_CONFIG_BASICAUTH", "false")},
	{ID: "redis-insight", Image: "redis/redisinsight", Tag: "2.56", Label: "Redis Insight", Description: "Redis UI", Category: "tools", Icon: "redis", Ports: []int{5540}},
	{ID: "mailpit", Image: "axllent/mailpit", Tag: "v1.20", Label: "Mailpit", Description: "SMTP catcher with web UI (8025)", Category: "tools", Icon: "mail", Ports: []int{1025, 8025},
		Connect: conn("SMTP_HOST", "{{host}}", "SMTP_PORT", "1025")},
	{ID: "portainer", Image: "portainer/portainer-ce", Tag: "2.21.1-alpine", Label: "Portainer", Description: "Container management UI", Category: "tools", Icon: "portainer", Ports: []int{9443}},
	{ID: "alpine", Image: "alpine", Tag: "3.20", Label: "Alpine", Description: "Minimal Linux for scripts and jobs", Category: "tools", Icon: "alpine", Command: "sleep infinity"},
	{ID: "ubuntu", Image: "ubuntu", Tag: "24.04", Label: "Ubuntu", Description: "Ubuntu base image", Category: "tools", Icon: "ubuntu", Command: "sleep infinity"},
	{ID: "busybox", Image: "busybox", Tag: "1.36", Label: "BusyBox", Description: "Tiny Unix toolbox", Category: "tools", Icon: "busybox", Command: "sleep infinity"},
}

// kratosSchema is a minimal email + password identity schema (base64), so
// Ory Kratos starts without a mounted config.
const kratosSchema = "eyIkaWQiOiJodHRwczovL25pbWJvLmxvY2FsL2RlZmF1bHQuc2NoZW1hLmpzb24iLCIkc2NoZW1hIjoiaHR0cDovL2pzb24tc2NoZW1hLm9yZy9kcmFmdC0wNy9zY2hlbWEjIiwidHlwZSI6Im9iamVjdCIsInByb3BlcnRpZXMiOnsidHJhaXRzIjp7InR5cGUiOiJvYmplY3QiLCJwcm9wZXJ0aWVzIjp7ImVtYWlsIjp7InR5cGUiOiJzdHJpbmciLCJmb3JtYXQiOiJlbWFpbCIsIm9yeS5zaC9rcmF0b3MiOnsiY3JlZGVudGlhbHMiOnsicGFzc3dvcmQiOnsiaWRlbnRpZmllciI6dHJ1ZX19fX19LCJyZXF1aXJlZCI6WyJlbWFpbCJdfX19"

// Images without a published port (base images, toolchains) still list
// `ports: []`: clients read the field as an array.
func init() {
	for i := range images {
		if images[i].Ports == nil {
			images[i].Ports = []int{}
		}
	}
}

// Images is the curated image palette.
func Images() []model.DockerImage { return images }

// Image finds a palette entry by ID.
func Image(id string) (model.DockerImage, bool) {
	for _, im := range images {
		if im.ID == id {
			return im, true
		}
	}
	return model.DockerImage{}, false
}

// ImageFor finds the palette entry of an image reference (repository match).
func ImageFor(ref string) (model.DockerImage, bool) {
	r := ParseImage(ref)
	if r == nil {
		return model.DockerImage{}, false
	}
	repo := strings.TrimPrefix(strings.TrimPrefix(r.Repo, "docker.io/"), "library/")
	for _, im := range images {
		if im.Image == repo {
			return im, true
		}
	}
	return model.DockerImage{}, false
}

// RenderConnect fills a connect template for a client of service host
// whose environment is env.
func RenderConnect(tmpl, host string, port int, env map[string]string) string {
	out := strings.ReplaceAll(tmpl, "{{host}}", host)
	out = strings.ReplaceAll(out, "{{port}}", strconv.Itoa(port))
	for k, v := range env {
		out = strings.ReplaceAll(out, "{{env."+k+"}}", v)
	}
	return out
}
