# The one public entry point (flexible shape).
resource "oci_load_balancer_load_balancer" "main" {
  compartment_id = var.compartment_ocid
  display_name   = local.name_prefix
  shape          = "flexible"
  subnet_ids     = [oci_core_subnet.tier["public"].id]
  is_private     = false
  freeform_tags  = local.common_tags

  shape_details {
    minimum_bandwidth_in_mbps = var.lb_bandwidth_mbps
    maximum_bandwidth_in_mbps = var.lb_bandwidth_mbps
  }
}

# One backend set per internal server; each only gets traffic while its
# server answers /health.
resource "oci_load_balancer_backend_set" "app" {
  for_each         = local.apps
  name             = each.key
  load_balancer_id = oci_load_balancer_load_balancer.main.id
  policy           = "ROUND_ROBIN"

  health_checker {
    protocol          = "HTTP"
    port              = 8080
    url_path          = "/health"
    return_code       = 200
    interval_ms       = 10000
    timeout_in_millis = 3000
    retries           = 3
  }
}

resource "oci_load_balancer_backend" "app" {
  for_each         = local.apps
  load_balancer_id = oci_load_balancer_load_balancer.main.id
  backendset_name  = oci_load_balancer_backend_set.app[each.key].name
  ip_address       = oci_core_instance.app[each.key].private_ip
  port             = 8080
}

# Path routing (routing policies replace the deprecated path route sets):
# /api and below go to the API server.
resource "oci_load_balancer_load_balancer_routing_policy" "paths" {
  name                       = "paths"
  load_balancer_id           = oci_load_balancer_load_balancer.main.id
  condition_language_version = "V1"

  rules {
    name      = "api"
    condition = "any(http.request.url.path sw (i '/api'))"

    actions {
      name             = "FORWARD_TO_BACKENDSET"
      backend_set_name = oci_load_balancer_backend_set.app["api"].name
    }
  }
}

resource "oci_load_balancer_listener" "http" {
  name                     = "http"
  load_balancer_id         = oci_load_balancer_load_balancer.main.id
  port                     = 80
  protocol                 = "HTTP"
  default_backend_set_name = oci_load_balancer_backend_set.app["web"].name
  routing_policy_name      = oci_load_balancer_load_balancer_routing_policy.paths.name
}
