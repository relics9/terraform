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

variable "credentials_file" {
  description = "Path to service account key JSON file"
  type        = string
  default     = "relics9-1dc2283b85f8.json"
}

variable "slack_webhook_url" {
  description = "Slack Incoming Webhook URL for error notifications"
  type        = string
  sensitive   = true
}

variable "slack_bot_token" {
  description = "Slack Bot Token (xoxb-...) for AI agent to read messages"
  type        = string
  sensitive   = true
}

variable "github_owner" {
  description = "GitHub repository owner"
  type        = string
  default     = "relics9"
}

variable "repo_map" {
  description = "Service name to GitHub repository mapping (e.g. \"example-api1=example1,example-api2=example2,foo-svc=foo,...\")"
  type        = string
  default     = ""
}

variable "log_filter" {
  description = "Cloud Logging filter to capture error logs"
  type        = string
  default     = "severity>=ERROR"
}
