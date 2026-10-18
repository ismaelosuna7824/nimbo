output "topic_id" {
  value = google_pubsub_topic.events.id
}

output "subscription_id" {
  value = google_pubsub_subscription.worker.id
}

output "bucket_name" {
  value = google_storage_bucket.archive.name
}
