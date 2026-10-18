variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "name" {
  type        = string
  default     = "items-api"
  description = "Prefix for every resource name."
}

variable "lambda_memory_mb" {
  type        = number
  default     = 256
  description = "Memory size of the API function."
}
