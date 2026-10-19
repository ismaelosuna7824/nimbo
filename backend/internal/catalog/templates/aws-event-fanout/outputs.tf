output "topic_arn" {
  value       = aws_sns_topic.events.arn
  description = "ARN of the topic: publish events here."
}

output "queue_urls" {
  value       = { for k, q in aws_sqs_queue.consumer : k => q.url }
  description = "URL of each consumer's queue, keyed by consumer name."
}
