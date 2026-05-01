# ============================================================
# PulseAnalytics — Makefile
# Each microservice can be built and run independently.
# ============================================================

BINARY_DIR := bin
GO         := go
IMAGE_TAG  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
REGISTRY   ?= us-docker.pkg.dev/my-project/pulse-analytics

SERVICES   := gateway auth enricher session funnel chwriter query-api alert-engine notification

MINIKUBE_PROFILE ?= minikube

.PHONY: all build test lint clean docker-build docker-push infra-up infra-down \
        run-gateway run-enricher run-session run-funnel run-chwriter \
        run-queryapi run-alertengine migrate-postgres migrate-clickhouse \
        swagger tf-dev tf-staging tf-prod \
        minikube-start minikube-build minikube-deploy minikube-deploy-monitoring \
        minikube-deploy-infra minikube-deploy-services minikube-deploy-loadtest \
        minikube-urls minikube-stop minikube-delete minikube-logs \
        minikube-grafana minikube-locust minikube-status \
        loadtest-install loadtest-run loadtest-headless

# ─── Build all services ───────────────────────────────────────────────────────
all: build

build: build-gateway build-enricher build-session build-funnel build-chwriter build-queryapi build-alertengine

build-gateway:
	@echo "→ Building gateway..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/gateway ./cmd/gateway

build-enricher:
	@echo "→ Building enricher..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/enricher ./cmd/enricher

build-session:
	@echo "→ Building session-engine..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/session-engine ./cmd/session

build-funnel:
	@echo "→ Building funnel-processor..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/funnel-processor ./cmd/funnel

build-chwriter:
	@echo "→ Building ch-writer..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/ch-writer ./cmd/chwriter

build-queryapi:
	@echo "→ Building query-api..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/query-api ./cmd/queryapi

build-alertengine:
	@echo "→ Building alert-engine..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY_DIR)/alert-engine ./cmd/alertengine

# ─── Run services individually (requires infra running via make infra-up) ─────
run-gateway: build-gateway
	@echo "→ Starting gateway on :8080 ..."
	CONFIG_PATH=configs/gateway.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_POSTGRES_DSN="postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable" \
	PULSE_MONGO_URI=mongodb://localhost:27017 \
	PULSE_CLICKHOUSE_HOSTS=localhost:9000 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/gateway

run-enricher: build-enricher
	@echo "→ Starting enricher ..."
	CONFIG_PATH=configs/enricher.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/enricher

run-session: build-session
	@echo "→ Starting session-engine ..."
	CONFIG_PATH=configs/base.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/session-engine

run-funnel: build-funnel
	@echo "→ Starting funnel-processor ..."
	CONFIG_PATH=configs/base.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_POSTGRES_DSN="postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable" \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/funnel-processor

run-chwriter: build-chwriter
	@echo "→ Starting ch-writer ..."
	CONFIG_PATH=configs/base.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_CLICKHOUSE_HOSTS=localhost:9000 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/ch-writer

run-queryapi: build-queryapi
	@echo "→ Starting query-api on :8082 ..."
	CONFIG_PATH=configs/queryapi.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_POSTGRES_DSN="postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable" \
	PULSE_CLICKHOUSE_HOSTS=localhost:9000 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/query-api

run-alertengine: build-alertengine
	@echo "→ Starting alert-engine ..."
	CONFIG_PATH=configs/base.yaml \
	PULSE_KAFKA_BROKERS=localhost:9092 \
	PULSE_REDIS_ADDRS=localhost:6379 \
	PULSE_POSTGRES_DSN="postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable" \
	PULSE_CLICKHOUSE_HOSTS=localhost:9000 \
	PULSE_SERVICE_ENVIRONMENT=development \
	$(BINARY_DIR)/alert-engine

# ─── Infrastructure (local dev) ───────────────────────────────────────────────
infra-up:
	@echo "→ Starting infrastructure (Kafka, Redis, ClickHouse, Postgres, Mongo) ..."
	docker compose up -d zookeeper kafka kafka-init redis clickhouse postgres mongo otel-collector prometheus grafana jaeger

infra-down:
	@echo "→ Stopping infrastructure ..."
	docker compose down

infra-logs:
	docker compose logs -f --tail=50

# ─── Run all services via docker-compose ─────────────────────────────────────
up: infra-up
	@echo "→ Starting all pulse services ..."
	docker compose up -d gateway enricher session-engine ch-writer query-api

down:
	docker compose down

logs:
	docker compose logs -f gateway enricher session-engine ch-writer query-api

# ─── Database migrations ──────────────────────────────────────────────────────
migrate-postgres:
	@echo "→ Running Postgres migrations ..."
	PGPASSWORD=pulse psql -h localhost -U pulse -d pulse \
		-f migrations/postgres/001_init.sql

migrate-clickhouse:
	@echo "→ Running ClickHouse migrations ..."
	cat migrations/clickhouse/001_init.sql | \
		clickhouse-client --host localhost --port 9000 \
		--user pulse --multiquery

migrate-mongo:
	@echo "→ Running MongoDB index setup ..."
	mongosh --host localhost:27017 pulse migrations/mongo/indexes.js

migrate-all: migrate-postgres migrate-clickhouse migrate-mongo

# ─── Swagger docs ────────────────────────────────────────────────────────────
swagger:
	@echo "→ Generating Swagger docs for gateway..."
	swag init -g cmd/gateway/main.go -o docs/gateway --packageName docs
	@echo "→ Generating Swagger docs for query-api..."
	swag init -g cmd/queryapi/main.go -o docs/queryapi --packageName docs
	@echo "→ Swagger docs generated."

# ─── Tests ───────────────────────────────────────────────────────────────────
test:
	$(GO) test ./... -race -timeout 5m

test-cover:
	$(GO) test ./... -race -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ─── Lint ────────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./... --timeout 5m

# ─── Docker images ───────────────────────────────────────────────────────────
docker-build:
	@echo "→ Building all service images (tag: $(IMAGE_TAG)) ..."
	@for svc in $(SERVICES); do \
		echo "  docker build $$svc ..."; \
		docker build -t pulse-$$svc:$(IMAGE_TAG) -f $$svc/Dockerfile . ; \
	done
	docker build -t pulse-frontend:$(IMAGE_TAG) -f frontend/Dockerfile frontend/

docker-push: docker-build
	@for svc in $(SERVICES) frontend; do \
		docker tag pulse-$$svc:$(IMAGE_TAG) $(REGISTRY)/$$svc:$(IMAGE_TAG); \
		docker push $(REGISTRY)/$$svc:$(IMAGE_TAG); \
	done

# ─── Terraform shortcuts ──────────────────────────────────────────────────────
tf-dev:
	cd deployments/terraform/environments/dev && terraform init && terraform apply

tf-staging:
	cd deployments/terraform/environments/staging && terraform init && terraform apply

tf-prod:
	cd deployments/terraform/environments/prod && terraform init && terraform plan

# ─── Clean ───────────────────────────────────────────────────────────────────
clean:
	rm -rf $(BINARY_DIR)/ coverage.out coverage.html

# ─── Minikube — full stack ────────────────────────────────────────────────────
# Starts Minikube, builds images, deploys everything (infra + LGTM + services + loadtest).
minikube-start:
	@echo "→ Starting Minikube (4 CPUs, 8GB RAM)..."
	minikube start \
		--profile=$(MINIKUBE_PROFILE) \
		--cpus=4 \
		--memory=8192 \
		--disk-size=40g \
		--driver=docker \
		--kubernetes-version=v1.30.0
	minikube addons enable ingress        -p $(MINIKUBE_PROFILE)
	minikube addons enable metrics-server -p $(MINIKUBE_PROFILE)
	minikube addons enable storage-provisioner -p $(MINIKUBE_PROFILE)
	@echo "✓ Minikube started"

# Build all service images directly into Minikube's Docker daemon (no registry needed)
minikube-build:
	@echo "→ Building images into Minikube Docker daemon..."
	eval $$(minikube docker-env -p $(MINIKUBE_PROFILE)) && \
	docker build -t pulse-gateway:$(IMAGE_TAG)             -f deployments/docker/gateway.Dockerfile . && \
	docker build -t pulse-enricher:$(IMAGE_TAG)            -f deployments/docker/enricher.Dockerfile . && \
	docker build -t pulse-session:$(IMAGE_TAG)             -f deployments/docker/session.Dockerfile . && \
	docker build -t pulse-funnel:$(IMAGE_TAG)              -f deployments/docker/funnel.Dockerfile . && \
	docker build -t pulse-chwriter:$(IMAGE_TAG)            -f deployments/docker/chwriter.Dockerfile . && \
	docker build -t pulse-query-api:$(IMAGE_TAG)           -f deployments/docker/queryapi.Dockerfile . && \
	docker build -t pulse-alertengine:$(IMAGE_TAG)         -f deployments/docker/alertengine.Dockerfile . && \
	docker build -t pulse-authservice:$(IMAGE_TAG)         -f deployments/docker/authservice.Dockerfile . && \
	docker build -t pulse-notificationservice:$(IMAGE_TAG) -f deployments/docker/notificationservice.Dockerfile .
	@echo "✓ All images built"

# Install Strimzi Kafka Operator
minikube-install-strimzi:
	@echo "→ Installing Strimzi Kafka Operator..."
	kubectl apply -f "https://strimzi.io/install/latest?namespace=pulse" --server-side --force-conflicts
	@echo "✓ Strimzi installed"

# Deploy infrastructure (Redis, ClickHouse, Postgres, Mongo, Kafka)
minikube-deploy-infra:
	@echo "→ Deploying infrastructure..."
	kubectl apply -f deployments/minikube/namespace.yaml
	kubectl apply -f deployments/minikube/secrets.yaml
	kubectl apply -f deployments/minikube/configmap.yaml
	kubectl apply -f deployments/minikube/infra/redis.yaml
	kubectl apply -f deployments/minikube/infra/clickhouse.yaml
	kubectl apply -f deployments/minikube/infra/postgres.yaml
	kubectl apply -f deployments/minikube/infra/mongo.yaml
	kubectl apply -f deployments/minikube/infra/kafka.yaml
	@echo "✓ Infrastructure deployed"

# Deploy LGTM observability stack (Loki + Grafana + Tempo + Mimir + OTel + Prometheus)
minikube-deploy-monitoring:
	@echo "→ Deploying LGTM observability stack..."
	kubectl apply -f deployments/minikube/monitoring/loki.yaml
	kubectl apply -f deployments/minikube/monitoring/tempo.yaml
	kubectl apply -f deployments/minikube/monitoring/mimir.yaml
	kubectl apply -f deployments/minikube/monitoring/otel-collector.yaml
	kubectl apply -f deployments/minikube/monitoring/prometheus.yaml
	kubectl apply -f deployments/minikube/monitoring/grafana-dashboards.yaml
	kubectl apply -f deployments/minikube/monitoring/grafana.yaml
	@echo "✓ LGTM stack deployed"

# Deploy all application services
minikube-deploy-services:
	@echo "→ Deploying application services..."
	kubectl apply -f deployments/minikube/services/gateway.yaml
	kubectl apply -f deployments/minikube/services/enricher.yaml
	kubectl apply -f deployments/minikube/services/session.yaml
	kubectl apply -f deployments/minikube/services/funnel.yaml
	kubectl apply -f deployments/minikube/services/chwriter.yaml
	kubectl apply -f deployments/minikube/services/query-api.yaml
	kubectl apply -f deployments/minikube/services/alertengine.yaml
	kubectl apply -f deployments/minikube/services/auth-service.yaml
	kubectl apply -f deployments/minikube/services/notification-service.yaml
	kubectl apply -f deployments/minikube/ingress.yaml
	@echo "✓ Services deployed"

# Deploy Locust load test
minikube-deploy-loadtest:
	@echo "→ Deploying Locust load test..."
	kubectl apply -f deployments/minikube/loadtest/locust.yaml
	@echo "✓ Locust deployed"

# Full deploy: start + build + deploy everything
minikube-deploy: minikube-start minikube-build minikube-install-strimzi \
                 minikube-deploy-infra minikube-deploy-monitoring \
                 minikube-deploy-services minikube-deploy-loadtest
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║       PulseAnalytics — Minikube Deploy Complete          ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@$(MAKE) minikube-urls

# Print all service URLs
minikube-urls:
	@echo ""
	@echo "─── Service URLs ────────────────────────────────────────────"
	@echo "  Gateway:    $$(minikube service gateway    -n pulse --url 2>/dev/null || echo 'use: kubectl port-forward svc/gateway 8080:8080 -n pulse')"
	@echo "  Query API:  $$(minikube service query-api  -n pulse --url 2>/dev/null || echo 'use: kubectl port-forward svc/query-api 8082:8082 -n pulse')"
	@echo "  Auth:       $$(minikube service auth-service -n pulse --url 2>/dev/null || echo 'use: kubectl port-forward svc/auth-service 8083:8083 -n pulse')"
	@echo ""
	@echo "─── Observability ───────────────────────────────────────────"
	@echo "  Grafana:    $$(minikube service grafana -n monitoring --url 2>/dev/null || echo 'use: kubectl port-forward svc/grafana 3000:3000 -n monitoring')"
	@echo "  Locust UI:  $$(minikube service locust-master -n loadtest --url 2>/dev/null || echo 'use: kubectl port-forward svc/locust-master 8089:8089 -n loadtest')"
	@echo ""
	@echo "─── Ingress (add to /etc/hosts: $$(minikube ip) pulse.local api.pulse.local grafana.pulse.local) ─"
	@echo "  http://pulse.local          → Gateway"
	@echo "  http://api.pulse.local      → Query API"
	@echo "  http://grafana.pulse.local  → Grafana (admin/pulse-admin)"
	@echo ""

# Open Grafana in browser
minikube-grafana:
	minikube service grafana -n monitoring -p $(MINIKUBE_PROFILE)

# Open Locust UI in browser
minikube-locust:
	minikube service locust-master -n loadtest -p $(MINIKUBE_PROFILE)

# Show pod status across all namespaces
minikube-status:
	@echo "─── pulse namespace ─────────────────────────────────────────"
	kubectl get pods -n pulse -o wide
	@echo ""
	@echo "─── monitoring namespace ────────────────────────────────────"
	kubectl get pods -n monitoring -o wide
	@echo ""
	@echo "─── loadtest namespace ──────────────────────────────────────"
	kubectl get pods -n loadtest -o wide

# Tail logs from all pulse services
minikube-logs:
	kubectl logs -n pulse -l app.kubernetes.io/part-of=pulse-analytics --all-containers --follow --max-log-requests=20

# Stop Minikube (preserves state)
minikube-stop:
	minikube stop -p $(MINIKUBE_PROFILE)

# Delete Minikube cluster entirely
minikube-delete:
	minikube delete -p $(MINIKUBE_PROFILE)

# ─── Load test (local, outside cluster) ──────────────────────────────────────
loadtest-install:
	@echo "→ Installing Locust..."
	pip install -r loadtest/requirements.txt

loadtest-run:
	@echo "→ Starting Locust web UI (open http://localhost:8089)..."
	@echo "   Set host to: $$(minikube service gateway -n pulse --url 2>/dev/null || echo 'http://localhost:8080')"
	locust -f loadtest/locustfile.py

loadtest-headless:
	@echo "→ Running headless load test (50 users, 5 min)..."
	locust -f loadtest/locustfile.py \
		--headless \
		--users=50 \
		--spawn-rate=5 \
		--run-time=5m \
		--host=$$(minikube service gateway -n pulse --url 2>/dev/null || echo 'http://localhost:8080') \
		--html=loadtest/report.html
	@echo "✓ Report saved to loadtest/report.html"

# ─── Help ────────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "PulseAnalytics — Available targets:"
	@echo ""
	@echo "  ── Minikube (full local cluster) ──────────────────────────"
	@echo "    make minikube-deploy          Full deploy: start + build + all services"
	@echo "    make minikube-start           Start Minikube (4 CPU, 8GB RAM)"
	@echo "    make minikube-build           Build all images into Minikube daemon"
	@echo "    make minikube-deploy-infra    Deploy Redis/ClickHouse/Postgres/Mongo/Kafka"
	@echo "    make minikube-deploy-monitoring  Deploy LGTM stack (Loki+Grafana+Tempo+Mimir+OTel)"
	@echo "    make minikube-deploy-services Deploy all 9 application services"
	@echo "    make minikube-deploy-loadtest Deploy Locust load test"
	@echo "    make minikube-status          Show pod status across all namespaces"
	@echo "    make minikube-urls            Print all service access URLs"
	@echo "    make minikube-grafana         Open Grafana in browser"
	@echo "    make minikube-locust          Open Locust UI in browser"
	@echo "    make minikube-logs            Tail all service logs"
	@echo "    make minikube-stop            Stop Minikube (preserves state)"
	@echo "    make minikube-delete          Delete Minikube cluster"
	@echo ""
	@echo "  ── Load Testing (local Locust) ────────────────────────────"
	@echo "    make loadtest-install         pip install locust"
	@echo "    make loadtest-run             Start Locust web UI (http://localhost:8089)"
	@echo "    make loadtest-headless        Run 50-user headless test for 5 min"
	@echo ""
	@echo "  ── Infrastructure (local docker-compose) ──────────────────"
	@echo "    make infra-up         Start Kafka, Redis, ClickHouse, Postgres, Mongo"
	@echo "    make infra-down       Stop all infrastructure"
	@echo "    make migrate-all      Run all DB migrations"
	@echo ""
	@echo "  ── Run services individually (after make infra-up) ────────"
	@echo "    make run-gateway      Ingest Gateway   :8080"
	@echo "    make run-enricher     Enrichment Service"
	@echo "    make run-session      Session Engine"
	@echo "    make run-funnel       Funnel Processor"
	@echo "    make run-chwriter     ClickHouse Writer"
	@echo "    make run-queryapi     Query API        :8082"
	@echo "    make run-alertengine  Alert Engine"
	@echo ""
	@echo "  ── Development ────────────────────────────────────────────"
	@echo "    make build            Build all service binaries to ./bin/"
	@echo "    make test             Run tests with race detector"
	@echo "    make lint             Run golangci-lint"
	@echo "    make swagger          Regenerate Swagger docs"
	@echo "    make docker-build     Build all Docker images"
	@echo ""
