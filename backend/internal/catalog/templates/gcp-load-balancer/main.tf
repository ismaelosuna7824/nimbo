resource "google_compute_instance_template" "app" {
  name_prefix  = "${local.name_prefix}-"
  machine_type = var.machine_type
  tags         = ["app"]

  disk {
    source_image = "debian-cloud/debian-12"
    auto_delete  = true
    boot         = true
  }

  network_interface {
    subnetwork = google_compute_subnetwork.app.id
  }

  shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }

  # A tiny Python HTTP service on port 8080 that answers with the VM's name.
  metadata = {
    startup-script = <<-EOT
      #!/bin/bash
      cat > /opt/app.py <<'PY'
      import json, socket
      from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

      class Handler(BaseHTTPRequestHandler):
          def do_GET(self):
              body = {"status": "ok"} if self.path == "/health" else {"server": socket.gethostname(), "path": self.path}
              payload = json.dumps(body).encode()
              self.send_response(200)
              self.send_header("content-type", "application/json")
              self.send_header("content-length", str(len(payload)))
              self.end_headers()
              self.wfile.write(payload)

      ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
      PY
      nohup python3 /opt/app.py >/var/log/app.log 2>&1 &
    EOT
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "google_compute_health_check" "app" {
  name                = "${local.name_prefix}-health"
  check_interval_sec  = 10
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 3

  http_health_check {
    port         = 8080
    request_path = "/health"
  }
}

# Keeps instance_count VMs across the region's zones and recreates any
# that keep failing the health check.
resource "google_compute_region_instance_group_manager" "app" {
  name               = "${local.name_prefix}-mig"
  region             = var.gcp_region
  base_instance_name = "${local.name_prefix}-app"
  target_size        = var.instance_count

  version {
    instance_template = google_compute_instance_template.app.self_link_unique
  }

  named_port {
    name = "http"
    port = 8080
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.app.id
    initial_delay_sec = 120
  }
}

resource "google_compute_backend_service" "app" {
  name                  = "${local.name_prefix}-app"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTP"
  port_name             = "http"
  health_checks         = [google_compute_health_check.app.id]

  backend {
    group           = google_compute_region_instance_group_manager.app.instance_group
    balancing_mode  = "UTILIZATION"
    capacity_scaler = 1
  }

  log_config {
    enable      = true
    sample_rate = 1
  }
}

resource "google_compute_url_map" "main" {
  name            = local.name_prefix
  default_service = google_compute_backend_service.app.id
}

resource "google_compute_target_http_proxy" "main" {
  name    = local.name_prefix
  url_map = google_compute_url_map.main.id
}

resource "google_compute_global_forwarding_rule" "http" {
  name                  = "${local.name_prefix}-http"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_address            = google_compute_global_address.main.id
  port_range            = "80"
  target                = google_compute_target_http_proxy.main.id
}
