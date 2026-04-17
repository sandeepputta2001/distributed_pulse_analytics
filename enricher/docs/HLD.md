# Enricher — High-Level Design

## Purpose

Consume raw ingest events from the `raw-events` Kafka topic, augment them with geographic and device metadata, and re-publish to `enriched-events` for downstream services (session engine, funnel processor, ClickHouse writer).

## Architecture

```
Gateway
  │
  ▼ Kafka: raw-events
┌─────────────────────┐
│     Enricher        │
│                     │
│  KafkaHandler       │
│    └► EnricherSvc   │
│        ├─► GeoIP    │  (MaxMind GeoLite2-City mmdb)
│        └─► UA parse │  (ua-parser)
│                     │
│  :8082  health      │
│  :9091  /metrics    │
└─────────────────────┘
  │
  ▼ Kafka: enriched-events
```

## Enrichments Applied

| Field | Source |
|-------|--------|
| `geo.country` | MaxMind GeoLite2 IP lookup |
| `geo.city` | MaxMind GeoLite2 IP lookup |
| `device.os` | User-Agent string parsing |
| `device.browser` | User-Agent string parsing |
| `device.type` | User-Agent classification (mobile/tablet/desktop) |
| `server_ts` | Gateway ingest timestamp |

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Stateless enrichment | No database writes; pure transform allows horizontal scaling |
| In-process GeoIP | Avoids network hop; MaxMind mmdb is ~60 MB, loaded at startup |
| Kafka → Kafka | Enables fan-out; both session and chwriter can consume enriched events independently |

## Throughput

At 50k events/sec the enricher is CPU-bound on GeoIP lookups. Scale by adding replicas — Kafka consumer groups distribute partitions automatically.
