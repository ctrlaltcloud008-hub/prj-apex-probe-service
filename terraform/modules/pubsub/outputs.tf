output "video_validated_topic_id" {
  description = "The ID of the video.validated topic owned by the probe service."
  value       = google_pubsub_topic.owned["video.validated"].id
}

output "probe_subscription_name" {
  description = "Name of the probe service's pull subscription on video.received."
  value       = google_pubsub_subscription.probe.name
}

output "probe_dlq_subscription_name" {
  description = "Name of the probe service's DLQ handler subscription."
  value       = google_pubsub_subscription.probe_dlq.name
}
