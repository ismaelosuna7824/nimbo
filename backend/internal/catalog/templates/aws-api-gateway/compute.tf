# One log group for every function of the API (Lambda advanced logging).
resource "aws_cloudwatch_log_group" "functions" {
  name              = "/aws/lambda/${local.name_prefix}"
  retention_in_days = var.log_retention_days
}

data "archive_file" "service" {
  for_each    = local.services
  type        = "zip"
  output_path = "${path.module}/build/${each.key}.zip"

  source {
    filename = "index.mjs"
    content  = <<-EOT
      // The ${each.key} service. The gateway already checked the caller and
      // passes who it is in the authorizer context.
      const records = ${jsonencode(each.value)};

      export const handler = async (event) => ({
        statusCode: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          service: "${each.key}",
          caller: event.requestContext?.authorizer?.lambda?.caller ?? null,
          items: records,
        }),
      });
    EOT
  }
}

resource "aws_lambda_function" "service" {
  for_each         = local.services
  function_name    = "${local.name_prefix}-${each.key}"
  role             = aws_iam_role.service.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.service[each.key].output_path
  source_code_hash = data.archive_file.service[each.key].output_base64sha256
  memory_size      = 128
  timeout          = 5

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.functions.name
  }
}

data "archive_file" "authorizer" {
  type        = "zip"
  output_path = "${path.module}/build/authorizer.zip"

  source {
    filename = "index.mjs"
    content  = <<-EOT
      // HTTP API Lambda authorizer (payload 2.0, simple responses): allows
      // requests whose Authorization header is "Bearer <token>".
      import { timingSafeEqual } from "node:crypto";
      import { GetParameterCommand, SSMClient } from "@aws-sdk/client-ssm";

      const ssm = new SSMClient({});
      let expected;

      const same = (a, b) => {
        const x = Buffer.from(a);
        const y = Buffer.from(b);
        return x.length === y.length && timingSafeEqual(x, y);
      };

      export const handler = async (event) => {
        if (expected === undefined) {
          const out = await ssm.send(new GetParameterCommand({ Name: process.env.TOKEN_PARAMETER, WithDecryption: true }));
          expected = "Bearer " + out.Parameter.Value;
        }
        const ok = same(event.headers?.authorization ?? "", expected);
        return { isAuthorized: ok, context: { caller: ok ? "api-client" : "anonymous" } };
      };
    EOT
  }
}

resource "aws_lambda_function" "authorizer" {
  function_name    = "${local.name_prefix}-authorizer"
  role             = aws_iam_role.authorizer.arn
  runtime          = "nodejs22.x"
  handler          = "index.handler"
  filename         = data.archive_file.authorizer.output_path
  source_code_hash = data.archive_file.authorizer.output_base64sha256
  memory_size      = 128
  timeout          = 5

  environment {
    variables = {
      TOKEN_PARAMETER = aws_ssm_parameter.api_token.name
    }
  }

  logging_config {
    log_format = "Text"
    log_group  = aws_cloudwatch_log_group.functions.name
  }
}
