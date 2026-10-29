package advisor

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Shared-database smells of Terraform designs, for every cloud: three or
// more services on one relational database (database-per-service) and, in
// production, two or more without a cache in front (cache-aside). A
// service is a deployable compute unit; functions deployed together (same
// role or API) count once, so an n-tier app or a function-per-route API
// never triggers them.

// tfService is one deployable service: its compute nodes and the resources
// they are made of (an ECS service's task definitions).
type tfService struct {
	nodes []*model.Node
	parts []*model.Node
}

func (s tfService) name() string { return s.nodes[0].Name }

// simpleServices are one service per compute resource.
func simpleServices(d *design, types ...string) []tfService {
	var out []tfService
	for _, n := range d.ofType(types...) {
		out = append(out, tfService{nodes: []*model.Node{n}, parts: []*model.Node{n}})
	}
	return out
}

// sqlShape describes a cloud's relational databases and how to give a
// service its own database or put a cache in front of one.
type sqlShape struct {
	cloud   string   // recommendation ID prefix
	servers []string // database servers, instances and clusters
	logical []string // databases on a server
	members []string // nodes that stand for their server (cluster instances)
	caches  []string // any of them in the design turns cache-aside off
	// own returns the HCL that gives svc its own database next to the
	// shared one.
	own      func(d *design, shared *sqlUse, svc tfService) string
	ownAdd   []Addition
	dbDocs   string
	cache    func(d *design, shared *sqlUse) string
	cacheAdd []Addition
	cacheDoc string
	cacheRul []string
}

// sqlUse is a database shared by services: a logical database, or a
// server whose databases the design does not name.
type sqlUse struct {
	server   *model.Node
	db       *model.Node
	services []tfService
}

func (u *sqlUse) addr() string {
	if u.db != nil {
		return addr(u.db)
	}
	return addr(u.server)
}

// sharedSQL maps the databases of shape to the services that refer to
// them, in design order. A service naming only a server with several
// databases is ambiguous and not counted.
func sharedSQL(d *design, shape sqlShape, services []tfService) []*sqlUse {
	var out []*sqlUse
	byKey := map[string]*sqlUse{}
	for _, s := range services {
		seen := map[string]bool{}
		use := func(server, db *model.Node) {
			key := server.ID
			if db != nil {
				key = db.ID
			}
			if seen[key] {
				return
			}
			seen[key] = true
			u := byKey[key]
			if u == nil {
				u = &sqlUse{server: server, db: db}
				byKey[key] = u
				out = append(out, u)
			}
			u.services = append(u.services, s)
		}
		for _, p := range s.parts {
			for _, l := range typed(d.referenced, p, shape.logical) {
				if srv := d.referenced(l, shape.servers...); len(srv) > 0 {
					use(srv[0], l)
				}
			}
			servers := d.referenced(p, shape.servers...)
			for _, m := range typed(d.referenced, p, shape.members) {
				servers = append(servers, d.referenced(m, shape.servers...)...)
			}
			for _, srv := range servers {
				switch dbs := typed(d.referrers, srv, shape.logical); len(dbs) {
				case 0:
					use(srv, nil)
				case 1:
					use(srv, dbs[0])
				}
			}
		}
	}
	return out
}

// typed applies a design lookup (referenced, referrers) restricted to
// types; no types matches nothing, where the lookups would match anything.
func typed(lookup func(*model.Node, ...string) []*model.Node, n *model.Node, types []string) []*model.Node {
	if len(types) == 0 {
		return nil
	}
	return lookup(n, types...)
}

func serviceNames(services []tfService) string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = addr(s.nodes[0])
	}
	return strings.Join(names, ", ")
}

// appendNew appends the non-nil nodes that nodes lacks.
func appendNew(nodes []*model.Node, more ...*model.Node) []*model.Node {
	for _, n := range more {
		if n != nil && !slices.Contains(nodes, n) {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// sqlSmells recommends a database per service for databases shared by
// three or more services, and a cache for databases read by two or more
// (production).
func sqlSmells(d *design, out *[]rec, shape sqlShape, services []tfService) {
	uses := sharedSQL(d, shape, services)
	var nodes []*model.Node
	var parts []string
	for _, u := range uses {
		if len(u.services) < 3 {
			continue
		}
		nodes = appendNew(nodes, u.server, u.db)
		for _, s := range u.services {
			nodes = appendNew(nodes, s.nodes...)
		}
		parts = append(parts, fmt.Sprintf("# %s is shared by %s. %s keeps it; each other service gets its own database and login, and reads the others' data through their APIs or events.",
			u.addr(), serviceNames(u.services), addr(u.services[0].nodes[0])))
		for _, s := range u.services[1:] {
			parts = append(parts, shape.own(d, u, s))
		}
	}
	if len(nodes) > 0 {
		d.add(out, prioDesign, Recommendation{
			ID:      shape.cloud + "_database_per_service",
			Pattern: "database-per-service",
			Title:   "Give each service its own database",
			Why: "Three or more services read and write the same database: a schema change for one breaks the others, one service's heavy queries slow them all, and none can be deployed, scaled or restored on its own, a distributed monolith at the data layer. " +
				"With a database per service (at least a database and login of its own on a shared server) each service owns its data and shares it through its API or events.",
			Pillars: []model.Pillar{model.PillarReliability, model.PillarOperations, model.PillarPerformance},
			Nodes:   addrs(nodes),
			Add:     shape.ownAdd,
			Snippet: joinLines(parts...),
			Scope:   ScopeRecommended,
			Skip:    "One application split into tiers or modules that are deployed together (n-tier, modular monolith): keep one database there, with a schema per module. Also reporting jobs that only read a replica.",
			Docs:    shape.dbDocs,
		})
	}
	if d.has(shape.caches...) {
		return
	}
	nodes, parts = nil, nil
	for _, u := range uses {
		if len(u.services) < 2 {
			continue
		}
		nodes = appendNew(nodes, u.server)
		parts = append(parts, fmt.Sprintf("# %s read %s: they read the cache first, load from the database on a miss and store the result with a TTL, and delete the key when they write it.\n%s",
			serviceNames(u.services), u.addr(), shape.cache(d, u)))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      shape.cloud + "_cache_aside",
		Pattern: "cache-aside",
		Title:   "Cache the hot reads of the shared database (cache-aside)",
		Why: "Several services read the same database on every request: each read is a round trip, and the database, which scales up rather than out, carries the whole read load. " +
			"With cache-aside each service reads a cache first, loads from the database on a miss and stores the result with a TTL, and invalidates the key when it writes; repeated reads stop reaching the database.",
		Pillars: []model.Pillar{model.PillarPerformance, model.PillarCost, model.PillarReliability},
		Nodes:   addrs(nodes),
		Add:     shape.cacheAdd,
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Write-heavy data, reads that must always be current (balances, stock counts), and databases whose working set already fits their memory with low latency.",
		Rules:   shape.cacheRul,
		Docs:    shape.cacheDoc,
	})
}

// dbIdent is a database or login name for a service ("orders_api").
func dbIdent(name string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, name))
}

// copyExpr is the source of attribute name of n, or fallback.
func copyExpr(n *model.Node, name, fallback string) string {
	return cmp.Or(expr(&n.Body, name), fallback)
}

// optional is "  name = <source of n's attribute>\n", or "" when unset.
func optional(n *model.Node, name string) string {
	if e := expr(&n.Body, name); e != "" {
		return fmt.Sprintf("  %s = %s\n", name, e)
	}
	return ""
}

// ---------------------------------------------------------------------------
// AWS

var awsSQL = sqlShape{
	cloud:   "aws",
	servers: []string{"aws_db_instance", "aws_rds_cluster"},
	members: []string{"aws_rds_cluster_instance"},
	caches:  []string{"aws_elasticache_replication_group", "aws_elasticache_cluster", "aws_elasticache_serverless_cache", "aws_memorydb_cluster", "aws_dax_cluster"},
	own: func(d *design, u *sqlUse, s tfService) string {
		srv := u.server
		name := d.unique(srv.Type, label(s.nodes[0], "db"))
		if srv.Type == "aws_rds_cluster" {
			class := `"db.t4g.medium"`
			if inst := d.referrers(srv, "aws_rds_cluster_instance"); len(inst) > 0 {
				class = copyExpr(inst[0], "instance_class", class)
			}
			return fmt.Sprintf(`resource "aws_rds_cluster" %q {
  cluster_identifier = %q
  engine = %s
%s  master_username = %s
  manage_master_user_password = true
  database_name = %q
  storage_encrypted = true
  backup_retention_period = 7
%s%s}

resource "aws_rds_cluster_instance" %q {
  identifier = %q
  cluster_identifier = aws_rds_cluster.%s.id
  instance_class = %s
  engine = aws_rds_cluster.%s.engine
}`, name, dashed(name), copyExpr(srv, "engine", `"aurora-postgresql"`), optional(srv, "engine_version"), copyExpr(srv, "master_username", `"app"`), dbIdent(s.name()),
				optional(srv, "db_subnet_group_name"), optional(srv, "vpc_security_group_ids"), d.unique("aws_rds_cluster_instance", name), dashed(name)+"-1", name, class, name)
		}
		engine := copyExpr(srv, "engine", `"postgres"`)
		dbName := fmt.Sprintf("  db_name = %q\n", dbIdent(s.name()))
		if strings.Contains(engine, "sqlserver") {
			dbName = ""
		}
		return fmt.Sprintf(`resource "aws_db_instance" %q {
  identifier = %q
  engine = %s
%s  instance_class = %s
  allocated_storage = %s
%s  username = %s
  manage_master_user_password = true
  storage_encrypted = true
  backup_retention_period = 7
%s%s}`, name, dashed(name), engine, optional(srv, "engine_version"), copyExpr(srv, "instance_class", `"db.t4g.micro"`), copyExpr(srv, "allocated_storage", "20"), dbName,
			copyExpr(srv, "username", `"app"`), optional(srv, "db_subnet_group_name"), optional(srv, "vpc_security_group_ids"))
	},
	ownAdd: []Addition{{Type: "aws_db_instance", Purpose: "a database instance (or Aurora cluster) per service, with an RDS-managed password; or one database and login per service on the shared instance, created by each service's migrations"}},
	dbDocs: "https://docs.aws.amazon.com/prescriptive-guidance/latest/modernization-data-persistence/database-per-service.html",
	cache: func(d *design, u *sqlUse) string {
		name := d.unique("aws_elasticache_replication_group", label(u.server, "cache"))
		return fmt.Sprintf(`resource "aws_elasticache_replication_group" %q {
  replication_group_id = %q
  description = "Cache-aside for reads of %s"
  engine = "valkey"
  node_type = "cache.t4g.small"
  num_cache_clusters = 2
  automatic_failover_enabled = true
  multi_az_enabled = true
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  # Place it in the database's private subnets (subnet_group_name) with a security group the services may reach.
}`, name, dashed(name), u.addr())
	},
	cacheAdd: []Addition{{Type: "aws_elasticache_replication_group", Purpose: "Valkey primary and replica across two zones with automatic failover and encryption"}},
	cacheDoc: "https://docs.aws.amazon.com/whitepapers/latest/database-caching-strategies-using-redis/caching-patterns.html",
	cacheRul: []string{"bp_elasticache_failover"},
}

// ---------------------------------------------------------------------------
// Azure

func azureSmells(d *design, out *[]rec) {
	sqlSmells(d, out, azureSQL, simpleServices(d, slices.Concat(azureWebApps, []string{"azurerm_container_app"})...))
}

var azureSQL = sqlShape{
	cloud:   "azure",
	servers: []string{"azurerm_postgresql_flexible_server", "azurerm_mysql_flexible_server", "azurerm_mssql_server"},
	logical: []string{"azurerm_postgresql_flexible_server_database", "azurerm_mysql_flexible_database", "azurerm_mssql_database"},
	caches:  []string{"azurerm_managed_redis", "azurerm_redis_cache", "azurerm_redis_enterprise_cluster"},
	own: func(d *design, u *sqlUse, s tfService) string {
		srv := u.server
		db := dbIdent(s.name())
		switch srv.Type {
		case "azurerm_mysql_flexible_server":
			rg, _ := azureRG(srv)
			return fmt.Sprintf("resource \"azurerm_mysql_flexible_database\" %q {\n  name = %q\n  resource_group_name = %s\n  server_name = %s.name\n  charset = \"utf8mb4\"\n  collation = \"utf8mb4_unicode_ci\"\n}",
				d.unique("azurerm_mysql_flexible_database", db), db, rg, addr(srv))
		case "azurerm_mssql_server":
			return fmt.Sprintf("resource \"azurerm_mssql_database\" %q {\n  name = %q\n  server_id = %s.id\n}", d.unique("azurerm_mssql_database", db), db, addr(srv))
		}
		return fmt.Sprintf("resource \"azurerm_postgresql_flexible_server_database\" %q {\n  name = %q\n  server_id = %s.id\n  charset = \"UTF8\"\n  collation = \"en_US.utf8\"\n}",
			d.unique("azurerm_postgresql_flexible_server_database", db), db, addr(srv))
	},
	ownAdd: []Addition{{Type: "azurerm_postgresql_flexible_server_database", Purpose: "a database per service on the server (azurerm_mysql_flexible_database, azurerm_mssql_database for MySQL and SQL Server), each reached with the service's managed identity as its own Microsoft Entra login"}},
	dbDocs: "https://learn.microsoft.com/en-us/azure/architecture/microservices/design/data-considerations",
	cache: func(d *design, u *sqlUse) string {
		rg, location := azureRG(u.server)
		name := d.unique("azurerm_managed_redis", label(u.server, "cache"))
		return fmt.Sprintf("resource \"azurerm_managed_redis\" %q {\n  name = %q\n  resource_group_name = %s\n  location = %s\n  sku_name = \"Balanced_B0\"\n  high_availability_enabled = true\n}",
			name, dashed(name), rg, location)
	},
	cacheAdd: []Addition{{Type: "azurerm_managed_redis", Purpose: "Azure Managed Redis with high availability; the services authenticate with Microsoft Entra ID"}},
	cacheDoc: "https://learn.microsoft.com/en-us/azure/architecture/patterns/cache-aside",
}

// ---------------------------------------------------------------------------
// Google Cloud

func gcpSmells(d *design, out *[]rec) {
	sqlSmells(d, out, gcpSQL, simpleServices(d, "google_cloud_run_v2_service", "google_cloud_run_service", "google_cloudfunctions2_function"))
}

var gcpSQL = sqlShape{
	cloud:   "gcp",
	servers: []string{"google_sql_database_instance"},
	logical: []string{"google_sql_database"},
	caches:  []string{"google_redis_instance", "google_redis_cluster", "google_memcache_instance"},
	own: func(d *design, u *sqlUse, s tfService) string {
		db := dbIdent(s.name())
		pw := d.unique("variable", db+"_db_password")
		return fmt.Sprintf(`resource "google_sql_database" %q {
  name = %q
  instance = %s.name
}

variable %q {
  type = string
  sensitive = true
  ephemeral = true
}

resource "google_sql_user" %q {
  name = %q
  instance = %s.name
  # Write-only: the password is never stored in state.
  password_wo = var.%s
  password_wo_version = 1
}`, d.unique("google_sql_database", db), db, addr(u.server), pw, d.unique("google_sql_user", db), db, addr(u.server), pw)
	},
	ownAdd: []Addition{
		{Type: "google_sql_database", Purpose: "a database per service on the instance"},
		{Type: "google_sql_user", Purpose: "a login per service, granted only its database"},
	},
	dbDocs: "https://microservices.io/patterns/data/database-per-service.html",
	cache: func(d *design, u *sqlUse) string {
		name := d.unique("google_redis_instance", label(u.server, "cache"))
		network := ""
		if e := expr(path(&u.server.Body, "settings", "ip_configuration"), "private_network"); e != "" {
			network = fmt.Sprintf("  authorized_network = %s\n  connect_mode = \"PRIVATE_SERVICE_ACCESS\"\n", e)
		}
		return fmt.Sprintf("resource \"google_redis_instance\" %q {\n  name = %q\n  tier = \"STANDARD_HA\"\n  memory_size_gb = 1\n%s%s  auth_enabled = true\n  transit_encryption_mode = \"SERVER_AUTHENTICATION\"\n}",
			name, dashed(name), optional(u.server, "region"), network)
	},
	cacheAdd: []Addition{{Type: "google_redis_instance", Purpose: "Memorystore for Redis, Standard tier (replica with automatic failover), AUTH and in-transit encryption"}},
	cacheDoc: "https://docs.cloud.google.com/memorystore/docs/redis/redis-overview",
}
