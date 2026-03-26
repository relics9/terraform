resource "google_service_account" "functions_sa" {
  account_id   = "cloud-functions-sa"
  display_name = "Cloud Functions Service Account"
  project      = var.project_id
}
