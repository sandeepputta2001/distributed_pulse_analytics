module "pulse" {
  source = "../../"

  project_id   = var.project_id
  region       = "us-central1"
  zone         = "us-central1-a"
  environment  = "staging"
  cluster_name = "pulse-analytics"

  app_node_machine_type = "e2-standard-4"
  app_node_min_count    = 1
  app_node_max_count    = 5

  postgres_tier     = "db-custom-2-7680"
  postgres_version  = "POSTGRES_16"
  postgres_password = var.postgres_password

  redis_memory_size_gb = 2
  redis_tier           = "STANDARD_HA"

  jwt_secret = var.jwt_secret
  image_tag  = var.image_tag
}
