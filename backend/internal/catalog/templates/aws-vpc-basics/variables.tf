variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "name" {
  type        = string
  default     = "main"
  description = "Prefix for every resource name."
}

variable "vpc_cidr" {
  type        = string
  default     = "10.0.0.0/16"
  description = "CIDR block of the VPC."
}

variable "public_subnets" {
  type = map(object({ cidr = string, az = string }))
  default = {
    a = { cidr = "10.0.1.0/24", az = "us-east-1a" }
    b = { cidr = "10.0.2.0/24", az = "us-east-1b" }
  }
  description = "Public subnets keyed by a short name."
}
