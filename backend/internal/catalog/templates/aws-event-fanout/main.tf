resource "aws_sns_topic" "events" {
  name = "${var.name}-events"
}

resource "aws_sqs_queue" "consumer" {
  for_each = toset(var.consumers)
  name     = "${var.name}-${each.key}"
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
