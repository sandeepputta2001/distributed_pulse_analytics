resource "google_redis_instance" "pulse" {
  name           = "${var.name_prefix}-redis"
  tier           = var.tier
  memory_size_gb = var.memory_size_gb
  region         = var.region

  authorized_network = var.network

  redis_version     = "REDIS_7_0"
  display_name      = "PulseAnalytics Redis — ${var.name_prefix}"
  reserved_ip_range = "10.48.0.0/29"

  auth_enabled = true

  persistence_config {
    persistence_mode    = "RDB"
    rdb_snapshot_period = "TWENTY_FOUR_HOURS"
  }

  maintenance_policy {
    weekly_maintenance_window {
      day = "SUNDAY"
      start_time {
        hours   = 3
        minutes = 0
        seconds = 0
        nanos   = 0
      }
    }
  }

  labels = var.labels
}
