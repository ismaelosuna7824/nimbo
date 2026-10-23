# The single public entry point.
resource "oci_apigateway_gateway" "main" {
  compartment_id = var.compartment_ocid
  display_name   = local.name_prefix
  endpoint_type  = "PUBLIC"
  subnet_id      = oci_core_subnet.tier["public"].id
  freeform_tags  = local.common_tags
}

# Routes, the authorizer every route requires and per-client rate limiting.
resource "oci_apigateway_deployment" "v1" {
  compartment_id = var.compartment_ocid
  gateway_id     = oci_apigateway_gateway.main.id
  display_name   = "${local.name_prefix}-v1"
  path_prefix    = "/v1"
  freeform_tags  = local.common_tags

  specification {
    request_policies {
      authentication {
        type                        = "CUSTOM_AUTHENTICATION"
        function_id                 = oci_functions_function.fn["authorizer"].id
        token_header                = "Authorization"
        is_anonymous_access_allowed = false
      }

      rate_limiting {
        rate_in_requests_per_second = var.rate_limit_per_second
        rate_key                    = "CLIENT_IP"
      }
    }

    dynamic "routes" {
      for_each = local.services
      content {
        path    = "/${routes.value}"
        methods = ["GET"]

        backend {
          type        = "ORACLE_FUNCTIONS_BACKEND"
          function_id = oci_functions_function.fn[routes.value].id
        }
      }
    }
  }

  depends_on = [oci_identity_policy.gateway_invokes_functions]
}
