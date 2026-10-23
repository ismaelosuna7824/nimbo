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

# Requests are spread round robin over the servers that answer /health.
resource "oci_load_balancer_backend_set" "app" {
  name             = "app"
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
  count            = var.instance_count
  load_balancer_id = oci_load_balancer_load_balancer.main.id
  backendset_name  = oci_load_balancer_backend_set.app.name
  ip_address       = oci_core_instance.app[count.index].private_ip
  port             = 8080
}

resource "oci_load_balancer_listener" "http" {
  name                     = "http"
  load_balancer_id         = oci_load_balancer_load_balancer.main.id
  port                     = 80
  protocol                 = "HTTP"
  default_backend_set_name = oci_load_balancer_backend_set.app.name
}
