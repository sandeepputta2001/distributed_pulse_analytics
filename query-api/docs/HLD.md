# Query API — High-Level Design

## Purpose

The Query API is the read-side service of PulseAnalytics. It exposes REST endpoints for:
1. **Analytics queries** — time-series event counts, DAU/WAU/MAU, funnel conversion rates, retention cohorts, session metrics (backed by ClickHouse).
2. **Metadata CRUD** — managing apps, funnel definitions, alert rules, cohorts, and A/B experiments (backed by PostgreSQL).

## Architecture

```
Client (Dashboard / SDK)
        │
        ▼ JWT
┌───────────────────┐
│    Query API      │
│  :8085 (HTTP)     │
│                   │
│  AnalyticsHandler │──────────► ClickHouse (events table)
│  MgmtHandler      │──────────► PostgreSQL (metadata)
│                   │
│  AnalyticsService │──┐
│    └ querying.Svc │  ├──► Redis (L1 cache / SWR)
└───────────────────┘  └──► ClickHouse
        │
        ▼ :9094
   Prometheus
```

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| Separate AnalyticsHandler and MgmtHandler | Clean separation of read-heavy analytics vs. low-frequency CRUD |
| Shared `querying.Service` | Encapsulates ClickHouse query logic, single-flight, SWR cache — reusable across services |
| JWT authentication | Stateless; each JWT contains `app_id` and `org_id` claims |
| Chi router | Lightweight, idiomatic, regex-free URL params |
| Distroless runtime image | Minimal attack surface (~15 MB) |

## Data Flows

### Analytics Query Flow
```
GET /v1/events/count?app_id=X&granularity=day
  → AnalyticsHandler.EventCount
  → AnalyticsService.EventCount
  → querying.Service (check Redis L1 cache)
  → ClickHouse query if cache miss
  → write result to cache
  → JSON response
```

### Metadata CRUD Flow
```
POST /v1/alerts
  → MgmtHandler.CreateAlert
  → PostgresRepo.CreateAlertRule
  → postgres.Client (pgx/v5)
  → JSON 201 response
```

## Scalability

- Stateless: horizontally scalable behind a load balancer.
- Redis caching with SWR (stale-while-revalidate) absorbs repeated identical queries.
- ClickHouse is columnar and optimised for aggregations over billions of events.
- Read replicas can be introduced by adding a second ClickHouse DSN without service changes.
