output "topic_id" {
  value       = google_pubsub_topic.events.id
  description = "ID of the topic: publish events here."
}

output "subscription_id" {
  value       = google_pubsub_subscription.worker.id
  description = "ID of the worker's pull subscription."
}

output "bucket_name" {
  value       = google_storage_bucket.archive.name
  description = "Name of the archive bucket."
}
