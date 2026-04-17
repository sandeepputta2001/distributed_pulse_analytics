output "cluster_name" {
  value = google_container_cluster.pulse.name
}

output "endpoint" {
  value     = google_container_cluster.pulse.endpoint
  sensitive = true
}

output "ca_certificate" {
  value     = google_container_cluster.pulse.master_auth[0].cluster_ca_certificate
  sensitive = true
}

output "node_service_account_email" {
  value = google_service_account.gke_nodes.email
}
