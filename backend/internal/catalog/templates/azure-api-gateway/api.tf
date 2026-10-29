# The gateway: one entry point, subscription keys, routing to the backends.
resource "azurerm_api_management" "main" {
  name                = "${local.name_prefix}-apim-${random_string.suffix.result}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  publisher_name      = var.publisher_name
  publisher_email     = var.publisher_email
  sku_name            = "Consumption_0"
  tags                = local.common_tags
}

resource "azurerm_api_management_backend" "service" {
  for_each            = local.services
  name                = each.key
  resource_group_name = azurerm_resource_group.main.name
  api_management_name = azurerm_api_management.main.name
  protocol            = "http"
  url                 = "https://${azurerm_linux_web_app.service[each.key].default_hostname}"
  description         = "The ${each.key} App Service."
}

resource "azurerm_api_management_api" "shop" {
  name                  = "shop"
  resource_group_name   = azurerm_resource_group.main.name
  api_management_name   = azurerm_api_management.main.name
  revision              = "1"
  display_name          = "Shop"
  path                  = "shop"
  protocols             = ["https"]
  subscription_required = true

  subscription_key_parameter_names {
    header = "Ocp-Apim-Subscription-Key"
    query  = "subscription-key"
  }
}

resource "azurerm_api_management_api_operation" "list" {
  for_each            = local.services
  operation_id        = "list-${each.key}"
  api_name            = azurerm_api_management_api.shop.name
  api_management_name = azurerm_api_management.main.name
  resource_group_name = azurerm_resource_group.main.name
  display_name        = "List ${each.key}"
  method              = "GET"
  url_template        = "/${each.key}"

  response {
    status_code = 200
  }
}

# Each operation goes to its own backend, rate-limited per subscription key.
resource "azurerm_api_management_api_operation_policy" "route" {
  for_each            = local.services
  api_name            = azurerm_api_management_api.shop.name
  api_management_name = azurerm_api_management.main.name
  resource_group_name = azurerm_resource_group.main.name
  operation_id        = azurerm_api_management_api_operation.list[each.key].operation_id
  xml_content         = <<-XML
    <policies>
      <inbound>
        <base />
        <rate-limit calls="${var.rate_limit_calls_per_minute}" renewal-period="60" />
        <set-backend-service backend-id="${azurerm_api_management_backend.service[each.key].name}" />
      </inbound>
      <backend>
        <base />
      </backend>
      <outbound>
        <base />
      </outbound>
      <on-error>
        <base />
      </on-error>
    </policies>
  XML
}

# Clients subscribe to the product and send their key with every call.
resource "azurerm_api_management_product" "shop" {
  product_id            = "shop"
  api_management_name   = azurerm_api_management.main.name
  resource_group_name   = azurerm_resource_group.main.name
  display_name          = "Shop"
  subscription_required = true
  approval_required     = false
  published             = true
}

resource "azurerm_api_management_product_api" "shop" {
  api_name            = azurerm_api_management_api.shop.name
  product_id          = azurerm_api_management_product.shop.product_id
  api_management_name = azurerm_api_management.main.name
  resource_group_name = azurerm_resource_group.main.name
}

resource "azurerm_api_management_subscription" "client" {
  display_name        = "Demo client"
  api_management_name = azurerm_api_management.main.name
  resource_group_name = azurerm_resource_group.main.name
  product_id          = azurerm_api_management_product.shop.id
  state               = "active"
  allow_tracing       = false
}
