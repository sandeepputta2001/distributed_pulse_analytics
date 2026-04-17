variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone for zonal resources"
  type        = string
  default     = "us-central1-a"
}

variable "environment" {
  description = "Deployment environment: dev | staging | prod"
  type        = string
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be dev, staging, or prod"
  }
}

variable "cluster_name" {
  description = "GKE cluster name"
  type        = string
  default     = "pulse-analytics"
}

# ── Node pools ────────────────────────────────────────────────────────────────

variable "app_node_machine_type" {
  description = "Machine type for the application node pool"
  type        = string
  default     = "e2-standard-4"
}

variable "app_node_min_count" {
  description = "Minimum node count per zone in the app pool"
  type        = number
  default     = 1
}

variable "app_node_max_count" {
  description = "Maximum node count per zone in the app pool"
  type        = number
  default     = 10
}

# ── Cloud SQL ─────────────────────────────────────────────────────────────────

variable "postgres_tier" {
  description = "Cloud SQL Postgres machine tier"
  type        = string
  default     = "db-custom-2-7680"
}

variable "postgres_version" {
  description = "PostgreSQL version"
  type        = string
  default     = "POSTGRES_16"
}

variable "postgres_password" {
  description = "Postgres pulse user password"
  type        = string
  sensitive   = true
}

# ── Redis (Memorystore) ───────────────────────────────────────────────────────

variable "redis_memory_size_gb" {
  description = "Redis memory size in GB"
  type        = number
  default     = 4
}

variable "redis_tier" {
  description = "BASIC or STANDARD_HA"
  type        = string
  default     = "STANDARD_HA"
}

# ── Kafka (Confluent Cloud or MSK) ────────────────────────────────────────────

variable "kafka_bootstrap_servers" {
  description = "Kafka bootstrap server addresses (external, e.g. Confluent Cloud)"
  type        = string
  default     = ""
}

variable "kafka_api_key" {
  description = "Confluent Cloud API key (if using managed Kafka)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "kafka_api_secret" {
  description = "Confluent Cloud API secret"
  type        = string
  sensitive   = true
  default     = ""
}

# ── Container Registry ────────────────────────────────────────────────────────

variable "image_tag" {
  description = "Docker image tag to deploy"
  type        = string
  default     = "latest"
}

# ── JWT ───────────────────────────────────────────────────────────────────────

variable "jwt_secret" {
  description = "JWT signing secret (min 32 chars)"
  type        = string
  sensitive   = true
}
