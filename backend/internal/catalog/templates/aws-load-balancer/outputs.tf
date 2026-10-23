output "alb_dns_name" {
  value       = aws_lb.app.dns_name
  description = "DNS name of the load balancer: the one public address of the servers."
}

output "url" {
  value       = "${local.https ? "https" : "http"}://${aws_lb.app.dns_name}/"
  description = "URL of the app through the load balancer."
}

output "target_group_arn" {
  value       = aws_lb_target_group.app.arn
  description = "Target group whose health checks decide which servers get traffic."
}

output "autoscaling_group" {
  value       = aws_autoscaling_group.app.name
  description = "Auto Scaling group that keeps the app servers running and registered."
}
