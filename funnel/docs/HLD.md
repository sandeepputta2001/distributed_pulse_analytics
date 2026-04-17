# Funnel Processor — High-Level Design

## Purpose

Track user progression through multi-step funnels in real time. Emit conversion events when users complete all steps within the configured window, enabling funnel analytics without expensive ClickHouse re-aggregation.

## Architecture

```
Kafka: enriched-events
      │
      ▼
┌─────────────────────┐
│  Funnel Processor   │
│                     │
│  KafkaHandler       │
│    └► FunnelSvc     │
│        ├─► Redis    │  (per-user step state)
│        ├─► Postgres │  (funnel definitions, hot-reload 30s)
│        └─► Kafka    │──► agg-results
│                     │
│  :8084  health      │
│  :9093  /metrics    │
└─────────────────────┘
```

## Funnel Processing Logic

```
for each event:
  for each active funnel where event.app_id == funnel.app_id:
    if event.name == funnel.steps[state.next_step]:
      advance state.next_step
      if state.next_step == len(funnel.steps):
        emit FunnelConversionEvent (converted=true)
        delete state
      elif now - state.start > funnel.window_seconds:
        emit FunnelConversionEvent (converted=false, dropped_at=step)
        delete state
      else:
        save state
```

## Redis Key Schema

```
funnel:{funnel_id}:{user_id}
  → JSON: { step, start_ts }
  → TTL: window_seconds + 5min
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Redis per-user state | Low-latency step tracking without ClickHouse queries |
| Hot-reload (30s) | Funnel definitions change rarely; stale by at most 30s is acceptable |
| agg-results topic | Downstream consumers (query-api, dashboards) receive pre-computed conversions |
