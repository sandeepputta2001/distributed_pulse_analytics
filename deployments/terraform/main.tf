locals {
  name_prefix = "pulse-${var.environment}"

  common_labels = {
    project     = "pulse-analytics"
    environment = var.environment
    managed_by  = "terraform"
  }
}

# ── Providers ─────────────────────────────────────────────────────────────────

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "google-beta" {
  project = var.project_id
  region  = var.region
}

provider "kubernetes" {
  host                   = "https://${module.gke.endpoint}"
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = base64decode(module.gke.ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = "https://${module.gke.endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(module.gke.ca_certificate)
  }
}

provider "confluent" {
  cloud_api_key    = var.kafka_api_key
  cloud_api_secret = var.kafka_api_secret
}

data "google_client_config" "default" {}

# ── Enable APIs ───────────────────────────────────────────────────────────────

resource "google_project_service" "apis" {
  for_each = toset([
    "container.googleapis.com",
    "sqladmin.googleapis.com",
    "redis.googleapis.com",
    "artifactregistry.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "iam.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
  ])
  service            = each.key
  disable_on_destroy = false
}

# ── Artifact Registry ─────────────────────────────────────────────────────────

resource "google_artifact_registry_repository" "pulse" {
  location      = var.region
  repository_id = "pulse-analytics"
  format        = "DOCKER"
  labels        = local.common_labels
  depends_on    = [google_project_service.apis]
}

# ── Secret Manager — JWT secret ───────────────────────────────────────────────

resource "google_secret_manager_secret" "jwt_secret" {
  secret_id = "${local.name_prefix}-jwt-secret"
  labels    = local.common_labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "jwt_secret" {
  secret      = google_secret_manager_secret.jwt_secret.id
  secret_data = var.jwt_secret
}

resource "google_secret_manager_secret" "postgres_password" {
  secret_id = "${local.name_prefix}-postgres-password"
  labels    = local.common_labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_password" {
  secret      = google_secret_manager_secret.postgres_password.id
  secret_data = var.postgres_password
}

# ── Modules ───────────────────────────────────────────────────────────────────

module "vpc" {
  source      = "./modules/vpc"
  name_prefix = local.name_prefix
  region      = var.region
  labels      = local.common_labels
  depends_on  = [google_project_service.apis]
}

module "gke" {
  source              = "./modules/gke"
  name_prefix         = local.name_prefix
  project_id          = var.project_id
  region              = var.region
  network             = module.vpc.network_self_link
  subnetwork          = module.vpc.subnetwork_self_link
  pods_cidr           = module.vpc.pods_cidr_name
  services_cidr       = module.vpc.services_cidr_name
  machine_type        = var.app_node_machine_type
  min_node_count      = var.app_node_min_count
  max_node_count      = var.app_node_max_count
  labels              = local.common_labels
  depends_on          = [module.vpc]
}

module "cloudsql" {
  source            = "./modules/cloudsql"
  name_prefix       = local.name_prefix
  region            = var.region
  zone              = var.zone
  tier              = var.postgres_tier
  postgres_version  = var.postgres_version
  postgres_password = var.postgres_password
  network           = module.vpc.network_self_link
  labels            = local.common_labels
  depends_on        = [module.vpc]
}

module "redis" {
  source          = "./modules/redis"
  name_prefix     = local.name_prefix
  region          = var.region
  memory_size_gb  = var.redis_memory_size_gb
  tier            = var.redis_tier
  network         = module.vpc.network_self_link
  labels          = local.common_labels
  depends_on      = [module.vpc]
}

module "iam" {
  source      = "./modules/iam"
  project_id  = var.project_id
  name_prefix = local.name_prefix
  depends_on  = [google_project_service.apis]
}

# Kafka is only provisioned when Confluent credentials are supplied.
# Set kafka_api_key = "" in dev to skip (use Strimzi in-cluster instead).
module "kafka" {
  count  = var.kafka_api_key != "" ? 1 : 0
  source = "./modules/kafka"

  name_prefix          = local.name_prefix
  environment          = var.environment
  region               = var.region
  project_id           = var.project_id
  confluent_api_key    = var.kafka_api_key
  confluent_api_secret = var.kafka_api_secret
  cku_count            = var.environment == "prod" ? 2 : 1
  labels               = local.common_labels

  depends_on = [google_project_service.apis]
}
