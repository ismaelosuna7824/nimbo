locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The internal servers the proxy hides: NGINX sends /api/ to "api" and
  # everything else to "web".
  apps = toset(["web", "api"])
  az   = data.aws_availability_zones.available.names[0]
}
