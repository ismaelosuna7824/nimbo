package advisor

import (
	"cmp"
	"fmt"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// OCI recommendations: WAF with rate limiting on public load balancers, API
// Gateway rate limiting, Vault-generated database passwords, bucket
// versioning, queue dead-lettering, customer-managed keys, instance pool
// autoscaling and alarms.

func ociRecs(d *design) []rec {
	var out []rec
	ociWAF(d, &out)
	ociAPIGateway(d, &out)
	ociSecrets(d, &out)
	ociBucketVersioning(d, &out)
	ociQueueDLQ(d, &out)
	ociEncryption(d, &out)
	ociAutoscaling(d, &out)
	ociAlarms(d, &out)
	return out
}

func ociCompartment(n *model.Node) string {
	return cmp.Or(expr(&n.Body, "compartment_id"), "var.compartment_id")
}

func ociWAF(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, lb := range d.ofType("oci_load_balancer_load_balancer", "oci_load_balancer") {
		if isTrue(&lb.Body, "is_private") {
			continue
		}
		d.public = true
		if d.referredBy(lb, "oci_waf_web_app_firewall") {
			continue
		}
		nodes = append(nodes, lb)
		pol := d.unique("oci_waf_web_app_firewall_policy", label(lb, "waf"))
		fw := d.unique("oci_waf_web_app_firewall", label(lb, "waf"))
		comp := ociCompartment(lb)
		parts = append(parts, fmt.Sprintf(`resource "oci_waf_web_app_firewall_policy" %q {
  compartment_id = %s
  display_name = %q

  actions {
    name = "allow"
    type = "ALLOW"
  }

  actions {
    name = "too-many-requests"
    type = "RETURN_HTTP_RESPONSE"
    code = 429
  }

  request_rate_limiting {
    rules {
      name = "per-client-ip"
      type = "REQUEST_RATE_LIMITING"
      action_name = "too-many-requests"
      configurations {
        period_in_seconds = 60
        requests_limit = 600
        action_duration_in_seconds = 60
      }
    }
  }
}

resource "oci_waf_web_app_firewall" %q {
  compartment_id = %s
  display_name = %q
  backend_type = "LOAD_BALANCER"
  load_balancer_id = %s.id
  web_app_firewall_policy_id = oci_waf_web_app_firewall_policy.%s.id
}`, pol, comp, dashed(pol), fw, comp, dashed(fw), addr(lb), pol))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioEdge, Recommendation{
		ID:    "oci_lb_waf",
		Title: "Attach an OCI WAF policy with rate limiting to the public load balancers",
		Why: "A public load balancer forwards every internet request to its backends. An OCI WAF policy rate-limits each client IP (429 above the limit); add request_protection rules with the OCI protection capabilities " +
			"(OWASP-based) once you have tuned them in detection mode.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "oci_waf_web_app_firewall_policy", Purpose: "per-client-IP request rate limiting (600 requests per minute)"},
			{Type: "oci_waf_web_app_firewall", Purpose: "attaches the policy to the load balancer"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    wafSkip,
		Rules:   []string{"bp_oci_lb_waf", "bp_oci_waf_rate_limit"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/WAF/RateLimiting/create_rate_limiting_rule.htm",
	})
}

func ociAPIGateway(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, gw := range d.ofType("oci_apigateway_gateway") {
		if t, _ := str(&gw.Body, "endpoint_type"); t == "PUBLIC" {
			d.public = true
		}
	}
	for _, dep := range d.ofType("oci_apigateway_deployment") {
		private := false
		for _, gw := range d.referenced(dep, "oci_apigateway_gateway") {
			t, _ := str(&gw.Body, "endpoint_type")
			private = t == "PRIVATE"
		}
		if private || set(path(&dep.Body, "specification", "request_policies"), "rate_limiting") {
			continue
		}
		nodes = append(nodes, dep)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  specification {\n    request_policies {\n      rate_limiting {\n        rate_in_requests_per_second = 50\n        rate_key = \"CLIENT_IP\"\n      }\n    }\n  }\n}", addr(dep), dep.Type, dep.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioThrottle, Recommendation{
		ID:      "oci_apigw_rate_limit",
		Title:   "Rate-limit the API Gateway deployments per client",
		Why:     "Without a rate-limiting policy one client can consume the gateway's and the backends' capacity (OWASP API4:2023). The deployment answers 429 above the limit before the backend sees the request.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "oci_apigateway_deployment", Purpose: "specification.request_policies.rate_limiting keyed on the client IP"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Private gateways reached only from your VCN.",
		Rules:   []string{"bp_oci_apigw_rate_limit"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/APIGateway/Tasks/apigatewaylimitingbackendaccess.htm",
	})
}

func ociSecrets(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	vault, key := "var.vault_id", "var.kms_key_id"
	if v := d.ofType("oci_kms_vault"); len(v) > 0 {
		vault = addr(v[0]) + ".id"
	}
	if k := d.ofType("oci_kms_key"); len(k) > 0 {
		key = addr(k[0]) + ".id"
	}
	for _, db := range d.ofType("oci_database_autonomous_database") {
		if _, literal := str(&db.Body, "admin_password"); !literal || set(&db.Body, "secret_id") {
			continue
		}
		nodes = append(nodes, db)
		sec := d.unique("oci_vault_secret", label(db, "admin"))
		parts = append(parts, fmt.Sprintf(`resource "oci_vault_secret" %q {
  compartment_id = %s
  vault_id = %s
  key_id = %s
  secret_name = %q
  enable_auto_generation = true
  secret_generation_context {
    generation_type = "PASSPHRASE"
    generation_template = "DBAAS_DEFAULT_PASSWORD"
  }
}

# Merge into the existing %s and remove admin_password:
resource %q %q {
  secret_id = oci_vault_secret.%s.id
}`, sec, ociCompartment(db), vault, key, dashed(sec), addr(db), db.Type, db.Name, sec))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioSecrets, Recommendation{
		ID:      "oci_vault_secrets",
		Title:   "Let OCI Vault generate and hold the database admin passwords",
		Why:     "A password written in Terraform ends up in state and plan output. A Vault secret generated by OCI never appears in the configuration; the database reads it by secret_id and you rotate it in the Vault.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "oci_vault_secret", Purpose: "auto-generated admin password"},
			{Type: "oci_database_autonomous_database", Purpose: "secret_id instead of admin_password"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Rules:   []string{"bp_oci_secret_rotation"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/KeyManagement/Concepts/keyoverview.htm",
	})
}

func ociBucketVersioning(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, b := range d.ofType("oci_objectstorage_bucket") {
		if v, _ := str(&b.Body, "versioning"); v == "Enabled" {
			continue
		}
		nodes = append(nodes, b)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  versioning = \"Enabled\"\n}", addr(b), b.Type, b.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioData, Recommendation{
		ID:      "oci_bucket_versioning",
		Title:   "Turn on object versioning for the buckets",
		Why:     "An overwritten or deleted object is gone for good by default; versioning keeps previous versions recoverable (add a lifecycle rule to expire them).",
		Pillars: pillarsData,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "oci_objectstorage_bucket", Purpose: "versioning = \"Enabled\""}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Buckets that only hold reproducible data.",
		Rules:   []string{"bp_oci_bucket_versioning"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/Object/Tasks/usingversioning.htm",
	})
}

func ociQueueDLQ(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, q := range d.ofType("oci_queue_queue") {
		if n, ok := number(&q.Body, "dead_letter_queue_delivery_count"); ok && n > 0 || !ok && set(&q.Body, "dead_letter_queue_delivery_count") {
			continue
		}
		nodes = append(nodes, q)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  dead_letter_queue_delivery_count = 5\n}", addr(q), q.Type, q.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioAsync, Recommendation{
		ID:      "oci_queue_dead_letter",
		Title:   "Move failing messages to the queue's dead-letter queue",
		Why:     "Without a delivery count a message that always fails is retried until it expires and is lost. With dead_letter_queue_delivery_count it moves to the dead-letter queue for inspection and replay.",
		Pillars: pillarsAsync,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "oci_queue_queue", Purpose: "dead_letter_queue_delivery_count = 5"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Messages that are safe to drop.",
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/queue/overview.htm",
	})
}

func ociEncryption(d *design, out *[]rec) {
	var nodes []*model.Node
	var patches []string
	key := ""
	keyRef := func() string {
		if key == "" {
			key = d.unique("oci_kms_key", "data")
		}
		return "oci_kms_key." + key + ".id"
	}
	var comp string
	for _, n := range d.ofType("oci_objectstorage_bucket", "oci_queue_queue") {
		attrName := "kms_key_id"
		if n.Type == "oci_queue_queue" {
			attrName = "custom_encryption_key_id"
		}
		if set(&n.Body, attrName) {
			continue
		}
		nodes = append(nodes, n)
		if comp == "" {
			comp = ociCompartment(n)
		}
		patches = append(patches, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  %s = %s\n}", addr(n), n.Type, n.Name, attrName, keyRef()))
	}
	if len(nodes) == 0 {
		return
	}
	vault := d.unique("oci_kms_vault", "data")
	keys := fmt.Sprintf(`resource "oci_kms_vault" %q {
  compartment_id = %s
  display_name = %q
  vault_type = "DEFAULT"
}

resource "oci_kms_key" %q {
  compartment_id = %s
  display_name = %q
  management_endpoint = oci_kms_vault.%s.management_endpoint
  is_auto_rotation_enabled = true
  key_shape {
    algorithm = "AES"
    length = 32
  }
  auto_key_rotation_details {
    rotation_interval_in_days = 90
  }
}`, vault, comp, dashed(vault), key, comp, dashed(key), vault)
	d.add(out, prioEncryption, Recommendation{
		ID:    "oci_encryption_cmk",
		Title: "Encrypt buckets and queues with a customer-managed Vault key",
		Why: "A customer-managed key lets you control and audit decryption, rotate the key and revoke access in an incident. The Object Storage and Queue services need a policy allowing them to use the key " +
			"(allow service objectstorage-<region> to use keys …).",
		Pillars: []model.Pillar{model.PillarSecurity},
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "oci_kms_vault", Purpose: "vault holding the key"},
			{Type: "oci_kms_key", Purpose: "AES-256 key with 90-day automatic rotation"},
		},
		Snippet: joinLines(append([]string{keys}, patches...)...),
		Scope:   ScopeProduction,
		Skip:    "Development data: Oracle-managed encryption at rest is enough there.",
		Rules:   []string{"bp_oci_customer_managed_key", "bp_oci_key_rotation"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/KeyManagement/Concepts/keyoverview.htm",
	})
}

func ociAutoscaling(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, pool := range d.ofType("oci_core_instance_pool") {
		if d.referredBy(pool, "oci_autoscaling_auto_scaling_configuration") {
			continue
		}
		nodes = append(nodes, pool)
		name := d.unique("oci_autoscaling_auto_scaling_configuration", pool.Name)
		rule := func(display, op string, threshold, change int) string {
			return fmt.Sprintf(`    rules {
      display_name = %q
      action {
        type = "CHANGE_COUNT_BY"
        value = %d
      }
      metric {
        metric_type = "CPU_UTILIZATION"
        threshold {
          operator = %q
          value = %d
        }
      }
    }`, display, change, op, threshold)
		}
		parts = append(parts, fmt.Sprintf(`resource "oci_autoscaling_auto_scaling_configuration" %q {
  compartment_id = %s
  display_name = %q
  cool_down_in_seconds = 300

  auto_scaling_resources {
    id = %s.id
    type = "instancePool"
  }

  policies {
    display_name = "cpu"
    policy_type = "threshold"
    capacity {
      initial = 2
      min = 2
      max = 10
    }
%s
%s
  }
}`, name, ociCompartment(pool), dashed(name), addr(pool), rule("scale-out", "GT", 70, 1), rule("scale-in", "LT", 30, -1)))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioScaling, Recommendation{
		ID:      "oci_autoscaling",
		Title:   "Scale the instance pools automatically, within bounds",
		Why:     "A fixed pool size is too small for a peak and paid for while idle. A threshold autoscaling policy adds instances above 70% CPU and removes them below 30%, between a minimum of 2 and a maximum that caps cost.",
		Pillars: pillarsScaling,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "oci_autoscaling_auto_scaling_configuration", Purpose: "2 to 10 instances on CPU thresholds"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Fixed-size batch pools and development environments.",
		Rules:   []string{"bp_oci_instance_pool_autoscaling"},
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/Compute/Tasks/autoscalinginstancepools.htm",
	})
}

func ociAlarms(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	topic := ""
	for _, lb := range d.ofType("oci_load_balancer_load_balancer", "oci_load_balancer") {
		if d.referredBy(lb, "oci_monitoring_alarm") {
			continue
		}
		nodes = append(nodes, lb)
		comp := ociCompartment(lb)
		if topic == "" {
			topic = d.unique("oci_ons_notification_topic", "alerts")
			parts = append(parts, fmt.Sprintf("resource \"oci_ons_notification_topic\" %q {\n  compartment_id = %s\n  name = %q\n}", topic, comp, dashed(topic)))
		}
		alarm := d.unique("oci_monitoring_alarm", label(lb, "5xx"))
		parts = append(parts, fmt.Sprintf(`resource "oci_monitoring_alarm" %q {
  compartment_id = %s
  metric_compartment_id = %s
  display_name = %q
  namespace = "oci_lbaas"
  query = "httpResponses5xx[1m]{resourceId = \"${%s.id}\", lbComponent = \"backendSet\"}.sum() > 5"
  severity = "CRITICAL"
  pending_duration = "PT5M"
  is_enabled = true
  destinations = [oci_ons_notification_topic.%s.id]
}`, alarm, comp, comp, dashed(alarm), addr(lb), topic))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioAlarms, Recommendation{
		ID:      "oci_alarms",
		Title:   "Alarm on 5xx responses of the load balancers",
		Why:     "Users notice errors first; an alarm on the load balancer's 5xx responses notifies you before they do. Add latency alarms on your backends' application metrics (the load balancer's metrics have no percentiles).",
		Pillars: pillarsAlarms,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "oci_monitoring_alarm", Purpose: "more than 5 HTTP 5xx responses per minute for 5 minutes"},
			{Type: "oci_ons_notification_topic", Purpose: "who is notified (add subscriptions)"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development load balancers nobody is on call for.",
		Docs:    "https://docs.oracle.com/en-us/iaas/Content/Balance/Reference/loadbalancermetrics.htm",
	})
}
