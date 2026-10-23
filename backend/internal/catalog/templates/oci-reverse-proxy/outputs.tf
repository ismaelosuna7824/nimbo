output "public_ip" {
  value       = oci_load_balancer_load_balancer.main.ip_address_details[0].ip_address
  description = "Public IP of the load balancer: / reaches the web server, /api/ the API server."
}

output "app_private_ips" {
  value       = { for name, app in oci_core_instance.app : name => app.private_ip }
  description = "Private addresses of the internal servers."
}

output "vcn_id" {
  value       = oci_core_vcn.main.id
  description = "OCID of the VCN."
}
