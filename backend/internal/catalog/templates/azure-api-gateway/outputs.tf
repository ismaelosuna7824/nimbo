output "gateway_url" {
  value       = "${azurerm_api_management.main.gateway_url}/${azurerm_api_management_api.shop.path}"
  description = "Base URL of the API: GET /orders and GET /users with the Ocp-Apim-Subscription-Key header."
}

output "subscription_key" {
  value       = azurerm_api_management_subscription.client.primary_key
  sensitive   = true
  description = "Primary key of the demo client's subscription."
}

output "backend_hostnames" {
  value       = { for name, app in azurerm_linux_web_app.service : name => app.default_hostname }
  description = "Host names of the backend App Services."
}
