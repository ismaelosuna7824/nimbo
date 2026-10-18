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

variable "consumers" {
  type        = list(string)
  default     = ["billing", "shipping"]
  description = "One SQS queue is subscribed to the topic per consumer."
}
