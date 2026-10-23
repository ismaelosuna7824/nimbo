locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The internal servers and the paths Front Door sends to each.
  apps = {
    web = ["/*"]
    api = ["/api/*"]
  }
}
