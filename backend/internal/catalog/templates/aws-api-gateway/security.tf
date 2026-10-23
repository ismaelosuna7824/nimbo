# The bearer token clients send; only the authorizer reads it.
resource "random_password" "api_token" {
  length  = 40
  special = false
}

resource "aws_ssm_parameter" "api_token" {
  name        = "/${var.project}/${var.environment}/api-token"
  description = "Bearer token the API authorizer accepts."
  type        = "SecureString"
  value       = random_password.api_token.result
}
