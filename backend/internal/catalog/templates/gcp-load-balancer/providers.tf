provider "google" {
  project        = var.gcp_project
  region         = var.gcp_region
  default_labels = local.common_labels
}
