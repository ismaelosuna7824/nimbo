variable "project" {
  type        = string
  default     = "order-flow"
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

variable "wait_seconds" {
  type        = number
  default     = 2
  description = "Pause between the check and the decision states."
}

variable "log_retention_days" {
  type        = number
  default     = 14
  description = "Days CloudWatch keeps the function's logs."
}
