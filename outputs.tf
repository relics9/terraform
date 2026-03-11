output "slack_notifier_function_url" {
  description = "Slack Notifier Cloud Function URL"
  value       = google_cloudfunctions2_function.slack_notifier.service_config[0].uri
}

output "ai_agent_function_url" {
  description = "AI Agent Cloud Function URL (Slack Events API Webhook URLに設定する)"
  value       = google_cloudfunctions2_function.ai_agent.service_config[0].uri
}

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

output "slack_error_alerts_channel_id" {
  description = "Slack #gcp-error-alerts チャンネルID"
  value       = slack_conversation.error_alerts.id
}

output "slack_ai_ops_channel_id" {
  description = "Slack #ai-ops チャンネルID"
  value       = slack_conversation.ai_ops.id
}
