resource "aws_dynamodb_table" "items" {
  name         = "${local.name_prefix}-items"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  server_side_encryption {
    enabled = true
  }
}

data "archive_file" "api" {
  type        = "zip"
  output_path = "${path.module}/build/api.zip"

  source {
    filename = "index.mjs"
    content  = <<-EOT
      import { randomUUID } from "node:crypto";
      import { DynamoDBClient, GetItemCommand, PutItemCommand, ScanCommand } from "@aws-sdk/client-dynamodb";

      const db = new DynamoDBClient({});
      const table = process.env.TABLE_NAME;

      const reply = (statusCode, body) => ({
        statusCode,
        headers: { "content-type": "application/json" },
        body: JSON.stringify(body),
      });

      const toItem = (record) => ({ id: record.id.S, ...JSON.parse(record.data?.S ?? "{}") });

      export const handler = async (event) => {
        const method = event.requestContext?.http?.method ?? "GET";
        const path = event.rawPath ?? "/";
        const id = path.match(/^\/items\/([^/]+)$/)?.[1];

        if (path === "/items" && method === "POST") {
          const data = event.body ? JSON.parse(event.isBase64Encoded ? Buffer.from(event.body, "base64").toString() : event.body) : {};
          const item = { id: randomUUID(), ...data };
          await db.send(new PutItemCommand({
            TableName: table,
            Item: { id: { S: item.id }, data: { S: JSON.stringify(data) } },
          }));
          return reply(201, item);
        }
        if (path === "/items" && method === "GET") {
          const out = await db.send(new ScanCommand({ TableName: table, Limit: 100 }));
          return reply(200, (out.Items ?? []).map(toItem));
        }
        if (id && method === "GET") {
          const out = await db.send(new GetItemCommand({ TableName: table, Key: { id: { S: id } } }));
          return out.Item ? reply(200, toItem(out.Item)) : reply(404, { message: "not found" });
        }
        return reply(200, { message: "Items API", routes: ["GET /items", "POST /items", "GET /items/{id}"] });
      };
    EOT
  }
}

resource "aws_iam_role" "lambda" {
  name = "${local.name_prefix}-lambda"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "items_access" {
  name = "${local.name_prefix}-items-access"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:Scan"]
      Resource = [aws_dynamodb_table.items.arn]
    }]
  })
}

resource "aws_lambda_function" "api" {
  function_name    = "${local.name_prefix}-handler"
  role             = aws_iam_role.lambda.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.api.output_path
  source_code_hash = data.archive_file.api.output_base64sha256
  memory_size      = var.lambda_memory_mb
  timeout          = 10

  environment {
    variables = {
      TABLE_NAME = aws_dynamodb_table.items.name
    }
  }
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/lambda/${aws_lambda_function.api.function_name}"
  retention_in_days = var.log_retention_days
}

resource "aws_iam_role_policy" "api_logs" {
  name = "${local.name_prefix}-api-logs"
  role = aws_iam_role.lambda.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = ["${aws_cloudwatch_log_group.api.arn}:*"]
    }]
  })
}

resource "aws_apigatewayv2_api" "http" {
  name          = local.name_prefix
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "api" {
  api_id                 = aws_apigatewayv2_api.http.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.api.invoke_arn
  integration_method     = "POST"
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_route" "proxy" {
  api_id    = aws_apigatewayv2_api.http.id
  route_key = "ANY /{proxy+}"
  target    = "integrations/${aws_apigatewayv2_integration.api.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true
}

resource "aws_lambda_permission" "api" {
  statement_id  = "AllowHttpApiInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.api.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http.execution_arn}/*/*"
}
