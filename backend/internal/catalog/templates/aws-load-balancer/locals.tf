locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The network is its own layer: this stack takes the subnets it is given,
  # else the default subnets of the VPC (one per availability zone).
  subnet_ids     = length(var.subnet_ids) > 0 ? var.subnet_ids : sort(data.aws_subnets.default.ids)
  app_subnet_ids = length(var.app_subnet_ids) > 0 ? var.app_subnet_ids : local.subnet_ids
  https          = var.certificate_arn != null
}
