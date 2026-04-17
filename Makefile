# ============================================================
# PulseAnalytics — Makefile
# Each microservice can be built and run independently.
# ============================================================

BINARY_DIR := bin
GO         := go
IMAGE_TAG  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
REGISTRY   ?= us-docker.pkg.dev/my-project/pulse-analytics

SERVICES   := gateway auth enricher session funnel chwriter query-api alert-engine notification

.PHONY: all build test lint clean docker-build docker-push infra-up infra-down \
        run-gateway run-enricher run-session run-funnel run-chwriter \
        run-queryapi run-alertengine migrate-postgres migrate-clickhouse \
        swagger tf-dev tf-staging tf-prod

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

# ─── Help ────────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "PulseAnalytics — Available targets:"
	@echo ""
	@echo "  Infrastructure:"
	@echo "    make infra-up         Start Kafka, Redis, ClickHouse, Postgres, Mongo"
	@echo "    make infra-down       Stop all infrastructure"
	@echo "    make migrate-all      Run all DB migrations"
	@echo ""
	@echo "  Run services individually (after make infra-up):"
	@echo "    make run-gateway      Ingest Gateway   :8080"
	@echo "    make run-enricher     Enrichment Service"
	@echo "    make run-session      Session Engine"
	@echo "    make run-funnel       Funnel Processor"
	@echo "    make run-chwriter     ClickHouse Writer"
	@echo "    make run-queryapi     Query API        :8082"
	@echo "    make run-alertengine  Alert Engine"
	@echo ""
	@echo "  Run everything:"
	@echo "    make up               Start all services via docker-compose"
	@echo "    make down             Stop all services"
	@echo "    make logs             Tail service logs"
	@echo ""
	@echo "  Development:"
	@echo "    make build            Build all service binaries to ./bin/"
	@echo "    make test             Run tests with race detector"
	@echo "    make lint             Run golangci-lint"
	@echo "    make swagger          Regenerate Swagger docs (requires swag CLI)"
	@echo "    make docker-build     Build all Docker images"
	@echo ""
