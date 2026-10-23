variable "project" {
  type        = string
  default     = "edge-proxy"
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

variable "vpc_cidr" {
  type        = string
  default     = "10.20.0.0/16"
  description = "Address range of the VPC: the proxy gets the first /24, the app servers the eleventh."
}

variable "instance_type" {
  type        = string
  default     = "t3.micro"
  description = "EC2 instance type of the proxy and the app servers (x86_64)."
}
