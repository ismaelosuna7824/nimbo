output "schedule_arn" {
  value = aws_scheduler_schedule.job.arn
}

output "function_name" {
  value = aws_lambda_function.job.function_name
}
