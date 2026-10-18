variable "gcp_project" {
  type        = string
  default     = "floci-local"
  description = "Google Cloud project ID (floci-local is the local Floci project)."
}

variable "region" {
  type        = string
  default     = "us-central1"
  description = "Default region."
}

variable "name" {
  type        = string
  default     = "events"
  description = "Prefix for every resource name."
}

variable "ack_deadline_seconds" {
  type        = number
  default     = 30
  description = "Seconds a subscriber has to acknowledge a message."
}
