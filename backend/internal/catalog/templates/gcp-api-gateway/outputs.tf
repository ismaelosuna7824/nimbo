output "api_url" {
  value       = "https://${var.api_hostname}/v1"
  description = "Base URL of the gateway: GET /orders and GET /users with the x-api-key header."
}

output "api_key" {
  value       = google_apigee_developer_app.demo.credentials[0].consumer_key
  sensitive   = true
  description = "API key of the demo client app."
}

output "service_urls" {
  value       = { for name, svc in google_cloud_run_v2_service.service : name => svc.uri }
  description = "Cloud Run URLs of the backend services (they require the gateway's identity token)."
}
