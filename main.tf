# ==============================================================================
# APIs を有効化
# ==============================================================================
resource "google_project_service" "apis" {
  for_each = toset([
    "cloudfunctions.googleapis.com",
    "cloudbuild.googleapis.com",
    "pubsub.googleapis.com",
    "logging.googleapis.com",
    "secretmanager.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "run.googleapis.com",
    "artifactregistry.googleapis.com",
    "eventarc.googleapis.com",
  ])

  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

# ==============================================================================
# Secret Manager - 機密情報の管理
# ==============================================================================
resource "google_secret_manager_secret" "slack_webhook_url" {
  secret_id = "slack-webhook-url"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "slack_webhook_url" {
  secret      = google_secret_manager_secret.slack_webhook_url.id
  secret_data = var.slack_webhook_url
}

resource "google_secret_manager_secret" "slack_bot_token" {
  secret_id = "slack-bot-token"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "slack_bot_token" {
  secret      = google_secret_manager_secret.slack_bot_token.id
  secret_data = var.slack_bot_token
}

resource "google_secret_manager_secret" "github_token" {
  secret_id = "github-token"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "github_token" {
  secret      = google_secret_manager_secret.github_token.id
  secret_data = local.github_token
}

resource "google_secret_manager_secret" "anthropic_api_key" {
  secret_id = "anthropic-api-key"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "anthropic_api_key" {
  secret      = google_secret_manager_secret.anthropic_api_key.id
  secret_data = local.anthropic_api_key
}

# ==============================================================================
# Pub/Sub - ログイベントのメッセージキュー
# ==============================================================================
resource "google_pubsub_topic" "error_logs" {
  name    = "error-logs"
  project = var.project_id

  depends_on = [google_project_service.apis]
}

resource "google_pubsub_subscription" "error_logs_sub" {
  name    = "error-logs-sub"
  topic   = google_pubsub_topic.error_logs.name
  project = var.project_id

  ack_deadline_seconds = 60

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "300s"
  }
}

# ==============================================================================
# Cloud Logging Sink - エラーログをPub/Subへルーティング
# ==============================================================================
resource "google_logging_project_sink" "error_logs" {
  name        = "error-logs-to-pubsub"
  project     = var.project_id
  destination = "pubsub.googleapis.com/${google_pubsub_topic.error_logs.id}"
  filter      = var.log_filter

  unique_writer_identity = true
}

# Log SinkのサービスアカウントにPub/Sub publish権限を付与
resource "google_pubsub_topic_iam_member" "log_sink_pubsub" {
  project = var.project_id
  topic   = google_pubsub_topic.error_logs.name
  role    = "roles/pubsub.publisher"
  member  = google_logging_project_sink.error_logs.writer_identity
}

# ==============================================================================
# Cloud Functions用サービスアカウント
# ==============================================================================
resource "google_service_account" "functions_sa" {
  account_id   = "cloud-functions-sa"
  display_name = "Cloud Functions Service Account"
  project      = var.project_id
}

# Secret Managerへのアクセス権
resource "google_project_iam_member" "functions_secret_accessor" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.functions_sa.email}"
}

# Pub/Sub サブスクライバー権限
resource "google_project_iam_member" "functions_pubsub_subscriber" {
  project = var.project_id
  role    = "roles/pubsub.subscriber"
  member  = "serviceAccount:${google_service_account.functions_sa.email}"
}

# ログ書き込み権限
resource "google_project_iam_member" "functions_log_writer" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.functions_sa.email}"
}

# ==============================================================================
# Cloud Storage - Cloud Functionsのソースコード保存
# ==============================================================================
resource "google_storage_bucket_object" "slack_notifier_source" {
  name   = "functions/slack-notifier-${filemd5("${path.module}/functions/slack_notifier/main.py")}.zip"
  bucket = "relics9"
  source = data.archive_file.slack_notifier.output_path
}

data "archive_file" "slack_notifier" {
  type        = "zip"
  output_path = "/tmp/slack_notifier.zip"
  source_dir  = "${path.module}/functions/slack_notifier"
}

resource "google_storage_bucket_object" "ai_agent_source" {
  name   = "functions/ai-agent-${filemd5("${path.module}/functions/ai_agent/main.py")}.zip"
  bucket = "relics9"
  source = data.archive_file.ai_agent.output_path
}

data "archive_file" "ai_agent" {
  type        = "zip"
  output_path = "/tmp/ai_agent.zip"
  source_dir  = "${path.module}/functions/ai_agent"
}

# ==============================================================================
# Cloud Function 1: Slack Notifier
# Pub/Subからエラーログを受け取りSlackに通知
# ==============================================================================
resource "google_cloudfunctions2_function" "slack_notifier" {
  name     = "slack-notifier"
  location = var.region
  project  = var.project_id

  build_config {
    runtime     = "python311"
    entry_point = "notify_slack"

    source {
      storage_source {
        bucket = "relics9"
        object = google_storage_bucket_object.slack_notifier_source.name
      }
    }
  }

  service_config {
    max_instance_count = 10
    min_instance_count = 0
    available_memory   = "256M"
    timeout_seconds    = 60

    service_account_email = google_service_account.functions_sa.email

    environment_variables = {
      PROJECT_ID       = var.project_id
      SLACK_CHANNEL_ID = slack_conversation.error_alerts.id
    }

    secret_environment_variables {
      key        = "SLACK_WEBHOOK_URL"
      project_id = var.project_id
      secret     = google_secret_manager_secret.slack_webhook_url.secret_id
      version    = "latest"
    }
  }

  event_trigger {
    trigger_region = var.region
    event_type     = "google.cloud.pubsub.topic.v1.messagePublished"
    pubsub_topic   = google_pubsub_topic.error_logs.id
    retry_policy   = "RETRY_POLICY_RETRY"
  }

  depends_on = [google_project_service.apis]
}

# ==============================================================================
# Cloud Function 2: AI Agent
# SlackのBotメンションを受けてClaudeでエラー分析 & GitHub PR作成
# ==============================================================================
resource "google_cloudfunctions2_function" "ai_agent" {
  name     = "ai-agent"
  location = var.region
  project  = var.project_id

  build_config {
    runtime     = "python311"
    entry_point = "handle_slack_event"

    source {
      storage_source {
        bucket = "relics9"
        object = google_storage_bucket_object.ai_agent_source.name
      }
    }
  }

  service_config {
    max_instance_count = 5
    min_instance_count = 0
    available_memory   = "512M"
    timeout_seconds    = 300

    service_account_email = google_service_account.functions_sa.email

    environment_variables = {
      PROJECT_ID   = var.project_id
      GITHUB_OWNER = var.github_owner
      GITHUB_REPO  = var.github_repo
    }

    secret_environment_variables {
      key        = "SLACK_BOT_TOKEN"
      project_id = var.project_id
      secret     = google_secret_manager_secret.slack_bot_token.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "GITHUB_TOKEN"
      project_id = var.project_id
      secret     = google_secret_manager_secret.github_token.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "ANTHROPIC_API_KEY"
      project_id = var.project_id
      secret     = google_secret_manager_secret.anthropic_api_key.secret_id
      version    = "latest"
    }
  }

  depends_on = [google_project_service.apis]
}

# AI AgentのCloud Functionを公開 (Slack Events APIからのWebhookを受け付けるため)
resource "google_cloud_run_service_iam_member" "ai_agent_public" {
  project  = var.project_id
  location = var.region
  service  = google_cloudfunctions2_function.ai_agent.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
