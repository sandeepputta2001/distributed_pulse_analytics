output "host" {
  value = google_redis_instance.pulse.host
}

output "port" {
  value = google_redis_instance.pulse.port
}

output "auth_string" {
  value     = google_redis_instance.pulse.auth_string
  sensitive = true
}
