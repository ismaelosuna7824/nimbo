output "public_ip" {
  value       = azurerm_public_ip.lb.ip_address
  description = "The load balancer's public IP: http://<ip>/ reaches a healthy instance."
}

output "scale_set_id" {
  value       = azurerm_linux_virtual_machine_scale_set.app.id
  description = "ID of the VM scale set behind the load balancer."
}

output "health_probe_id" {
  value       = azurerm_lb_probe.http.id
  description = "ID of the health probe that decides which instances get traffic."
}
