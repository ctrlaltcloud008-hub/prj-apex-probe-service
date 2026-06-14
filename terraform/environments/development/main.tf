# Apply order: prj-apex-ingestion-service (owns video.received + DLQ) must be
# applied before this repo; this module references those topics via data lookups.
module "pubsub" {
  source         = "../../modules/pubsub"
  project_id     = var.project_id
  project_region = var.project_region
  environment    = var.environment
}
