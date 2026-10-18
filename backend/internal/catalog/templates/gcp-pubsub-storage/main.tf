resource "google_pubsub_topic" "events" {
  name = var.name
}

resource "google_pubsub_topic" "dead_letter" {
  name = "${var.name}-dead-letter"
}

resource "google_pubsub_subscription" "worker" {
  name                 = "${var.name}-worker"
  topic                = google_pubsub_topic.events.id
  ack_deadline_seconds = var.ack_deadline_seconds

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dead_letter.id
    max_delivery_attempts = 5
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "300s"
  }
}

resource "google_storage_bucket" "archive" {
  name                        = "${var.gcp_project}-${var.name}-archive"
  location                    = "US"
  force_destroy               = true
  uniform_bucket_level_access = true

  lifecycle_rule {
    condition {
      age = 30
    }
    action {
      type          = "SetStorageClass"
      storage_class = "NEARLINE"
    }
  }
}
