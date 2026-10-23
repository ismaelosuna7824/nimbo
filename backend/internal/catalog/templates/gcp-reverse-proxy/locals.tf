locals {
  name_prefix = "${var.project}-${var.environment}"
  # GCP label keys and values must be lowercase.
  common_labels = {
    project     = var.project
    environment = var.environment
    managed_by  = "nimbo"
  }

  # The internal services the load balancer hides: /api/* goes to "api",
  # everything else to "web".
  services = toset(["web", "api"])
}
