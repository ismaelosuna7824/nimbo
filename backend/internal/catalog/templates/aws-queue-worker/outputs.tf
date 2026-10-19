output "queue_url" {
  value       = aws_sqs_queue.jobs.url
  description = "URL of the jobs queue: send messages here."
}

output "dead_letter_queue_url" {
  value       = aws_sqs_queue.dlq.url
  description = "URL of the dead-letter queue that receives jobs which failed max_receive_count times."
}

output "function_name" {
  value       = aws_lambda_function.worker.function_name
  description = "Name of the Lambda function that processes the jobs."
}
