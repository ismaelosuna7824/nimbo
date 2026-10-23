data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

# One role per kind of function: the authorizer alone may read the token.
resource "aws_iam_role" "service" {
  name               = "${local.name_prefix}-service"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role" "authorizer" {
  name               = "${local.name_prefix}-authorizer"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy" "logs" {
  for_each = { service = aws_iam_role.service.id, authorizer = aws_iam_role.authorizer.id }
  name     = "${local.name_prefix}-${each.key}-logs"
  role     = each.value
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = ["${aws_cloudwatch_log_group.functions.arn}:*"]
    }]
  })
}

resource "aws_iam_role_policy" "read_token" {
  name = "${local.name_prefix}-read-token"
  role = aws_iam_role.authorizer.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ssm:GetParameter"]
      Resource = [aws_ssm_parameter.api_token.arn]
    }]
  })
}
