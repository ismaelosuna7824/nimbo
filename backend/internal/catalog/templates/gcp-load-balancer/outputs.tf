output "address" {
  value       = google_compute_global_address.main.address
  description = "The load balancer's public IP: http://<address>/ reaches a healthy VM."
}

output "instance_group" {
  value       = google_compute_region_instance_group_manager.app.instance_group
  description = "The managed instance group behind the load balancer."
}

output "backend_service" {
  value       = google_compute_backend_service.app.id
  description = "Backend service whose health check decides which VMs get traffic."
}
