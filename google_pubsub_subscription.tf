# Push サブスクリプション: anthropic-agent の /notify エンドポイントへ転送
resource "google_pubsub_subscription" "error_logs_push" {
  name    = "error-logs-push"
  topic   = google_pubsub_topic.error_logs.name
  project = var.project_id

  ack_deadline_seconds = 300

  push_config {
    push_endpoint = "${google_cloud_run_v2_service.anthropic_agent.uri}/notify"

    oidc_token {
      service_account_email = google_service_account.functions_sa.email
    }
  }

  retry_policy {
    minimum_backoff = "60s"
    maximum_backoff = "600s"
  }

  # メッセージ保持期間（滞留メッセージを自動破棄）
  message_retention_duration = "3600s"

  depends_on = [google_cloud_run_v2_service.anthropic_agent]
}
