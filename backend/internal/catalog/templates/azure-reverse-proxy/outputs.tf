output "url" {
  value       = "https://${azurerm_cdn_frontdoor_endpoint.main.host_name}/"
  description = "Public URL of Front Door: / reaches the web app, /api/ the API app."
}

output "app_hostnames" {
  value       = { for name, app in azurerm_linux_web_app.app : name => app.default_hostname }
  description = "Host names of the App Services (they answer 403 unless the request comes through Front Door)."
}

output "front_door_id" {
  value       = azurerm_cdn_frontdoor_profile.main.resource_guid
  description = "Front Door ID the apps require in the X-Azure-FDID header."
}
