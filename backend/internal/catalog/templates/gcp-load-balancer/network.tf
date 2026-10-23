resource "google_compute_network" "main" {
  name                    = local.name_prefix
  auto_create_subnetworks = false
}

# The instances have no external IP; only the load balancer reaches them.
resource "google_compute_subnetwork" "app" {
  name                     = "${local.name_prefix}-app"
  network                  = google_compute_network.main.id
  region                   = var.gcp_region
  ip_cidr_range            = var.subnet_cidr
  private_ip_google_access = true
}

# Google front ends (load balancer traffic and health checks) are the only
# sources allowed to reach the app port.
resource "google_compute_firewall" "lb_to_app" {
  name          = "${local.name_prefix}-lb-to-app"
  network       = google_compute_network.main.id
  direction     = "INGRESS"
  source_ranges = ["130.211.0.0/22", "35.191.0.0/16"]
  target_tags   = ["app"]

  allow {
    protocol = "tcp"
    ports    = ["8080"]
  }
}

resource "google_compute_global_address" "main" {
  name = "${local.name_prefix}-ip"
}
