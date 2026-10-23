variable "project" {
  type        = string
  default     = "edge-lb"
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

variable "instance_shape" {
  type        = string
  default     = "VM.Standard.E4.Flex"
  description = "Compute shape of the instances (a flexible shape)."
}

variable "instance_ocpus" {
  type        = number
  default     = 1
  description = "OCPUs of each instance."
}

variable "instance_memory_gbs" {
  type        = number
  default     = 8
  description = "Memory of each instance in GB."
}

variable "lb_bandwidth_mbps" {
  type        = number
  default     = 10
  description = "Minimum and maximum bandwidth of the flexible load balancer in Mbps (10 is the always-free size)."
}

variable "instance_count" {
  type        = number
  default     = 2
  description = "Number of app servers behind the load balancer."

  validation {
    condition     = var.instance_count >= 2
    error_message = "Use at least two instances so one can fail its health check while the other serves."
  }
}
