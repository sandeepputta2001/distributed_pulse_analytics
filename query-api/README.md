# Query API

Serves all analytics query and CRUD management endpoints. Reads from ClickHouse (analytics) and PostgreSQL (metadata: apps, funnels, alerts, cohorts, experiments).

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | /health | Health check |
| GET | /metrics | Prometheus metrics |
| GET | /swagger/ | OpenAPI UI |
| GET | /v1/events/count | Event count time-series |
| POST | /v1/funnels/query | Funnel conversion rates |
| GET | /v1/dau | Daily/weekly/monthly active users |
| POST | /v1/retention | Day-N retention cohorts |
| GET | /v1/sessions/metrics | Session KPIs |
| CRUD | /v1/apps | App management |
| CRUD | /v1/funnels | Funnel definitions |
| CRUD | /v1/alerts | Alert rules |
| CRUD | /v1/cohorts | Cohort definitions |
| CRUD | /v1/experiments | A/B experiments |

All `/v1/*` endpoints require a valid JWT (`Authorization: Bearer <token>`).

## Development

```bash
# Run locally
make run

# Build binary
make build

# Run tests
make test

# Docker
make docker-run
```

## Configuration

See `configs/config.yaml`. All values can be overridden with environment variables.

## Architecture

```
cmd/main.go
internal/
  handler/
    analytics_handler.go   # EventCount, DAU, Retention, Funnel, Session
    mgmt_handler.go        # CRUD for apps/funnels/alerts/cohorts/experiments
  service/
    analytics_service.go   # Delegates to shared querying.Service
  repo/
    postgres_repo.go       # Postgres CRUD wrappers
```
