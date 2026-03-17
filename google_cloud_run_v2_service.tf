resource "google_cloud_run_v2_service" "anthropic_agent" {
  name     = "anthropic-agent"
  location = var.region
  project  = var.project_id

  template {
    service_account = google_service_account.functions_sa.email

    timeout = "300s"

    # 同時実行数を制限（ループ暴走防止）
    max_instance_request_concurrency = 4

    scaling {
      max_instance_count = 3
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/relics9/anthropic-agent:latest"

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "ANTHROPIC_CLAUDE_MODEL"
        value = var.anthropic_claude_model
      }
      env {
        name  = "GITHUB_USER"
        value = var.github_owner
      }
      env {
        name  = "PROJECT_ID"
        value = var.project_id
      }
      env {
        name  = "REPO_MAP"
        value = var.repo_map
      }
      env {
        name  = "SLACK_BOT_NAME"
        value = var.slack_bot_name
      }
      env {
        name  = "SOURCE_HASH"
        value = null_resource.build_anthropic_agent.triggers["source_hash"]
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
        name = "SLACK_BOT_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_bot_token.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "SLACK_SIGNING_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_signing_secret.secret_id
            version = "latest"
          }
        }
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
    }
  }

  depends_on = [null_resource.build_anthropic_agent]
}
