
output "pubsub_topic_name" {
  description = "Pub/Sub topic for error logs"
  value       = google_pubsub_topic.error_logs.name
}

output "log_sink_name" {
  description = "Cloud Logging sink name"
  value       = google_logging_project_sink.error_logs.name
}

output "functions_service_account" {
  description = "Service account used by Cloud Functions"
  value       = google_service_account.functions_sa.email
}

output "slack_error_alert_analysis_channel_id" {
  description = "Slack #error-alert-analyse channel ID"
  value       = slack_conversation.error_alert_analysis.id
}

output "anthropic_agent_url" {
  description = "Anthropic Agent Cloud Run URL (Slack Events API Webhook URLに設定する)"
  value       = google_cloud_run_v2_service.anthropic_agent.uri
}
