resource "google_project_service" "apis" {
  for_each           = toset(["apigee.googleapis.com", "run.googleapis.com"])
  service            = each.value
  disable_on_destroy = false
}

# The identity the gateway calls the services with.
resource "google_service_account" "proxy" {
  account_id   = "${local.name_prefix}-proxy"
  display_name = "Apigee proxy ${local.name_prefix}"
}

# The backend services: they require an identity token, so only the
# gateway's service account can call them.
resource "google_cloud_run_v2_service" "service" {
  for_each            = local.services
  name                = "${local.name_prefix}-${each.key}"
  location            = var.gcp_region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis]

  template {
    scaling {
      max_instance_count = 5
    }

    containers {
      image = var.service_image

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

resource "google_cloud_run_v2_service_iam_member" "proxy" {
  for_each = local.services
  name     = google_cloud_run_v2_service.service[each.key].name
  location = var.gcp_region
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.proxy.email}"
}
