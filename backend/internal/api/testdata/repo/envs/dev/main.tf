# Shared network for the dev environment.
module "network" {
  source = "../../modules/network"

  name = var.name
  cidr = var.cidr
  azs  = var.azs
}

# Registry module: downloaded by terraform init.
module "logs" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "4.1.2"

  bucket = "${var.name}-logs"
}

data "archive_file" "api" {
  type        = "zip"
  source_dir  = "${path.module}/src"
  output_path = "${path.module}/build/api.zip"
}

resource "aws_iam_role" "api" {
  name = "${var.name}-api"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
}

resource "aws_lambda_function" "api" {
  function_name    = "${var.name}-api"
  role             = aws_iam_role.api.arn
  runtime          = "python3.12"
  handler          = "handler.handle"
  filename         = data.archive_file.api.output_path
  source_code_hash = data.archive_file.api.output_base64sha256
  memory_size      = var.lambda_memory

  vpc_config {
    subnet_ids         = module.network.subnet_ids
    security_group_ids = []
  }
}
