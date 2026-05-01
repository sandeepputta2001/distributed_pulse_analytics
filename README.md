# PulseAnalytics

A production-grade, high-throughput analytics platform inspired by Amplitude and MoEngage. Built in Go as a distributed microservices monorepo targeting **100M events/second**, **100M+ users**, **<200ms query P95**, and **99.99% uptime**.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Data Flow](#data-flow)
- [Services](#services)
- [Technology Stack](#technology-stack)
- [Project Structure](#project-structure)
- [Quick Start (Local)](#quick-start-local)
- [Running on Minikube](#running-on-minikube)
- [Observability — LGTM Stack](#observability--lgtm-stack)
- [Load Testing with Locust](#load-testing-with-locust)
- [Running Services Individually](#running-services-individually)
- [Authentication](#authentication)
- [API Reference](#api-reference)
- [Database Schemas](#database-schemas)
- [Kafka Topics](#kafka-topics)
- [Rate Limiting](#rate-limiting)
- [Caching Strategy](#caching-strategy)
- [Deduplication](#deduplication)
- [Observability](#observability)
- [Configuration Reference](#configuration-reference)
- [Client SDKs](#client-sdks)
- [Kubernetes Deployment](#kubernetes-deployment)
- [CI/CD Pipeline](#cicd-pipeline)
- [Makefile Reference](#makefile-reference)
- [Sharding & Replication](#sharding--replication)
- [Performance Targets](#performance-targets)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CLIENT LAYER                                    │
│                                                                              │
│   Browser / Mobile (TypeScript SDK)    Go SDK     Server-Side HTTP POST      │
└────────────────────┬────────────────────┬──────────────────┬────────────────┘
                     │                    │                   │
                     ▼                    ▼                   ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                      INGEST GATEWAY  :8080                                   │
│                                                                              │
│  API Key auth (X-API-Key)  ·  Redis Lua token-bucket rate limiting           │
│  Two-stage dedup: bloom filter + Redis SET  ·  MaxMind GeoIP2 (in-process)   │
│  Request validation + size limits  ·  MongoDB raw event archive              │
│  Kafka producer (snappy, acks=leader)  ·  Swagger UI at /swagger/            │
└────────────────────────────────┬────────────────────────────────────────────┘
                                 │  raw-events  (12 partitions)
                                 ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                       ENRICHMENT SERVICE                                     │
│                                                                              │
│  Consumes raw-events  ·  UA parsing (platform / browser / os detection)      │
│  GeoIP lookup  ·  Session ID assignment (Redis SETEX 30-min window)          │
│  Server-side timestamp correction  ·  Publishes → enriched-events            │
└────────────────────────────────┬────────────────────────────────────────────┘
                                 │  enriched-events  (12 partitions)
              ┌──────────────────┼────────────────────┐
              ▼                  ▼                    ▼
┌─────────────────┐  ┌─────────────────────┐  ┌───────────────────────────┐
│  SESSION ENGINE │  │   FUNNEL PROCESSOR  │  │        CH-WRITER          │
│                 │  │                     │  │                           │
│ Session start / │  │ Loads funnel defs   │  │ Buffers enriched events   │
│ end detection   │  │ from PostgreSQL     │  │ Bulk inserts to ClickHouse │
│ Duration calc   │  │ Redis ZSET tracking │  │ (1s / 500K rows batch)    │
│ Publishes →     │  │ Emits conversion    │  │                           │
│ session-events  │  │ events              │  └───────────────────────────┘
└─────────────────┘  └─────────────────────┘
                                 │  agg-results / session-events
              ┌──────────────────┴───────────────────────────────────────┐
              ▼                                                          ▼
┌─────────────────────────────────────────┐    ┌────────────────────────────┐
│            QUERY API  :8082             │    │       ALERT ENGINE          │
│                                         │    │                            │
│  JWT Bearer auth (HS256)                │    │ Polls ClickHouse on schedule│
│  Analytics: events, funnels, retention, │    │ Evaluates threshold rules   │
│  sessions, active users, experiments    │    │ Publishes → notifications   │
│  CRUD: apps, orgs, alerts, cohorts,     │    │ Redis cooldown (30 min)     │
│  experiments, funnels                   │    └────────────────────────────┘
│  Redis query cache (5 min TTL)          │                  │
│  Swagger UI at /swagger/                │                  ▼
└────────────────┬────────────────────────┘    ┌────────────────────────────┐
                 │                             │   NOTIFICATION SERVICE      │
                 ▼                             │                            │
┌─────────────────────────────────────────┐   │ Consumes notifications      │
│      REACT / VITE FRONTEND  :5173        │   │ Sends webhooks + email      │
│                                         │   └────────────────────────────┘
│  Dashboard · Events · Funnels ·          │
│  Sessions · Retention · Cohorts ·        │
│  Experiments · Alerts · Settings         │
│  Vite proxy → gateway :8080              │
│              → query-api :8082           │
└─────────────────────────────────────────┘
```

---

## Data Flow

### End-to-end Event Pipeline

```
SDK                    Gateway               Kafka              Enricher
 │                        │                    │                    │
 │── POST /v1/events ────▶│                    │                    │
 │   X-API-Key: pk_live_  │── rate limit ─────▶│                    │
 │                        │── dedup check      │                    │
 │                        │── GeoIP enrich     │                    │
 │                        │── validate         │                    │
 │                        │── produce ─────────▶  raw-events        │
 │◀── 202 Accepted ───────│                    │                    │
 │                        │                    │── consume ─────────▶
 │                        │                    │                    │── UA parse
 │                        │                    │                    │── session ID
 │                        │                    │                    │── produce ──▶ enriched-events
 │                        │                    │                    │
 │                        │                    │◀── CH-Writer consumes enriched-events
 │                        │                    │    batches → ClickHouse bulk INSERT
```

### Query Flow (with 3-tier cache)

```
Frontend            Query API          Redis Cache       ClickHouse
    │                   │                   │                 │
    │── GET /v1/... ───▶│                   │                 │
    │   Bearer JWT      │── cache lookup ──▶│                 │
    │                   │                   │ HIT → return    │
    │                   │◀──────────────────│                 │
    │                   │                   │ MISS ↓          │
    │                   │── CH query ────────────────────────▶│
    │                   │◀───────────────── results ──────────│
    │                   │── cache set ──────▶                 │
    │◀── JSON ──────────│                   │                 │
```

### Authentication Flow

```
Client                Query API             PostgreSQL
   │                      │                     │
   │─ POST /v1/auth/login ▶                     │
   │  { "api_key": "pk_live_..." }               │
   │                      │── GetAppByAPIKey ───▶│
   │                      │◀── app record ───────│
   │                      │── sign JWT (HS256)   │
   │◀── { "token": "eyJ..." } ──────────────────│
   │                      │                     │
   │─ GET /v1/events/count ▶                    │
   │  Authorization: Bearer eyJ...              │
   │                      │── verify JWT        │
   │◀── analytics data ───│                     │
```

### Alert Pipeline

```
Alert Engine                  ClickHouse               Notification Service
     │                             │                           │
     │── evaluate rule (every 1m) ─▶                          │
     │   SELECT count() WHERE ...  │                           │
     │◀── metric value ────────────│                           │
     │                             │                           │
     │── threshold breached?       │                           │
     │── check Redis cooldown      │                           │
     │── publish to notifications ──────────────────────────▶ │
     │                             │                           │── POST webhook
     │                             │                           │── SMTP email
```

---

## Services

| Service | Binary | Port | Description |
|---------|--------|------|-------------|
| **Ingest Gateway** | `cmd/gateway` | 8080 | Accepts event batches from SDKs. API key auth, Redis token-bucket rate limiting, two-stage dedup (bloom + Redis), GeoIP enrichment. Publishes to Kafka `raw-events`. |
| **Enricher** | `cmd/enricher` | — | Consumes `raw-events`. Adds MaxMind GeoIP2 country/city, User-Agent OS/browser parsing, server-side timestamp. Assigns session IDs via Redis SETEX. Publishes to `enriched-events`. |
| **Session Engine** | `cmd/session` | — | Consumes `enriched-events`. Detects session boundaries via 30-min inactivity window in Redis. Emits `session_start` / `session_end` synthetic events. Publishes to `session-events`. |
| **Funnel Processor** | `cmd/funnel` | — | Consumes `session-events`. Loads funnel definitions from PostgreSQL. Tracks ordered step completion per user with Redis ZSETs. Emits `funnel_conversion` events. |
| **ClickHouse Writer** | `cmd/chwriter` | — | Consumes `session-events` (enriched + sessionized events). Buffers rows and bulk-inserts to ClickHouse every 1 second or at 500K rows. Primary OLAP write path. |
| **Query API** | `cmd/queryapi` | 8082 | Stateless HTTP API. Analytics queries (events, funnels, retention, DAU, sessions, experiments). Full CRUD for apps, orgs, alerts, cohorts, experiments. JWT auth. 4-tier cache (L1 SWR + Redis + CH MVs + CH raw), single-flight dedup, circuit breaker, per-tenant bulkhead. |
| **Alert Engine** | `cmd/alertengine` | — | Polls ClickHouse every minute against all active alert rules. Fires webhooks and email when thresholds are breached. Redis cooldown prevents duplicate alerts. |
| **Notification Service** | `cmd/notificationservice` | — | Consumes `notifications` Kafka topic. Delivers alerts via HTTP webhook and SMTP email. |
| **Auth Service** | `cmd/authservice` | 8083 | Standalone auth service (API key → JWT). The Query API also exposes `/v1/auth/login` directly for frontend convenience. |

### Supporting Infrastructure

| Service | Port | Purpose |
|---------|------|---------|
| Kafka | 9092 | Event streaming backbone |
| Zookeeper | 2181 | Kafka coordination |
| Redis | 6379 | Rate limiting, session state, dedup, query cache |
| ClickHouse | 9000 / 8123 | OLAP analytical store |
| PostgreSQL | 5432 | OLTP metadata (orgs, apps, funnels, alerts, experiments) |
| MongoDB | 27017 | Raw event archive + user profiles (90-day TTL) |
| Prometheus | 9090 | Metrics collection |
| Grafana | 3000 | Metrics dashboards (admin/admin) |
| Jaeger | 16686 | Distributed trace UI |
| OTel Collector | 4317 / 4318 | OpenTelemetry aggregation |

---

## Technology Stack

| Layer | Technology | Why |
|-------|-----------|-----|
| **Language** | Go 1.25 | High throughput, minimal GC, first-class concurrency |
| **Frontend** | React 18 + Vite | Fast dev server, small bundles, Vite proxy for local dev |
| **Event Streaming** | Apache Kafka (franz-go) | 12-partition topics, snappy compression, consumer groups |
| **OLAP Database** | ClickHouse | `windowFunnel`, `uniqHLL12`, Materialized Views, S3 TTL tiering |
| **Metadata DB** | PostgreSQL 16 (pgx/v5) | ACID transactions, Aurora-compatible |
| **Profile Store** | MongoDB 7 | Schema-flexible user profiles + 90-day TTL raw archive |
| **Cache / RL** | Redis 7 (go-redis/v9) | Lua token bucket, session state, bloom fallback, query cache |
| **Auth** | JWT HS256 (golang-jwt/v5) | Stateless tokens; API key → JWT exchange at `/v1/auth/login` |
| **GeoIP** | MaxMind GeoIP2 | In-process lookup ~1µs; no external HTTP on hot path |
| **Dedup** | bits-and-blooms + Redis SET | Two-stage: fast probabilistic + exact distributed |
| **Metrics** | Prometheus + Grafana | Counters/histograms/gauges per service |
| **Tracing** | OpenTelemetry + Jaeger | Spans across HTTP and Kafka; configurable sampling |
| **Config** | Viper | YAML files + `PULSE_*` environment variable overrides |
| **Logging** | go.uber.org/zap | JSON in prod, colored console in dev |
| **API Docs** | swaggo/swag (Swagger 2.0) | Embedded in each HTTP service at `/swagger/` |
| **Containers** | Distroless `gcr.io/distroless/static-debian12` | ~15 MB images, no shell |
| **Orchestration** | Kubernetes (GKE) + KEDA | HPA on CPU; KEDA on Kafka consumer lag |
| **CI/CD** | GitHub Actions + ArgoCD | lint → test → build → Trivy scan → GitOps deploy |

---

## Project Structure

```
pulse-analytics/
├── cmd/                              # One binary per service
│   ├── gateway/main.go               # Ingest Gateway (:8080)
│   ├── enricher/main.go              # Event Enricher
│   ├── session/main.go               # Session Engine
│   ├── funnel/main.go                # Funnel Processor
│   ├── chwriter/main.go              # ClickHouse Writer
│   ├── queryapi/
│   │   ├── main.go                   # Query API (:8082) + JWT auth wiring
│   │   ├── analytics_handlers.go     # Analytics query endpoints
│   │   └── mgmt_handlers.go          # CRUD management endpoints
│   ├── alertengine/main.go           # Alert Engine
│   ├── authservice/main.go           # Standalone Auth Service (:8083)
│   └── notificationservice/main.go   # Notification Service
│
├── internal/                         # Shared packages (not importable externally)
│   ├── auth/                         # JWT generation + validation, middleware
│   ├── bulkhead/                     # Per-tenant + global concurrency limiter
│   ├── cache/                        # In-process L1 cache with stale-while-revalidate
│   ├── circuitbreaker/               # Circuit breaker (closed/open/half-open)
│   ├── clickhouse/                   # ClickHouse client + buffered sharded writer
│   ├── config/                       # Viper config loader (all Config structs)
│   ├── consistent/                   # Consistent hash ring (FNV-1a)
│   ├── dedup/                        # Two-stage bloom + Redis deduplication
│   ├── enricher/                     # GeoIP + UA enrichment logic
│   ├── funnel/                       # Funnel step evaluation
│   ├── geo/                          # MaxMind GeoIP2 wrapper
│   ├── health/                       # Deep readiness checker (parallel dependency probes)
│   ├── kafka/                        # franz-go producer + consumer wrappers
│   ├── metrics/                      # Prometheus registry and instruments
│   ├── models/                       # Domain types: Event, App, Org, Alert, ...
│   ├── mongo/                        # MongoDB client + archive operations
│   ├── postgres/                     # pgx/v5 pool + all CRUD methods (R/W split)
│   ├── querying/                     # Analytics query service (ClickHouse + caching)
│   ├── ratelimit/                    # Redis Lua token-bucket rate limiter
│   ├── redis/                        # Redis cluster client, Lua scripts
│   ├── session/                      # Session boundary detection
│   ├── tracing/                      # OpenTelemetry OTLP gRPC setup
│   └── validator/                    # Event payload validation
│
├── frontend/                         # React/Vite SPA
│   ├── src/
│   │   ├── api/
│   │   │   ├── client.js             # Base HTTP client (JWT auth, 401 redirect)
│   │   │   ├── gateway.js            # Ingest gateway calls
│   │   │   └── queryapi.js           # Analytics + CRUD API calls
│   │   ├── context/
│   │   │   ├── AuthContext.jsx       # JWT storage, login/logout
│   │   │   └── ToastContext.jsx
│   │   └── pages/
│   │       ├── Login.jsx             # API key → JWT login + demo mode
│   │       ├── Dashboard.jsx
│   │       ├── EventAnalytics.jsx
│   │       ├── Funnels.jsx
│   │       ├── Sessions.jsx
│   │       ├── Retention.jsx
│   │       ├── ActiveUsers.jsx
│   │       ├── Cohorts.jsx
│   │       ├── Experiments.jsx
│   │       ├── Alerts.jsx
│   │       ├── SDKIngest.jsx
│   │       └── Settings.jsx
│   └── vite.config.js                # Proxy: /api/gateway→:8080, /api/query→:8082
│
├── sdk/
│   ├── go/client.go                  # Go ingest client (batching, gzip, flush)
│   └── js/index.ts                   # TypeScript browser + Node.js SDK
│
├── migrations/
│   ├── postgres/
│   │   ├── 001_init.sql              # Core schema (orgs, apps, funnels, ...)
│   │   └── 002_analytics.sql
│   ├── clickhouse/
│   │   ├── 001_init.sql              # events table + Distributed table
│   │   └── 002_analytics.sql         # Materialized Views (DAU, hourly, revenue)
│   └── mongo/
│       └── indexes.js                # TTL + compound indexes
│
├── configs/
│   ├── base.yaml                     # Shared defaults
│   ├── gateway.yaml                  # Gateway overrides (port, Kafka acks, sampling)
│   ├── enricher.yaml                 # Enricher overrides
│   ├── queryapi.yaml                 # Query API overrides (write timeout 60s)
│   ├── otel-collector.yaml           # OTel Collector pipeline
│   └── prometheus.yaml               # Prometheus scrape config
│
├── deployments/
│   ├── docker/
│   │   ├── gateway.Dockerfile        # Multi-stage: go build → distroless
│   │   ├── base.Dockerfile           # Parameterised (BUILD_CMD arg)
│   │   └── authservice.Dockerfile
│   └── k8s/                          # Kubernetes manifests + KEDA ScaledObjects
│
├── docs/                             # Generated Swagger specs
├── proto/events/v1/events.proto      # Protobuf schema
├── api/                              # OpenAPI 3.0 YAML specs
├── docker-compose.yml                # Full local dev stack
├── Makefile
└── go.mod
```

---

## Quick Start (Local)

### Prerequisites

- Docker 24+ and Docker Compose v2
- Go 1.25+
- Node.js 20+

### 1. Clone and start the full stack

```bash
git clone https://github.com/your-org/pulse-analytics.git
cd pulse-analytics

# Start all infrastructure + application services
docker compose up -d

# Check health (wait ~30s)
docker compose ps
```

### 2. Run database migrations

```bash
make migrate-all
# or individually:
make migrate-postgres
make migrate-clickhouse
make migrate-mongo
```

### 3. Register an org and get an API key

```bash
curl -X POST http://localhost:8082/v1/orgs/register \
  -H 'Content-Type: application/json' \
  -d '{"org_name":"Acme Corp","app_name":"My App","email":"admin@acme.com"}'

# Response: { "org_id": "...", "app_id": "...", "api_key": "pk_live_..." }
```

### 4. Start the frontend

```bash
cd frontend
npm install
npm run dev
# Open http://localhost:5173
```

Enter your API key on the login page. The frontend exchanges it for a JWT via `POST /v1/auth/login`.

### 5. Send your first events

```bash
curl -X POST http://localhost:8080/v1/events \
  -H 'X-API-Key: pk_live_...' \
  -H 'Content-Type: application/json' \
  -d '{
    "app_id": "app_...",
    "user_id": "user_001",
    "device_id": "device_abc",
    "events": [
      { "event_name": "app_open",            "props": { "version": "2.1.0" } },
      { "event_name": "purchase_completed",  "revenue": 29.99 }
    ]
  }'
```

### 6. Access dashboards

| URL | Service |
|-----|---------|
| http://localhost:5173 | React Frontend |
| http://localhost:8080/swagger/ | Gateway Swagger UI |
| http://localhost:8082/swagger/ | Query API Swagger UI |
| http://localhost:3000 | Grafana (admin / admin) |
| http://localhost:9090 | Prometheus |
| http://localhost:16686 | Jaeger Tracing |

---

## Running Services Individually

```bash
# Start infrastructure only (no application services)
make infra-up && make migrate-all

# Build all binaries
make build

# Run each service in a separate terminal
make run-gateway       # :8080
make run-enricher
make run-session
make run-funnel
make run-chwriter
make run-queryapi      # :8082
make run-alertengine
```

Each `run-*` target injects the correct env vars for local infrastructure automatically.

---

## Authentication

PulseAnalytics uses a **two-tier authentication model**:

### Ingest Gateway — API Key

Events are sent to the gateway using the `X-API-Key` header. The gateway looks up the key in PostgreSQL (cached 5 min in Redis) and enforces per-app rate limits.

```http
POST /v1/events
X-API-Key: pk_live_your_api_key_here
```

API keys are issued during org registration and can be rotated via the Query API (`POST /v1/apps/:id/rotate-key`).

### Query API — JWT (HS256)

All Query API endpoints require a JWT Bearer token.

**Step 1 — Exchange your API key for a JWT:**

```bash
curl -X POST http://localhost:8082/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{ "api_key": "pk_live_..." }'

# Response:
{ "token": "eyJ...", "app_id": "app_...", "org_id": "org_..." }
```

**Step 2 — Use the JWT for all subsequent requests:**

```bash
curl http://localhost:8082/v1/events/count?granularity=day \
  -H 'Authorization: Bearer eyJ...'
```

JWT tokens include `app_id`, `org_id`, and `role` claims and expire after 7 days. On `401`, the frontend clears localStorage and redirects to `/login`.

**Demo Mode:** When the backend is unreachable the login page generates a local demo JWT from any non-empty API key. A toast message indicates demo mode.

---

## API Reference

### Ingest Gateway — `:8080`

**Auth:** `X-API-Key` header on all routes except `/health`.

#### `POST /v1/events`

Ingest a batch of up to 500 events.

```json
{
  "app_id":    "app_...",
  "user_id":   "user_001",
  "device_id": "device_abc",
  "events": [
    {
      "event_id":   "optional-uuid",
      "event_name": "purchase_completed",
      "event_time": 1700000000000,
      "revenue":    29.99,
      "props": { "item_id": "sku_999", "category": "electronics" }
    }
  ]
}
```

Response `202 Accepted`:
```json
{ "accepted": 1, "filtered": 0 }
```

#### `POST /v1/identify`

Upsert user profile traits into MongoDB.

```json
{ "user_id": "user_001", "traits": { "email": "user@example.com", "plan": "pro" } }
```

#### `GET /health`

Returns `{ "status": "ok" }`.

---

### Query API — `:8082`

**Auth:** `Authorization: Bearer <JWT>` on all routes except `/v1/auth/login` and `/health`.

---

#### Authentication

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/auth/login` | None | Exchange API key for JWT |

```bash
POST /v1/auth/login
{ "api_key": "pk_live_..." }
# → { "token": "eyJ...", "app_id": "...", "org_id": "..." }
```

---

#### Analytics Queries

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/events/count` | Event count by name, over a time range |
| `GET` | `/v1/events/series` | Time-series event counts |
| `GET` | `/v1/events/breakdown` | Event count broken down by a property |
| `GET` | `/v1/users/active` | DAU / WAU / MAU |
| `GET` | `/v1/sessions/summary` | Session count, avg duration, bounce rate |
| `GET` | `/v1/retention` | Retention cohort table (N-day retention) |
| `GET` | `/v1/funnels/:id/results` | Funnel step conversion rates |

**Common query parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `from` | ISO8601 | Start of time range |
| `to` | ISO8601 | End of time range |
| `event_name` | string | Filter by event name |
| `granularity` | string | `hour` \| `day` \| `week` \| `month` |
| `breakdown` | string | Property key to group by |
| `platform` | string | `ios` \| `android` \| `web` \| … |
| `country` | string | ISO 3166-1 alpha-2 |

---

#### App Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/apps` | List all apps in the org |
| `POST` | `/v1/apps` | Create a new app |
| `PUT` | `/v1/apps/:id` | Update app name / rate limits |
| `DELETE` | `/v1/apps/:id` | Deactivate an app (soft delete) |
| `POST` | `/v1/apps/:id/rotate-key` | Rotate the app's API key |

```json
// POST /v1/apps body
{ "name": "My Mobile App", "rps": 5000, "burst": 25000 }

// PUT /v1/apps/:id body
{ "name": "Updated Name", "rps": 10000, "burst": 50000 }
```

---

#### Org Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/orgs` | Get current org details |
| `POST` | `/v1/orgs/register` | Register a new org + default app (public) |
| `PUT` | `/v1/orgs/:id` | Update org name or plan |

```json
// POST /v1/orgs/register body
{ "org_name": "Acme Corp", "app_name": "Acme App", "email": "admin@acme.com" }

// → { "org_id": "...", "app_id": "...", "api_key": "pk_live_..." }
```

---

#### Funnel Definitions

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/funnels` | List funnel definitions for the app |
| `POST` | `/v1/funnels` | Create or upsert a funnel |
| `GET` | `/v1/funnels/:id/results` | Query funnel conversion results |

```json
// POST /v1/funnels body
{
  "name": "Activation Funnel",
  "steps": ["app_open", "sign_up", "onboarding_complete", "purchase_completed"],
  "window_seconds": 604800
}
```

---

#### Alert Rules

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/alerts` | List active alert rules |
| `POST` | `/v1/alerts` | Create an alert rule |
| `PUT` | `/v1/alerts/:id` | Update an alert rule |
| `DELETE` | `/v1/alerts/:id` | Delete an alert rule |

```json
// POST /v1/alerts body
{
  "name":        "High error rate",
  "metric_name": "error_occurred",
  "condition":   "gt",
  "threshold":   100,
  "window_mins": 5,
  "channels":    ["webhook", "email"],
  "webhook_url": "https://hooks.slack.com/services/...",
  "email_to":    "oncall@acme.com"
}
```

**Conditions:** `gt` | `lt` | `gte` | `lte`

---

#### Cohort Definitions

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/cohorts` | List cohort definitions |
| `POST` | `/v1/cohorts` | Create a cohort |
| `DELETE` | `/v1/cohorts/:id` | Delete a cohort |

```json
// POST /v1/cohorts body
{
  "name":        "Power Users",
  "description": "Users with 10+ sessions in last 30 days",
  "filter_sql":  "user_id IN (SELECT user_id FROM events WHERE event_time > now() - INTERVAL 30 DAY GROUP BY user_id HAVING countDistinct(session_id) >= 10)"
}
```

---

#### Experiments (A/B Testing)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/experiments` | List experiments |
| `POST` | `/v1/experiments` | Create an experiment |
| `PUT` | `/v1/experiments/:id` | Update experiment status / goal |
| `DELETE` | `/v1/experiments/:id` | Delete an experiment |

```json
// POST /v1/experiments body
{
  "name":        "Checkout button colour",
  "description": "Test green vs blue CTA",
  "status":      "draft",
  "goal_event":  "purchase_completed",
  "variants": [
    { "id": "control",   "name": "Blue",  "weight": 50 },
    { "id": "treatment", "name": "Green", "weight": 50 }
  ]
}
```

**Statuses:** `draft` → `running` → `paused` | `completed`

---

## Database Schemas

### PostgreSQL (Metadata / OLTP)

```sql
-- Organisations
CREATE TABLE orgs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    plan       TEXT        NOT NULL DEFAULT 'free'
                           CHECK (plan IN ('free', 'growth', 'enterprise')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Apps / tenants
CREATE TABLE apps (
    id         UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID    NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    api_key    TEXT    NOT NULL UNIQUE,
    rps        FLOAT   NOT NULL DEFAULT 10000,
    burst      INT     NOT NULL DEFAULT 50000,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX apps_api_key_idx ON apps(api_key) WHERE active = TRUE;

-- Funnel definitions
CREATE TABLE funnel_definitions (
    funnel_id      UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id         UUID    NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    steps          TEXT[]  NOT NULL,           -- ordered event names
    window_seconds BIGINT  NOT NULL DEFAULT 604800
);

-- Alert rules
CREATE TABLE alert_rules (
    id          UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID    NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    metric_name TEXT    NOT NULL,
    condition   TEXT    NOT NULL CHECK (condition IN ('gt','lt','gte','lte')),
    threshold   FLOAT   NOT NULL,
    window_mins INT     NOT NULL DEFAULT 5,
    channels    TEXT[]  NOT NULL DEFAULT '{}',
    webhook_url TEXT,
    email_to    TEXT,
    active      BOOLEAN NOT NULL DEFAULT TRUE
);

-- Cohort definitions
CREATE TABLE cohort_definitions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    filter_sql  TEXT NOT NULL,
    user_count  BIGINT DEFAULT 0
);

-- Experiments
CREATE TABLE experiments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'draft',
    goal_event  TEXT,
    variants    JSONB,
    start_at    TIMESTAMPTZ,
    end_at      TIMESTAMPTZ
);
```

### ClickHouse (OLAP / Analytics)

```sql
CREATE TABLE events
(
    app_id         LowCardinality(String),
    event_id       UUID                DEFAULT generateUUIDv4(),
    user_id        String,
    device_id      String,
    event_name     LowCardinality(String),
    event_time     DateTime64(3, 'UTC'),
    server_time    DateTime64(3, 'UTC') DEFAULT now64(3),
    session_id     String,
    country_code   LowCardinality(FixedString(2)),
    platform       LowCardinality(String),
    app_version    LowCardinality(String),
    os_family      LowCardinality(String),
    browser        LowCardinality(String),
    city           LowCardinality(String),
    revenue        Nullable(Float64),
    props          Map(String, String),
    campaign_id    LowCardinality(String),
    install_source LowCardinality(String),
    INDEX props_bf props TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX event_name_minmax event_name TYPE set(1000) GRANULARITY 1
)
ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/events', '{replica}')
PARTITION BY (toYYYYMMDD(event_time), app_id)
ORDER BY (app_id, event_name, toStartOfHour(event_time), user_id)
TTL event_time + INTERVAL 90 DAY  TO DISK 's3_cold',
    event_time + INTERVAL 365 DAY DELETE;
```

**Key Materialized Views** (pre-aggregated for fast dashboard queries):

| View | Engine | Purpose |
|------|--------|---------|
| `dau_mv` | `AggregatingMergeTree` | Daily active users via `uniqState(user_id)` — <10ms DAU |
| `hourly_counts_mv` | `SummingMergeTree` | Hourly event counts per app |
| `revenue_mv` | `SummingMergeTree` | Daily revenue aggregates per platform |

**Key ClickHouse functions used:**
- `windowFunnel(window)(timestamp, step1=true, step2=true, …)` — funnel analysis
- `uniqHLL12(user_id)` — approximate unique user counts at massive scale
- `uniqMerge(dau_state)` — fast aggregation from pre-computed `uniqState`
- `countIf(condition)` — filtered event counts

### MongoDB (Profiles / Archive)

| Collection | Purpose |
|-----------|---------|
| `raw_events` | Full raw event archive. TTL index on `server_time` → 90-day expiry. |
| `user_profiles` | Merged user traits from `/v1/identify`. Keyed on `(app_id, user_id)`. |

Indexes: unique `event_id`, TTL `server_time`, compound `(app_id, event_time)`, Atlas Search on `event_name` and `props`.

---

## Kafka Topics

| Topic | Partitions | Producer | Consumer(s) | Description |
|-------|-----------|----------|------------|-------------|
| `raw-events` | 12 | Gateway | Enricher | Raw event batches as received |
| `enriched-events` | 12 | Enricher | CH-Writer, Session Engine, Funnel Processor | With GeoIP, UA, session ID |
| `session-events` | 6 | Session Engine | CH-Writer | Session start/end synthetics |
| `agg-results` | 4 | CH-Writer / Funnel | Downstream | Aggregation results |
| `dlq-events` | 2 | Any service | Dead Letter Monitor | Failed processing |
| `notifications` | 2 | Alert Engine | Notification Service | Alert payloads |

**Partition key:** `app_id:device_id` — guarantees per-device ordering while distributing load.

**Compression:** Snappy on all topics — good CPU/size tradeoff at high throughput.

---

## Rate Limiting

Redis Lua **token-bucket** algorithm, evaluated atomically per API key:

```
Algorithm per request:
  1. GETSET pulse:rl:{api_key} → { tokens, last_refill_ts }
  2. Refill = elapsed_seconds × rps
  3. tokens = min(tokens + refill, burst)
  4. If tokens >= batch_size: tokens -= batch_size; allow
  5. Else: return 429
```

Per-app limits are stored in `apps.rps` / `apps.burst` in PostgreSQL:

| Plan | RPS | Burst |
|------|-----|-------|
| Free | 1,000 | 5,000 |
| Growth | 10,000 | 50,000 |
| Enterprise | 100,000 | 500,000 |

If Redis is unreachable, falls back to an in-process `golang.org/x/time/rate` limiter.

---

## Caching Strategy

Four-tier cache to serve most dashboard queries from memory:

```
Request
  │
  ▼
L1: In-process L1Cache         TTL: 60s, stale-while-revalidate (per pod)
  │ miss / stale               SWR: returns stale data + triggers async refresh
  ▼
L2: Redis cluster              TTL: 5 min  (shared across all Query API replicas)
  │ miss
  ▼
L3: ClickHouse Materialized Views          (pre-aggregated hourly/daily rollups)
  │ miss
  ▼
L4: ClickHouse raw events table            (full scan, worst case)
```

Additional resilience in the Query API: **single-flight** coalesces concurrent identical queries into one ClickHouse call; a **circuit breaker** (5 failures → 30s open) prevents cascade failures; a **bulkhead** (20 per-tenant / 200 global) stops noisy tenants from starving others.

Cache keys are SHA256 hashes of `app_id + query_type + time_range + filters`. Expiry is TTL-only (no event-driven invalidation); dashboards display a "last updated at" timestamp to communicate staleness.

---

## Deduplication

Events are deduplicated at two stages to prevent double-counting across Gateway replicas:

**Stage 1 — In-process Bloom Filter** (fast path, zero network hops)
- Per-app `bits-and-blooms` filter with 0.1% false positive rate
- Memory only, checked before touching Redis

**Stage 2 — Redis SET** (exact, distributed)
- Key: `pulse:dedup:{app_id}:{event_id}`
- TTL: 24 hours
- Guarantees correctness even with multiple Gateway pods

Events that pass both stages are published to Kafka. ClickHouse `ReplicatedMergeTree` provides a final merge-level dedup on `event_id` for the cluster case.

---

## Observability

### Metrics (Prometheus)

All services expose `/metrics`. Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `pulse_ingest_requests_total` | Counter | Gateway requests (labeled by `status`) |
| `pulse_ingest_latency_seconds` | Histogram | Gateway P50/P95/P99 |
| `pulse_kafka_published_total` | Counter | Kafka publishes (labeled by `topic`, `status`) |
| `pulse_kafka_consumer_lag` | Gauge | Consumer group lag per topic |
| `pulse_clickhouse_insert_duration_seconds` | Histogram | CH-Writer batch insert latency |
| `pulse_ratelimit_rejected_total` | Counter | Rate-limited requests per app |
| `pulse_query_requests_total` | Counter | Query API requests (by `query_type`) |
| `pulse_query_latency_seconds` | Histogram | Query P95 per type |
| `pulse_active_sessions` | Gauge | Open sessions in Redis |
| `pulse_alert_evaluations_total` | Counter | Alert rule evaluations |

Grafana at `http://localhost:3000` with pre-provisioned Prometheus data source.

**SLO alert rules:**
- Ingest error rate > 1% → critical
- Gateway P95 > 200ms → warning
- Kafka consumer lag > 1M messages → critical
- Query API P95 > 200ms → SLO breach

### Distributed Tracing (OpenTelemetry)

Trace context propagated via HTTP headers (`traceparent`) and Kafka message headers. OTLP gRPC export to collector at `:4317`, forwarded to Jaeger.

Configure sampling rate per service:
```yaml
# configs/gateway.yaml
telemetry:
  samplingrate: 0.001   # 0.1% — high-traffic gateway

# configs/queryapi.yaml
telemetry:
  samplingrate: 0.1     # 10% — query API
```

Jaeger UI: `http://localhost:16686`

### Structured Logging (zap)

- `production`: JSON format, INFO level
- `development`: coloured console, DEBUG level
- Every line includes `service`, `env`, `trace_id`, `span_id`

Set via `PULSE_SERVICE_ENVIRONMENT=production`.

---

## Configuration Reference

Configuration is loaded from a YAML file (set by `CONFIG_PATH`) then overridden by `PULSE_*` environment variables.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PULSE_KAFKA_BROKERS` | `localhost:9092` | Comma-separated broker list |
| `PULSE_REDIS_ADDRS` | `localhost:6379` | Comma-separated Redis addresses |
| `PULSE_POSTGRES_DSN` | — | PostgreSQL connection string |
| `PULSE_CLICKHOUSE_HOSTS` | `localhost:9000` | Comma-separated ClickHouse hosts |
| `PULSE_CLICKHOUSE_USER` | `pulse` | ClickHouse user |
| `PULSE_CLICKHOUSE_PASSWORD` | — | ClickHouse password |
| `PULSE_CLICKHOUSE_DATABASE` | `pulse` | ClickHouse database |
| `PULSE_MONGO_URI` | `mongodb://localhost:27017` | MongoDB connection URI |
| `PULSE_MONGO_DATABASE` | `pulse` | MongoDB database |
| `PULSE_AUTH_JWT_SECRET` | — | HS256 signing secret (min 32 chars, **required in prod**) |
| `PULSE_AUTH_JWT_EXPIRY` | `24h` | JWT token lifetime (24 hours) |
| `PULSE_AUTH_API_KEY_TTL` | `300s` | Redis API key cache TTL |
| `PULSE_GEOIP_DB_PATH` | `/data/GeoLite2-City.mmdb` | MaxMind GeoIP2 database |
| `PULSE_BLOOM_CAPACITY` | `1000000000` | Bloom filter capacity (1B events) |
| `PULSE_BLOOM_FP_RATE` | `0.001` | Bloom filter false positive rate |
| `PULSE_OTEL_ENDPOINT` | `localhost:4317` | OTel collector gRPC endpoint |
| `PULSE_SERVICE_ENVIRONMENT` | `development` | `development` / `staging` / `production` |
| `PULSE_SERVICE_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `CONFIG_PATH` | service-specific | Override YAML config file path |

### Production Example

```bash
export PULSE_KAFKA_BROKERS="kafka-1:9092,kafka-2:9092,kafka-3:9092"
export PULSE_REDIS_ADDRS="redis-cluster:6379"
export PULSE_POSTGRES_DSN="postgres://pulse:secret@aurora.us-east-1.rds.amazonaws.com:5432/pulse"
export PULSE_CLICKHOUSE_HOSTS="ch-1:9000,ch-2:9000,ch-3:9000"
export PULSE_AUTH_JWT_SECRET="$(openssl rand -hex 32)"
export PULSE_SERVICE_ENVIRONMENT="production"
```

---

## Client SDKs

### Go SDK

```go
import pulse "github.com/pulse-analytics/sdk/go"

client := pulse.New(
    "https://gateway.pulse-analytics.io",
    "pk_live_your_api_key",
    pulse.WithAppID("app_abc123"),
    pulse.WithDeviceID("device-uuid"),
    pulse.WithMaxBatch(100),
    pulse.WithFlushInterval(2*time.Second),
)
defer client.Close()

// Non-blocking — batched and flushed automatically
client.Track(ctx, pulse.Event{
    EventName: "purchase_completed",
    Revenue:   29.99,
    Props:     map[string]any{"item_id": "sku_999"},
})

// Identify a user
client.Identify(ctx, pulse.IdentifyPayload{
    UserID: "user_001",
    Traits: map[string]any{"plan": "pro"},
})
```

**Features:** background flush goroutine, auto-flush on max batch, gzip compression, thread-safe.

### TypeScript / JavaScript SDK

```typescript
import { PulseClient } from '@pulse-analytics/sdk';

const pulse = new PulseClient({
  baseUrl:         'https://gateway.pulse-analytics.io',
  apiKey:          'pk_live_your_api_key',
  appId:           'app_abc123',
  deviceId:        crypto.randomUUID(),
  userId:          currentUser.id,
  flushIntervalMs: 2000,
});

// Non-blocking
pulse.track('page_viewed', { path: '/pricing' });
pulse.track('purchase_completed', { price: 29.99 }, /*revenue=*/ 29.99);

// Identify after login
pulse.identify({ user_id: 'user_001', traits: { plan: 'pro' } });

// Flush before page unload
window.addEventListener('beforeunload', () => pulse.flush());
```

**Features:** works in browser (`keepalive: true` for page-unload delivery) and Node.js (`unref()` timer for clean exit), TypeScript types throughout.

---

## Running on Minikube

Run the entire PulseAnalytics stack locally on Minikube — all 9 services, full LGTM observability, and Locust load testing.

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| minikube | ≥ 1.33 | https://minikube.sigs.k8s.io/docs/start/ |
| kubectl | ≥ 1.28 | https://kubernetes.io/docs/tasks/tools/ |
| docker | ≥ 24 | https://docs.docker.com/get-docker/ |
| helm | ≥ 3.14 | https://helm.sh/docs/intro/install/ |
| python3 + pip | ≥ 3.10 | (for local Locust) |

**Recommended Minikube resources:** 4 CPUs, 8 GB RAM, 40 GB disk.

### One-command deploy

```bash
make minikube-deploy
```

This runs the full sequence: start Minikube → build images → install Strimzi → deploy infra → deploy LGTM stack → deploy services → deploy Locust.

### Step-by-step

```bash
# 1. Start Minikube
make minikube-start

# 2. Build all service images into Minikube's Docker daemon (no registry needed)
make minikube-build

# 3. Install Strimzi Kafka Operator
make minikube-install-strimzi

# 4. Deploy infrastructure (Redis, ClickHouse, Postgres, Mongo, Kafka)
make minikube-deploy-infra

# 5. Deploy LGTM observability stack
make minikube-deploy-monitoring

# 6. Deploy application services
make minikube-deploy-services

# 7. Deploy Locust load test
make minikube-deploy-loadtest

# 8. Print all access URLs
make minikube-urls
```

### Accessing services

After deploy, add to `/etc/hosts`:

```
$(minikube ip)  pulse.local api.pulse.local grafana.pulse.local
```

| Service | URL | Notes |
|---------|-----|-------|
| Gateway (ingest) | `http://pulse.local` | or NodePort via `minikube service gateway -n pulse` |
| Query API | `http://api.pulse.local` | or NodePort via `minikube service query-api -n pulse` |
| Grafana | `http://grafana.pulse.local` | admin / pulse-admin |
| Locust UI | `minikube service locust-master -n loadtest` | load test control |
| Prometheus | `kubectl port-forward svc/prometheus 9090:9090 -n monitoring` | |
| Mimir | `kubectl port-forward svc/mimir 9009:9009 -n monitoring` | |
| Loki | `kubectl port-forward svc/loki 3100:3100 -n monitoring` | |
| Tempo | `kubectl port-forward svc/tempo 3200:3200 -n monitoring` | |

### Useful commands

```bash
make minikube-status    # pod status across all namespaces
make minikube-logs      # tail all service logs
make minikube-grafana   # open Grafana in browser
make minikube-locust    # open Locust UI in browser
make minikube-stop      # stop Minikube (preserves state)
make minikube-delete    # delete cluster entirely
```

---

## Observability — LGTM Stack

PulseAnalytics ships a full **Grafana LGTM** (Loki + Grafana + Tempo + Mimir) stack with OpenTelemetry as the unified collection layer.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         TELEMETRY PIPELINE                                   │
│                                                                              │
│  All Services                                                                │
│  (OTLP gRPC :4317)  ──►  OpenTelemetry Collector                            │
│                               │                                              │
│                    ┌──────────┼──────────────┐                               │
│                    ▼          ▼              ▼                               │
│                 Tempo       Mimir           Loki                             │
│               (traces)    (metrics)        (logs)                            │
│                    └──────────┼──────────────┘                               │
│                               ▼                                              │
│                            Grafana                                           │
│                    (unified dashboards :3000)                                │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Role | Port |
|-----------|------|------|
| **OpenTelemetry Collector** | Receives OTLP traces/metrics/logs; routes to backends | 4317 (gRPC), 4318 (HTTP) |
| **Grafana Tempo** | Distributed tracing backend (replaces Jaeger) | 3200 |
| **Grafana Mimir** | Long-term metrics storage (Prometheus-compatible) | 9009 |
| **Grafana Loki** | Log aggregation (structured JSON logs from all pods) | 3100 |
| **Prometheus** | Short-term scrape + remote_write to Mimir | 9090 |
| **Grafana** | Unified UI: dashboards, trace explorer, log viewer | 3000 |

### OTel Collector pipeline

```
Receivers:   otlp (gRPC/HTTP), filelog (pod logs), prometheus (self-scrape)
Processors:  memory_limiter → k8sattributes → resource → batch → tail_sampling
Exporters:
  traces  → Tempo  (otlp/grpc)
  metrics → Mimir  (prometheusremotewrite) + Prometheus scrape endpoint (:8889)
  logs    → Loki   (loki exporter)
```

Tail-based sampling policy:
- **100%** of error traces
- **100%** of traces with latency > 200ms
- **10%** probabilistic sampling for the rest

### Pre-built Grafana dashboards

| Dashboard | UID | Description |
|-----------|-----|-------------|
| Ingest Gateway | `pulse-ingest` | Events/sec, error rate, P95 latency, Kafka lag, logs, traces |
| Query API | `pulse-queryapi` | Request rate, latency percentiles, cache hit rate, CH insert rate, logs |
| Infrastructure | `pulse-infra` | Kafka lag by topic, Redis memory, ClickHouse queries, pod CPU/memory |
| SLO Tracking | `pulse-slo` | Ingest availability, query P95 SLO, error budget |
| Sessions & Funnels | `pulse-sessions` | Session rate, duration percentiles, logs |
| Load Test | `pulse-loadtest` | Locust RPS, failures, response time, active users |

### Trace correlation

Grafana is configured with full trace-to-log and trace-to-metric correlation:
- Click a trace in Tempo → jump to matching Loki logs for that pod/namespace
- Click a span → see related Mimir metrics for that service
- Service map auto-generated from Tempo span metrics

---

## Load Testing with Locust

### Run inside the cluster (recommended)

Locust runs in distributed mode (1 master + 2 workers) inside the `loadtest` namespace, targeting the gateway service directly via ClusterIP DNS.

```bash
# Deploy Locust
make minikube-deploy-loadtest

# Open Locust web UI
make minikube-locust

# In the UI: set number of users (e.g. 50) and spawn rate (e.g. 5), click Start
```

### Run locally against Minikube

```bash
# Install Locust
make loadtest-install

# Interactive web UI
make loadtest-run

# Headless 5-minute test (50 users)
make loadtest-headless
```

### Load test scenarios

The `loadtest/locustfile.py` simulates two user types:

**SDKUser (80% weight)** — event ingestion:
- Small batches (1–10 events): 70% of requests
- Medium batches (25–100 events): 20% of requests
- Large batches (200–500 events): 5% of requests
- Health checks: 5%

**DashboardUser (20% weight)** — analytics queries:
- Event count queries: 40%
- Active users queries: 30%
- Retention queries: 20%
- Health checks: 10%

### Viewing results in Grafana

Open the **Load Test (Locust)** dashboard in Grafana to see Locust metrics (RPS, failures, response time, active users) alongside the gateway's real-time event throughput — all in one view.

---

## Kubernetes Deployment

### Cluster Topology (GKE)

```
GKE Cluster — Namespace: pulse-prod
│
├── gateway             HPA: 3–200 pods  │ scale on CPU >60% + RPS metric
├── enricher            KEDA: Kafka lag   │ raw-events lag > 50K
├── session-engine      KEDA: Kafka lag   │ enriched-events lag > 50K
├── funnel-processor    KEDA: Kafka lag   │ enriched-events lag > 50K
├── ch-writer           KEDA: Kafka lag   │ enriched-events lag > 100K
├── query-api           HPA: 2–20 pods   │ scale on CPU >70%
├── alert-engine        1 replica (leader election)
└── notification-service 2 replicas

Managed Services:
  Cloud Spanner / Aurora PostgreSQL
  Confluent Cloud Kafka / MSK
  Redis Memorystore / ElastiCache
  ClickHouse Cloud / self-managed
  MongoDB Atlas
```

### Deploy

```bash
export IMAGE_TAG=$(git rev-parse --short HEAD)
make docker-build docker-push ECR=123456789.dkr.ecr.us-east-1.amazonaws.com
kubectl apply -f deployments/k8s/
```

### Required Secrets

```bash
kubectl -n pulse-prod create secret generic pulse-secrets \
  --from-literal=postgres-dsn="postgres://..." \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" \
  --from-literal=kafka-brokers="..." \
  --from-literal=redis-addrs="..."
```

### HPA Example (gateway)

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: gateway
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: gateway
  minReplicas: 3
  maxReplicas: 200
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 60
```

### KEDA Example (enricher — Kafka lag)

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: enricher
spec:
  scaleTargetRef:
    name: enricher
  minReplicaCount: 2
  maxReplicaCount: 50
  triggers:
  - type: kafka
    metadata:
      bootstrapServers: kafka:9092
      consumerGroup: enricher-group
      topic: raw-events
      lagThreshold: "50000"
```

---

## CI/CD Pipeline

```
GitHub Push / PR
  │
  ├─ lint        golangci-lint (5 min timeout)
  │
  ├─ test        go test ./... -race -coverprofile
  │              Redis sidecar for integration tests
  │
  └─ build  (on main / staging after lint+test)
       │
       ├─ [matrix: 9 services in parallel]
       │   ├─ docker buildx (layer cache via GitHub Actions cache)
       │   ├─ push to ECR
       │   └─ Trivy CRITICAL scan (blocks on findings)
       │
       └─ deploy  (main only, after build)
            ├─ Update image tags in GitOps repo (yq)
            └─ ArgoCD auto-syncs within 3 minutes
                 Argo Rollouts: canary 10%→25%→50%→100%
                 Auto-rollback: error_rate>1% or P95>500ms
```

---

## Makefile Reference

```
Minikube (full local cluster):
  make minikube-deploy            Full deploy: start + build + all services + LGTM + Locust
  make minikube-start             Start Minikube (4 CPU, 8GB RAM, docker driver)
  make minikube-build             Build all images into Minikube Docker daemon
  make minikube-install-strimzi   Install Strimzi Kafka Operator
  make minikube-deploy-infra      Deploy Redis/ClickHouse/Postgres/Mongo/Kafka
  make minikube-deploy-monitoring Deploy LGTM stack (Loki+Grafana+Tempo+Mimir+OTel+Prometheus)
  make minikube-deploy-services   Deploy all 9 application services
  make minikube-deploy-loadtest   Deploy Locust load test (master + 2 workers)
  make minikube-status            Show pod status across all namespaces
  make minikube-urls              Print all service access URLs
  make minikube-grafana           Open Grafana in browser
  make minikube-locust            Open Locust UI in browser
  make minikube-logs              Tail all service logs
  make minikube-stop              Stop Minikube (preserves state)
  make minikube-delete            Delete Minikube cluster entirely

Load Testing (local Locust):
  make loadtest-install           pip install locust
  make loadtest-run               Start Locust web UI (http://localhost:8089)
  make loadtest-headless          Run 50-user headless test for 5 min → loadtest/report.html

Infrastructure (local docker-compose):
  make infra-up           Start Kafka, Redis, ClickHouse, Postgres, Mongo, Observability
  make infra-down         Stop all infrastructure containers
  make migrate-all        Run all DB migrations (Postgres + ClickHouse + Mongo)
  make migrate-postgres   Postgres migrations only
  make migrate-clickhouse ClickHouse migrations only

Run services (requires make infra-up):
  make run-gateway        Ingest Gateway   :8080
  make run-enricher       Enrichment Service (Kafka consumer)
  make run-session        Session Engine     (Kafka consumer)
  make run-funnel         Funnel Processor   (Kafka consumer)
  make run-chwriter       ClickHouse Writer  (Kafka consumer)
  make run-queryapi       Query API          :8082
  make run-alertengine    Alert Engine       (scheduler)

Run everything via Docker Compose:
  make up                 Start all services
  make down               Stop all services
  make logs               Tail service logs

Development:
  make build              Build all binaries to ./bin/
  make test               go test ./... -race
  make test-cover         Tests with coverage report (coverage.html)
  make lint               golangci-lint
  make swagger            Regenerate Swagger docs (requires swag CLI)
  make clean              Remove ./bin/ and coverage files

Docker / deploy:
  make docker-build       Build all Docker images
  make docker-push        Tag and push images to ECR (set ECR= variable)
  make help               Print all targets
```

---

## Sharding & Replication

All four storage systems are configured for horizontal scale and replica-based read offloading out of the box.

### PostgreSQL — Read/Write Splitting

The `internal/postgres.Client` maintains a **write pool** (primary) and a **read pool** (one pool per replica DSN, round-robin). All `SELECT`-only methods automatically use a replica; all mutations and transactions go to the primary.

```yaml
postgres:
  dsn: "postgres://pulse:pass@primary:5432/pulse"
  replica_dsns:
    - "postgres://pulse:pass@replica-1:5432/pulse"
    - "postgres://pulse:pass@replica-2:5432/pulse"
```

### ClickHouse — Application-Level Sharding

`internal/clickhouse.Pool` opens one connection per shard host. `ShardedWriter` splits incoming event batches by `FNV-1a(app_id) % numShards`, routing each app's events to a consistent shard. Each shard has its own independent write-behind buffer (500K events, 1s flush interval), so a slow shard does not block the others. Read queries use a separate round-robin pool of `ReadHosts`.

```yaml
clickhouse:
  shard_hosts: ["ch-0:9000", "ch-1:9000", "ch-2:9000"]
  read_hosts:  ["ch-replica-0:9000", "ch-replica-1:9000"]
```

### MongoDB — Read Preference Routing

`internal/mongo.Client` maintains two `*mongo.Database` handles over one connection pool. Writes (`InsertRawBatch`, `UpsertUserProfile`) use the primary handle; reads (`GetRawEvents`, `GetUserProfile`) use a handle configured with the read preference from config (default: `secondaryPreferred`).

```yaml
mongo:
  uri: "mongodb://mongo-0:27017,mongo-1:27017,mongo-2:27017"
  read_preference: "secondaryPreferred"
  replica_set: "rs0"
```

### Redis — Cluster Mode

`internal/redis.Client` wraps `redis.UniversalClient` — tries cluster mode on startup, falls back to single-node automatically (useful for local dev). In production, a 6-node cluster (3 primary + 3 replica) with `RouteByLatency: true` provides automatic hash-slot routing and failover.

```yaml
redis:
  addrs: ["redis-0:6379", "redis-1:6379", "redis-2:6379",
          "redis-3:6379", "redis-4:6379", "redis-5:6379"]
```

---

## Performance Targets

| Metric | Target | Mechanism |
|--------|--------|-----------|
| **Ingest throughput** | 100M events/sec | 200 Gateway pods × 500K/sec each; 12-partition Kafka |
| **Ingest P99 latency** | <10ms | Async Kafka publish; in-memory dedup; no sync DB on hot path |
| **Query P95 latency** | <200ms | 3-tier cache; ClickHouse Materialized Views; partition pruning |
| **DAU query** | <10ms | `uniqMerge(dau_state)` from pre-computed `AggregatingMergeTree` |
| **Funnel analysis** | <200ms | `windowFunnel()` with `(app_id, event_name, event_time)` ORDER BY |
| **Retention query** | <3s for 90-day window | Pre-aggregated daily cohort views |
| **CH write lag** | <1s | Buffered writer: 1s interval or 500K rows, whichever comes first |
| **Dedup accuracy** | >99.9% | Bloom (0.1% FPR) + exact Redis SET with 24h TTL |
| **Uptime** | 99.99% | Multi-AZ, HPA, KEDA, Argo Rollouts canary + auto-rollback |

### Capacity Math

At 100M events/sec sustained:
- **Kafka:** 12 partitions × ~8.3M msg/sec per partition; 3-broker cluster with replication factor 3
- **Gateway:** 200 pods × 500K events/sec each; each pod handles ~1,000 req/sec at batch-500
- **ClickHouse:** 100M events/sec × ~200 bytes avg = ~20 GB/sec ingest; requires 10+ node cluster
- **Redis (rate limiting):** ~200K API-key lookups/sec served from in-process bloom + Redis pipeline

---

## License

MIT License — see [LICENSE](LICENSE) for details.

PulseAnalytics is free to self-host. Commercial support and managed hosting options are available.
