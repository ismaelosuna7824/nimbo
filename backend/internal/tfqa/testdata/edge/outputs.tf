output "queue_urls" {
  description = "URL per queue key"
  value       = { for k, q in aws_sqs_queue.queues : k => q.url }
}

output "handler_arn" {
  value      = aws_lambda_function.handler.arn
  depends_on = [aws_iam_role.handler]

  precondition {
    condition     = aws_lambda_function.handler.runtime != ""
    error_message = "The handler has no runtime."
  }
}

output "role" {
  value     = local.role_arn
  sensitive = true
} # end of outputs
