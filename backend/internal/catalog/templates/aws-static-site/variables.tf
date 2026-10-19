variable "project" {
  type        = string
  default     = "static-site"
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

variable "index_html" {
  type        = string
  default     = "<!doctype html><html><head><title>Hello</title></head><body><h1>Hello from Nimbo</h1></body></html>"
  description = "Content of index.html."
}
