# Dockerイメージをビルド & プッシュ (Cloud Build使用)
resource "null_resource" "build_anthropic_agent" {
  triggers = {
    source_hash = sha256(join("", [
      for f in sort(fileset(var.anthropic_agent_path, "**")) :
      filesha256("${var.anthropic_agent_path}/${f}")
    ]))
  }

  provisioner "local-exec" {
    command = "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=${path.module}/${var.credentials_file} gcloud builds submit ${var.anthropic_agent_path} --tag ${var.region}-docker.pkg.dev/${var.project_id}/relics9/anthropic-agent:latest --project=${var.project_id}"
  }

  depends_on = [google_artifact_registry_repository.docker]
}
