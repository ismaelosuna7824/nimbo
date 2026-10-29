package advisor

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Design smells of docker and Kubernetes designs: three or more services on
// one relational database (database-per-service), in production two or
// more without a cache (cache-aside), and (Kubernetes) several Services
// exposed on their own load balancer or node port instead of one Ingress
// (api-gateway). Services that publish their own compose ports are
// docker_reverse_proxy's trigger already.

// sqlDialects are the relational images whose official images run the
// scripts of /docker-entrypoint-initdb.d on first start, by SQL dialect.
var sqlDialects = map[string]string{"postgres": "postgres", "postgis": "postgres", "mysql": "mysql", "mariadb": "mysql"}

// cacheImages are in-memory caches (last repository part).
var cacheImages = []string{"redis", "valkey", "memcached", "dragonfly", "keydb"}

// readStore reports a database a cache can front.
func readStore(image string) bool { _, sql := sqlDialects[image]; return sql || image == "mongo" }

// initSQL creates one database and login per service.
func initSQL(dialect string, services []string, indent string) string {
	var b strings.Builder
	for _, s := range services {
		id := dbIdent(s)
		if dialect == "mysql" {
			fmt.Fprintf(&b, "%sCREATE DATABASE IF NOT EXISTS %s;\n%sCREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY 'change-me';\n%sGRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%';\n", indent, id, indent, id, indent, id, id)
			continue
		}
		fmt.Fprintf(&b, "%sCREATE USER %s WITH PASSWORD 'change-me';\n%sCREATE DATABASE %s OWNER %s;\n%sREVOKE ALL ON DATABASE %s FROM PUBLIC;\n", indent, id, indent, id, id, indent, id)
	}
	return b.String()
}

// dbNameKey matches environment variables that name a database.
var dbNameKey = regexp.MustCompile(`(?i)^(DB|DATABASE|POSTGRES|MYSQL|MARIADB|PG)_?(NAME|DB|DATABASE)$`)

// databaseOf is the database name a set of environment values selects on
// host: the path of a connection URL to host, or a *_DB_NAME variable.
func databaseOf(host string, env map[string]string) string {
	url := regexp.MustCompile(`(?i)(?:@|//)` + regexp.QuoteMeta(host) + `(?::\d+)?/([A-Za-z0-9_-]+)`)
	for _, k := range sortedKeys(env) {
		if m := url.FindStringSubmatch(env[k]); m != nil {
			return m[1]
		}
	}
	for _, k := range sortedKeys(env) {
		if dbNameKey.MatchString(k) && env[k] != "" {
			return env[k]
		}
	}
	return ""
}

// ownDatabases reports consumers that each name a different database on
// the shared server: they already have a database per service.
func ownDatabases(names []string) bool {
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

const (
	dbPerServiceTitle = "Give each service its own database"
	dbPerServiceWhy   = "Three or more services read and write the same database: a schema change for one breaks the others, one service's heavy queries slow them all, and none can be deployed, scaled or restored on its own, a distributed monolith at the data layer. " +
		"With a database (and login) per service each service owns its data and shares it through its API or events; locally they can stay on one server."
	dbPerServiceSkip = "One application split into tiers or modules that are deployed together (n-tier, modular monolith): keep one database there, with a schema per module."
	dbPerServiceDocs = "https://microservices.io/patterns/data/database-per-service.html"
	cacheAsideTitle  = "Cache the hot reads of the shared database (cache-aside)"
	cacheAsideWhy    = "Several services read the same database on every request: each read is a round trip and the database carries the whole read load. " +
		"With cache-aside each service reads the cache first, loads from the database on a miss and stores the result with a TTL, and deletes the key when it writes."
	cacheAsideSkip = "Write-heavy data, reads that must always be current (balances, stock counts), and small data sets the database keeps in memory anyway."
	cacheAsideDocs = "https://learn.microsoft.com/en-us/azure/architecture/patterns/cache-aside"
)

// ---------------------------------------------------------------------------
// Docker Compose

func dockerSmells(c *model.ComposeModel, svcs []service, production bool, free func(kind, name string) string, add func(int, Recommendation)) {
	active := map[string]service{}
	cached := false
	for _, s := range svcs {
		active[s.name] = s
		cached = cached || slices.Contains(cacheImages, imageName(s.ServiceFacts))
	}
	var dbNodes, cacheNodes []string
	var dbHeader, dbServices, dbConfigs, cacheSnippet strings.Builder
	var cacheApps []string
	for _, s := range svcs {
		image := imageName(s.ServiceFacts)
		if !readStore(image) {
			continue
		}
		var apps, names []string
		for _, a := range consumers(c, s.name) {
			app, ok := active[a]
			if !ok || isProxy(app.ServiceFacts) {
				continue
			}
			if _, data := dataOf(app.ServiceFacts); data {
				continue
			}
			apps = append(apps, a)
			env := map[string]string{}
			for _, e := range app.Environment {
				if e.Value != nil {
					env[e.Key] = *e.Value
				}
			}
			names = append(names, databaseOf(s.name, env))
		}
		if dialect, sql := sqlDialects[image]; sql && len(apps) >= 3 && !ownDatabases(names) {
			dbNodes = append(dbNodes, model.ComposeServiceID(s.name))
			conf := free("config", s.name+"-per-service")
			fmt.Fprintf(&dbHeader, "# %s is shared by %s: on its first start (empty data volume) it creates one database and login per service (on an existing volume, run the script once).\n", s.name, strings.Join(apps, ", "))
			fmt.Fprintf(&dbServices, "  %s:\n    configs:\n      - source: %s\n        target: /docker-entrypoint-initdb.d/10-per-service.sql\n", s.name, conf)
			fmt.Fprintf(&dbConfigs, "  %s:\n    content: |\n%s", conf, initSQL(dialect, apps, "      "))
		}
		if production && !cached && len(apps) >= 2 {
			cacheNodes = append(cacheNodes, model.ComposeServiceID(s.name))
			for _, a := range apps {
				if !slices.Contains(cacheApps, a) {
					cacheApps = append(cacheApps, a)
				}
			}
		}
	}
	if len(dbNodes) > 0 {
		add(prioDesign, Recommendation{
			ID:      "docker_database_per_service",
			Pattern: "database-per-service",
			Title:   dbPerServiceTitle,
			Why:     dbPerServiceWhy,
			Pillars: []model.Pillar{model.PillarReliability, model.PillarOperations, model.PillarPerformance},
			Nodes:   dbNodes,
			Add: []Addition{
				{Type: "configs", Purpose: "init script creating one database and login per service on the shared server"},
				{Type: "environment", Purpose: "each service's connection string names its own database and login"},
			},
			Snippet: dbHeader.String() + "# Point each service's connection string at its own database and login (passwords from secrets in production).\nservices:\n" + dbServices.String() + "configs:\n" + dbConfigs.String(),
			Scope:   ScopeRecommended,
			Skip:    dbPerServiceSkip,
			Docs:    dbPerServiceDocs,
		})
	}
	if len(cacheNodes) == 0 {
		return
	}
	cache := free("service", "cache")
	fmt.Fprintf(&cacheSnippet, "# Merge into the production overlay.\nservices:\n  %s:\n    image: valkey/valkey:8-alpine\n    restart: unless-stopped\n    command: [\"valkey-server\", \"--maxmemory\", \"256mb\", \"--maxmemory-policy\", \"allkeys-lru\"]\n    healthcheck:\n      test: [\"CMD\", \"valkey-cli\", \"ping\"]\n      interval: 10s\n      timeout: 3s\n      retries: 3\n", cache)
	for _, a := range cacheApps {
		fmt.Fprintf(&cacheSnippet, "  %s:\n    environment:\n      CACHE_URL: redis://%s:6379\n", a, cache)
	}
	add(prioDesign, Recommendation{
		ID:      "docker_cache_aside",
		Pattern: "cache-aside",
		Title:   cacheAsideTitle,
		Why:     cacheAsideWhy,
		Pillars: []model.Pillar{model.PillarPerformance, model.PillarCost, model.PillarReliability},
		Nodes:   cacheNodes,
		Add: []Addition{
			{Type: "services." + cache, Purpose: "Valkey with a memory bound and LRU eviction"},
			{Type: "environment", Purpose: "CACHE_URL for the services that read the database"},
		},
		Snippet: cacheSnippet.String(),
		Scope:   ScopeProduction,
		Skip:    cacheAsideSkip,
		Docs:    cacheAsideDocs,
	})
}

// ---------------------------------------------------------------------------
// Kubernetes

// kubeImages are the last repository parts of a workload's container
// images.
func kubeImages(w *model.KubeNode) []string {
	var out []string
	if w.Workload == nil {
		return nil
	}
	for _, c := range w.Workload.Containers {
		if c.ImageRef != nil {
			out = append(out, c.ImageRef.Repo[strings.LastIndex(c.ImageRef.Repo, "/")+1:])
		}
	}
	return out
}

// kubeIsData reports a workload running a database, cache or broker image.
func kubeIsData(w *model.KubeNode) bool {
	return slices.ContainsFunc(kubeImages(w), func(img string) bool { _, ok := dataImages[img]; return ok })
}

func kubeIsProxy(w *model.KubeNode) bool {
	return slices.ContainsFunc(kubeImages(w), func(img string) bool { return slices.Contains(proxyImages, img) })
}

// kubeConsumers are the application workloads that name one of the Services
// selecting w in their environment, with the database each names.
func kubeConsumers(g *kubeGraph, w *model.KubeNode) ([]*model.KubeNode, []string) {
	var apps []*model.KubeNode
	var names []string
	for _, svc := range g.incoming(w.ID, model.KubeEdgeSelects) {
		for _, app := range g.incoming(svc.ID, model.KubeEdgeInferred) {
			if slices.Contains(apps, app) || app.Workload == nil || kubeIsData(app) {
				continue
			}
			apps = append(apps, app)
			env := map[string]string{}
			for _, c := range app.Workload.Containers {
				for _, e := range c.Env {
					if e.Value != nil {
						env[e.Name] = *e.Value
					}
				}
			}
			names = append(names, databaseOf(svc.Name, env))
		}
	}
	return apps, names
}

// kubeContainerOf is the name of w's first container running image (last
// repository part).
func kubeContainerOf(w *model.KubeNode, image string) string {
	for _, c := range w.Workload.Containers {
		if c.ImageRef != nil && c.ImageRef.Repo[strings.LastIndex(c.ImageRef.Repo, "/")+1:] == image {
			return c.Name
		}
	}
	return ""
}

func kubeSmells(g *kubeGraph, production bool, add func(int, Recommendation)) {
	kubeSharedData(g, production, add)
	kubeSingleEntry(g, add)
}

func kubeSharedData(g *kubeGraph, production bool, add func(int, Recommendation)) {
	cached := false
	for _, w := range g.ofKind("Deployment", "StatefulSet") {
		cached = cached || slices.ContainsFunc(kubeImages(w), func(img string) bool { return slices.Contains(cacheImages, img) })
	}
	var dbNodes, dbDocs, cacheNodes []string
	var cacheApps []*model.KubeNode
	for _, w := range g.ofKind("Deployment", "StatefulSet") {
		images := kubeImages(w)
		store := slices.IndexFunc(images, readStore)
		if store < 0 {
			continue
		}
		apps, names := kubeConsumers(g, w)
		if dialect, sql := sqlDialects[images[store]]; sql && len(apps) >= 3 && !ownDatabases(names) {
			dbNodes = append(dbNodes, w.ID)
			appNames := make([]string, len(apps))
			for i, a := range apps {
				appNames[i] = a.Name
			}
			cm := w.Name + "-per-service"
			dbDocs = append(dbDocs, fmt.Sprintf(`# %s/%s is shared by %s. On its first start (empty data volume) it runs this script: one database and login per service (on an existing volume, run it once).
# Point each service's connection string at its own (passwords from a Secret in production).
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  10-per-service.sql: |
%s`, w.Kind, w.Name, strings.Join(appNames, ", "), cm, ns(w), initSQL(dialect, appNames, "    ")),
				fmt.Sprintf(`# Merge into %s/%s:
apiVersion: apps/v1
kind: %s
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      volumes:
        - name: per-service-init
          configMap:
            name: %s
      containers:
        - name: %s
          volumeMounts:
            - name: per-service-init
              mountPath: /docker-entrypoint-initdb.d
`, w.Kind, w.Name, w.Kind, w.Name, ns(w), cm, kubeContainerOf(w, images[store])))
		}
		if production && !cached && len(apps) >= 2 {
			cacheNodes = append(cacheNodes, w.ID)
			for _, a := range apps {
				if !slices.Contains(cacheApps, a) {
					cacheApps = append(cacheApps, a)
				}
			}
		}
	}
	if len(dbNodes) > 0 {
		add(prioDesign, Recommendation{
			ID:      "k8s_database_per_service",
			Pattern: "database-per-service",
			Title:   dbPerServiceTitle,
			Why:     dbPerServiceWhy,
			Pillars: []model.Pillar{model.PillarReliability, model.PillarOperations, model.PillarPerformance},
			Nodes:   dbNodes,
			Add: []Addition{
				{Type: "ConfigMap", Purpose: "init script creating one database and login per service"},
				{Type: "Deployment", Purpose: "mounts the script on /docker-entrypoint-initdb.d of the database container"},
			},
			Snippet: strings.Join(dbDocs, "---\n"),
			Scope:   ScopeRecommended,
			Skip:    dbPerServiceSkip,
			Docs:    dbPerServiceDocs,
		})
	}
	if len(cacheNodes) == 0 {
		return
	}
	namespace := ns(cacheApps[0])
	cache := "cache"
	if g.byID["Deployment/"+namespace+"/cache"] != nil || g.byID["Service/"+namespace+"/cache"] != nil {
		cache = "cache-aside"
	}
	docs := []string{fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
    spec:
      containers:
        - name: valkey
          image: valkey/valkey:8-alpine
          args: ["--maxmemory", "256mb", "--maxmemory-policy", "allkeys-lru"]
          ports:
            - containerPort: 6379
          resources:
            requests:
              cpu: 100m
              memory: 300Mi
            limits:
              memory: 300Mi
          readinessProbe:
            exec:
              command: ["valkey-cli", "ping"]
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  selector:
    app: %[1]s
  ports:
    - port: 6379
      targetPort: 6379
`, cache, namespace)}
	for _, a := range cacheApps {
		docs = append(docs, fmt.Sprintf(`# Merge into %s/%s:
apiVersion: apps/v1
kind: %s
metadata:
  name: %s
  namespace: %s
spec:
  template:
    spec:
      containers:
        - name: %s
          env:
            - name: CACHE_URL
              value: redis://%s.%s.svc:6379
`, a.Kind, a.Name, a.Kind, a.Name, ns(a), a.Workload.Containers[0].Name, cache, namespace))
	}
	add(prioDesign, Recommendation{
		ID:      "k8s_cache_aside",
		Pattern: "cache-aside",
		Title:   cacheAsideTitle,
		Why:     cacheAsideWhy,
		Pillars: []model.Pillar{model.PillarPerformance, model.PillarCost, model.PillarReliability},
		Nodes:   cacheNodes,
		Add: []Addition{
			{Type: "Deployment", Purpose: "Valkey with a memory bound and LRU eviction"},
			{Type: "Service", Purpose: "cache: the address the services read through"},
		},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeProduction,
		Skip:    cacheAsideSkip,
		Docs:    cacheAsideDocs,
	})
}

// kubeSingleEntry recommends one Ingress for application Services exposed
// on their own load balancer or node port (two or more).
func kubeSingleEntry(g *kubeGraph, add func(int, Recommendation)) {
	type exposed struct {
		svc  *model.KubeNode
		port int
	}
	var entries []exposed
	for _, svc := range g.ofKind("Service") {
		if svc.Service == nil || svc.Service.Type != "LoadBalancer" && svc.Service.Type != "NodePort" || len(g.incoming(svc.ID, model.KubeEdgeRoutes)) > 0 {
			continue
		}
		workloads := g.outgoing(svc.ID, model.KubeEdgeSelects)
		if len(workloads) == 0 || slices.ContainsFunc(workloads, func(w *model.KubeNode) bool { return kubeIsData(w) || kubeIsProxy(w) }) {
			continue
		}
		for _, p := range svc.Service.Ports {
			if p.Protocol == "" || strings.EqualFold(p.Protocol, "TCP") {
				entries = append(entries, exposed{svc, p.Port})
				break
			}
		}
	}
	if len(entries) < 2 {
		return
	}
	var nodes, docs []string
	rules := map[string]*strings.Builder{}
	var namespaces []string
	for _, e := range entries {
		nodes = append(nodes, e.svc.ID)
		namespace := ns(e.svc)
		b := rules[namespace]
		if b == nil {
			b = &strings.Builder{}
			rules[namespace] = b
			namespaces = append(namespaces, namespace)
		}
		fmt.Fprintf(b, "    - host: %s.localhost\n      http:\n        paths:\n          - path: /\n            pathType: Prefix\n            backend:\n              service:\n                name: %s\n                port:\n                  number: %d\n", e.svc.Name, e.svc.Name, e.port)
		docs = append(docs, fmt.Sprintf("# Merge into Service/%s (remove its nodePort entries): only the Ingress is exposed.\napiVersion: v1\nkind: Service\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  type: ClusterIP\n", e.svc.Name, e.svc.Name, namespace))
	}
	for i := len(namespaces) - 1; i >= 0; i-- {
		namespace := namespaces[i]
		docs = slices.Insert(docs, 0, fmt.Sprintf("# One entry point routing by host (use your domains in production; *.localhost resolves to this machine).\napiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: gateway\n  namespace: %s\nspec:\n  rules:\n%s", namespace, rules[namespace].String()))
	}
	add(prioDesign, Recommendation{
		ID:      "k8s_single_entry_ingress",
		Pattern: "api-gateway",
		Title:   "Expose the services through one Ingress instead of a load balancer each",
		Why: "Each Service of type LoadBalancer or NodePort is a separate entry point: its own address and port, its own TLS and rate limiting to configure (or none), and clients coupled to the internal service layout. " +
			"One Ingress (Traefik on k3s) routes by host or path to ClusterIP Services and is the single place for TLS, rate limiting and authentication.",
		Pillars: []model.Pillar{model.PillarSecurity, model.PillarOperations, model.PillarCost},
		Nodes:   nodes,
		Add: []Addition{
			{Type: "Ingress", Purpose: "one host rule per service, routed by the cluster's ingress controller"},
			{Type: "Service", Purpose: "type ClusterIP: reachable only through the Ingress"},
		},
		Snippet: strings.Join(docs, "---\n"),
		Scope:   ScopeRecommended,
		Skip:    "Non-HTTP services (databases, brokers, raw TCP or UDP protocols) and a single public service.",
		Rules:   []string{"k8s_ingress_backend"},
		Docs:    "https://kubernetes.io/docs/concepts/services-networking/ingress/",
	})
}
