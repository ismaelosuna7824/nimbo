// Technology icons (images, runtimes, frameworks, databases, proxies…) for Docker
// workspaces, the service catalog and the Architecture view. The SVGs are
// third-party logos under their owners' terms (see NOTICE.md). Each icon is its
// own asset file, fetched only when an <img> shows it.

export type TechCategory =
  | "platform"
  | "web"
  | "language"
  | "framework"
  | "database"
  | "cache"
  | "search"
  | "messaging"
  | "storage"
  | "security"
  | "observability"
  | "cms"
  | "devtools"
  | "os"
  | "generic";

type IconDef = readonly [label: string, category: TechCategory, darkInk?: true];

/** id → [label, category, drawn in dark ink only]. Every id has an ./assets/<id>.svg file. */
const ICONS = {
  docker: ["Docker", "platform"],
  kubernetes: ["Kubernetes", "platform"],
  helm: ["Helm", "platform", true],
  azure: ["Microsoft Azure", "platform"],
  nginx: ["NGINX", "web"],
  apache: ["Apache HTTP Server", "web"],
  caddy: ["Caddy", "web"],
  traefik: ["Traefik", "web"],
  haproxy: ["HAProxy", "web"],
  envoy: ["Envoy", "web"],
  tomcat: ["Apache Tomcat", "web"],
  go: ["Go", "language"],
  node: ["Node.js", "language"],
  deno: ["Deno", "language", true],
  bun: ["Bun", "language"],
  python: ["Python", "language"],
  rust: ["Rust", "language", true],
  java: ["Java", "language"],
  kotlin: ["Kotlin", "language"],
  scala: ["Scala", "language"],
  dotnet: [".NET", "language"],
  csharp: ["C#", "language"],
  php: ["PHP", "language"],
  ruby: ["Ruby", "language"],
  elixir: ["Elixir", "language"],
  erlang: ["Erlang", "language"],
  swift: ["Swift", "language"],
  cpp: ["C++", "language"],
  c: ["C", "language"],
  typescript: ["TypeScript", "language"],
  javascript: ["JavaScript", "language"],
  dart: ["Dart", "language"],
  perl: ["Perl", "language"],
  lua: ["Lua", "language"],
  zig: ["Zig", "language"],
  haskell: ["Haskell", "language"],
  r: ["R", "language"],
  julia: ["Julia", "language"],
  nextjs: ["Next.js", "framework"],
  nuxt: ["Nuxt", "framework"],
  react: ["React", "framework"],
  vue: ["Vue.js", "framework"],
  angular: ["Angular", "framework"],
  svelte: ["Svelte", "framework"],
  express: ["Express", "framework", true],
  nestjs: ["NestJS", "framework"],
  spring: ["Spring", "framework"],
  quarkus: ["Quarkus", "framework"],
  django: ["Django", "framework", true],
  fastapi: ["FastAPI", "framework"],
  flask: ["Flask", "framework", true],
  rails: ["Ruby on Rails", "framework"],
  laravel: ["Laravel", "framework"],
  symfony: ["Symfony", "framework", true],
  phoenix: ["Phoenix", "framework"],
  gin: ["Gin", "framework"],
  fastify: ["Fastify", "framework", true],
  koa: ["Koa", "framework", true],
  hono: ["Hono", "framework"],
  remix: ["Remix", "framework", true],
  astro: ["Astro", "framework", true],
  socketio: ["Socket.IO", "framework", true],
  streamlit: ["Streamlit", "framework"],
  aiohttp: ["AIOHTTP", "framework"],
  fiber: ["Fiber", "framework"],
  actix: ["Actix Web", "framework", true],
  rocket: ["Rocket", "framework"],
  tokio: ["Tokio", "framework", true],
  gunicorn: ["Gunicorn", "web"],
  celery: ["Celery", "messaging"],
  postgres: ["PostgreSQL", "database"],
  mysql: ["MySQL", "database", true],
  mariadb: ["MariaDB", "database", true],
  mongodb: ["MongoDB", "database"],
  sqlserver: ["SQL Server", "database"],
  "oracle-db": ["Oracle Database", "database"],
  cassandra: ["Apache Cassandra", "database"],
  clickhouse: ["ClickHouse", "database"],
  neo4j: ["Neo4j", "database"],
  couchdb: ["Apache CouchDB", "database"],
  influxdb: ["InfluxDB", "database", true],
  cockroachdb: ["CockroachDB", "database"],
  sqlite: ["SQLite", "database"],
  dynamodb: ["DynamoDB Local", "database"],
  supabase: ["Supabase", "database"],
  adminer: ["Adminer", "database", true],
  redis: ["Redis", "cache"],
  valkey: ["Valkey", "cache"],
  memcached: ["Memcached", "cache"],
  elasticsearch: ["Elasticsearch", "search"],
  kibana: ["Kibana", "observability"],
  opensearch: ["OpenSearch", "search"],
  rabbitmq: ["RabbitMQ", "messaging"],
  kafka: ["Apache Kafka", "messaging", true],
  nats: ["NATS", "messaging"],
  mosquitto: ["Eclipse Mosquitto", "messaging", true],
  minio: ["MinIO", "storage"],
  localstack: ["LocalStack", "storage"],
  keycloak: ["Keycloak", "security", true],
  vault: ["HashiCorp Vault", "security"],
  consul: ["HashiCorp Consul", "security"],
  grafana: ["Grafana", "observability"],
  prometheus: ["Prometheus", "observability"],
  jaeger: ["Jaeger", "observability"],
  otel: ["OpenTelemetry", "observability"],
  loki: ["Grafana Loki", "observability"],
  sentry: ["Sentry", "observability", true],
  wordpress: ["WordPress", "cms", true],
  ghost: ["Ghost", "cms", true],
  drupal: ["Drupal", "cms"],
  joomla: ["Joomla", "cms"],
  nextcloud: ["Nextcloud", "cms"],
  strapi: ["Strapi", "cms"],
  directus: ["Directus", "cms", true],
  n8n: ["n8n", "devtools"],
  nodered: ["Node-RED", "devtools"],
  airflow: ["Apache Airflow", "devtools"],
  jupyter: ["Jupyter", "devtools"],
  jenkins: ["Jenkins", "devtools"],
  gitlab: ["GitLab", "devtools"],
  sonarqube: ["SonarQube", "devtools", true],
  gitea: ["Gitea", "devtools"],
  prisma: ["Prisma", "devtools", true],
  mail: ["Mail catcher", "devtools"],
  portainer: ["Portainer", "devtools"],
  maven: ["Apache Maven", "devtools"],
  gradle: ["Gradle", "devtools", true],
  alpine: ["Alpine Linux", "os"],
  ubuntu: ["Ubuntu", "os"],
  debian: ["Debian", "os"],
  centos: ["CentOS", "os"],
  fedora: ["Fedora", "os"],
  rockylinux: ["Rocky Linux", "os"],
  redhat: ["Red Hat UBI", "os"],
  linux: ["Linux", "os", true],
  distroless: ["Distroless", "os"],
  chainguard: ["Chainguard", "os"],
  busybox: ["BusyBox", "os"],
  varnish: ["Varnish Cache", "web"],
  squid: ["Squid", "web"],
  kong: ["Kong", "web", true],
  apisix: ["Apache APISIX", "web"],
  krakend: ["KrakenD", "web"],
  oauth: ["OAuth2 Proxy", "security", true],
  jetty: ["Eclipse Jetty", "web"],
  wildfly: ["WildFly", "web"],
  timescale: ["TimescaleDB", "database"],
  questdb: ["QuestDB", "database"],
  surrealdb: ["SurrealDB", "database"],
  scylladb: ["ScyllaDB", "database"],
  arangodb: ["ArangoDB", "database"],
  couchbase: ["Couchbase", "database"],
  etcd: ["etcd", "database"],
  dragonfly: ["Dragonfly", "cache"],
  meilisearch: ["Meilisearch", "search"],
  typesense: ["Typesense", "search"],
  solr: ["Apache Solr", "search"],
  qdrant: ["Qdrant", "search"],
  weaviate: ["Weaviate", "search"],
  chroma: ["Chroma", "search"],
  milvus: ["Milvus", "search"],
  activemq: ["Apache ActiveMQ", "messaging"],
  pulsar: ["Apache Pulsar", "messaging", true],
  nsq: ["NSQ", "messaging"],
  zookeeper: ["Apache ZooKeeper", "messaging"],
  emqx: ["EMQX", "messaging"],
  rustfs: ["RustFS", "storage"],
  authentik: ["authentik", "security"],
  ory: ["Ory", "security"],
  zitadel: ["ZITADEL", "security"],
  dex: ["Dex", "security"],
  logto: ["Logto", "security"],
  vaultwarden: ["Vaultwarden", "security", true],
  victoriametrics: ["VictoriaMetrics", "observability"],
  zipkin: ["Zipkin", "observability"],
  openobserve: ["OpenObserve", "observability"],
  netdata: ["Netdata", "observability"],
  cadvisor: ["cAdvisor", "observability"],
  uptimekuma: ["Uptime Kuma", "observability"],
  temporal: ["Temporal", "devtools", true],
  prefect: ["Prefect", "devtools", true],
  metabase: ["Metabase", "devtools"],
  superset: ["Apache Superset", "devtools"],
  nifi: ["Apache NiFi", "devtools"],
  ollama: ["Ollama", "devtools", true],
  openwebui: ["Open WebUI", "devtools"],
  litellm: ["LiteLLM", "devtools"],
  flowise: ["Flowise", "devtools"],
  woodpecker: ["Woodpecker CI", "devtools"],
  verdaccio: ["Verdaccio", "devtools", true],
  sonatype: ["Sonatype Nexus", "devtools", true],
  zot: ["zot", "devtools"],
  vscode: ["code-server", "devtools"],
  wiremock: ["WireMock", "devtools"],
  swagger: ["Swagger UI", "devtools"],
  selenium: ["Selenium", "devtools"],
  mattermost: ["Mattermost", "cms", true],
  outline: ["Outline", "cms", true],
  plausible: ["Plausible Analytics", "observability"],
  umami: ["Umami", "observability", true],
  matomo: ["Matomo", "observability"],
  appsmith: ["Appsmith", "devtools", true],
  hasura: ["Hasura", "database"],
  pocketbase: ["PocketBase", "database"],
  nocodb: ["NocoDB", "database"],
  coredns: ["CoreDNS", "web"],
  pihole: ["Pi-hole", "web"],
  container: ["Container", "generic"],
  volume: ["Volume", "generic"],
  network: ["Network", "generic"],
  secret: ["Secret", "generic"],
  config: ["Config", "generic"],
  "build-stage": ["Build stage", "generic"],
  external: ["External", "generic"],
  port: ["Port", "generic"],
} as const satisfies Record<string, IconDef>;

export type TechIconId = keyof typeof ICONS;

export type TechIconInfo = {
  id: TechIconId;
  label: string;
  /** Bundled asset URL of the SVG (same-origin; the Architecture SVG export inlines it). */
  src: string;
  category: TechCategory;
  /** Logo drawn in black/near-black only: give it a light backdrop on dark surfaces. */
  darkInk: boolean;
  /** True for the kind fallbacks (container, volume, network…), not a recognised technology. */
  generic: boolean;
};

/** All icon ids of the registry. */
export const TECH_ICON_IDS = Object.keys(ICONS) as readonly TechIconId[];

export function isTechIconId(id: string): id is TechIconId {
  return Object.hasOwn(ICONS, id);
}

/** Bundled URL of an icon (Vite turns this pattern into a glob of ./assets). */
export function techIconSrc(id: TechIconId): string {
  return new URL(`./assets/${id}.svg`, import.meta.url).href;
}

/** Icon of a registry id (e.g. a catalog entry's icon); unknown ids get the generic container. */
export function techIcon(id: string): TechIconInfo {
  const key: TechIconId = isTechIconId(id) ? id : "container";
  const [label, category, darkInk] = ICONS[key] as IconDef;
  return { id: key, label, src: techIconSrc(key), category, darkInk: darkInk === true, generic: category === "generic" };
}

// ---------------------------------------------------------------------------
// Image references

export type ImageRef = {
  /** Registry host, "docker.io" for Docker Hub. */
  registry: string;
  /** Repository path without registry; Docker Hub official images keep their "library/" prefix. */
  repository: string;
  /** Last path segment of the repository ("redis" for "bitnami/redis"). */
  name: string;
  tag?: string;
  digest?: string;
};

const DOCKER_HUB_HOSTS = new Set(["docker.io", "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com"]);

/**
 * Parses an image reference the way the Docker CLI does: an optional registry host (a first
 * segment with a "." or ":" or "localhost"), a repository path, an optional tag and digest.
 * Compose interpolations (`${TAG:-latest}`, `$REGISTRY`) are dropped; references without a
 * repository left return undefined.
 */
export function parseImageRef(ref: string): ImageRef | undefined {
  let s = ref
    .trim()
    .toLowerCase()
    .replace(/\$\{[^}]*\}/g, "")
    .replace(/\$[a-z_][a-z0-9_]*/g, "");
  let digest: string | undefined;
  const at = s.indexOf("@");
  if (at >= 0) {
    digest = s.slice(at + 1) || undefined;
    s = s.slice(0, at);
  }
  let tag: string | undefined;
  const colon = s.lastIndexOf(":");
  if (colon > s.lastIndexOf("/")) {
    tag = s.slice(colon + 1) || undefined;
    s = s.slice(0, colon);
  }
  const parts = s.split("/").filter(Boolean);
  let registry = "docker.io";
  if (parts.length > 1 && /[.:]|^localhost$/.test(parts[0])) registry = parts.shift()!;
  if (DOCKER_HUB_HOSTS.has(registry)) registry = "docker.io";
  if (!parts.length) return undefined;
  if (registry === "docker.io" && parts.length === 1) parts.unshift("library");
  return { registry, repository: parts.join("/"), name: parts[parts.length - 1], tag, digest };
}

/**
 * Repositories (with or without registry) matched before the image name: pattern ending in "/"
 * is a prefix, otherwise the exact repository. Vendors whose image names would read as another
 * technology (prom/node-exporter is Prometheus, not Node.js) belong here.
 */
const REPO_RULES: readonly (readonly [pattern: string, id: TechIconId])[] = [
  ["mcr.microsoft.com/dotnet/", "dotnet"],
  ["mcr.microsoft.com/mssql/", "sqlserver"],
  ["mcr.microsoft.com/azure-sql-edge", "sqlserver"],
  ["mcr.microsoft.com/azure-storage/", "azure"],
  ["mcr.microsoft.com/azure-functions/", "azure"],
  ["mcr.microsoft.com/cosmosdb/", "azure"],
  ["mcr.microsoft.com/azure-cli", "azure"],
  ["container-registry.oracle.com/database/", "oracle-db"],
  ["container-registry.oracle.com/mysql/", "mysql"],
  ["container-registry.oracle.com/java/", "java"],
  ["docker.elastic.co/kibana/", "kibana"],
  ["docker.elastic.co/", "elasticsearch"],
  ["prom/", "prometheus"],
  ["quay.io/prometheus/", "prometheus"],
  ["grafana/loki", "loki"],
  ["grafana/promtail", "loki"],
  ["grafana/", "grafana"],
  ["jaegertracing/", "jaeger"],
  ["otel/", "otel"],
  ["confluentinc/", "kafka"],
  ["jupyter/", "jupyter"],
  ["quay.io/jupyter/", "jupyter"],
  ["portainer/", "portainer"],
  ["supabase/", "supabase"],
  ["kindest/", "kubernetes"],
  ["rancher/k3s", "kubernetes"],
  ["moby/", "docker"],
  ["docker/", "docker"],
  ["rocker/", "r"],
  ["getsentry/", "sentry"],
  // Vendors whose image names say "server", "ui", "graphql-engine", "community-edition"…
  ["temporalio/", "temporal"],
  ["oryd/", "ory"],
  ["goauthentik/", "authentik"],
  ["couchbase/", "couchbase"],
  ["vaultwarden/", "vaultwarden"],
  ["hasura/", "hasura"],
  ["plausible/", "plausible"],
  ["selenium/", "selenium"],
  ["codercom/", "vscode"],
  ["sonatype/", "sonatype"],
  ["zitadel/", "zitadel"],
  ["dexidp/", "dex"],
  ["woodpeckerci/", "woodpecker"],
  ["getmeili/", "meilisearch"],
  ["semitechnologies/", "weaviate"],
  ["chromadb/", "chroma"],
  ["milvusdb/", "milvus"],
  ["apachepulsar/", "pulsar"],
  ["openzipkin/", "zipkin"],
  ["victoriametrics/", "victoriametrics"],
  ["zinclabs/", "openobserve"],
  // Redis Inc. images (redislabs/redismod, redis/redis-stack…) name the module bundle, not "redis".
  ["redislabs/", "redis"],
  ["redis/", "redis"],
];

/** Repositories tried only when the image name says nothing (base-image vendors). */
const REPO_FALLBACK: readonly (readonly [pattern: string, id: TechIconId])[] = [
  ["gcr.io/distroless/", "distroless"],
  ["cgr.dev/", "chainguard"],
  ["chainguard/", "chainguard"],
  ["registry.access.redhat.com/", "redhat"],
  ["registry.redhat.io/", "redhat"],
  ["redhat/", "redhat"],
];

/**
 * Image names (last repository segment) → icon; also tried with the name's first `-`/`_`/`.`
 * token, with and without a trailing version ("nodejs20-debian12" → nodejs).
 */
const NAMES: Record<string, TechIconId> = {
  docker: "docker",
  dind: "docker",
  registry: "docker",
  kubectl: "kubernetes",
  k3s: "kubernetes",
  kubernetes: "kubernetes",
  helm: "helm",
  nginx: "nginx",
  openresty: "nginx",
  httpd: "apache",
  apache: "apache",
  caddy: "caddy",
  traefik: "traefik",
  haproxy: "haproxy",
  envoy: "envoy",
  tomcat: "tomcat",
  golang: "go",
  go: "go",
  node: "node",
  nodejs: "node",
  deno: "deno",
  bun: "bun",
  python: "python",
  pypy: "python",
  rust: "rust",
  java: "java",
  openjdk: "java",
  jdk: "java",
  jre: "java",
  "eclipse-temurin": "java",
  temurin: "java",
  amazoncorretto: "java",
  corretto: "java",
  ibmjava: "java",
  "ibm-semeru-runtimes": "java",
  sapmachine: "java",
  zulu: "java",
  "zulu-openjdk": "java",
  liberica: "java",
  clojure: "java",
  groovy: "java",
  kotlin: "kotlin",
  scala: "scala",
  sbt: "scala",
  "scala-sbt": "scala",
  dotnet: "dotnet",
  aspnet: "dotnet",
  mono: "csharp",
  php: "php",
  ruby: "ruby",
  jruby: "ruby",
  elixir: "elixir",
  erlang: "erlang",
  swift: "swift",
  gcc: "cpp",
  clang: "cpp",
  haskell: "haskell",
  dart: "dart",
  perl: "perl",
  lua: "lua",
  zig: "zig",
  julia: "julia",
  "r-base": "r",
  maven: "maven",
  gradle: "gradle",
  postgres: "postgres",
  postgresql: "postgres",
  postgis: "postgres",
  timescaledb: "timescale",
  postgrest: "postgres",
  pgvector: "postgres",
  pgadmin4: "postgres",
  pgbouncer: "postgres",
  mysql: "mysql",
  percona: "mysql",
  "percona-server": "mysql",
  mariadb: "mariadb",
  mongo: "mongodb",
  mongodb: "mongodb",
  "mongodb-community-server": "mongodb",
  "mongo-express": "mongodb",
  mssql: "sqlserver",
  "oracle-xe": "oracle-db",
  "oracle-free": "oracle-db",
  cassandra: "cassandra",
  clickhouse: "clickhouse",
  "clickhouse-server": "clickhouse",
  neo4j: "neo4j",
  couchdb: "couchdb",
  influxdb: "influxdb",
  cockroach: "cockroachdb",
  cockroachdb: "cockroachdb",
  sqlite: "sqlite",
  sqlite3: "sqlite",
  "dynamodb-local": "dynamodb",
  dynamodb: "dynamodb",
  adminer: "adminer",
  redis: "redis",
  valkey: "valkey",
  memcached: "memcached",
  elasticsearch: "elasticsearch",
  kibana: "kibana",
  logstash: "elasticsearch",
  opensearch: "opensearch",
  rabbitmq: "rabbitmq",
  kafka: "kafka",
  nats: "nats",
  "eclipse-mosquitto": "mosquitto",
  mosquitto: "mosquitto",
  minio: "minio",
  localstack: "localstack",
  keycloak: "keycloak",
  vault: "vault",
  consul: "consul",
  grafana: "grafana",
  prometheus: "prometheus",
  alertmanager: "prometheus",
  "node-exporter": "prometheus",
  pushgateway: "prometheus",
  jaeger: "jaeger",
  "opentelemetry-collector": "otel",
  "opentelemetry-collector-contrib": "otel",
  loki: "loki",
  promtail: "loki",
  sentry: "sentry",
  wordpress: "wordpress",
  ghost: "ghost",
  drupal: "drupal",
  joomla: "joomla",
  nextcloud: "nextcloud",
  strapi: "strapi",
  directus: "directus",
  n8n: "n8n",
  "node-red": "nodered",
  airflow: "airflow",
  jupyter: "jupyter",
  jupyterhub: "jupyter",
  jenkins: "jenkins",
  gitlab: "gitlab",
  sonarqube: "sonarqube",
  gitea: "gitea",
  azurite: "azure",
  mailpit: "mail",
  mailhog: "mail",
  maildev: "mail",
  smtp4dev: "mail",
  greenmail: "mail",
  portainer: "portainer",
  alpine: "alpine",
  ubuntu: "ubuntu",
  debian: "debian",
  centos: "centos",
  fedora: "fedora",
  rockylinux: "rockylinux",
  almalinux: "linux",
  amazonlinux: "linux",
  oraclelinux: "linux",
  archlinux: "linux",
  leap: "linux",
  ubi: "redhat",
  "ubi-minimal": "redhat",
  "ubi-micro": "redhat",
  ubi8: "redhat",
  ubi9: "redhat",
  "wolfi-base": "chainguard",
  busybox: "busybox",
  distroless: "distroless",
  varnish: "varnish",
  squid: "squid",
  kong: "kong",
  apisix: "apisix",
  krakend: "krakend",
  "oauth2-proxy": "oauth",
  jetty: "jetty",
  wildfly: "wildfly",
  questdb: "questdb",
  surrealdb: "surrealdb",
  scylla: "scylladb",
  scylladb: "scylladb",
  arangodb: "arangodb",
  couchbase: "couchbase",
  etcd: "etcd",
  dragonfly: "dragonfly",
  meilisearch: "meilisearch",
  typesense: "typesense",
  solr: "solr",
  qdrant: "qdrant",
  weaviate: "weaviate",
  chroma: "chroma",
  milvus: "milvus",
  activemq: "activemq",
  artemis: "activemq",
  pulsar: "pulsar",
  nsq: "nsq",
  zookeeper: "zookeeper",
  emqx: "emqx",
  rustfs: "rustfs",
  authentik: "authentik",
  kratos: "ory",
  hydra: "ory",
  keto: "ory",
  oathkeeper: "ory",
  zitadel: "zitadel",
  dex: "dex",
  logto: "logto",
  vaultwarden: "vaultwarden",
  tempo: "grafana",
  alloy: "grafana",
  "victoria-metrics": "victoriametrics",
  vmagent: "victoriametrics",
  zipkin: "zipkin",
  openobserve: "openobserve",
  netdata: "netdata",
  cadvisor: "cadvisor",
  "uptime-kuma": "uptimekuma",
  temporal: "temporal",
  prefect: "prefect",
  metabase: "metabase",
  superset: "superset",
  nifi: "nifi",
  ollama: "ollama",
  "open-webui": "openwebui",
  litellm: "litellm",
  flowise: "flowise",
  woodpecker: "woodpecker",
  verdaccio: "verdaccio",
  nexus3: "sonatype",
  nexus: "sonatype",
  zot: "zot",
  "code-server": "vscode",
  wiremock: "wiremock",
  "swagger-ui": "swagger",
  mattermost: "mattermost",
  outline: "outline",
  umami: "umami",
  matomo: "matomo",
  appsmith: "appsmith",
  pocketbase: "pocketbase",
  nocodb: "nocodb",
  coredns: "coredns",
  pihole: "pihole",
};

/** Own-property lookup (image names like "constructor" must not hit Object.prototype). */
function lookup<V>(table: Record<string, V>, key: string): V | undefined {
  return Object.hasOwn(table, key) ? table[key] : undefined;
}

function matchRepo(rules: readonly (readonly [string, TechIconId])[], full: string, repo: string) {
  for (const [pattern, id] of rules) {
    const hit = pattern.endsWith("/")
      ? full.startsWith(pattern) || repo.startsWith(pattern)
      : full === pattern || repo === pattern;
    if (hit) return id;
  }
  return undefined;
}

/** Icon id of an image reference ("postgres:16", "ghcr.io/org/app@sha256:…"), when known. */
export function imageIconId(image: string): TechIconId | undefined {
  const ref = parseImageRef(image);
  if (!ref) return undefined;
  const full = `${ref.registry}/${ref.repository}`;
  // Docker Hub mirrors (public.ecr.aws/docker/library/x, mirror.gcr.io/library/x) are the official image.
  const repo = ref.repository.replace(/^docker\/library\//, "library/");
  const byRepo = matchRepo(REPO_RULES, full, repo);
  if (byRepo) return byRepo;
  const token = ref.name.split(/[-_.]/)[0];
  const byName = lookup(NAMES, ref.name) ?? lookup(NAMES, token) ?? lookup(NAMES, token.replace(/\d+$/, ""));
  if (byName) return byName;
  return matchRepo(REPO_FALLBACK, full, repo);
}

// ---------------------------------------------------------------------------
// Stack hints (frameworks named by a service's dependencies)

/**
 * Framework ids by precedence: meta-frameworks before the libraries they build on, servers,
 * workers and ORMs last (a FastAPI app also lists uvicorn, a Django one celery).
 */
const FRAMEWORK_ORDER: readonly TechIconId[] = [
  "nextjs",
  "nuxt",
  "remix",
  "astro",
  "nestjs",
  "angular",
  "svelte",
  "spring",
  "quarkus",
  "django",
  "fastapi",
  "flask",
  "streamlit",
  "aiohttp",
  "rails",
  "laravel",
  "symfony",
  "phoenix",
  "gin",
  "fiber",
  "actix",
  "rocket",
  "fastify",
  "koa",
  "hono",
  "express",
  "vue",
  "react",
  "socketio",
  "celery",
  "gunicorn",
  "tokio",
  "prisma",
];

/** Language hints by precedence: the runtime before the language compiled to it. */
const LANGUAGE_ORDER: readonly TechIconId[] = [
  "bun",
  "deno",
  "node",
  "typescript",
  "javascript",
  "python",
  "go",
  "kotlin",
  "scala",
  "java",
  "dotnet",
  "csharp",
  "ruby",
  "php",
  "rust",
  "elixir",
  "erlang",
  "swift",
  "cpp",
];

const STACK_ALIASES: Record<string, TechIconId> = {
  next: "nextjs",
  nextjs: "nextjs",
  "next.js": "nextjs",
  nuxt: "nuxt",
  nuxt3: "nuxt",
  remix: "remix",
  "remix-run": "remix",
  astro: "astro",
  nestjs: "nestjs",
  angular: "angular",
  svelte: "svelte",
  sveltejs: "svelte",
  sveltekit: "svelte",
  spring: "spring",
  "spring-boot": "spring",
  springboot: "spring",
  "org.springframework.boot": "spring",
  "org.springframework": "spring",
  quarkus: "quarkus",
  "io.quarkus": "quarkus",
  django: "django",
  fastapi: "fastapi",
  flask: "flask",
  streamlit: "streamlit",
  aiohttp: "aiohttp",
  rails: "rails",
  "ruby-on-rails": "rails",
  laravel: "laravel",
  symfony: "symfony",
  phoenix: "phoenix",
  gin: "gin",
  "gin-gonic": "gin",
  fiber: "fiber",
  gofiber: "fiber",
  actix: "actix",
  "actix-web": "actix",
  rocket: "rocket",
  fastify: "fastify",
  koa: "koa",
  hono: "hono",
  express: "express",
  expressjs: "express",
  vue: "vue",
  vuejs: "vue",
  react: "react",
  socketio: "socketio",
  "socket.io": "socketio",
  celery: "celery",
  gunicorn: "gunicorn",
  tokio: "tokio",
  prisma: "prisma",
  // Languages and runtimes, and frameworks without an icon of their own.
  node: "node",
  nodejs: "node",
  bun: "bun",
  deno: "deno",
  typescript: "typescript",
  javascript: "javascript",
  python: "python",
  uvicorn: "python",
  go: "go",
  golang: "go",
  echo: "go",
  labstack: "go",
  chi: "go",
  "go-chi": "go",
  gorilla: "go",
  java: "java",
  micronaut: "java",
  "io.micronaut": "java",
  kotlin: "kotlin",
  scala: "scala",
  dotnet: "dotnet",
  aspnet: "dotnet",
  aspnetcore: "dotnet",
  csharp: "csharp",
  ruby: "ruby",
  sinatra: "ruby",
  php: "php",
  slim: "php",
  rust: "rust",
  axum: "rust",
  elixir: "elixir",
  erlang: "erlang",
  swift: "swift",
};

/** Icon ids (frameworks and languages) named by stack hints. */
function stackIds(stack: readonly string[] | undefined): Set<TechIconId> {
  const found = new Set<TechIconId>();
  for (const hint of stack ?? []) {
    for (const raw of hint.trim().toLowerCase().split(/[/:@\s]+/)) {
      const token = /^[a-z0-9._-]+/.exec(raw)?.[0]?.replace(/[._-]+$/, "");
      if (!token) continue;
      const id = lookup(STACK_ALIASES, token) ?? (token.startsWith("spring-boot") ? "spring" : undefined);
      if (id) found.add(id);
    }
  }
  return found;
}

/**
 * Framework named by stack hints: package names ("@nestjs/core", "next"), module paths
 * ("github.com/gin-gonic/gin"), Maven coordinates ("org.springframework.boot:spring-boot-starter-web"),
 * requirement lines ("fastapi==0.110") or plain names ("Django").
 */
export function stackIconId(stack: readonly string[] | undefined): TechIconId | undefined {
  const found = stackIds(stack);
  return FRAMEWORK_ORDER.find((id) => found.has(id));
}

// ---------------------------------------------------------------------------
// Compose graph nodes

/** Fallback icon per graph node kind (see the Docker workspace model). */
const KIND_ICONS: Record<string, TechIconId> = {
  service: "container",
  "build-stage": "build-stage",
  volume: "volume",
  network: "network",
  secret: "secret",
  config: "config",
  external: "external",
  port: "port",
};

/** Kinds that never carry a technology (their icon is the kind's). */
const PLAIN_KINDS = new Set(["volume", "network", "secret", "config", "external", "port"]);

export type TechIconQuery = {
  /** `image:` of the service. */
  image?: string;
  /** FROM images of the service's Dockerfile stages, in file order (the last one is the runtime). */
  baseImages?: readonly string[];
  /** Dependency/framework hints of the service's build context. */
  stack?: readonly string[];
  /** Graph node kind: service, build-stage, volume, network, secret, config, external, port. */
  kind?: string;
};

const NOT_PRODUCT = new Set<TechCategory>(["language", "os", "generic"]);

/**
 * Icon of a compose graph node. A built service is what its runtime stage is (the `image:`
 * only names the result), an image-only service is its image. A product image (nginx,
 * postgres…) wins; otherwise a framework named by the stack hints wins over the language
 * runtime of the images, then a language named by the hints, then the OS base; unknown
 * images get the kind's generic icon.
 */
export function techIconFor({ image, baseImages, stack, kind }: TechIconQuery): TechIconInfo {
  const fallback = KIND_ICONS[kind ?? "service"] ?? "container";
  if (kind && PLAIN_KINDS.has(kind)) return techIcon(fallback);
  const candidates = [...(baseImages ?? [])].reverse();
  if (image) candidates.push(image);
  const ids = candidates.map(imageIconId).filter((id): id is TechIconId => id !== undefined);
  const category = (id: TechIconId) => (ICONS[id] as IconDef)[1];
  const runtime = candidates.length ? imageIconId(candidates[0]) : undefined;
  const hinted = stackIds(stack);
  const id =
    (runtime && !NOT_PRODUCT.has(category(runtime)) ? runtime : undefined) ??
    FRAMEWORK_ORDER.find((x) => hinted.has(x)) ??
    ids.find((x) => category(x) === "language") ??
    LANGUAGE_ORDER.find((x) => hinted.has(x)) ??
    ids.find((x) => !NOT_PRODUCT.has(category(x))) ??
    ids.find((x) => category(x) === "os") ??
    fallback;
  return techIcon(id);
}
