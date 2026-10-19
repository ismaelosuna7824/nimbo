output "vpc_id" {
  value       = aws_vpc.main.id
  description = "ID of the VPC."
}

output "public_subnet_ids" {
  value       = [for s in aws_subnet.public : s.id]
  description = "IDs of the public subnets."
}

output "web_security_group_id" {
  value       = aws_security_group.web.id
  description = "ID of the security group that allows HTTP and HTTPS."
}
