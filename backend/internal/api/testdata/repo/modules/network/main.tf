variable "name" {
  type = string
}

variable "cidr" {
  type = string
}

variable "azs" {
  type = list(string)
}

resource "aws_vpc" "this" {
  cidr_block           = var.cidr # the whole range of the environment
  enable_dns_hostnames = true

  tags = {
    Name = var.name
  }
}

module "subnets" {
  source = "./modules/subnets"

  vpc_id = aws_vpc.this.id
  cidr   = var.cidr
  azs    = var.azs
}

output "vpc_id" {
  value = aws_vpc.this.id
}

output "subnet_ids" {
  value = module.subnets.ids
}
