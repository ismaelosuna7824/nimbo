locals {
  name_prefix = "${var.project}-${var.environment}"
  # GCP label keys and values must be lowercase.
  common_labels = {
    project     = var.project
    environment = var.environment
    managed_by  = "nimbo"
  }
}
