# Funnel Processor

Kafka stream processor: consumes enriched events, tracks per-user funnel step progress in Redis, and publishes `FunnelConversionEvent` to `agg-results` when a user completes a funnel or the conversion window expires.

Funnel definitions are loaded from PostgreSQL every 30 seconds (hot-reload).

## Development

```bash
make run
make test
make docker-run
```

## Configuration

See `configs/config.yaml`.
