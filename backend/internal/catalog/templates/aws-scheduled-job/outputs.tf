output "schedule_arn" {
  value       = aws_scheduler_schedule.job.arn
  description = "ARN of the EventBridge schedule."
}

output "function_name" {
  value       = aws_lambda_function.job.function_name
  description = "Name of the Lambda function the schedule runs."
}
