output "api_id" {
  value       = aws_apigatewayv2_api.http.id
  description = "HTTP API ID (the local sandbox URL is /execute-api/<id>/$default/items)."
}

output "api_endpoint" {
  value = aws_apigatewayv2_api.http.api_endpoint
}

output "table_name" {
  value = aws_dynamodb_table.items.name
}

output "function_name" {
  value = aws_lambda_function.api.function_name
}
