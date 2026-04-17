# Gateway Service

High-throughput event ingest microservice for PulseAnalytics.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/events` | Ingest event batch (up to 500 events) |
| `POST` | `/v1/identify` | Upsert user profile |
| `POST` | `/v1/track` | Track a single event |
| `GET` | `/health` | Liveness probe |
| `GET` | `/ready` | Readiness probe (checks Kafka, Redis, Postgres) |
| `GET` | `/metrics` | Prometheus scrape endpoint |
| `GET` | `/swagger/*` | Swagger UI |

## Quick Start

```bash
# Run locally
make run

# Run with Docker Compose (includes Kafka, Redis, Postgres, Mongo)
make up

# Build binary
make build

# Run tests
make test

# Build Docker image
make docker-build
```

## Configuration

Set via `configs/config.yaml` or environment variables prefixed with `PULSE_`:

```bash
PULSE_SERVICE_ENVIRONMENT=production
PULSE_KAFKA_BROKERS=kafka:9092
PULSE_REDIS_ADDRS=redis:6379
PULSE_POSTGRES_DSN=postgres://pulse:pulse@postgres:5432/pulse
PULSE_MONGO_URI=mongodb://mongo:27017
PULSE_AUTH_JWTSECRET=your-32-char-secret
```

## Architecture

See [docs/HLD.md](docs/HLD.md) and [docs/LLD.md](docs/LLD.md).

## API Documentation

OpenAPI spec: [docs/openapi.yaml](docs/openapi.yaml)

Swagger UI available at `http://localhost:8080/swagger/index.html` when running.

## Performance

- **P99 latency**: < 10ms (fire-and-forget Kafka publish)
- **Throughput**: 500K events/sec per pod
- **Scale**: HPA 3–200 pods on CPU > 60%
- **Dedup**: Bloom filter (0.1% FPR) + Redis SET (exact, 24h)
- **Rate limit**: Redis Lua token-bucket per app (configurable RPS + burst)
