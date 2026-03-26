resource "google_logging_project_sink" "error_logs" {
  name        = "error-logs-to-pubsub"
  project     = var.project_id
  destination = "pubsub.googleapis.com/${google_pubsub_topic.error_logs.id}"
  filter      = var.log_filter

  unique_writer_identity = true
}
