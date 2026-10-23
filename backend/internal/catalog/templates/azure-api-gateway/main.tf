resource "azurerm_resource_group" "main" {
  name     = "${local.name_prefix}-rg"
  location = var.location
  tags     = local.common_tags
}

# App Service and API Management names are global: a short random suffix
# keeps them unique.
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

# The two backend services. The Consumption gateway has no fixed outbound
# addresses; on a dedicated tier, restrict the apps to its public IPs.
resource "azurerm_linux_web_app" "service" {
  for_each            = local.services
  name                = "${local.name_prefix}-${each.key}-${random_string.suffix.result}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  service_plan_id     = azurerm_service_plan.main.id
  https_only          = true
  tags                = local.common_tags

  site_config {
    always_on           = true
    ftps_state          = "Disabled"
    http2_enabled       = true
    minimum_tls_version = "1.2"
    health_check_path   = "/"

    health_check_eviction_time_in_min = 5

    application_stack {
      docker_image_name   = var.app_image
      docker_registry_url = "https://index.docker.io"
    }
  }
}
