// A second locals block in another file.
locals {
  queue_count = length(var.queues)
  splat_names = values(aws_sqs_queue.queues)[*].name

  # left for later:
  # unused = true
}
