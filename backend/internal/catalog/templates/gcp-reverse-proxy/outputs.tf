output "address" {
  value       = google_compute_global_address.main.address
  description = "The load balancer's public IP: point the domain's A record here."
}

output "url" {
  value       = "https://${var.domain}/"
  description = "Public URL: / reaches the web service, /api/ the API service."
}

output "service_urls" {
  value       = { for name, svc in google_cloud_run_v2_service.app : name => svc.uri }
  description = "Cloud Run URLs of the services (they refuse traffic that doesn't come through the load balancer)."
}
