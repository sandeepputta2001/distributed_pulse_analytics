output "network_self_link" {
  value = google_compute_network.pulse.self_link
}

output "network_name" {
  value = google_compute_network.pulse.name
}

output "subnetwork_self_link" {
  value = google_compute_subnetwork.pulse.self_link
}

output "subnetwork_name" {
  value = google_compute_subnetwork.pulse.name
}

output "pods_cidr_name" {
  value = "${var.name_prefix}-pods"
}

output "services_cidr_name" {
  value = "${var.name_prefix}-services"
}
