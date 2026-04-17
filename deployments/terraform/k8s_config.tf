# k8s_config.tf — bootstrap the pulse namespace and service accounts
# in GKE after the cluster is created.

resource "kubernetes_namespace" "pulse" {
  metadata {
    name = "pulse"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
  depends_on = [module.gke]
}

# Create a Kubernetes SA for each service, annotated for Workload Identity
resource "kubernetes_service_account" "services" {
  for_each = module.iam.workload_sa_emails

  metadata {
    name      = each.key
    namespace = kubernetes_namespace.pulse.metadata[0].name
    annotations = {
      "iam.gke.io/gcp-service-account" = each.value
    }
  }
}

# ConfigMap with non-secret runtime config (overridden by per-service configs)
resource "kubernetes_config_map" "pulse" {
  metadata {
    name      = "pulse-config"
    namespace = kubernetes_namespace.pulse.metadata[0].name
  }

  data = {
    POSTGRES_HOST   = module.cloudsql.private_ip
    REDIS_HOST      = module.redis.host
    REDIS_PORT      = tostring(module.redis.port)
    CLICKHOUSE_HOST = "clickhouse.pulse.svc.cluster.local"
    KAFKA_BROKERS   = var.kafka_bootstrap_servers
    ENVIRONMENT     = var.environment
    REGION          = var.region
  }
}

# Secret with sensitive runtime config (sourced from Secret Manager values)
resource "kubernetes_secret" "pulse" {
  metadata {
    name      = "pulse-secrets"
    namespace = kubernetes_namespace.pulse.metadata[0].name
  }

  data = {
    POSTGRES_PASSWORD = var.postgres_password
    JWT_SECRET        = var.jwt_secret
    KAFKA_API_KEY     = var.kafka_api_key
    KAFKA_API_SECRET  = var.kafka_api_secret
  }

  type = "Opaque"
}
