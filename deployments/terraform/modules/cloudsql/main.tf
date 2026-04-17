resource "random_id" "db_suffix" {
  byte_length = 4
}

resource "google_sql_database_instance" "pulse" {
  name             = "${var.name_prefix}-pg-${random_id.db_suffix.hex}"
  database_version = var.postgres_version
  region           = var.region

  settings {
    tier              = var.tier
    availability_type = var.name_prefix == "pulse-prod" ? "REGIONAL" : "ZONAL"
    disk_size         = 100
    disk_type         = "PD_SSD"
    disk_autoresize   = true

    backup_configuration {
      enabled                        = true
      start_time                     = "03:00"
      point_in_time_recovery_enabled = true
      backup_retention_settings {
        retained_backups = 7
      }
    }

    ip_configuration {
      ipv4_enabled    = false
      private_network = var.network
    }

    database_flags {
      name  = "max_connections"
      value = "200"
    }

    database_flags {
      name  = "shared_buffers"
      value = "256000"  # 256 MB in KB
    }

    insights_config {
      query_insights_enabled  = true
      query_string_length     = 1024
      record_application_tags = true
      record_client_address   = false
    }

    user_labels = var.labels
  }

  deletion_protection = var.name_prefix == "pulse-prod"

  lifecycle {
    prevent_destroy = false
  }
}

resource "google_sql_database" "pulse" {
  name     = "pulse"
  instance = google_sql_database_instance.pulse.name
}

resource "google_sql_user" "pulse" {
  name     = "pulse"
  instance = google_sql_database_instance.pulse.name
  password = var.postgres_password
}
