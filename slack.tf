# ==============================================================================
# Slack Provider
# チャンネル管理には channels:manage スコープを持つ Bot Token が必要
# ==============================================================================
provider "slack" {
  token = var.slack_bot_token
}

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
