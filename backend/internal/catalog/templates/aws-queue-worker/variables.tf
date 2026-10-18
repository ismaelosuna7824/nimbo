variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "name" {
  type        = string
  default     = "orders"
  description = "Prefix for every resource name."
}

variable "batch_size" {
  type        = number
  default     = 10
  description = "Maximum messages per Lambda invocation."
}

variable "max_receive_count" {
  type        = number
  default     = 3
  description = "Deliveries before a message moves to the dead-letter queue."
}
