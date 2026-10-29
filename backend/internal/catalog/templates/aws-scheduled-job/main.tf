data "archive_file" "job" {
  type        = "zip"
  source_dir  = "${path.module}/services/job/src"
  output_path = "${path.module}/build/job.zip"
}

resource "aws_iam_role" "lambda" {
  name = "${local.name_prefix}-job"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_lambda_function" "job" {
  function_name    = "${local.name_prefix}-job"
  role             = aws_iam_role.lambda.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.job.output_path
  source_code_hash = data.archive_file.job.output_base64sha256
  timeout          = 60
}

resource "aws_cloudwatch_log_group" "job" {
  name              = "/aws/lambda/${aws_lambda_function.job.function_name}"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role_policy" "job_logs" {
  name = "${local.name_prefix}-job-logs"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = ["${aws_cloudwatch_log_group.job.arn}:*"]
    }]
  })
}

resource "aws_iam_role" "scheduler" {
  name = "${local.name_prefix}-scheduler"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "invoke_job" {
  name = "${local.name_prefix}-invoke-job"
  role = aws_iam_role.scheduler.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]
      Resource = [aws_lambda_function.job.arn]
    }]
  })
}

resource "aws_scheduler_schedule" "job" {
  name                = "${local.name_prefix}-schedule"
  schedule_expression = var.schedule_expression

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.job.arn
    role_arn = aws_iam_role.scheduler.arn
    input    = jsonencode({ source = "scheduler" })
  }
}
