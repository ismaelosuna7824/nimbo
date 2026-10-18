moved {
  from = aws_sqs_queue.jobs
  to   = aws_sqs_queue.queues["jobs"]
}

# Adopt queues created outside Terraform.
import {
  for_each = var.existing_queues
  to       = aws_sqs_queue.imported[each.key]
  id       = each.value
}

removed {
  from = aws_s3_bucket.legacy

  lifecycle {
    destroy = false
  }
}

check "handler_reachable" {
  data "aws_lambda_function" "probe" {
    function_name = aws_lambda_function.handler.function_name
  }

  assert {
    condition     = data.aws_lambda_function.probe.last_modified != ""
    error_message = "The handler function is not deployed."
  }
}
