variable "name_prefix" {
  description = "Resource name prefix, e.g. pulse-prod"
  type        = string
}

variable "environment" {
  description = "Deployment environment: dev | staging | prod"
  type        = string
  validation {
    condition     = contains(["dev", "staging", "prod"], var.environment)
    error_message = "environment must be dev, staging, or prod"
  }
}

variable "region" {
  description = "GCP region for the Confluent Cloud cluster (must match cluster VPC region)"
  type        = string
  default     = "us-central1"
}

variable "confluent_api_key" {
  description = "Confluent Cloud organisation API key (from Confluent console → API keys)"
  type        = string
  sensitive   = true
}

variable "confluent_api_secret" {
  description = "Confluent Cloud organisation API secret"
  type        = string
  sensitive   = true
}

variable "cku_count" {
  description = "Number of CKUs for Dedicated cluster (prod only). 1 CKU ≈ 250 MB/s ingress."
  type        = number
  default     = 2
}

variable "project_id" {
  description = "GCP project ID (for Secret Manager secrets)"
  type        = string
}

variable "labels" {
  description = "GCP resource labels"
  type        = map(string)
  default     = {}
}
