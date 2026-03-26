terraform {
  required_version = ">= 1.5.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    slack = {
      source  = "pablovarela/slack"
      version = "~> 1.2"
    }
  }

  backend "gcs" {
    bucket = "relics9"
    prefix = "terraform/state"
  }
}

provider "google" {
  project     = var.project_id
  region      = var.region
  credentials = file(var.credentials_file)
}

# ==============================================================================
# Slack Provider
# チャンネル管理には channels:manage スコープを持つ Bot Token が必要
# ==============================================================================
provider "slack" {
  token = var.slack_bot_token
}
