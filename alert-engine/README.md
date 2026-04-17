# Alert Engine

Periodically evaluates alert rules stored in PostgreSQL against live ClickHouse metrics and publishes fired alerts to Kafka (`alert-fired` topic) for the notification service to deliver.

## How It Works

1. Cron job runs every 60 seconds.
2. Loads all active `AlertRule` records from PostgreSQL.
3. For each rule, queries ClickHouse with a window aggregation (event_rate, error_rate, p99_latency, active_users).
4. Compares the result against the rule's threshold using the configured operator (`>`, `>=`, `<`, `<=`, `==`).
5. If threshold is breached, publishes an `AlertFiredEvent` JSON message to Kafka.

## Supported Metrics

| Metric | Description |
|--------|-------------|
| `event_rate` | Events per minute in the window |
| `error_rate` | Fraction of error events |
| `p99_latency` | P99 `latency_ms` from `api_call` events |
| `active_users` | Unique device IDs in the window |

## Development

```bash
make run
make test
make docker-run
```

## Configuration

See `configs/config.yaml`.
