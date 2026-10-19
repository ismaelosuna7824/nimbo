resource "google_pubsub_topic" "events" {
  name   = "${local.name_prefix}-events"
  labels = local.common_labels
}

resource "google_pubsub_topic" "dead_letter" {
  name   = "${local.name_prefix}-dead-letter"
  labels = local.common_labels
}

resource "google_pubsub_subscription" "worker" {
  name                 = "${local.name_prefix}-worker"
  topic                = google_pubsub_topic.events.id
  ack_deadline_seconds = var.ack_deadline_seconds
  labels               = local.common_labels

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
  name                        = "${var.gcp_project}-${local.name_prefix}-archive"
  location                    = "US"
  force_destroy               = true
  uniform_bucket_level_access = true
  labels                      = local.common_labels

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
