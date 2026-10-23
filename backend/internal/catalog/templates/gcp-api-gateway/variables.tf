variable "project" {
  type        = string
  default     = "edge-api"
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

variable "api_hostname" {
  type        = string
  default     = "api.example.com"
  description = "Host name clients call; Apigee serves it through the environment group."
}

variable "service_image" {
  type        = string
  default     = "nginxdemos/hello:0.4-plain-text"
  description = "Container image of both backend services (Docker Hub); it answers with the server name and the path it received."
}
