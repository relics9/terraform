# Log SinkのサービスアカウントにPub/Sub publish権限を付与
resource "google_pubsub_topic_iam_member" "log_sink_pubsub" {
  project = var.project_id
  topic   = google_pubsub_topic.error_logs.name
  role    = "roles/pubsub.publisher"
  member  = google_logging_project_sink.error_logs.writer_identity
}
