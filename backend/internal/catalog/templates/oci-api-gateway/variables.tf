variable "project" {
  type        = string
  default     = "edge-api"
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

variable "oci_region" {
  type        = string
  default     = "us-ashburn-1"
  description = "OCI region to deploy into."
}

variable "compartment_ocid" {
  type        = string
  default     = "ocid1.tenancy.oc1..flocilocaltenancy0000000000000000000000000000000000000000"
  description = "Compartment OCID for every resource (defaults to the local Floci tenancy root compartment)."
}

variable "vcn_cidr" {
  type        = string
  default     = "10.50.0.0/16"
  description = "Address range of the VCN: the public subnet is its first /24, the private subnet the second."
}

variable "function_images" {
  type        = map(string)
  description = "OCIR image (with tag) of each function: orders, users and authorizer; build and push them with the Fn CLI (fn deploy)."

  validation {
    condition     = alltrue([for name in ["orders", "users", "authorizer"] : contains(keys(var.function_images), name)])
    error_message = "function_images needs the orders, users and authorizer images."
  }
}

variable "rate_limit_per_second" {
  type        = number
  default     = 50
  description = "Requests per second the gateway accepts from one client IP."
}
