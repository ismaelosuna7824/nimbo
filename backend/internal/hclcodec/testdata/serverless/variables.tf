variable "region" {
  type    = string
  default = "us-east-1"
}

variable "stage" {
  type        = string
  description = "Deployment stage"
  default     = "dev"

  validation {
    condition     = contains(["dev", "prod"], var.stage)
    error_message = "Stage must be dev or prod."
  }
}

variable "memory_mb" {
  type    = number
  default = 128
}
