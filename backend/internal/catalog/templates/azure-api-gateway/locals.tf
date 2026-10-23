locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The backend services: API Management exposes GET /<name> for each.
  services = toset(["orders", "users"])
}
