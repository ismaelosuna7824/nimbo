resource "azurerm_resource_group" "main" {
  name     = "${local.name_prefix}-rg"
  location = var.location
  tags     = local.common_tags
}

# App Service names are global: a short random suffix keeps them unique.
resource "random_string" "suffix" {
  length  = 6
  special = false
  upper   = false
}

resource "azurerm_service_plan" "main" {
  name                = "${local.name_prefix}-plan"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  os_type             = "Linux"
  sku_name            = var.app_service_sku
  tags                = local.common_tags
}

# The internal servers: they refuse every request that does not come
# through this Front Door profile (service tag + X-Azure-FDID header).
resource "azurerm_linux_web_app" "app" {
  for_each                      = local.apps
  name                          = "${local.name_prefix}-${each.key}-${random_string.suffix.result}"
  resource_group_name           = azurerm_resource_group.main.name
  location                      = azurerm_resource_group.main.location
  service_plan_id               = azurerm_service_plan.main.id
  https_only                    = true
  public_network_access_enabled = true
  tags                          = local.common_tags

  site_config {
    always_on                         = true
    ftps_state                        = "Disabled"
    http2_enabled                     = true
    minimum_tls_version               = "1.2"
    health_check_path                 = "/"
    health_check_eviction_time_in_min = 5
    ip_restriction_default_action     = "Deny"

    application_stack {
      docker_image_name   = var.app_image
      docker_registry_url = "https://index.docker.io"
    }

    ip_restriction {
      name        = "front-door-only"
      priority    = 100
      action      = "Allow"
      service_tag = "AzureFrontDoor.Backend"

      headers {
        x_azure_fdid = [azurerm_cdn_frontdoor_profile.main.resource_guid]
      }
    }
  }
}

# Front Door: the one public endpoint, terminating HTTPS at the edge.
resource "azurerm_cdn_frontdoor_profile" "main" {
  name                     = "${local.name_prefix}-fd"
  resource_group_name      = azurerm_resource_group.main.name
  sku_name                 = "Standard_AzureFrontDoor"
  response_timeout_seconds = 60
  tags                     = local.common_tags
}

resource "azurerm_cdn_frontdoor_endpoint" "main" {
  name                     = "${local.name_prefix}-${random_string.suffix.result}"
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.main.id
  tags                     = local.common_tags
}

resource "azurerm_cdn_frontdoor_origin_group" "app" {
  for_each                 = local.apps
  name                     = each.key
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.main.id

  load_balancing {
    sample_size                 = 4
    successful_samples_required = 3
  }

  health_probe {
    path                = "/"
    protocol            = "Https"
    request_type        = "HEAD"
    interval_in_seconds = 100
  }
}

resource "azurerm_cdn_frontdoor_origin" "app" {
  for_each                       = local.apps
  name                           = each.key
  cdn_frontdoor_origin_group_id  = azurerm_cdn_frontdoor_origin_group.app[each.key].id
  enabled                        = true
  host_name                      = azurerm_linux_web_app.app[each.key].default_hostname
  origin_host_header             = azurerm_linux_web_app.app[each.key].default_hostname
  certificate_name_check_enabled = true
  http_port                      = 80
  https_port                     = 443
  priority                       = 1
  weight                         = 1000
}

# Path routing: /api/* to the API app, everything else to the web app.
resource "azurerm_cdn_frontdoor_route" "app" {
  for_each                      = local.apps
  name                          = each.key
  cdn_frontdoor_endpoint_id     = azurerm_cdn_frontdoor_endpoint.main.id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.app[each.key].id
  cdn_frontdoor_origin_ids      = [azurerm_cdn_frontdoor_origin.app[each.key].id]
  patterns_to_match             = each.value
  supported_protocols           = ["Http", "Https"]
  https_redirect_enabled        = true
  forwarding_protocol           = "HttpsOnly"
  link_to_default_domain        = true
}
