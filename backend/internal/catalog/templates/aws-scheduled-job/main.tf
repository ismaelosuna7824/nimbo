data "archive_file" "job" {
  type        = "zip"
  output_path = "${path.module}/build/job.zip"

  source {
    filename = "index.mjs"
    content  = <<-EOT
      export const handler = async (event) => {
        const ranAt = new Date().toISOString();
        console.log("scheduled job ran", ranAt, JSON.stringify(event));
        return { ok: true, ranAt };
      };
    EOT
  }
}

resource "aws_iam_role" "lambda" {
  name = "${var.name}-job"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "lambda_logs" {
  role       = aws_iam_role.lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_lambda_function" "job" {
  function_name    = "${var.name}-job"
  role             = aws_iam_role.lambda.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.job.output_path
  source_code_hash = data.archive_file.job.output_base64sha256
  timeout          = 60
}

resource "aws_iam_role" "scheduler" {
  name = "${var.name}-scheduler"
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
  name = "${var.name}-invoke-job"
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
  name                = "${var.name}-schedule"
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
