variable "aws_region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to deploy into."
}

variable "site_name" {
  type        = string
  default     = "static-site"
  description = "Bucket name prefix; AWS appends a unique suffix."
}

variable "index_html" {
  type        = string
  default     = "<!doctype html><html><head><title>Hello</title></head><body><h1>Hello from Nimbo</h1></body></html>"
  description = "Content of index.html."
}
