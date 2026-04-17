# Session Engine — High-Level Design

## Purpose

Convert a stream of raw user events into meaningful session boundaries. Track per-device state in Redis and synthesize `session_start` / `session_end` events that downstream services can use for session metrics.

## Architecture

```
Kafka: enriched-events
      │
      ▼
┌─────────────────────┐
│   Session Engine    │
│                     │
│  KafkaHandler       │
│    └► SessionSvc    │
│        │            │
│        ├─► Redis    │  (session state per device, TTL=35min)
│        └─► Kafka    │──► session-events
│                     │
│  :8083  health      │
│  :9092  /metrics    │
└─────────────────────┘
```

## Session State Machine

```
[No session] ─── first event ──► [Active] ─── inactivity 30min ──► [Ended]
                                    │                                    │
                                    └────── event within window ─────────┘
                                                (reset TTL)
```

## Redis Key Schema

```
session:{app_id}:{device_id}
  → JSON: { session_id, start_ts, last_ts, event_count }
  → TTL: 35 minutes
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Redis for state | Sub-millisecond reads; TTL auto-expires stale sessions |
| 35-min TTL > 30-min timeout | Safety margin prevents premature expiry under lag |
| Synthetic events in Kafka | Downstream (chwriter, query-api) get session lifecycle without coupling to session engine |
