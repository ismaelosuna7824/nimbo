# One log group for every function of the API (Lambda advanced logging).
resource "aws_cloudwatch_log_group" "functions" {
  name              = "/aws/lambda/${local.name_prefix}"
  retention_in_days = var.log_retention_days
}

# Every service function runs the same code (services/service/src); its
# name and records come from the environment.
data "archive_file" "service" {
  type        = "zip"
  source_dir  = "${path.module}/services/service/src"
  output_path = "${path.module}/build/service.zip"
}

resource "aws_lambda_function" "service" {
  for_each         = local.services
  function_name    = "${local.name_prefix}-${each.key}"
  role             = aws_iam_role.service.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.service.output_path
  source_code_hash = data.archive_file.service.output_base64sha256
  memory_size      = 128
  timeout          = 5

  environment {
    variables = {
      SERVICE_NAME    = each.key
      SERVICE_RECORDS = jsonencode(each.value)
    }
  }

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.functions.name
  }
}

data "archive_file" "authorizer" {
  type        = "zip"
  source_dir  = "${path.module}/services/authorizer/src"
  output_path = "${path.module}/build/authorizer.zip"
}

resource "aws_lambda_function" "authorizer" {
  function_name    = "${local.name_prefix}-authorizer"
  role             = aws_iam_role.authorizer.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.authorizer.output_path
  source_code_hash = data.archive_file.authorizer.output_base64sha256
  memory_size      = 128
  timeout          = 5

  environment {
    variables = {
      TOKEN_PARAMETER = aws_ssm_parameter.api_token.name
    }
  }

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.functions.name
  }
}
