resource "google_pubsub_topic" "error_logs" {
  name    = "error-logs"
  project = var.project_id

  depends_on = [google_project_service.apis]
}
