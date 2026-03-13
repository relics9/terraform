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

# Push サブスクリプション: anthropic-agent の /notify エンドポイントへ転送
resource "google_pubsub_subscription" "error_logs_push" {
  name    = "error-logs-push"
  topic   = google_pubsub_topic.error_logs.name
  project = var.project_id

  ack_deadline_seconds = 300

  push_config {
    push_endpoint = "${google_cloud_run_v2_service.anthropic_agent.uri}/notify"

    oidc_token {
      service_account_email = google_service_account.functions_sa.email
    }
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "300s"
  }

  depends_on = [google_cloud_run_v2_service.anthropic_agent]
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

# (Cloud Functions は anthropic-agent Cloud Run に統合済み)

# ==============================================================================
# Artifact Registry - Dockerイメージ保存
# ==============================================================================
resource "google_artifact_registry_repository" "docker" {
  project       = var.project_id
  location      = var.region
  repository_id = "relics9"
  format        = "DOCKER"

  depends_on = [google_project_service.apis]
}

# ==============================================================================
# Cloud Run Service: Anthropic Agent (Go)
# ==============================================================================

# Dockerイメージをビルド & プッシュ (Cloud Build使用)
resource "null_resource" "build_anthropic_agent" {
  triggers = {
    source_hash = sha256(join("", [
      for f in sort(fileset("${path.module}/services/anthropic_agent", "**")) :
      filesha256("${path.module}/services/anthropic_agent/${f}")
    ]))
  }

  provisioner "local-exec" {
    command = "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=${path.module}/${var.credentials_file} gcloud builds submit ${path.module}/services/anthropic_agent --tag ${var.region}-docker.pkg.dev/${var.project_id}/relics9/anthropic-agent:latest --project=${var.project_id}"
  }

  depends_on = [google_artifact_registry_repository.docker]
}

resource "google_cloud_run_v2_service" "anthropic_agent" {
  name     = "anthropic-agent"
  location = var.region
  project  = var.project_id

  template {
    service_account = google_service_account.functions_sa.email

    timeout = "300s"

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/relics9/anthropic-agent:latest"

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "PROJECT_ID"
        value = var.project_id
      }
      env {
        name  = "GITHUB_USER"
        value = var.github_owner
      }
      env {
        name  = "REPO_MAP"
        value = var.repo_map
      }

      env {
        name = "SLACK_WEBHOOK_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_webhook_url.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "SLACK_BOT_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_bot_token.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "GITHUB_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.github_token.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "ANTHROPIC_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.anthropic_api_key.secret_id
            version = "latest"
          }
        }
      }
    }
  }

  depends_on = [null_resource.build_anthropic_agent]
}

# 公開アクセス許可 (Slack Events APIからのWebhookを受け付けるため)
resource "google_cloud_run_v2_service_iam_member" "anthropic_agent_public" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.anthropic_agent.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
