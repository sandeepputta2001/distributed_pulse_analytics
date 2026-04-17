# Workload Identity service accounts — one per microservice that needs
# GCP resource access (Secret Manager, Cloud SQL, etc.)

locals {
  services = [
    "gateway",
    "auth",
    "enricher",
    "session",
    "funnel",
    "chwriter",
    "query-api",
    "alert-engine",
    "notification",
  ]
}

resource "google_service_account" "workload" {
  for_each     = toset(local.services)
  account_id   = "${var.name_prefix}-${each.key}"
  display_name = "PulseAnalytics ${each.key} workload SA"
}

# Grant Secret Manager access to all service SAs
resource "google_project_iam_member" "secret_accessor" {
  for_each = toset(local.services)
  project  = var.project_id
  role     = "roles/secretmanager.secretAccessor"
  member   = "serviceAccount:${google_service_account.workload[each.key].email}"
}

# Workload Identity binding — maps k8s SA → GCP SA
# The k8s SA must be created in the 'pulse' namespace with:
#   iam.gke.io/gcp-service-account: <GSA_EMAIL>
resource "google_service_account_iam_member" "workload_identity" {
  for_each           = toset(local.services)
  service_account_id = google_service_account.workload[each.key].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[pulse/${each.key}]"
}

# Cloud SQL client role for services that need DB access
resource "google_project_iam_member" "cloudsql_client" {
  for_each = toset(["gateway", "auth", "query-api", "alert-engine", "notification", "funnel"])
  project  = var.project_id
  role     = "roles/cloudsql.client"
  member   = "serviceAccount:${google_service_account.workload[each.key].email}"
}
