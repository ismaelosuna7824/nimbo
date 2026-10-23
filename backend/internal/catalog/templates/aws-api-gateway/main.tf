# The gateway, defined by its OpenAPI document: routes, Lambda
# integrations and the authorizer every route requires.
resource "aws_apigatewayv2_api" "http" {
  name          = local.name_prefix
  protocol_type = "HTTP"
  description   = "Single entry for the orders and users services."
  # Importing the document sets the API version from info.version.
  version = "1.0"
  body = jsonencode({
    openapi = "3.0.1"
    info    = { title = local.name_prefix, version = "1.0" }
    paths = {
      for name, fn in aws_lambda_function.service : "/${name}" => {
        get = {
          operationId = "list-${name}"
          security    = [{ bearer = [] }]
          "x-amazon-apigateway-integration" = {
            type                 = "aws_proxy"
            httpMethod           = "POST"
            uri                  = fn.invoke_arn
            payloadFormatVersion = "2.0"
          }
        }
      }
    }
    components = {
      securitySchemes = {
        bearer = {
          type = "apiKey"
          name = "Authorization"
          in   = "header"
          "x-amazon-apigateway-authorizer" = {
            type                           = "request"
            identitySource                 = "$request.header.Authorization"
            authorizerUri                  = aws_lambda_function.authorizer.invoke_arn
            authorizerPayloadFormatVersion = "2.0"
            enableSimpleResponses          = true
            authorizerResultTtlInSeconds   = 300
          }
        }
      }
    }
  })
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_rate_limit  = var.throttling_rate_limit
    throttling_burst_limit = var.throttling_burst_limit
  }
}

resource "aws_lambda_permission" "api" {
  for_each      = local.invoked_functions
  statement_id  = "AllowHttpApiInvoke"
  action        = "lambda:InvokeFunction"
  function_name = each.value
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http.execution_arn}/*"
}
