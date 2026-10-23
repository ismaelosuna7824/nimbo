variable "project" {
  type        = string
  default     = "edge-lb"
  description = "Project name: the first part of every resource name and the Project tag."

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,19}$", var.project))
    error_message = "project must be 2-20 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "environment" {
  type        = string
  default     = "dev"
  description = "Deployment environment (dev, staging, prod, …): part of every resource name and the Environment tag."

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,11}$", var.environment))
    error_message = "environment must be 1-12 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "vpc_id" {
  type        = string
  default     = null
  description = "VPC of the load balancer and servers; null uses the region's default VPC."
}

variable "subnet_ids" {
  type        = list(string)
  default     = []
  description = "Public subnets in at least two availability zones for the load balancer; empty uses the VPC's default subnets."
}

variable "app_subnet_ids" {
  type        = list(string)
  default     = []
  description = "Private subnets for the app servers; empty uses the load balancer's subnets."
}

variable "instance_count" {
  type        = number
  default     = 2
  description = "Number of app servers the Auto Scaling group keeps behind the load balancer."

  validation {
    condition     = var.instance_count >= 2
    error_message = "Use at least two servers so one can fail its health check while the other serves."
  }
}

variable "instance_type" {
  type        = string
  default     = "t3.micro"
  description = "EC2 instance type of the app servers (x86_64)."
}

variable "certificate_arn" {
  type        = string
  default     = null
  description = "ACM certificate for an HTTPS listener; when set, HTTP redirects to HTTPS."
}
