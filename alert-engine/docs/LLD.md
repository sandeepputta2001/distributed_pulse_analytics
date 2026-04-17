# Alert Engine — Low-Level Design

## Package Layout

```
alert-engine/
├── cmd/main.go                      # Wire-up: repos, services, scheduler, HTTP server
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   └── scheduler.go            # Scheduler (cron wrapper) + health endpoint
│   ├── service/
│   │   └── alert_service.go        # EvaluateAll, evaluate, compare
│   └── repo/
│       └── repo.go                 # ListActiveAlerts, QueryMetricValue
└── docs/
```

## Key Types

### Scheduler (handler layer)
```go
type Scheduler struct {
    svc  *service.AlertService
    cron *cron.Cron
    m    *metrics.Registry
    log  *zap.Logger
}
func (s *Scheduler) Start()           // registers @every 1m job
func (s *Scheduler) Stop()            // graceful shutdown
func (s *Scheduler) Health(w, r)      // HTTP health check
```

### AlertService
```go
type AlertService struct {
    repo      *repo.Repo
    publisher *kafka.Producer
    topic     string
    log       *zap.Logger
}
func (s *AlertService) EvaluateAll(ctx) error   // main evaluation loop
func (s *AlertService) evaluate(ctx, rule) error // single rule evaluation
func compare(value float64, op string, threshold float64) bool
```

### Repo
```go
type Repo struct { pg *postgres.Client; ch *clickhouse.Client; log *zap.Logger }
func (r *Repo) ListActiveAlerts(ctx) ([]*models.AlertRule, error)
func (r *Repo) QueryMetricValue(ctx, appID, metric string, windowMinutes int) (float64, error)
```

## ClickHouse Queries Per Metric

| Metric | SQL Pattern |
|--------|------------|
| `event_rate` | `count() / windowMin FROM events WHERE app_id=? AND time>=now()-toIntervalMinute(?)` |
| `error_rate` | `countIf(event='error') / count()` in window |
| `p99_latency` | `quantile(0.99)(props['latency_ms'])` from `api_call` events |
| `active_users` | `uniq(device_id)` in window |

## Metrics Exported

| Name | Type | Description |
|------|------|-------------|
| `alert_engine_evals_total` | Counter | Total evaluation cycles completed |
| `alert_engine_eval_errors_total` | Counter | Evaluation cycles with errors |

## AlertFiredEvent Kafka Message

```json
{
  "rule_id": "a-1234",
  "app_id": "app-xyz",
  "metric": "error_rate",
  "value": 0.12,
  "threshold": 0.05,
  "operator": ">",
  "fired_at": "2026-01-01T00:00:00Z"
}
```

Topic: `alert-fired` | Key: `app_id`
