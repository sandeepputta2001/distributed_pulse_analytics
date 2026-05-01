#!/usr/bin/env bash
# ─── PulseAnalytics — Minikube Full Deploy Script ────────────────────────────
# Deploys the entire stack: infra, LGTM observability, app services, load test.
#
# Prerequisites:
#   - minikube >= 1.33  (https://minikube.sigs.k8s.io/docs/start/)
#   - kubectl >= 1.28
#   - helm >= 3.14
#   - docker (for building images)
#
# Usage:
#   chmod +x deployments/minikube/deploy.sh
#   ./deployments/minikube/deploy.sh [--skip-build] [--skip-images]
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MINIKUBE_PROFILE="${MINIKUBE_PROFILE:-minikube}"
IMAGE_TAG="${IMAGE_TAG:-dev}"
SKIP_BUILD="${SKIP_BUILD:-false}"
SKIP_IMAGES="${SKIP_IMAGES:-false}"

# Parse flags
for arg in "$@"; do
  case $arg in
    --skip-build)  SKIP_BUILD=true  ;;
    --skip-images) SKIP_IMAGES=true ;;
  esac
done

log()  { echo -e "\033[1;34m[deploy]\033[0m $*"; }
ok()   { echo -e "\033[1;32m[  ok  ]\033[0m $*"; }
warn() { echo -e "\033[1;33m[ warn ]\033[0m $*"; }
die()  { echo -e "\033[1;31m[ fail ]\033[0m $*" >&2; exit 1; }

# ─── 1. Minikube setup ────────────────────────────────────────────────────────
log "Checking Minikube..."
if ! minikube status -p "${MINIKUBE_PROFILE}" | grep -q "Running"; then
  log "Starting Minikube (4 CPUs, 8GB RAM, 40GB disk)..."
  minikube start \
    --profile="${MINIKUBE_PROFILE}" \
    --cpus=4 \
    --memory=8192 \
    --disk-size=40g \
    --driver=docker \
    --kubernetes-version=v1.30.0 \
    --addons=ingress,metrics-server,storage-provisioner
else
  ok "Minikube already running"
fi

# Enable required addons
log "Enabling Minikube addons..."
minikube addons enable ingress         -p "${MINIKUBE_PROFILE}" 2>/dev/null || true
minikube addons enable metrics-server  -p "${MINIKUBE_PROFILE}" 2>/dev/null || true
minikube addons enable storage-provisioner -p "${MINIKUBE_PROFILE}" 2>/dev/null || true

# ─── 2. Build images into Minikube Docker daemon ─────────────────────────────
if [ "${SKIP_IMAGES}" = "false" ]; then
  log "Pointing Docker to Minikube daemon..."
  eval "$(minikube docker-env -p "${MINIKUBE_PROFILE}")"

  if [ "${SKIP_BUILD}" = "false" ]; then
    log "Building service images..."
    cd "${ROOT_DIR}"

    SERVICES=(gateway enricher session funnel chwriter queryapi alertengine authservice notificationservice)
    BINARY_NAMES=(gateway enricher session-engine funnel-processor ch-writer query-api alert-engine auth-service notification-service)
    IMAGE_NAMES=(pulse-gateway pulse-enricher pulse-session pulse-funnel pulse-chwriter pulse-query-api pulse-alertengine pulse-authservice pulse-notificationservice)
    CMD_DIRS=(gateway enricher session funnel chwriter queryapi alertengine authservice notificationservice)

    for i in "${!SERVICES[@]}"; do
      svc="${SERVICES[$i]}"
      img="${IMAGE_NAMES[$i]}"
      cmd="${CMD_DIRS[$i]}"
      log "Building ${img}:${IMAGE_TAG}..."
      docker build \
        -f "deployments/docker/${svc}.Dockerfile" \
        -t "${img}:${IMAGE_TAG}" \
        . || warn "Build failed for ${img} — skipping"
    done
    ok "All images built"
  else
    warn "Skipping image build (--skip-build)"
  fi
else
  warn "Skipping image build (--skip-images)"
fi

# ─── 3. Install Strimzi Kafka Operator ───────────────────────────────────────
log "Installing Strimzi Kafka Operator..."
kubectl apply -f "https://strimzi.io/install/latest?namespace=pulse" \
  --server-side --force-conflicts 2>/dev/null || \
kubectl apply -f "https://strimzi.io/install/latest?namespace=pulse" || \
  warn "Strimzi install failed — Kafka may not work"

# ─── 4. Apply namespaces and base resources ───────────────────────────────────
log "Creating namespaces..."
kubectl apply -f "${SCRIPT_DIR}/namespace.yaml"

log "Applying secrets..."
kubectl apply -f "${SCRIPT_DIR}/secrets.yaml"

log "Applying ConfigMaps..."
kubectl apply -f "${SCRIPT_DIR}/configmap.yaml"

# ─── 5. Deploy infrastructure ─────────────────────────────────────────────────
log "Deploying infrastructure (Redis, ClickHouse, Postgres, Mongo)..."
kubectl apply -f "${SCRIPT_DIR}/infra/redis.yaml"
kubectl apply -f "${SCRIPT_DIR}/infra/clickhouse.yaml"
kubectl apply -f "${SCRIPT_DIR}/infra/postgres.yaml"
kubectl apply -f "${SCRIPT_DIR}/infra/mongo.yaml"

log "Deploying Kafka (Strimzi)..."
kubectl apply -f "${SCRIPT_DIR}/infra/kafka.yaml"

log "Waiting for infrastructure to be ready..."
kubectl rollout status deployment/redis      -n pulse --timeout=120s || warn "Redis not ready"
kubectl rollout status deployment/clickhouse -n pulse --timeout=120s || warn "ClickHouse not ready"
kubectl rollout status deployment/postgres   -n pulse --timeout=120s || warn "Postgres not ready"
kubectl rollout status deployment/mongo      -n pulse --timeout=120s || warn "Mongo not ready"

# ─── 6. Deploy LGTM observability stack ──────────────────────────────────────
log "Deploying LGTM observability stack..."
kubectl apply -f "${SCRIPT_DIR}/monitoring/loki.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/tempo.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/mimir.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/otel-collector.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/prometheus.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/grafana-dashboards.yaml"
kubectl apply -f "${SCRIPT_DIR}/monitoring/grafana.yaml"

log "Waiting for observability stack..."
kubectl rollout status deployment/loki          -n monitoring --timeout=120s || warn "Loki not ready"
kubectl rollout status deployment/tempo         -n monitoring --timeout=120s || warn "Tempo not ready"
kubectl rollout status deployment/mimir         -n monitoring --timeout=180s || warn "Mimir not ready"
kubectl rollout status deployment/otel-collector -n monitoring --timeout=120s || warn "OTel not ready"
kubectl rollout status deployment/prometheus    -n monitoring --timeout=120s || warn "Prometheus not ready"
kubectl rollout status deployment/grafana       -n monitoring --timeout=120s || warn "Grafana not ready"

# ─── 7. Deploy application services ──────────────────────────────────────────
log "Deploying application services..."
kubectl apply -f "${SCRIPT_DIR}/services/gateway.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/enricher.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/session.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/funnel.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/chwriter.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/query-api.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/alertengine.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/auth-service.yaml"
kubectl apply -f "${SCRIPT_DIR}/services/notification-service.yaml"

# ─── 8. Apply ingress ─────────────────────────────────────────────────────────
log "Applying ingress rules..."
kubectl apply -f "${SCRIPT_DIR}/ingress.yaml"

# ─── 9. Deploy load test ──────────────────────────────────────────────────────
log "Deploying Locust load test..."
kubectl apply -f "${SCRIPT_DIR}/loadtest/locust.yaml"

# ─── 10. Print access URLs ────────────────────────────────────────────────────
MINIKUBE_IP="$(minikube ip -p "${MINIKUBE_PROFILE}")"

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║          PulseAnalytics — Minikube Deploy Complete           ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Add to /etc/hosts:                                          ║"
echo "║    ${MINIKUBE_IP}  pulse.local api.pulse.local grafana.pulse.local"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Service URLs (via NodePort):                                ║"
echo "║    Gateway:   $(minikube service gateway   -n pulse --url 2>/dev/null || echo 'pending')"
echo "║    Query API: $(minikube service query-api -n pulse --url 2>/dev/null || echo 'pending')"
echo "║    Auth:      $(minikube service auth-service -n pulse --url 2>/dev/null || echo 'pending')"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Observability:                                              ║"
echo "║    Grafana:   $(minikube service grafana -n monitoring --url 2>/dev/null || echo 'pending')  (admin/pulse-admin)"
echo "║    Prometheus: kubectl port-forward svc/prometheus 9090:9090 -n monitoring"
echo "║    Mimir:      kubectl port-forward svc/mimir 9009:9009 -n monitoring"
echo "║    Loki:       kubectl port-forward svc/loki 3100:3100 -n monitoring"
echo "║    Tempo:      kubectl port-forward svc/tempo 3200:3200 -n monitoring"
echo "╠══════════════════════════════════════════════════════════════╣"
echo "║  Load Test:                                                  ║"
echo "║    Locust UI: $(minikube service locust-master -n loadtest --url 2>/dev/null || echo 'pending')"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
ok "Deploy complete!"
