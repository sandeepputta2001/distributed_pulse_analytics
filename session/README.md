# Session Engine

Kafka stream processor: consumes enriched events from `enriched-events`, tracks per-device session state in Redis, detects session boundaries (30-minute inactivity timeout), and emits synthetic `session_start` / `session_end` events to `session-events`.

## Session Rules

- A new session starts on the first event from a device.
- A session ends when no event is received for 30 minutes.
- Session duration is stored in the `session_duration_s` event property.

## Development

```bash
make run
make test
make docker-run
```

## Configuration

See `configs/config.yaml`.
