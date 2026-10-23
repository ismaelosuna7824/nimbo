variable "project" {
  type        = string
  default     = "edge-lb"
  description = "Project name: the first part of every resource name and the project label."

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,19}$", var.project))
    error_message = "project must be 2-20 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "environment" {
  type        = string
  default     = "dev"
  description = "Deployment environment (dev, staging, prod, …): part of every resource name and the environment label."

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

variable "subnet_cidr" {
  type        = string
  default     = "10.40.1.0/24"
  description = "Address range of the instances' subnet."
}

variable "instance_count" {
  type        = number
  default     = 2
  description = "Number of VMs the managed instance group keeps running."

  validation {
    condition     = var.instance_count >= 2
    error_message = "Use at least two instances so one can fail its health check while the other serves."
  }
}

variable "machine_type" {
  type        = string
  default     = "e2-small"
  description = "Machine type of the instances."
}
