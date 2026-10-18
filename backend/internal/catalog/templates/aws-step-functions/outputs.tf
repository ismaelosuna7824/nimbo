output "state_machine_arn" {
  value = aws_sfn_state_machine.flow.arn
}

output "function_name" {
  value = aws_lambda_function.check.function_name
}
