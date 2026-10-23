resource "oci_functions_application" "main" {
  compartment_id = var.compartment_ocid
  display_name   = local.name_prefix
  subnet_ids     = [oci_core_subnet.tier["private"].id]
  shape          = "GENERIC_X86"
  freeform_tags  = local.common_tags
}

# orders and users answer their route; authorizer checks the token of
# every request (an OCI API Gateway authorizer function).
resource "oci_functions_function" "fn" {
  for_each           = local.functions
  application_id     = oci_functions_application.main.id
  display_name       = "${local.name_prefix}-${each.key}"
  memory_in_mbs      = 256
  timeout_in_seconds = 30
  freeform_tags      = local.common_tags

  source_details {
    source_type = "CONTAINER_IMAGE"
    image       = var.function_images[each.key]
  }
}

# API Gateway may invoke the functions of this compartment.
resource "oci_identity_policy" "gateway_invokes_functions" {
  compartment_id = var.compartment_ocid
  name           = "${local.name_prefix}-gateway-functions"
  description    = "Lets the ${local.name_prefix} API gateway invoke its functions."
  freeform_tags  = local.common_tags
  statements = [
    "ALLOW any-user to use functions-family in compartment id ${var.compartment_ocid} where ALL {request.principal.type = 'ApiGateway', request.resource.compartment.id = '${var.compartment_ocid}'}",
  ]
}
