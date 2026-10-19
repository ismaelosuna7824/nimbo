resource "aws_sqs_queue" "dlq" {
  name                      = "${local.name_prefix}-dlq"
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "jobs" {
  name                       = "${local.name_prefix}-jobs"
  visibility_timeout_seconds = 60
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.max_receive_count
  })
}

data "archive_file" "worker" {
  type        = "zip"
  output_path = "${path.module}/build/worker.zip"

  source {
    filename = "index.mjs"
    content  = <<-EOT
      export const handler = async (event) => {
        const batchItemFailures = [];
        for (const record of event.Records ?? []) {
          try {
            const job = JSON.parse(record.body);
            console.log("processing job", record.messageId, job);
          } catch (err) {
            console.error("invalid job", record.messageId, err);
            batchItemFailures.push({ itemIdentifier: record.messageId });
          }
        }
        return { batchItemFailures };
      };
    EOT
  }
}

resource "aws_iam_role" "lambda" {
  name = "${local.name_prefix}-worker"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "consume" {
  name = "${local.name_prefix}-consume-jobs"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"]
      Resource = [aws_sqs_queue.jobs.arn]
    }]
  })
}

resource "aws_lambda_function" "worker" {
  function_name    = "${local.name_prefix}-worker"
  role             = aws_iam_role.lambda.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.worker.output_path
  source_code_hash = data.archive_file.worker.output_base64sha256
  timeout          = 30
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/aws/lambda/${aws_lambda_function.worker.function_name}"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role_policy" "worker_logs" {
  name = "${local.name_prefix}-worker-logs"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = ["${aws_cloudwatch_log_group.worker.arn}:*"]
    }]
  })
}

resource "aws_lambda_event_source_mapping" "jobs" {
  event_source_arn        = aws_sqs_queue.jobs.arn
  function_name           = aws_lambda_function.worker.arn
  batch_size              = var.batch_size
  function_response_types = ["ReportBatchItemFailures"]
  depends_on              = [aws_iam_role_policy.consume]
}
