locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  public_cidr  = cidrsubnet(var.vcn_cidr, 8, 0)
  private_cidr = cidrsubnet(var.vcn_cidr, 8, 1)
  tiers = {
    public = {
      cidr    = local.public_cidr
      public  = true
      ingress = [{ description = "HTTP from anywhere", cidr = "0.0.0.0/0", port = 80 }]
      egress  = [{ description = "To the app servers", cidr = local.private_cidr, port = 8080 }]
    }
    private = {
      cidr    = local.private_cidr
      public  = false
      ingress = [{ description = "From the load balancer", cidr = local.public_cidr, port = 8080 }]
      egress  = []
    }
  }
}
