package advisor

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Google Cloud recommendations: Cloud Armor on external backend services
// (and an external Application Load Balancer with Cloud Armor in front of
// public Cloud Run services in production), Secret Manager references,
// Cloud SQL backups and private IP, customer-managed keys, autoscaling with
// bounds, alert policies and Pub/Sub dead-letter topics.

func gcpRecs(d *design) []rec {
	var out []rec
	gcpCloudArmor(d, &out)
	gcpCloudRunEdge(d, &out)
	gcpSecrets(d, &out)
	gcpDataProtection(d, &out)
	gcpPrivateSQL(d, &out)
	gcpPubSubDLQ(d, &out)
	gcpEncryption(d, &out)
	gcpAutoscaling(d, &out)
	gcpAlerts(d, &out)
	gcpSmells(d, &out)
	return out
}

// cloudArmorPolicy is a security policy with OWASP CRS preconfigured WAF
// rules and a per-IP throttle.
func cloudArmorPolicy(name string) string {
	return fmt.Sprintf(`resource "google_compute_security_policy" %q {
  name = %q

  rule {
    description = "OWASP CRS: SQL injection and cross-site scripting"
    action = "deny(403)"
    priority = 1000
    match {
      expr {
        expression = "evaluatePreconfiguredWaf('sqli-v33-stable') || evaluatePreconfiguredWaf('xss-v33-stable')"
      }
    }
  }

  rule {
    description = "At most 600 requests per minute per client IP"
    action = "throttle"
    priority = 2000
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action = "deny(429)"
      enforce_on_key = "IP"
      rate_limit_threshold {
        count = 600
        interval_sec = 60
      }
    }
  }

  rule {
    description = "Default rule"
    action = "allow"
    priority = 2147483647
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
  }
}`, name, dashed(name))
}

func gcpCloudArmor(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	policy := ""
	for _, bs := range d.ofType("google_compute_backend_service") {
		if s, _ := str(&bs.Body, "load_balancing_scheme"); s != "" && s != "EXTERNAL" && s != "EXTERNAL_MANAGED" {
			continue
		}
		d.public = true
		if set(&bs.Body, "security_policy") {
			continue
		}
		nodes = append(nodes, bs)
		if policy == "" {
			policy = d.unique("google_compute_security_policy", "edge")
			parts = append(parts, cloudArmorPolicy(policy))
		}
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  security_policy = google_compute_security_policy.%s.id\n}", addr(bs), bs.Type, bs.Name, policy))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "gcp_cloud_armor",
		Pattern: "gateway-offloading",
		Title:   "Protect the external backend services with a Cloud Armor policy",
		Why:     "An external load balancer forwards every internet request to its backends. Cloud Armor blocks common exploits with the OWASP Core Rule Set (preconfigured WAF rules) and throttles clients per IP at Google's edge.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_compute_security_policy", Purpose: "preconfigured WAF rules (SQLi, XSS) plus a per-IP throttle"},
			{Type: "google_compute_backend_service", Purpose: "security_policy"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    wafSkip,
		Rules:   []string{"bp_gcp_cloud_armor", "bp_gcp_armor_rules"},
		Docs:    "https://cloud.google.com/armor/docs/rate-limiting-overview",
	})
}

// gcpPublicRun are the Cloud Run services anyone can invoke (allUsers
// invoker) on their default ingress.
func gcpPublicRun(d *design) []*model.Node {
	var out []*model.Node
	for _, svc := range d.ofType("google_cloud_run_v2_service") {
		if ing, _ := str(&svc.Body, "ingress"); ing != "" && ing != "INGRESS_TRAFFIC_ALL" {
			continue
		}
		for _, m := range d.referrers(svc, "google_cloud_run_v2_service_iam_member", "google_cloud_run_v2_service_iam_binding") {
			member := expr(&m.Body, "member") + expr(&m.Body, "members")
			if strings.Contains(member, "allUsers") {
				out = append(out, svc)
				break
			}
		}
	}
	return out
}

func gcpCloudRunEdge(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, svc := range gcpPublicRun(d) {
		d.public = true
		if d.referredBy(svc, "google_compute_region_network_endpoint_group") {
			continue
		}
		nodes = append(nodes, svc)
		if len(nodes) > 1 {
			continue
		}
		neg := d.unique("google_compute_region_network_endpoint_group", svc.Name)
		bs := d.unique("google_compute_backend_service", svc.Name)
		policy := d.unique("google_compute_security_policy", "edge")
		um := d.unique("google_compute_url_map", "edge")
		cert := d.unique("google_compute_managed_ssl_certificate", "edge")
		proxy := d.unique("google_compute_target_https_proxy", "edge")
		fr := d.unique("google_compute_global_forwarding_rule", "edge")
		region := cmp.Or(expr(&svc.Body, "location"), "var.region")
		parts = append(parts, fmt.Sprintf(`resource "google_compute_region_network_endpoint_group" %q {
  name = %q
  region = %s
  network_endpoint_type = "SERVERLESS"
  cloud_run {
    service = %s.name
  }
}

resource "google_compute_backend_service" %q {
  name = %q
  load_balancing_scheme = "EXTERNAL_MANAGED"
  security_policy = google_compute_security_policy.%s.id
  backend {
    group = google_compute_region_network_endpoint_group.%s.id
  }
}

resource "google_compute_url_map" %q {
  name = %q
  default_service = google_compute_backend_service.%s.id
}

resource "google_compute_managed_ssl_certificate" %q {
  name = %q
  managed {
    domains = ["api.example.com"] # your domain
  }
}

resource "google_compute_target_https_proxy" %q {
  name = %q
  url_map = google_compute_url_map.%s.id
  ssl_certificates = [google_compute_managed_ssl_certificate.%s.id]
}

resource "google_compute_global_forwarding_rule" %q {
  name = %q
  load_balancing_scheme = "EXTERNAL_MANAGED"
  port_range = "443"
  target = google_compute_target_https_proxy.%s.id
}

# Merge into the existing %s: only the load balancer reaches it.
resource %q %q {
  ingress = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
}`, neg, dashed(neg), region, addr(svc), bs, dashed(bs), policy, neg, um, dashed(um), bs, cert, dashed(cert), proxy, dashed(proxy), um, cert, fr, dashed(fr), proxy,
			addr(svc), svc.Type, svc.Name))
		parts = append(parts, cloudArmorPolicy(policy))
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > 1 {
		parts = append(parts, "# Add a serverless NEG and backend service (or a URL map path) the same way for: "+strings.Join(addrs(nodes[1:]), ", "))
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "gcp_cloud_run_edge",
		Pattern: "gateway-offloading",
		Title:   "Serve the public Cloud Run services through an external Application Load Balancer with Cloud Armor",
		Why: "A Cloud Run service invocable by allUsers on its run.app URL has no WAF or per-client rate limit in front. An external Application Load Balancer with a serverless NEG adds Cloud Armor, your domain and a managed certificate; " +
			"setting ingress to internal-and-load-balancer closes the direct URL.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_compute_region_network_endpoint_group", Purpose: "serverless NEG pointing at the service"},
			{Type: "google_compute_backend_service", Purpose: "EXTERNAL_MANAGED backend with the Cloud Armor policy"},
			{Type: "google_compute_security_policy", Purpose: "preconfigured WAF rules plus a per-IP throttle"},
			{Type: "google_compute_url_map", Purpose: "routes to the backend"},
			{Type: "google_compute_managed_ssl_certificate", Purpose: "certificate for your domain"},
			{Type: "google_compute_target_https_proxy", Purpose: "HTTPS termination"},
			{Type: "google_compute_global_forwarding_rule", Purpose: "the public IP and port 443"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Prototypes and internal services: keep IAM-only invocation (no allUsers) instead.",
		Rules:   []string{"bp_gcp_cloud_run_public", "bp_gcp_cloud_armor"},
		Docs:    "https://cloud.google.com/load-balancing/docs/https/setup-global-ext-https-serverless",
	})
}

func gcpSecrets(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, svc := range d.ofType("google_cloud_run_v2_service") {
		tpl := path(&svc.Body, "template")
		if tpl == nil {
			continue
		}
		found := false
		for _, c := range tpl.Blocks {
			if c.Type != "containers" || found {
				continue
			}
			for _, e := range c.Body.Blocks {
				name, _ := str(&e.Body, "name")
				if e.Type != "env" || !secretKey.MatchString(name) {
					continue
				}
				if v, ok := str(&e.Body, "value"); !ok || v == "" {
					continue
				}
				found = true
				sec := d.unique("google_secret_manager_secret", label(svc, strings.ToLower(name)))
				parts = append(parts, fmt.Sprintf(`# Add the secret value as a version outside the configuration (gcloud secrets versions add).
resource "google_secret_manager_secret" %q {
  secret_id = %q
  replication {
    auto {}
  }
}

# Merge into the existing %s, replacing the literal value of %s:
resource %q %q {
  template {
    containers {
      env {
        name = %q
        value_source {
          secret_key_ref {
            secret = google_secret_manager_secret.%s.secret_id
            version = "latest"
          }
        }
      }
    }
  }
}`, sec, dashed(sec), addr(svc), name, svc.Type, svc.Name, name, sec))
				break
			}
		}
		if found {
			nodes = append(nodes, svc)
		}
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioSecrets, Recommendation{
		ID:      "gcp_secret_manager",
		Pattern: "external-configuration-store",
		Title:   "Read credentials from Secret Manager, not literal environment values",
		Why:     "A literal secret in the service definition is stored in Terraform state, the revision's configuration and anyone's console view. Secret Manager keeps it encrypted, versioned and audited; grant the service account roles/secretmanager.secretAccessor on that secret only.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_secret_manager_secret", Purpose: "holds each secret"},
			{Type: "google_cloud_run_v2_service", Purpose: "env value_source.secret_key_ref instead of the literal value"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Non-secret configuration (URLs, feature flags).",
		Docs:    "https://cloud.google.com/run/docs/configuring/services/secrets",
	})
}

func gcpDataProtection(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, db := range d.ofType("google_sql_database_instance") {
		bc := path(&db.Body, "settings", "backup_configuration")
		version, _ := str(&db.Body, "database_version")
		pitr := "point_in_time_recovery_enabled"
		if strings.HasPrefix(version, "MYSQL") {
			pitr = "binary_log_enabled"
		}
		if isTrue(bc, "enabled") && (strings.HasPrefix(version, "SQLSERVER") || isTrue(bc, pitr)) {
			continue
		}
		nodes = append(nodes, db)
		line := "\n      " + pitr + " = true"
		if strings.HasPrefix(version, "SQLSERVER") {
			line = ""
		}
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  settings {\n    backup_configuration {\n      enabled = true%s\n    }\n  }\n}", addr(db), db.Type, db.Name, line))
		add[db.Type] = Addition{Type: db.Type, Purpose: "automated backups with point-in-time recovery"}
	}
	for _, b := range d.ofType("google_storage_bucket") {
		if isTrue(path(&b.Body, "versioning"), "enabled") {
			continue
		}
		nodes = append(nodes, b)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  versioning {\n    enabled = true\n  }\n}", addr(b), b.Type, b.Name))
		add[b.Type] = Addition{Type: b.Type, Purpose: "object versioning (add a lifecycle rule to delete old versions)"}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioData, Recommendation{
		ID:      "gcp_data_protection",
		Title:   "Turn on backups, point-in-time recovery and object versioning",
		Why:     "A bad migration or deletion destroys data in seconds; automated backups with point-in-time recovery and object versioning make it recoverable.",
		Pillars: pillarsData,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Scratch data you can recreate from source.",
		Rules:   []string{"bp_gcp_sql_backups"},
		Docs:    "https://cloud.google.com/sql/docs/postgres/backup-recovery/backups",
	})
}

func gcpPrivateSQL(d *design, out *[]rec) {
	var nodes []*model.Node
	var patches []string
	for _, db := range d.ofType("google_sql_database_instance") {
		if isFalse(path(&db.Body, "settings", "ip_configuration"), "ipv4_enabled") {
			continue
		}
		nodes = append(nodes, db)
		patches = append(patches, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  settings {\n    ip_configuration {\n      ipv4_enabled = false\n      private_network = NETWORK\n      ssl_mode = \"ENCRYPTED_ONLY\"\n    }\n  }\n  depends_on = [google_service_networking_connection.CONN]\n}", addr(db), db.Type, db.Name))
	}
	if len(nodes) == 0 {
		return
	}
	network := "var.network_id"
	if nets := d.ofType("google_compute_network"); len(nets) > 0 {
		network = addr(nets[0]) + ".id"
	}
	addrName := d.unique("google_compute_global_address", "private_services")
	conn := d.unique("google_service_networking_connection", "private_services")
	for i := range patches {
		patches[i] = strings.NewReplacer("NETWORK", network, "CONN", conn).Replace(patches[i])
	}
	setup := fmt.Sprintf(`resource "google_compute_global_address" %q {
  name = %q
  purpose = "VPC_PEERING"
  address_type = "INTERNAL"
  prefix_length = 16
  network = %s
}

resource "google_service_networking_connection" %q {
  network = %s
  service = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.%s.name]
}`, addrName, dashed(addrName), network, conn, network, addrName)
	d.add(out, prioPrivate, Recommendation{
		ID:      "gcp_sql_private_ip",
		Title:   "Give Cloud SQL a private IP and turn off its public IP",
		Why:     "A public IP exposes the database to the internet, guarded only by authorized networks and passwords. With private services access it gets an address in your VPC that only your workloads reach.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_compute_global_address", Purpose: "range reserved for Google-managed services"},
			{Type: "google_service_networking_connection", Purpose: "private services access peering"},
			{Type: "google_sql_database_instance", Purpose: "ipv4_enabled = false, private_network, encrypted connections only"},
		},
		Snippet: joinLines(append([]string{setup}, patches...)...),
		Scope:   ScopeProduction,
		Skip:    "Development instances reached from a laptop: use the Cloud SQL Auth Proxy there.",
		Rules:   []string{"bp_gcp_sql_public_ip"},
		Docs:    "https://cloud.google.com/sql/docs/postgres/configure-private-ip",
	})
}

func gcpPubSubDLQ(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, sub := range d.ofType("google_pubsub_subscription") {
		if set(&sub.Body, "dead_letter_policy") {
			continue
		}
		nodes = append(nodes, sub)
		topic := d.unique("google_pubsub_topic", label(sub, "dead_letter"))
		parts = append(parts, fmt.Sprintf(`resource "google_pubsub_topic" %q {
  name = %q
}

# Merge into the existing %s:
resource %q %q {
  dead_letter_policy {
    dead_letter_topic = google_pubsub_topic.%s.id
    max_delivery_attempts = 5
  }
}`, topic, dashed(topic), addr(sub), sub.Type, sub.Name, topic))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioAsync, Recommendation{
		ID:      "gcp_pubsub_dead_letter",
		Pattern: "competing-consumers",
		Title:   "Give each subscription a dead-letter topic",
		Why:     "A message that always fails is redelivered until it expires, wasting consumer capacity, and then it is lost. A dead-letter topic keeps it after 5 attempts; the Pub/Sub service agent needs publisher on that topic and subscriber on the subscription.",
		Pillars: pillarsAsync,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "google_pubsub_topic", Purpose: "dead-letter topic (attach a subscription to inspect and replay)"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Subscriptions whose messages are safe to drop.",
		Docs:    "https://cloud.google.com/pubsub/docs/handling-failures",
	})
}

func gcpEncryption(d *design, out *[]rec) {
	var nodes []*model.Node
	var patches []string
	key := ""
	keyRef := func() string {
		if key == "" {
			key = d.unique("google_kms_crypto_key", "data")
		}
		return "google_kms_crypto_key." + key + ".id"
	}
	for _, n := range d.ofType("google_sql_database_instance", "google_storage_bucket", "google_pubsub_topic") {
		var patch string
		switch n.Type {
		case "google_sql_database_instance":
			if set(&n.Body, "encryption_key_name") {
				continue
			}
			patch = "  encryption_key_name = " + keyRef()
		case "google_storage_bucket":
			if set(path(&n.Body, "encryption"), "default_kms_key_name") {
				continue
			}
			patch = "  encryption {\n    default_kms_key_name = " + keyRef() + "\n  }"
		case "google_pubsub_topic":
			if set(&n.Body, "kms_key_name") || strings.HasSuffix(n.Name, "dead_letter") {
				continue
			}
			patch = "  kms_key_name = " + keyRef()
		}
		nodes = append(nodes, n)
		patches = append(patches, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n%s\n}", addr(n), n.Type, n.Name, patch))
	}
	if len(nodes) == 0 {
		return
	}
	ring := d.unique("google_kms_key_ring", "data")
	keys := fmt.Sprintf(`resource "google_kms_key_ring" %q {
  name = %q
  location = var.region # same location as the data
}

resource "google_kms_crypto_key" %q {
  name = %q
  key_ring = google_kms_key_ring.%s.id
  rotation_period = "7776000s" # 90 days
}`, ring, dashed(ring), key, dashed(key), ring)
	d.add(out, prioEncryption, Recommendation{
		ID:    "gcp_encryption_cmek",
		Title: "Encrypt data stores and topics with a customer-managed key (CMEK)",
		Why: "A customer-managed Cloud KMS key lets you control and audit decryption, rotate the key and revoke access in an incident. Each service's agent needs roles/cloudkms.cryptoKeyEncrypterDecrypter on the key; " +
			"Cloud SQL takes the key only at creation, so decide before production data lands.",
		Pillars: []model.Pillar{model.PillarSecurity},
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_kms_key_ring", Purpose: "key ring in the data's location"},
			{Type: "google_kms_crypto_key", Purpose: "key with 90-day rotation"},
		},
		Snippet: joinLines(append([]string{keys}, patches...)...),
		Scope:   ScopeProduction,
		Skip:    "Development data: Google-managed encryption at rest is enough there.",
		Rules:   []string{"bp_gcp_kms_rotation"},
		Docs:    "https://cloud.google.com/kms/docs/cmek",
	})
}

func gcpAutoscaling(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, mig := range d.ofType("google_compute_instance_group_manager", "google_compute_region_instance_group_manager") {
		scaler := "google_compute_autoscaler"
		loc := "zone = " + cmp.Or(expr(&mig.Body, "zone"), "var.zone")
		if mig.Type == "google_compute_region_instance_group_manager" {
			scaler = "google_compute_region_autoscaler"
			loc = "region = " + cmp.Or(expr(&mig.Body, "region"), "var.region")
		}
		if d.referredBy(mig, scaler) {
			continue
		}
		nodes = append(nodes, mig)
		name := d.unique(scaler, mig.Name)
		parts = append(parts, fmt.Sprintf(`resource %q %q {
  name = %q
  %s
  target = %s.id

  autoscaling_policy {
    min_replicas = 2
    max_replicas = 10
    cooldown_period = 60
    cpu_utilization {
      target = 0.6
    }
  }
}`, scaler, name, dashed(name), loc, addr(mig)))
		add[scaler] = Addition{Type: scaler, Purpose: "2 to 10 instances, target 60% CPU"}
	}
	for _, svc := range d.ofType("google_cloud_run_v2_service") {
		if set(path(&svc.Body, "template", "scaling"), "max_instance_count") {
			continue
		}
		nodes = append(nodes, svc)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  template {\n    scaling {\n      min_instance_count = 1 # 0 outside production\n      max_instance_count = 10\n    }\n  }\n}", addr(svc), svc.Type, svc.Name))
		add[svc.Type] = Addition{Type: svc.Type, Purpose: "template.scaling bounds: a warm instance against cold starts and a cap on cost and downstream load"}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioScaling, Recommendation{
		ID:      "gcp_autoscaling",
		Title:   "Scale the compute tiers automatically, within bounds",
		Why:     "Fixed-size instance groups are too small for a peak and paid for while idle; an unbounded Cloud Run service can scale into thousands of instances and overwhelm the database behind it. Autoscalers with min and max bounds handle both.",
		Pillars: pillarsScaling,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Fixed-size batch workers and development environments.",
		Rules:   []string{"bp_gcp_mig_autoscaling", "bp_gcp_cloud_run_max_instances"},
		Docs:    "https://cloud.google.com/compute/docs/autoscaler",
	})
}

func gcpAlerts(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	channel := ""
	for _, svc := range d.ofType("google_cloud_run_v2_service") {
		if d.referredBy(svc, "google_monitoring_alert_policy") {
			continue
		}
		nodes = append(nodes, svc)
		if len(nodes) > 3 {
			continue
		}
		if channel == "" {
			channel = d.unique("google_monitoring_notification_channel", "oncall")
			parts = append(parts, fmt.Sprintf("resource \"google_monitoring_notification_channel\" %q {\n  display_name = \"On-call\"\n  type = \"email\"\n  labels = {\n    email_address = \"oncall@example.com\"\n  }\n}", channel))
		}
		filter := fmt.Sprintf(`resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${%s.name}\"`, addr(svc))
		policy := func(suffix, title, metric, aligner, reducer, threshold string) string {
			n := d.unique("google_monitoring_alert_policy", label(svc, suffix))
			return fmt.Sprintf(`resource "google_monitoring_alert_policy" %q {
  display_name = "%s: %s"
  combiner = "OR"
  notification_channels = [google_monitoring_notification_channel.%s.id]

  conditions {
    display_name = %q
    condition_threshold {
      filter = "%s AND %s"
      comparison = "COMPARISON_GT"
      threshold_value = %s
      duration = "300s"
      aggregations {
        alignment_period = "60s"
        per_series_aligner = %q
        cross_series_reducer = %q
      }
    }
  }
}`, n, svc.Name, title, channel, title, filter, metric, threshold, aligner, reducer)
		}
		parts = append(parts,
			policy("5xx", "5xx responses", `metric.type = \"run.googleapis.com/request_count\" AND metric.labels.response_code_class = \"5xx\"`, "ALIGN_SUM", "REDUCE_SUM", "5"),
			policy("p95", "p95 latency (ms)", `metric.type = \"run.googleapis.com/request_latencies\"`, "ALIGN_PERCENTILE_95", "REDUCE_MAX", "1000"))
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > 3 {
		parts = append(parts, "# Same pattern for: "+strings.Join(addrs(nodes[3:]), ", "))
	}
	d.add(out, prioAlarms, Recommendation{
		ID:      "gcp_alerts",
		Title:   "Alert on 5xx responses and p95 latency of the Cloud Run services",
		Why:     "Users notice errors and slow responses first; alert policies on server errors and the 95th percentile latency notify you before they do.",
		Pillars: pillarsAlarms,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "google_monitoring_alert_policy", Purpose: "5xx over 5 per minute and p95 latency above 1 s, for 5 minutes"},
			{Type: "google_monitoring_notification_channel", Purpose: "who is notified"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development services nobody is on call for.",
		Docs:    "https://cloud.google.com/monitoring/alerts/policies-in-api",
	})
}
