terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

variable "azs" {
  type    = list(string)
  default = ["eu-west-1a", "eu-west-1b"]
}

variable "ingress_rules" {
  type = map(object({
    port  = number
    cidrs = list(string)
  }))
  default = {
    https = {
      port  = 443
      cidrs = ["0.0.0.0/0"]
    }
  }
}

variable "enable_logs" {
  type    = bool
  default = false
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name            = "main"
  cidr            = "10.0.0.0/16"
  azs             = var.azs
  private_subnets = ["10.0.1.0/24", "10.0.2.0/24"]
}

resource "aws_security_group" "web" {
  name   = "web"
  vpc_id = module.vpc.vpc_id

  dynamic "ingress" {
    for_each = var.ingress_rules
    content {
      description = "Allow ${ingress.key}"
      from_port   = ingress.value.port
      to_port     = ingress.value.port
      protocol    = "tcp"
      cidr_blocks = ingress.value.cidrs
    }
  }

  lifecycle {
    create_before_destroy = true
    ignore_changes        = [tags]
  }
}

resource "aws_instance" "web" {
  count                  = length(module.vpc.private_subnets)
  ami                    = "ami-12345678"
  instance_type          = "t3.micro"
  subnet_id              = module.vpc.private_subnets[count.index]
  vpc_security_group_ids = [aws_security_group.web.id]
  user_data              = <<-EOT
    #!/bin/bash
    echo "hello from ${var.azs[0]}" > /tmp/hello
  EOT

  tags = {
    Name = "web-${count.index}"
  }

  depends_on = [aws_iam_role_policy.logs, module.vpc]
}

resource "aws_eip" "web" {
  for_each = { for idx, inst in aws_instance.web : idx => inst }
  instance = each.value.id
  domain   = "vpc"
}

resource "aws_lb_target_group_attachment" "web" {
  count            = 2
  target_group_arn = aws_lb_target_group.web.arn
  target_id        = aws_instance.web[count.index].id
}

resource "aws_lb_target_group" "web" {
  name     = "web"
  port     = 80
  protocol = "HTTP"
  vpc_id   = module.vpc.vpc_id
}

resource "aws_cloudwatch_log_group" "flow" {
  count = var.enable_logs ? 1 : 0
  name  = "/vpc/flow"
}

resource "aws_iam_role" "logs" {
  name = "flow-logs"

  assume_role_policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "vpc-flow-logs.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF
}

resource "aws_iam_role_policy" "logs" {
  role = aws_iam_role.logs.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:PutLogEvents"]
      Resource = aws_cloudwatch_log_group.flow[*].arn
    }]
  })
}

resource "aws_route53_record" "web" {
  zone_id = "Z123"
  name    = "web.example.com"
  type    = "A"
  ttl     = 60
  records = aws_eip.web["0"].public_ip == null ? [] : [aws_eip.web["0"].public_ip]
}

moved {
  from = aws_instance.app
  to   = aws_instance.web
}

output "instance_ids" {
  value       = aws_instance.web[*].id
  description = "IDs of ${length(aws_instance.web)} instances"
}

output "unknown" {
  value = aws_s3_bucket.missing.arn
}
