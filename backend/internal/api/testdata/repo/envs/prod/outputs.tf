output "vpc_id" {
  value = module.network.vpc_id
}

output "function_name" {
  value = aws_lambda_function.api.function_name
}
