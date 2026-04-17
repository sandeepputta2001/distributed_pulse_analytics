module "pulse" {
  source = "../../"

  project_id   = var.project_id
  region       = "us-central1"
  zone         = "us-central1-a"
  environment  = "prod"
  cluster_name = "pulse-analytics"

  # Production sizing
  app_node_machine_type = "e2-standard-8"
  app_node_min_count    = 3
  app_node_max_count    = 20

  postgres_tier     = "db-custom-4-15360"
  postgres_version  = "POSTGRES_16"
  postgres_password = var.postgres_password

  redis_memory_size_gb = 8
  redis_tier           = "STANDARD_HA"

  jwt_secret = var.jwt_secret
  image_tag  = var.image_tag
}
