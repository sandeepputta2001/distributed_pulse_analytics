output "gke_cluster_name" {
  description = "GKE cluster name"
  value       = module.gke.cluster_name
}

output "gke_cluster_endpoint" {
  description = "GKE cluster API endpoint"
  value       = module.gke.endpoint
  sensitive   = true
}

output "gke_cluster_ca_certificate" {
  description = "GKE cluster CA certificate (base64)"
  value       = module.gke.ca_certificate
  sensitive   = true
}

output "postgres_connection_name" {
  description = "Cloud SQL connection name for Cloud SQL Auth Proxy"
  value       = module.cloudsql.connection_name
}

output "postgres_private_ip" {
  description = "Cloud SQL private IP"
  value       = module.cloudsql.private_ip
}

output "redis_host" {
  description = "Memorystore Redis host"
  value       = module.redis.host
}

output "redis_port" {
  description = "Memorystore Redis port"
  value       = module.redis.port
}

output "vpc_network_name" {
  description = "VPC network name"
  value       = module.vpc.network_name
}

output "artifact_registry_url" {
  description = "Artifact Registry Docker URL for pushing images"
  value       = "us-docker.pkg.dev/${var.project_id}/pulse-analytics"
}

output "kubectl_config_command" {
  description = "Command to configure kubectl"
  value       = "gcloud container clusters get-credentials ${module.gke.cluster_name} --region ${var.region} --project ${var.project_id}"
}

output "kafka_bootstrap_endpoint" {
  description = "Confluent Cloud Kafka bootstrap endpoint (empty when Kafka not provisioned)"
  value       = length(module.kafka) > 0 ? module.kafka[0].bootstrap_endpoint : ""
  sensitive   = true
}

output "kafka_rest_endpoint" {
  description = "Confluent REST Proxy endpoint"
  value       = length(module.kafka) > 0 ? module.kafka[0].rest_endpoint : ""
}

output "kafka_producer_api_key" {
  description = "Producer API key ID (inject as PULSE_KAFKA_API_KEY for gateway/session/funnel)"
  value       = length(module.kafka) > 0 ? module.kafka[0].producer_api_key_id : ""
}

output "kafka_consumer_api_key" {
  description = "Consumer API key ID (inject as PULSE_KAFKA_API_KEY for enricher/ch-writer/alert-engine)"
  value       = length(module.kafka) > 0 ? module.kafka[0].consumer_api_key_id : ""
}

output "kafka_topic_ids" {
  description = "Map of Confluent topic name → resource ID"
  value       = length(module.kafka) > 0 ? module.kafka[0].topic_names : {}
}
