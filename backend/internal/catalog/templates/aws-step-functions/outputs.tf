output "state_machine_arn" {
  value       = aws_sfn_state_machine.flow.arn
  description = "ARN of the state machine: start executions with it."
}

output "function_name" {
  value       = aws_lambda_function.check.function_name
  description = "Name of the Lambda function that checks each order."
}
