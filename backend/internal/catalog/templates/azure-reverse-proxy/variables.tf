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

variable "location" {
  type        = string
  default     = "eastus"
  description = "Azure region of the resource group and its resources."
}

variable "app_service_sku" {
  type        = string
  default     = "B1"
  description = "App Service plan SKU shared by both apps."
}

variable "app_image" {
  type        = string
  default     = "nginxdemos/hello:0.4-plain-text"
  description = "Container image of both apps (Docker Hub); it answers with the server name and the path it received."
}
