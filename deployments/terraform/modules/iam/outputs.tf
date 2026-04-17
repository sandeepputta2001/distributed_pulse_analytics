output "workload_sa_emails" {
  description = "Map of service name → GCP service account email"
  value = {
    for svc, sa in google_service_account.workload :
    svc => sa.email
  }
}
