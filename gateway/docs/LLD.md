# Gateway Service — Low-Level Design

## Package Structure
```
gateway/
├── cmd/main.go                  # Wire-up: config → infra → service → handler → router
├── internal/
│   ├── handler/handler.go       # HTTP handlers (HandleIngest, HandleIdentify, HandleTrack)
│   ├── service/service.go       # Ingest pipeline (dedup, Kafka publish)
│   └── repo/repo.go             # Data access (Postgres API key, MongoDB archive)
├── configs/config.yaml
└── docs/
```

## handler.Handler
Responsible for: HTTP parsing, auth context extraction, response writing.
- `HandleIngest`: Reads + decompresses body → calls `service.ProcessBatch` → async MongoDB archive
- `HandleIdentify`: Decodes JSON → `service.PublishIdentify` → async `repo.UpsertUserProfile`
- `HandleTrack`: Decodes JSON → `service.PublishTrack`
- `PrometheusMiddleware`: Wraps each request with `IngestRequests` counter + `IngestLatency` histogram

## service.IngestService
Responsible for: business rules — dedup, Kafka publish.
- `ProcessBatch(ctx, appID, deviceID, clientIP, events)` → `IngestResult{Accepted, Filtered}`
  1. For each event: if `EventID == ""` → generate UUID
  2. `dedup.Filter.TestAndAdd(eventID)` → skip duplicates
  3. Build `IngestPayload{batch, client_ip, server_ts}`
  4. `kafka.Producer.PublishAsync(topic, partKey, payload)`
- `PublishIdentify` / `PublishTrack`: thin wrappers around Kafka publish

## repo.Repo
Responsible for: all I/O to Postgres and MongoDB.
- `GetAppByAPIKey` → delegates to `postgres.Client.GetAppByAPIKey`
- `InsertRawBatch` → `mongo.Client.InsertRawBatch` (nil-safe)
- `UpsertUserProfile` → `mongo.Client.UpsertUserProfile` (nil-safe)

## Request Flow (POST /v1/events)
```
Request
  │
  ├─ middleware.RealIP, RequestID, Recoverer, CORS
  ├─ PrometheusMiddleware         (latency histogram start)
  ├─ auth.APIKeyMiddleware
  │     ├─ Redis L1 GET auth:key:{key}
  │     │     hit  → App{ID, RPS, Burst}
  │     │     miss → postgres.GetAppByAPIKey → Redis SET (5min TTL)
  │     └─ ratelimit.Allow(appID, RPS, Burst)
  │           Redis Lua token-bucket
  │           429 if exhausted
  │
  └─ handler.HandleIngest
        ├─ readBody (gzip-aware, 10MB max)
        ├─ json.Unmarshal → EventBatch
        ├─ validate: 1 ≤ len(events) ≤ 500
        ├─ service.ProcessBatch
        │     ├─ dedup.TestAndAdd (Bloom → Redis SET)
        │     └─ kafka.PublishAsync("raw-events", key, payload)
        ├─ go repo.InsertRawBatch (goroutine, 5s timeout)
        └─ 202 {"accepted": N, "filtered": M}
```

## Deduplication Detail
```
Stage 1 — Bloom Filter (in-process)
  bloom.TestAndSet(sha256(event_id))
  FPR = 0.1%  →  0.1% false negatives (acceptable)
  Cost: ~1 bit per event, zero network

Stage 2 — Redis SET (exact, distributed)
  SADD pulse:dedup:{app_id}:{event_id}  TTL=24h
  Cost: ~1ms Redis round-trip
  Result: exact dedup across all pods

Stage 3 — ClickHouse MergeTree (final)
  Dedup by event_id at merge time
  Guards against any race between pods
```

## Configuration Hot-spots
| Key | Default | Notes |
|-----|---------|-------|
| `http.maxbodybytes` | 10MB | Prevents OOM on large batches |
| `bloom.capacity` | 1B | ~1.25 GB RAM per pod; tune per traffic |
| `ratelimit.defaultrps` | 10,000 | Override per-app in `apps` table |
| `kafka.acks` | leader | Latency-optimised; use `all` for durability |

## Error Handling
| Scenario | Response | Side Effect |
|----------|----------|-------------|
| Missing X-API-Key | 401 | — |
| Invalid API key | 401 | Warning log |
| Rate limit exceeded | 429 | `Retry-After: 1` header |
| Kafka unavailable | 503 | Circuit breaker opens after 5 failures |
| MongoDB unavailable | — (non-fatal) | Warning log; ingest still succeeds |
