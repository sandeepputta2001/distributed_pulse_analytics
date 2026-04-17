output "connection_name" {
  value = google_sql_database_instance.pulse.connection_name
}

output "private_ip" {
  value = google_sql_database_instance.pulse.private_ip_address
}

output "instance_name" {
  value = google_sql_database_instance.pulse.name
}
