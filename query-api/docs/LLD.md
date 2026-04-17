# Query API — Low-Level Design

## Package Layout

```
query-api/
├── cmd/main.go                          # Wire-up, router, graceful shutdown
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   ├── analytics_handler.go         # AnalyticsHandler (EventCount, DAU, Retention, Funnel, Session)
│   │   └── mgmt_handler.go             # MgmtHandler (apps, funnels, alerts, cohorts, experiments)
│   ├── service/
│   │   └── analytics_service.go        # Thin wrapper around shared querying.Service
│   └── repo/
│       └── postgres_repo.go            # Postgres CRUD wrapper
└── docs/
    └── openapi.yaml
```

## Key Types

### AnalyticsHandler
```go
type AnalyticsHandler struct {
    svc *service.AnalyticsService
    m   *metrics.Registry
    log *zap.Logger
}
```
Handles: `EventCount`, `FunnelQuery`, `DAU`, `Retention`, `SessionMetrics`

### MgmtHandler
```go
type MgmtHandler struct {
    repo *repo.PostgresRepo
    m    *metrics.Registry
    log  *zap.Logger
}
```
Handles CRUD for: `Funnel`, `App`, `Alert`, `Cohort`, `Experiment`

### AnalyticsService
Wraps `querying.Service` from shared module. Exposes typed methods:
- `EventCount(ctx, EventCountRequest) → EventCountResponse`
- `Funnel(ctx, FunnelRequest) → FunnelResponse`
- `DAU(ctx, DAURequest) → EventCountResponse`
- `Retention(ctx, RetentionRequest) → RetentionResponse`
- `RawQueryRow(ctx, sql, args...) → Scanner` — for ad-hoc queries (e.g. session metrics)

### PostgresRepo
Thin wrappers over `postgres.Client` methods. No business logic.

## Middleware Stack (Chi)

```
RequestID → RealIP → Recoverer → CORS → JWTMiddleware (for /v1/*)
```

## Error Handling

All handlers use `writeError(w, status, msg)` which returns:
```json
{"error": "message"}
```
HTTP status codes follow REST conventions: 400 (bad input), 401 (auth), 404 (not found), 500 (internal).

## ID Generation

Management entities use `fmt.Sprintf("prefix-%d", time.Now().UnixNano())` for simple collision-resistant IDs in development. In production, replace with ULIDs or UUIDs.

## Caching Strategy (via querying.Service)

| Layer | TTL | Strategy |
|-------|-----|----------|
| Redis L1 | 60 s | SWR — serve stale, refresh async |
| Single-flight | — | Coalesce concurrent identical queries |
| ClickHouse | — | Source of truth |

## ClickHouse Query Patterns

- `EventCount`: `SELECT toStartOfInterval(event_time, …) AS bucket, count() … GROUP BY bucket`
- `DAU`: `SELECT bucket, uniq(device_id) AS dau GROUP BY bucket`
- `Retention`: `SELECT cohort_date, day_n, uniq(user_id) AS retained`
- `SessionMetrics`: Ad-hoc aggregation on `session_end` events (via `RawQueryRow`)

## Route Table

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | /v1/events/count | AnalyticsHandler.EventCount |
| POST | /v1/funnels/query | AnalyticsHandler.FunnelQuery |
| GET | /v1/dau | AnalyticsHandler.DAU |
| POST | /v1/retention | AnalyticsHandler.Retention |
| GET | /v1/sessions/metrics | AnalyticsHandler.SessionMetrics |
| GET/POST | /v1/funnels | MgmtHandler.ListFunnels / CreateFunnel |
| GET/PUT/DELETE | /v1/apps/{id} | MgmtHandler.GetApp / UpdateApp / DeleteApp |
| GET/POST | /v1/alerts | MgmtHandler.ListAlerts / CreateAlert |
| PUT/DELETE | /v1/alerts/{id} | MgmtHandler.UpdateAlert / DeleteAlert |
| GET/POST | /v1/cohorts | MgmtHandler.ListCohorts / CreateCohort |
| DELETE | /v1/cohorts/{id} | MgmtHandler.DeleteCohort |
| GET/POST | /v1/experiments | MgmtHandler.ListExperiments / CreateExperiment |
| PUT/DELETE | /v1/experiments/{id} | MgmtHandler.UpdateExperiment / DeleteExperiment |
