terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

variable "vpc_id" {
  type = string
}

variable "cidr" {
  type = string
}

variable "azs" {
  type = list(string)
}

resource "aws_subnet" "this" {
  count = length(var.azs)

  vpc_id            = var.vpc_id
  cidr_block        = cidrsubnet(var.cidr, 8, count.index)
  availability_zone = var.azs[count.index]
}

output "ids" {
  value = aws_subnet.this[*].id
}
