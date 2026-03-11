# ==============================================================================
# ローカルファイルからトークンを読み込む
# 各JSONファイルはプレーンテキスト形式
# ==============================================================================
locals {
  # Slack App Configuration Token (slack-token.json)
  _slack_content = file("${path.module}/slack-token.json")
  slack_token    = trimspace(regex("Access Token\n([^\n]+)", local._slack_content)[0])

  # GitHub Personal Access Token (github-token.json)
  _github_content = file("${path.module}/github-token.json")
  github_token    = trimspace(regex("(ghp_[A-Za-z0-9]+)", local._github_content)[0])

  # Anthropic API Key (claude-token.json)
  _claude_content   = file("${path.module}/claude-token.json")
  anthropic_api_key = trimspace(regex("(sk-ant-[A-Za-z0-9_\\-]+)", local._claude_content)[0])
}
