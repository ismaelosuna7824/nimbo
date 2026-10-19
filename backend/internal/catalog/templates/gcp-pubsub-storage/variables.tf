variable "project" {
  type        = string
  default     = "events"
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

variable "gcp_project" {
  type        = string
  default     = "floci-local"
  description = "Google Cloud project ID (floci-local is the local Floci project)."
}

variable "gcp_region" {
  type        = string
  default     = "us-central1"
  description = "Google Cloud region of the provider and regional resources."
}

variable "ack_deadline_seconds" {
  type        = number
  default     = 30
  description = "Seconds a subscriber has to acknowledge a message."
}
