# ClickHouse Writer — High-Level Design

## Purpose

Fan-out consumer that persists enriched and session events to ClickHouse with high throughput using batched native-protocol inserts.

## Architecture

```
Kafka: enriched-events ──┐
Kafka: session-events  ──┤
                         ▼
                 ┌───────────────┐
                 │   chwriter    │
                 │               │
                 │ KafkaHandler  │
                 │   └► BatchSvc │
                 │       └► CH   │──► events table (MergeTree)
                 │               │
                 │  :8088 health │
                 │  :9097 metrics│
                 └───────────────┘
```

## Batching Strategy

```
┌──────────────────────────────────┐
│  incoming events                 │
│  → in-memory buffer              │
│                                  │
│  flush when:                     │
│    buffer >= 10,000 rows  OR     │
│    ticker fires (every 5s)       │
└──────────────────────────────────┘
```

## ClickHouse Table

```sql
CREATE TABLE events (
    app_id        LowCardinality(String),
    event_name    LowCardinality(String),
    device_id     String,
    user_id       String,
    session_id    String,
    event_time    DateTime64(3),
    props         Map(String, String),
    country       LowCardinality(String),
    city          String,
    os            LowCardinality(String),
    browser       LowCardinality(String),
    device_type   LowCardinality(String),
    server_ts     Int64
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(event_time)
ORDER BY (app_id, event_time, device_id);
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Batched inserts | ClickHouse performs best with large batches; 10k rows ≈ 500ms per insert at 10MB/s |
| Native protocol | 5-10x faster than HTTP interface for bulk inserts |
| Single table | Simplifies queries; `event_name` filters replace multiple tables |
| MergeTree + partition by month | Efficient range scans; easy TTL management |
