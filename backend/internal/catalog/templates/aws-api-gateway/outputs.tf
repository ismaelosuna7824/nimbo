output "api_id" {
  value       = aws_apigatewayv2_api.http.id
  description = "HTTP API ID (the local sandbox URL is /execute-api/<id>/$default/orders)."
}

output "api_endpoint" {
  value       = aws_apigatewayv2_api.http.api_endpoint
  description = "Base URL of the gateway: GET /orders and GET /users with Authorization: Bearer <api_token>."
}

output "api_token" {
  value       = random_password.api_token.result
  sensitive   = true
  description = "Bearer token the authorizer accepts (terraform output -raw api_token)."
}

output "token_parameter" {
  value       = aws_ssm_parameter.api_token.name
  description = "SSM SecureString parameter that holds the token."
}

output "service_functions" {
  value       = { for name, fn in aws_lambda_function.service : name => fn.function_name }
  description = "Lambda function behind each route."
}
