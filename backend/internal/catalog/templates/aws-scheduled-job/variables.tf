variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "name" {
  type        = string
  default     = "nightly-report"
  description = "Prefix for every resource name."
}

variable "schedule_expression" {
  type        = string
  default     = "rate(5 minutes)"
  description = "EventBridge Scheduler expression: rate(...), cron(...) or at(...)."
}
