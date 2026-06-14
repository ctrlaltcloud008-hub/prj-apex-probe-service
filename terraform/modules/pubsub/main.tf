locals {
  default_labels = {
    team        = "apex"
    environment = var.environment
    managed_by  = "terraform"
  }

  # Topics owned by this service: probe is the logical producer of
  # video.validated (written via the outbox, delivered by the relay).
  owned_topics = [
    "video.validated",
  ]
}

# video.received and its DLQ are owned by prj-apex-ingestion-service
# (the logical producer of the video.received event).
data "google_pubsub_topic" "video_received" {
  project = var.project_id
  name    = "video.received"
}

data "google_pubsub_topic" "video_received_dlq" {
  project = var.project_id
  name    = "video.received.dlq"
}

resource "google_pubsub_topic" "owned" {
  for_each = toset(local.owned_topics)

  project = var.project_id
  name    = each.value

  message_retention_duration = var.message_retention_duration
  labels                     = local.default_labels
}

# Main work subscription: probe consumes video.received.
resource "google_pubsub_subscription" "probe" {
  project = var.project_id
  name    = "sub-probe"
  topic   = data.google_pubsub_topic.video_received.id

  ack_deadline_seconds       = var.ack_deadline_seconds
  message_retention_duration = var.message_retention_duration
  retain_acked_messages      = false

  retry_policy {
    minimum_backoff = var.retry_minimum_backoff
    maximum_backoff = var.retry_maximum_backoff
  }

  dead_letter_policy {
    dead_letter_topic     = data.google_pubsub_topic.video_received_dlq.id
    max_delivery_attempts = var.dlq_max_delivery_attempts
  }

  labels = local.default_labels
}

# DLQ subscription: the probe service's DLQ handler marks exhausted
# video.received messages as FAILED in Spanner.
resource "google_pubsub_subscription" "probe_dlq" {
  project = var.project_id
  name    = "sub-probe-dlq"
  topic   = data.google_pubsub_topic.video_received_dlq.id

  ack_deadline_seconds       = 60
  message_retention_duration = var.message_retention_duration
  retain_acked_messages      = false

  retry_policy {
    minimum_backoff = var.retry_minimum_backoff
    maximum_backoff = var.retry_maximum_backoff
  }

  labels = local.default_labels
}
