# Apigee X (pay-as-you-go, no VPC peering): organization, a runtime
# instance, an environment and the environment group serving api_hostname.
resource "google_apigee_organization" "main" {
  project_id          = var.gcp_project
  analytics_region    = var.gcp_region
  runtime_type        = "CLOUD"
  billing_type        = "PAYG"
  disable_vpc_peering = true
  depends_on          = [google_project_service.apis]
}

resource "google_apigee_instance" "main" {
  name     = local.name_prefix
  location = var.gcp_region
  org_id   = google_apigee_organization.main.id
}

resource "google_apigee_environment" "main" {
  name        = var.environment
  org_id      = google_apigee_organization.main.id
  type        = "INTERMEDIATE"
  description = "Runs the ${local.name_prefix} proxy."
}

resource "google_apigee_instance_attachment" "main" {
  instance_id = google_apigee_instance.main.id
  environment = google_apigee_environment.main.name
}

resource "google_apigee_envgroup" "main" {
  name      = local.name_prefix
  org_id    = google_apigee_organization.main.id
  hostnames = [var.api_hostname]
}

resource "google_apigee_envgroup_attachment" "main" {
  envgroup_id = google_apigee_envgroup.main.id
  environment = google_apigee_environment.main.name
}

data "archive_file" "proxy" {
  type        = "zip"
  output_path = "${path.module}/build/${local.name_prefix}-proxy.zip"

  dynamic "source" {
    for_each = local.proxy_files
    content {
      filename = source.key
      content  = source.value
    }
  }
}

resource "google_apigee_api" "main" {
  name           = local.name_prefix
  org_id         = google_apigee_organization.main.id
  config_bundle  = data.archive_file.proxy.output_path
  detect_md5hash = data.archive_file.proxy.output_md5
}

resource "google_apigee_api_deployment" "main" {
  org_id          = google_apigee_organization.main.name
  environment     = google_apigee_environment.main.name
  proxy_id        = google_apigee_api.main.name
  revision        = google_apigee_api.main.latest_revision_id
  service_account = google_service_account.proxy.email
  depends_on      = [google_apigee_instance_attachment.main]
}

# What a client subscribes to: the proxy's paths in this environment.
resource "google_apigee_api_product" "main" {
  name          = local.name_prefix
  display_name  = "Edge API"
  org_id        = google_apigee_organization.main.id
  approval_type = "auto"
  environments  = [google_apigee_environment.main.name]
  proxies       = [google_apigee_api.main.name]
  api_resources = ["/orders", "/orders/**", "/users", "/users/**"]
}

resource "google_apigee_developer" "demo" {
  org_id     = google_apigee_organization.main.id
  email      = "demo-developer@example.com"
  first_name = "Demo"
  last_name  = "Developer"
  user_name  = "demo-developer"
}

# The demo client: its credentials hold the API key sent as x-api-key.
resource "google_apigee_developer_app" "demo" {
  name            = "${local.name_prefix}-demo"
  org_id          = google_apigee_organization.main.id
  developer_email = google_apigee_developer.demo.email
  api_products    = [google_apigee_api_product.main.name]
  callback_url    = "https://${var.api_hostname}/callback"
}
