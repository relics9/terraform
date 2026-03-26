# 公開アクセス許可 (Slack Events APIからのWebhookを受け付けるため)
resource "google_cloud_run_v2_service_iam_member" "anthropic_agent_public" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.anthropic_agent.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
