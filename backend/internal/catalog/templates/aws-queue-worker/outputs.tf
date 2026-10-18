output "queue_url" {
  value = aws_sqs_queue.jobs.url
}

output "dead_letter_queue_url" {
  value = aws_sqs_queue.dlq.url
}

output "function_name" {
  value = aws_lambda_function.worker.function_name
}
