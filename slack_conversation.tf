
# ==============================================================================
# Slack チャンネル
# ==============================================================================

resource "slack_conversation" "error_alert_analysis" {
  name                   = "error-alert-analysis"
  topic                  = "GCP Error Alerts & AI Analysis"
  purpose                = "Monitors GCP error logs, analyzes them with Claude AI, and auto-creates GitHub PRs/Issues"
  is_private             = false
  action_on_destroy      = "archive"
  adopt_existing_channel = true
}
