variable "region" {
  type    = string
  default = "us-east-1"
}

variable "name" {
  type = string
}

variable "cidr" {
  type = string
}

variable "azs" {
  type = list(string)
}

variable "lambda_memory" {
  type    = number
  default = 128
}
