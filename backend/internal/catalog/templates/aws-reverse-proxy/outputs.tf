output "proxy_instance_id" {
  value       = aws_instance.proxy.id
  description = "ID of the NGINX proxy instance (its Endpoints card has the local URL)."
}

output "proxy_url" {
  value       = "http://${aws_instance.proxy.public_ip}/"
  description = "Public URL of the reverse proxy: / reaches the web server, /api/ the API server."
}

output "app_private_ips" {
  value       = { for name, app in aws_instance.app : name => app.private_ip }
  description = "Private addresses of the app servers, reachable only from the proxy."
}

output "vpc_id" {
  value       = aws_vpc.main.id
  description = "ID of the VPC."
}
