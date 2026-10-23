output "api_url" {
  value       = oci_apigateway_deployment.v1.endpoint
  description = "Base URL of the deployment: GET /orders and GET /users with an Authorization header the authorizer accepts."
}

output "gateway_hostname" {
  value       = oci_apigateway_gateway.main.hostname
  description = "Host name of the API gateway."
}

output "function_ids" {
  value       = { for name, fn in oci_functions_function.fn : name => fn.id }
  description = "OCIDs of the functions behind the routes and of the authorizer."
}
