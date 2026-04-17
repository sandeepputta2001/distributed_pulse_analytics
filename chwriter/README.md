# ClickHouse Writer (chwriter)

Kafka consumer that reads from `enriched-events` and `session-events`, batches records, and bulk-inserts into ClickHouse using the native protocol.

## Topics consumed

| Topic | Table |
|-------|-------|
| `enriched-events` | `events` |
| `session-events` | `events` (session_start / session_end rows) |

## Batching

Events are accumulated in memory and flushed every 5 seconds or when the batch reaches 10,000 rows, whichever comes first.

## Development

```bash
make run
make test
make docker-run
```

## Configuration

See `configs/config.yaml`.
