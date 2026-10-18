variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "name" {
  type        = string
  default     = "order-flow"
  description = "Prefix for every resource name."
}

variable "wait_seconds" {
  type        = number
  default     = 2
  description = "Pause between the check and the decision states."
}
