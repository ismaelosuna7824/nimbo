output "api_id" {
  value       = aws_apigatewayv2_api.http.id
  description = "HTTP API ID (the local sandbox URL is /execute-api/<id>/$default/items)."
}

output "api_endpoint" {
  value       = aws_apigatewayv2_api.http.api_endpoint
  description = "Base URL of the HTTP API."
}

output "table_name" {
  value       = aws_dynamodb_table.items.name
  description = "Name of the DynamoDB table that stores the items."
}

output "function_name" {
  value       = aws_lambda_function.api.function_name
  description = "Name of the Lambda function behind the API."
}
