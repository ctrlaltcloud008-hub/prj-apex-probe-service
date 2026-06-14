variable "project_id" {
  type        = string
  description = "The unique identifier for the GCP project for resource organization and billing."
  validation {
    condition     = length(var.project_id) > 0
    error_message = "The project_id must not be empty."
  }
}

variable "project_region" {
  type        = string
  description = "The GCP region where the resources will be deployed, impacting latency and compliance."
  validation {
    condition     = length(var.project_region) > 0
    error_message = "The project_region must be specified."
  }
}

variable "message_retention_duration" {
  type        = string
  description = "The duration for which messages are retained in Pub/Sub topics, specified in seconds (e.g., '604800s' for 7 days)."
  default     = "604800s" # Default to 7 days
}

variable "retry_minimum_backoff" {
  type        = string
  description = "Minimum backoff for retry policy"
  default     = "10s"
}

variable "retry_maximum_backoff" {
  type        = string
  description = "Maximum backoff for retry policy"
  default     = "600s"
}

variable "ack_deadline_seconds" {
  type        = number
  description = "Ack deadline in seconds for the probe pull subscription. ffprobe runs up to 5 minutes; the client extends the deadline while processing."
  default     = 120
}

variable "dlq_max_delivery_attempts" {
  type        = number
  description = "Max delivery attempts before routing to DLQ"
  default     = 5
}

variable "environment" {
  type        = string
  description = "The deployment environment (e.g., dev, staging, prod) for resource organization and management."
}
