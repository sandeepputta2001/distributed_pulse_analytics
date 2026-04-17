# Alert Engine — High-Level Design

## Purpose

Evaluate user-defined metric thresholds against live ClickHouse data on a scheduled cadence and emit `AlertFiredEvent` messages to Kafka so the notification service can deliver them.

## Architecture

```
                   ┌────────────────────────┐
                   │    Alert Engine        │
                   │                        │
  cron @every 1m ──► Scheduler.EvaluateAll  │
                   │    │                   │
                   │    ▼                   │
                   │  AlertService          │
                   │    │                   │
                   │    ├──► PostgreSQL     │  (read alert rules)
                   │    ├──► ClickHouse     │  (aggregation query)
                   │    └──► Kafka producer │──► alert-fired topic
                   │                        │
                   │  :8086  health/ready   │
                   │  :9095  /metrics       │
                   └────────────────────────┘
```

## Alert Rule Model

```
AlertRule {
  id, app_id, metric, operator, threshold, window_minutes, active
}
```

Stored in PostgreSQL. Created/managed via the Query API (`/v1/alerts`).

## Evaluation Loop

```
Every 60s:
  rules ← SELECT * FROM alert_rules WHERE active = true
  for each rule:
    val ← ClickHouse(metric, app_id, window_minutes)
    if compare(val, operator, threshold):
      publish AlertFiredEvent → Kafka
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Cron-based evaluation | Simple, predictable; stateless — no distributed coordination |
| ClickHouse for metrics | Same source of truth as analytics; no additional storage |
| Kafka for fired alerts | Decouples detection from notification delivery; notification service can scale independently |
| Per-minute granularity | Fine-grained enough for most production alert use cases |

## Scalability Notes

- Stateless: multiple replicas are safe since each evaluation is idempotent (no write-back).
- For high rule counts (>10k), add per-app batching and parallelise ClickHouse queries.
- Alert deduplication (avoid re-firing the same alert every minute) can be added via Redis `SET NX EX`.
