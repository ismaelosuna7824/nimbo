data "archive_file" "check" {
  type        = "zip"
  source_dir  = "${path.module}/services/check/src"
  output_path = "${path.module}/build/check.zip"
}

resource "aws_iam_role" "lambda" {
  name = "${local.name_prefix}-check"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_lambda_function" "check" {
  function_name    = "${local.name_prefix}-check"
  role             = aws_iam_role.lambda.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.check.output_path
  source_code_hash = data.archive_file.check.output_base64sha256
  timeout          = 10
}

resource "aws_cloudwatch_log_group" "check" {
  name              = "/aws/lambda/${aws_lambda_function.check.function_name}"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role_policy" "check_logs" {
  name = "${local.name_prefix}-check-logs"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = ["${aws_cloudwatch_log_group.check.arn}:*"]
    }]
  })
}

resource "aws_iam_role" "states" {
  name = "${local.name_prefix}-states"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "states.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "invoke_check" {
  name = "${local.name_prefix}-invoke-check"
  role = aws_iam_role.states.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]
      Resource = [aws_lambda_function.check.arn]
    }]
  })
}

resource "aws_sfn_state_machine" "flow" {
  name     = local.name_prefix
  role_arn = aws_iam_role.states.arn
  definition = jsonencode({
    Comment = "Checks an order and branches on approval"
    StartAt = "Check"
    States = {
      Check = {
        Type     = "Task"
        Resource = aws_lambda_function.check.arn
        Next     = "Wait"
      }
      Wait = {
        Type    = "Wait"
        Seconds = var.wait_seconds
        Next    = "Decide"
      }
      Decide = {
        Type = "Choice"
        Choices = [{
          Variable      = "$.approved"
          BooleanEquals = true
          Next          = "Approved"
        }]
        Default = "Rejected"
      }
      Approved = { Type = "Succeed" }
      Rejected = { Type = "Fail", Error = "OrderRejected", Cause = "Amount above limit" }
    }
  })
  depends_on = [aws_iam_role_policy.invoke_check]
}
