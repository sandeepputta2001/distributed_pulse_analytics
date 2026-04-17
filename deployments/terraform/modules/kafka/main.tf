# ─── Confluent Cloud — PulseAnalytics Kafka ───────────────────────────────────
# Provisions a Confluent Cloud Kafka cluster plus all topics, service accounts,
# and API keys needed by the nine microservices.
#
# Requires:  confluentinc/confluent Terraform provider (see versions.tf).
# The provider must be configured at the root level:
#   provider "confluent" { cloud_api_key = var.confluent_api_key; cloud_api_secret = var.confluent_api_secret }
# ─────────────────────────────────────────────────────────────────────────────

# ── Environment & cluster ─────────────────────────────────────────────────────

resource "confluent_environment" "pulse" {
  display_name = "pulse-${var.environment}"

  stream_governance {
    package = var.environment == "prod" ? "ESSENTIALS" : "NONE"
  }
}

resource "confluent_kafka_cluster" "pulse" {
  display_name = "pulse-${var.environment}"
  availability = var.environment == "prod" ? "MULTI_ZONE" : "SINGLE_ZONE"
  cloud        = "GCP"
  region       = var.region

  # Dedicated in prod/staging for throughput; Basic in dev to save cost
  dynamic "basic" {
    for_each = var.environment == "dev" ? [1] : []
    content {}
  }

  dynamic "standard" {
    for_each = var.environment == "staging" ? [1] : []
    content {}
  }

  dynamic "dedicated" {
    for_each = var.environment == "prod" ? [1] : []
    content {
      cku = var.cku_count   # 2 CKU = ~150 MB/s throughput per cluster
    }
  }

  environment {
    id = confluent_environment.pulse.id
  }
}

# ── Topics ────────────────────────────────────────────────────────────────────
# Matches the topic design in LLD §10.1

locals {
  topics = {
    "raw-events" = {
      partitions         = 12
      retention_ms       = "172800000"  # 48 h
      compression        = "snappy"
      min_insync_replicas = "2"
    }
    "enriched-events" = {
      partitions         = 12
      retention_ms       = "172800000"  # 48 h
      compression        = "snappy"
      min_insync_replicas = "2"
    }
    "session-events" = {
      partitions         = 6
      retention_ms       = "86400000"   # 24 h
      compression        = "snappy"
      min_insync_replicas = "2"
    }
    "agg-results" = {
      partitions         = 4
      retention_ms       = "86400000"   # 24 h
      compression        = "snappy"
      min_insync_replicas = "2"
    }
    "dlq-events" = {
      partitions         = 2
      retention_ms       = "604800000"  # 7 days — manual review window
      compression        = "gzip"
      min_insync_replicas = "1"         # DLQ: prefer availability over strict durability
    }
    "notifications" = {
      partitions         = 2
      retention_ms       = "86400000"   # 24 h
      compression        = "snappy"
      min_insync_replicas = "1"
    }
  }
}

resource "confluent_kafka_topic" "topics" {
  for_each = local.topics

  topic_name       = each.key
  partitions_count = each.value.partitions

  kafka_cluster {
    id = confluent_kafka_cluster.pulse.id
  }

  rest_endpoint = confluent_kafka_cluster.pulse.rest_endpoint

  config = {
    "retention.ms"              = each.value.retention_ms
    "compression.type"          = each.value.compression
    "min.insync.replicas"       = each.value.min_insync_replicas
    "cleanup.policy"            = "delete"
    "delete.retention.ms"       = "86400000"
    "segment.ms"                = "3600000"   # 1-hour segment rollover
    "max.message.bytes"         = "5242880"   # 5 MB max (matches gateway body limit)
  }

  credentials {
    key    = confluent_api_key.cluster_admin.id
    secret = confluent_api_key.cluster_admin.secret
  }

  depends_on = [confluent_role_binding.cluster_admin]
}

# ── Service accounts ──────────────────────────────────────────────────────────
# One SA per logical role: producer (gateway/enricher/session/funnel),
# consumer (enricher/session/funnel/chwriter/alertengine/notification),
# admin (topic management in CI).

resource "confluent_service_account" "producer" {
  display_name = "pulse-producer-${var.environment}"
  description  = "Ingest pipeline services: gateway, session-engine, funnel-processor"
}

resource "confluent_service_account" "consumer" {
  display_name = "pulse-consumer-${var.environment}"
  description  = "Downstream consumers: enricher, ch-writer, alert-engine, notification"
}

resource "confluent_service_account" "cluster_admin" {
  display_name = "pulse-cluster-admin-${var.environment}"
  description  = "Used by Terraform to manage topics"
}

# ── Role bindings ─────────────────────────────────────────────────────────────

# Cluster admin — needed by Terraform to create/modify topics
resource "confluent_role_binding" "cluster_admin" {
  principal   = "User:${confluent_service_account.cluster_admin.id}"
  role_name   = "CloudClusterAdmin"
  crn_pattern = confluent_kafka_cluster.pulse.rbac_crn
}

# Producer: write to all topics
resource "confluent_role_binding" "producer_writer" {
  principal   = "User:${confluent_service_account.producer.id}"
  role_name   = "DeveloperWrite"
  crn_pattern = "${confluent_kafka_cluster.pulse.rbac_crn}/kafka=${confluent_kafka_cluster.pulse.id}/topic=*"
}

# Consumer: read from all topics
resource "confluent_role_binding" "consumer_reader" {
  principal   = "User:${confluent_service_account.consumer.id}"
  role_name   = "DeveloperRead"
  crn_pattern = "${confluent_kafka_cluster.pulse.rbac_crn}/kafka=${confluent_kafka_cluster.pulse.id}/topic=*"
}

# Consumer: manage consumer groups (required for auto-offset-reset, group metadata)
resource "confluent_role_binding" "consumer_group" {
  principal   = "User:${confluent_service_account.consumer.id}"
  role_name   = "DeveloperRead"
  crn_pattern = "${confluent_kafka_cluster.pulse.rbac_crn}/kafka=${confluent_kafka_cluster.pulse.id}/group=*"
}

# ── API keys ──────────────────────────────────────────────────────────────────

resource "confluent_api_key" "producer" {
  display_name = "pulse-producer-${var.environment}"
  description  = "API key for producer service account"

  owner {
    id          = confluent_service_account.producer.id
    api_version = confluent_service_account.producer.api_version
    kind        = confluent_service_account.producer.kind
  }

  managed_resource {
    id          = confluent_kafka_cluster.pulse.id
    api_version = confluent_kafka_cluster.pulse.api_version
    kind        = confluent_kafka_cluster.pulse.kind

    environment {
      id = confluent_environment.pulse.id
    }
  }

  depends_on = [confluent_role_binding.producer_writer]
}

resource "confluent_api_key" "consumer" {
  display_name = "pulse-consumer-${var.environment}"
  description  = "API key for consumer service account"

  owner {
    id          = confluent_service_account.consumer.id
    api_version = confluent_service_account.consumer.api_version
    kind        = confluent_service_account.consumer.kind
  }

  managed_resource {
    id          = confluent_kafka_cluster.pulse.id
    api_version = confluent_kafka_cluster.pulse.api_version
    kind        = confluent_kafka_cluster.pulse.kind

    environment {
      id = confluent_environment.pulse.id
    }
  }

  depends_on = [confluent_role_binding.consumer_reader]
}

resource "confluent_api_key" "cluster_admin" {
  display_name = "pulse-cluster-admin-${var.environment}"
  description  = "API key for Terraform topic management"

  owner {
    id          = confluent_service_account.cluster_admin.id
    api_version = confluent_service_account.cluster_admin.api_version
    kind        = confluent_service_account.cluster_admin.kind
  }

  managed_resource {
    id          = confluent_kafka_cluster.pulse.id
    api_version = confluent_kafka_cluster.pulse.api_version
    kind        = confluent_kafka_cluster.pulse.kind

    environment {
      id = confluent_environment.pulse.id
    }
  }

  depends_on = [confluent_role_binding.cluster_admin]
}

# ── GCP Secret Manager — store Kafka credentials ──────────────────────────────
# Pods read PULSE_KAFKA_BROKERS, PULSE_KAFKA_API_KEY, PULSE_KAFKA_API_SECRET
# from k8s Secrets (populated by ESO or Terraform kubernetes_secret resource).

resource "google_secret_manager_secret" "kafka_bootstrap" {
  secret_id = "${var.name_prefix}-kafka-bootstrap"
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "kafka_bootstrap" {
  secret      = google_secret_manager_secret.kafka_bootstrap.id
  secret_data = confluent_kafka_cluster.pulse.bootstrap_endpoint
}

resource "google_secret_manager_secret" "kafka_producer_key" {
  secret_id = "${var.name_prefix}-kafka-producer-key"
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "kafka_producer_key" {
  secret      = google_secret_manager_secret.kafka_producer_key.id
  secret_data = "${confluent_api_key.producer.id}:${confluent_api_key.producer.secret}"
}

resource "google_secret_manager_secret" "kafka_consumer_key" {
  secret_id = "${var.name_prefix}-kafka-consumer-key"
  labels    = var.labels

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "kafka_consumer_key" {
  secret      = google_secret_manager_secret.kafka_consumer_key.id
  secret_data = "${confluent_api_key.consumer.id}:${confluent_api_key.consumer.secret}"
}
