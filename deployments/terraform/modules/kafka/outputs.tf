output "bootstrap_endpoint" {
  description = "Kafka cluster bootstrap endpoint (host:port). Inject as PULSE_KAFKA_BROKERS."
  value       = confluent_kafka_cluster.pulse.bootstrap_endpoint
  sensitive   = true
}

output "rest_endpoint" {
  description = "Confluent REST Proxy endpoint (for HTTP-based admin operations)"
  value       = confluent_kafka_cluster.pulse.rest_endpoint
}

output "cluster_id" {
  description = "Confluent Kafka cluster ID"
  value       = confluent_kafka_cluster.pulse.id
}

output "environment_id" {
  description = "Confluent environment ID"
  value       = confluent_environment.pulse.id
}

output "producer_api_key_id" {
  description = "API key ID for producer service account (gateway, session, funnel)"
  value       = confluent_api_key.producer.id
}

output "producer_api_key_secret" {
  description = "API key secret for producer service account"
  value       = confluent_api_key.producer.secret
  sensitive   = true
}

output "consumer_api_key_id" {
  description = "API key ID for consumer service account (enricher, ch-writer, alert-engine, notification)"
  value       = confluent_api_key.consumer.id
}

output "consumer_api_key_secret" {
  description = "API key secret for consumer service account"
  value       = confluent_api_key.consumer.secret
  sensitive   = true
}

output "kafka_bootstrap_secret_name" {
  description = "GCP Secret Manager secret name holding the bootstrap endpoint"
  value       = google_secret_manager_secret.kafka_bootstrap.secret_id
}

output "topic_names" {
  description = "Map of topic name → Confluent topic resource ID"
  value       = { for k, v in confluent_kafka_topic.topics : k => v.id }
}
