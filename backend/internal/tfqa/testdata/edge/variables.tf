variable "region" {
  type        = string
  default     = "us-east-1"
  description = "Primary region"
}

variable "stage" {
  type     = string
  nullable = false
  default  = "dev"

  validation {
    condition     = contains(["dev", "staging", "prod"], var.stage)
    error_message = "stage must be dev, staging or prod."
  }

  # A second rule, with a heredoc message.
  validation {
    condition     = length(var.stage) <= 8
    error_message = <<-EOT
      stage is too long:
      keep it under 8 characters.
    EOT
  }
}

variable "api_token" {
  type      = string
  sensitive = true
  ephemeral = true
  default   = null
}

variable "queues" {
  type = map(object({
    delay   = optional(number, 0)
    fifo    = optional(bool, false)
    comment = optional(string)
  }))
  default = {
    jobs = {}
    events = {
      delay = 5
    }
  }
}

variable "existing_queues" {
  type    = map(string)
  default = {}
}

variable "ingress" {
  type = list(object({ port = number, cidr = string }))
  default = [
    { port = 80, cidr = "0.0.0.0/0" },
    { port = 443, cidr = "0.0.0.0/0" }, # trailing comma
  ]
}
