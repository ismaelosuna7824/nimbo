output "api_id" {
  value = aws_apigatewayv2_api.http.id
}

output "route_keys" {
  value = [for r in aws_apigatewayv2_route.orders : r.route_key]
}
