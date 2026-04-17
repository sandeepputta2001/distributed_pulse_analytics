module "pulse" {
  source = "../../"

  project_id   = var.project_id
  region       = "us-central1"
  zone         = "us-central1-a"
  environment  = "dev"
  cluster_name = "pulse-analytics"

  # Smaller / cheaper nodes for dev
  app_node_machine_type = "e2-standard-2"
  app_node_min_count    = 1
  app_node_max_count    = 3

  postgres_tier     = "db-g1-small"
  postgres_version  = "POSTGRES_16"
  postgres_password = var.postgres_password

  redis_memory_size_gb = 1
  redis_tier           = "BASIC"

  jwt_secret = var.jwt_secret
  image_tag  = var.image_tag
}
