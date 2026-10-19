variable "project" {
  type        = string
  default     = "nimbo"
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
  description = "Compartment OCID for the bucket (defaults to the local Floci tenancy root compartment)."
}
