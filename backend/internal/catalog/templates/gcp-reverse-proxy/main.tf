# The internal services: their ingress only accepts traffic from Google
# Cloud load balancers, so the public reaches them only through the URL map.
resource "google_cloud_run_v2_service" "app" {
  for_each            = local.services
  name                = "${local.name_prefix}-${each.key}"
  location            = var.gcp_region
  ingress             = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  deletion_protection = false

  template {
    scaling {
      max_instance_count = 5
    }

    containers {
      image = var.app_image

      ports {
        container_port = 80
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "256Mi"
        }
      }
    }
  }
}

# Serverless NEGs don't authenticate: the services allow unauthenticated
# calls, and their ingress setting keeps everything but the load balancer out.
resource "google_cloud_run_v2_service_iam_member" "invoker" {
  for_each = local.services
  name     = google_cloud_run_v2_service.app[each.key].name
  location = var.gcp_region
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_compute_region_network_endpoint_group" "app" {
  for_each              = local.services
  name                  = "${local.name_prefix}-${each.key}-neg"
  region                = var.gcp_region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.app[each.key].name
  }
}

resource "google_compute_backend_service" "app" {
  for_each              = local.services
  name                  = "${local.name_prefix}-${each.key}"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTPS"

  backend {
    group = google_compute_region_network_endpoint_group.app[each.key].id
  }

  log_config {
    enable      = true
    sample_rate = 1
  }
}

# Path routing: /api and /api/* to the API service, the rest to the web one.
resource "google_compute_url_map" "https" {
  name            = "${local.name_prefix}-https"
  default_service = google_compute_backend_service.app["web"].id

  host_rule {
    hosts        = ["*"]
    path_matcher = "routes"
  }

  path_matcher {
    name            = "routes"
    default_service = google_compute_backend_service.app["web"].id

    path_rule {
      paths   = ["/api", "/api/*"]
      service = google_compute_backend_service.app["api"].id
    }
  }
}

resource "google_compute_managed_ssl_certificate" "main" {
  name = "${local.name_prefix}-cert"

  managed {
    domains = [var.domain]
  }
}

resource "google_compute_target_https_proxy" "main" {
  name             = "${local.name_prefix}-https"
  url_map          = google_compute_url_map.https.id
  ssl_certificates = [google_compute_managed_ssl_certificate.main.id]
}

resource "google_compute_global_address" "main" {
  name = "${local.name_prefix}-ip"
}

resource "google_compute_global_forwarding_rule" "https" {
  name                  = "${local.name_prefix}-https"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_address            = google_compute_global_address.main.id
  port_range            = "443"
  target                = google_compute_target_https_proxy.main.id
}

# Plain HTTP only redirects to HTTPS.
resource "google_compute_url_map" "redirect" {
  name = "${local.name_prefix}-redirect"

  default_url_redirect {
    https_redirect = true
    strip_query    = false
  }
}

resource "google_compute_target_http_proxy" "redirect" {
  name    = "${local.name_prefix}-redirect"
  url_map = google_compute_url_map.redirect.id
}

resource "google_compute_global_forwarding_rule" "http" {
  name                  = "${local.name_prefix}-http"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_address            = google_compute_global_address.main.id
  port_range            = "80"
  target                = google_compute_target_http_proxy.redirect.id
}
