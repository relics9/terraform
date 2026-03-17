resource "google_artifact_registry_repository" "docker" {
  project       = var.project_id
  location      = var.region
  repository_id = "relics9"
  format        = "DOCKER"

  depends_on = [google_project_service.apis]
}
