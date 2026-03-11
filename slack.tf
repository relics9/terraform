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

# GCPエラー通知チャンネル
resource "slack_conversation" "error_alerts" {
  name                   = "gcp-error-alerts"
  topic                  = "GCPエラー自動通知 | AI分析 → GitHub PR自動作成"
  purpose                = "GCPのエラーログを監視し、AIがエラーを分析してGitHub PRを自動作成するチャンネルです"
  is_private             = false
  action_on_destroy      = "archive"
  adopt_existing_channel = true
}

# AI分析・PR通知チャンネル
resource "slack_conversation" "ai_ops" {
  name                   = "ai-ops"
  topic                  = "AI Agent による自動修正通知"
  purpose                = "AI AgentがGitHub PRを作成した際の通知チャンネルです"
  is_private             = false
  action_on_destroy      = "archive"
  adopt_existing_channel = true
}
