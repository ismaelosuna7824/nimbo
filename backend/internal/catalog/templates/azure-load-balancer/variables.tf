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

variable "location" {
  type        = string
  default     = "eastus"
  description = "Azure region of the resource group and its resources."
}

variable "vnet_cidr" {
  type        = string
  default     = "10.30.0.0/16"
  description = "Address range of the virtual network; the app subnet is its first /24."
}

variable "instance_count" {
  type        = number
  default     = 2
  description = "Number of VMs in the scale set behind the load balancer."

  validation {
    condition     = var.instance_count >= 2
    error_message = "Use at least two instances so one can fail its health probe while the other serves."
  }
}

variable "vm_size" {
  type        = string
  default     = "Standard_B1s"
  description = "VM size of the scale set instances."
}

variable "admin_ssh_public_key" {
  type        = string
  description = "SSH public key of the azureuser account (OpenSSH format); no inbound SSH rule is opened."
}
