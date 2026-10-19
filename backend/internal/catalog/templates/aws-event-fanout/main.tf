resource "aws_kms_key" "events" {
  description             = "Encrypts messages at rest in the ${local.name_prefix}-events topic"
  enable_key_rotation     = true
  deletion_window_in_days = 7
}

resource "aws_sns_topic" "events" {
  name = "${local.name_prefix}-events"
  # Publishers need kms:GenerateDataKey and kms:Decrypt on this key.
  kms_master_key_id = aws_kms_key.events.arn
}

resource "aws_sqs_queue" "consumer" {
  for_each                = toset(var.consumers)
  name                    = "${local.name_prefix}-${each.key}"
  sqs_managed_sse_enabled = true
}

data "aws_iam_policy_document" "allow_topic" {
  for_each = aws_sqs_queue.consumer

  statement {
    sid       = "AllowTopicToSendMessage"
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [each.value.arn]

    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }

    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_sns_topic.events.arn]
    }
  }
}

resource "aws_sqs_queue_policy" "consumer" {
  for_each  = aws_sqs_queue.consumer
  queue_url = each.value.url
  policy    = data.aws_iam_policy_document.allow_topic[each.key].json
}

resource "aws_sns_topic_subscription" "consumer" {
  for_each             = aws_sqs_queue.consumer
  topic_arn            = aws_sns_topic.events.arn
  protocol             = "sqs"
  endpoint             = each.value.arn
  raw_message_delivery = true
}
