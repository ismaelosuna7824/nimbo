locals {
  name_prefix = "${var.project}-${var.environment}"
  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "nimbo"
  }

  # The services behind the gateway (GET /v1/<name>) and the authorizer.
  services  = toset(["orders", "users"])
  functions = setunion(local.services, ["authorizer"])

  # Oracle Services Network: the functions pull their images from OCIR
  # through the service gateway.
  osn = data.oci_core_services.all.services[0]

  # Public subnet: the gateway (HTTPS from anywhere). Private subnet: the
  # functions, which only talk to Oracle services.
  tiers = {
    public = {
      cidr    = cidrsubnet(var.vcn_cidr, 8, 0)
      public  = true
      route   = { destination = "0.0.0.0/0", type = "CIDR_BLOCK", target = oci_core_internet_gateway.main.id }
      ingress = [{ description = "HTTPS from anywhere", cidr = "0.0.0.0/0", port = 443 }]
      egress  = [{ description = "To the functions service", cidr = local.osn.cidr_block, type = "SERVICE_CIDR_BLOCK", port = 443 }]
    }
    private = {
      cidr    = cidrsubnet(var.vcn_cidr, 8, 1)
      public  = false
      route   = { destination = local.osn.cidr_block, type = "SERVICE_CIDR_BLOCK", target = oci_core_service_gateway.main.id }
      ingress = []
      egress  = [{ description = "To Oracle services (OCIR, logging)", cidr = local.osn.cidr_block, type = "SERVICE_CIDR_BLOCK", port = 443 }]
    }
  }
}
