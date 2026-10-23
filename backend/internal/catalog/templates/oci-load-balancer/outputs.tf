output "public_ip" {
  value       = oci_load_balancer_load_balancer.main.ip_address_details[0].ip_address
  description = "Public IP of the load balancer: http://<ip>/ reaches a healthy server."
}

output "instance_ids" {
  value       = oci_core_instance.app[*].id
  description = "OCIDs of the app servers."
}

output "backend_set" {
  value       = oci_load_balancer_backend_set.app.name
  description = "Backend set whose health checker decides which servers get traffic."
}
