variable "credentials_file" {
  description = "Path to service account key JSON file"
  type        = string
  default     = "relics9-1dc2283b85f8.json"
}

variable "github_owner" {
  description = "GitHub repository owner"
  type        = string
  default     = "relics9"
}

variable "log_filter" {
  description = "Cloud Logging filter to capture error logs"
  type        = string
  default     = "severity>=ERROR"
}

variable "project_id" {
  description = "GCP Project ID"
  type        = string
  default     = "relics9"
}

variable "region" {
  description = "GCP Region"
  type        = string
  default     = "asia-northeast1"
}

variable "repo_map" {
  description = "Service name to GitHub repository mapping (e.g. \"service_name=git_repo_name,example-api=example,...\")"
  type        = string
  default     = ""
}

variable "slack_bot_name" {
  description = "Slack bot mention name (e.g. \"@Claude AI\")"
  type        = string
  default     = "@Claude AI"
}

variable "slack_bot_token" {
  description = "Slack Bot Token (xoxb-...) for AI agent to read messages"
  type        = string
  sensitive   = true
}

variable "slack_webhook_url" {
  description = "Slack Incoming Webhook URL for error notifications"
  type        = string
  sensitive   = true
}
