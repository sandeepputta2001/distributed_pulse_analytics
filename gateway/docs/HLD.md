# Gateway Service — High-Level Design

## Overview
The **Ingest Gateway** is the entry point for all SDK-generated analytics events. It
authenticates requests, applies rate limiting, deduplicates events, and publishes
them to Kafka with fire-and-forget semantics — targeting **<10ms P99 latency** at
**100M events/second** cluster-wide.

## Responsibilities
| Concern | Mechanism |
|---------|-----------|
| Authentication | API key → Redis L1 cache → Postgres fallback |
| Rate limiting | Redis Lua token-bucket (per-app RPS + burst) |
| Deduplication | Bloom filter (in-process, 0.1% FPR) + Redis SET (exact, 24h TTL) |
| Event publishing | Kafka `raw-events` topic (async, snappy compression) |
| Profile storage | MongoDB async write (non-blocking) |
| GeoIP | MaxMind GeoLite2 in-process (~1µs, no external HTTP) |

## Architecture Diagram
```
SDK Client
    │  POST /v1/events  (X-API-Key)
    ▼
┌─────────────────────────────────────────────┐
│              Ingest Gateway (:8080)          │
│                                             │
│  1. Auth middleware  → Redis cache          │
│  2. Rate limit       → Redis Lua script     │
│  3. Bloom filter     → in-process           │
│  4. Redis exact dedup (24h SET)             │
│  5. kafka.PublishAsync("raw-events")        │
│  6. MongoDB async archive (goroutine)       │
│     → return 202 Accepted                  │
└─────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
    Kafka                  MongoDB
  raw-events             (archive)
         │
         ▼
    Enricher Service
```

## Key Design Decisions
- **Fire-and-forget**: Gateway returns 202 before Kafka ack — latency is not gated on broker confirmation.
- **In-process Bloom filter**: First dedup stage has zero network cost; second stage (Redis SET) is exact but shared across pods.
- **Stateless pods**: All state (rate limits, dedup) lives in Redis/Kafka → horizontal scale with HPA.
- **Separate metrics port (9091)**: Prometheus scrape does not compete with ingest traffic on 8080.

## SLOs
| Metric | Target |
|--------|--------|
| P99 ingest latency | < 10ms |
| Error rate | < 0.1% |
| Throughput per pod | 500K events/sec |
| Uptime | 99.99% |
