# PulseAnalytics — High Level Design (HLD)

> Analytics platform at Amplitude/MoEngage scale: user behaviour event tracking, funnels, retention, sessions, alerts.

---

## Table of Contents

1. [Assumptions](#1-assumptions)
2. [Back-of-the-Envelope Estimation](#2-back-of-the-envelope-estimation)
3. [System Goals & Non-Goals](#3-system-goals--non-goals)
4. [High-Level Architecture](#4-high-level-architecture)
5. [Component Overview](#5-component-overview)
6. [Data Flow](#6-data-flow)
7. [Scaling Strategy](#7-scaling-strategy)
8. [Single Points of Failure & Mitigation](#8-single-points-of-failure--mitigation)
9. [Explicit Tradeoffs](#9-explicit-tradeoffs)
10. [Consistency, Availability & Latency Model](#10-consistency-availability--latency-model)
11. [Bottlenecks & Mitigations](#11-bottlenecks--mitigations)
12. [Cost vs Performance](#12-cost-vs-performance)

---

## 1. Assumptions

### Users & Tenants

| Dimension | Value | Rationale |
|-----------|-------|-----------|
| Paying tenants (apps) | 5,000 | Mid-size SaaS with enterprise customers |
| Free-tier apps | 50,000 | On-ramp funnel |
| End users tracked across all apps | 500M unique users | Sum of all tenants' user bases |
| Daily Active Users (DAU) generating events | 100M | 20% of total user pool active daily |
| Concurrent SDK clients sending events | ~500K | Mix of mobile, web, server-side SDKs |
| Dashboard analysts (query users) | 20,000 | ~4 per paying tenant |
| Dashboard concurrent sessions | 2,000 | 10% of analysts online simultaneously |

### Traffic Assumptions

| Dimension | Value |
|-----------|-------|
| Average events per user per day | 200 |
| Peak-to-average multiplier | 3× (product launches, campaigns) |
| Read : Write ratio | 1 : 20 (write-heavy analytics; reads are dashboards + alerts) |
| Query types | 70% cached/pre-aggregated, 30% ad-hoc ClickHouse |
| Batch ingest vs single-event | 80% batch (SDK flush), 20% single-event |
| Event payload average size | 500 bytes (JSON, compressed ~200 bytes) |
| Ingest compression (Snappy on Kafka) | ~2.5× ratio |

### Deployment Assumption

Multi-region active-active on AWS (us-east-1 primary, eu-west-1, ap-southeast-1). ClickHouse cluster per region with cross-region replication for global queries.

---

## 2. Back-of-the-Envelope Estimation

### Writes (Ingest) QPS

```
Daily events = 100M users × 200 events/user = 20 Billion events/day
Average write QPS = 20B / 86,400s ≈ 231,000 events/s
Peak write QPS    = 231,000 × 3 ≈ 700,000 events/s

Gateway receives SDK batches (avg 50 events each):
  Average batch RPS = 231,000 / 50 ≈ 4,600 HTTP req/s per region
  Peak batch RPS    ≈ 14,000 HTTP req/s per region
```

### Reads (Query) QPS

```
Analyst dashboard auto-refresh = every 60s per open tab
Concurrent analysts = 2,000
Read QPS from dashboards = 2,000 / 60 ≈ 34 query req/s

Alert engine polls = 500 alert rules × 1 poll/60s ≈ 9 query req/s
Total read QPS ≈ 43 req/s (order of magnitude lower than writes)
Cache hit rate target = 70% → ~13 ClickHouse queries/s
```

### Storage Estimation

```
Raw event size (MongoDB archive):
  500 bytes × 20B events/day = 10 TB/day raw
  Per year = 3.65 PB (cold-tiered to S3 after 30 days)

ClickHouse (columnar, compressed):
  Compression ratio ≈ 10× for analytics workloads
  10 TB/day → 1 TB/day after ClickHouse compression
  90-day hot tier: 1 TB × 90 = 90 TB per region
  365-day cold tier (S3): ~365 TB per region

Kafka (retention = 48 hours):
  700,000 events/s × 200 bytes (snappy) × 86,400s × 2 days = ~24 TB Kafka log
  Across 12 partitions → ~2 TB per partition broker

Redis:
  Session state: 100M DAU × 500 bytes (session struct) = 50 GB worst-case
  Rate limit counters: 5K apps × 200 bytes = 1 MB (negligible)
  Query result cache: 10K unique queries × 50 KB avg result = 500 MB

PostgreSQL (metadata):
  5,000 apps × 10 KB metadata = 50 MB (trivially small)
  Funnel/cohort/alert definitions: <10 GB total
```

### Bandwidth Estimation

```
Ingest bandwidth (peak):
  700,000 events/s × 200 bytes (gzip compressed) = 140 MB/s inbound
  ≈ 1.12 Gbps inbound

Kafka internal replication (3× replication):
  140 MB/s × 3 = 420 MB/s intra-cluster = ~3.4 Gbps

ClickHouse write throughput:
  After enrichment + session overhead: ~300 MB/s to CH cluster

Query response bandwidth:
  43 queries/s × 100 KB avg response = 4.3 MB/s outbound (negligible)

Total cluster network requirement: ~10 Gbps backbone
```

### Summary Table

| Metric | Average | Peak |
|--------|---------|------|
| Write events/sec | 231,000 | 700,000 |
| HTTP req/sec (ingest) | 4,600 | 14,000 |
| Read queries/sec | 43 | ~200 (dashboard spike) |
| ClickHouse queries/sec | 13 | 60 |
| Inbound bandwidth | 45 MB/s | 140 MB/s |
| ClickHouse hot storage/region | — | 90 TB |
| Kafka log size | — | 24 TB |
| Redis memory (session + cache) | — | ~55 GB |

---

## 3. System Goals & Non-Goals

### Goals

- Ingest 100M events/second cluster-wide with <50ms p99 gateway latency.
- Query analytics on 10B+ rows with <200ms P95 latency.
- 99.99% availability for ingest (four-nines = 52 min/year downtime budget).
- 99.9% availability for query API (three-nines = 8.7 hr/year).
- Multi-tenant isolation: one noisy tenant cannot starve others.
- Event deduplication to avoid double-counting.
- Sub-minute alert firing latency.

### Non-Goals

- Real-time streaming SQL (Flink/Spark Streaming); pre-aggregation covers most needs.
- Cross-tenant data sharing.
- GDPR erasure pipeline (deferred; would need event-level delete in ClickHouse via `ALTER TABLE ... DELETE`).
- Full ML/prediction layer.

---

## 4. High-Level Architecture

```
                        ┌──────────────────────────────────────────────────────────────┐
                        │                     CLIENT LAYER                              │
                        │  Mobile SDK   Web SDK    Server SDK    Dashboard (React SPA)  │
                        └─────────┬──────────┬─────────┬──────────────┬───────────────┘
                                  │          │         │              │ (query)
                    ┌─────────────▼──────────▼─────────▼──────────┐  │
                    │          INGRESS / CDN / WAF                  │  │
                    │   AWS ALB + CloudFront + Shield Standard      │  │
                    └────────────────────┬──────────────────────────┘  │
                                         │                              │
              ┌──────────────────────────▼──────────────────────────┐  │
              │                   INGEST TIER                         │  │
              │   ┌─────────────────────────────────────────────┐    │  │
              │   │           Gateway Service (×N pods)          │    │  │
              │   │  • API Key auth  • Rate limiting (Redis)      │    │  │
              │   │  • Bloom dedup   • GeoIP enrichment           │    │  │
              │   │  • Validation    • MongoDB archive write       │    │  │
              │   └────────────────────────────────────────────── ┘    │  │
              └──────────────────────────┬──────────────────────────┘  │
                                         │ raw-events (Kafka)           │
              ┌──────────────────────────▼──────────────────────────┐  │
              │                 PROCESSING TIER                       │  │
              │  ┌────────────────┐  ┌──────────────┐  ┌──────────┐ │  │
              │  │ Enricher ×N    │  │Session Engine│  │  Funnel  │ │  │
              │  │ (UA, GeoIP,    │  │  ×N pods     │  │Processor │ │  │
              │  │  timestamps)   │  │  (Redis)     │  │  ×N pods │ │  │
              │  └───────┬────────┘  └──────┬───────┘  └────┬─────┘ │  │
              └──────────┼─────────────────┼─────────────── ┼───────┘  │
                         │ enriched-events  │ session-events │ agg-results
              ┌──────────▼─────────────────▼─────────────── ▼───────┐  │
              │                  STORAGE TIER                         │  │
              │  ┌──────────────────────────────┐  ┌──────────────┐  │  │
              │  │       CH-Writer ×N pods       │  │  Alert       │  │  │
              │  │  (1s batches, 500K rows/batch)│  │  Engine      │  │  │
              │  └──────────────┬───────────────┘  └──────┬───────┘  │  │
              │                 │                          │           │  │
              │  ┌──────────────▼───────────────┐  ┌──────▼───────┐  │  │
              │  │   ClickHouse Cluster          │  │ Notification │  │  │
              │  │  (ReplicatedMergeTree, 3 shard│  │   Service    │  │  │
              │  │   × 2 replicas = 6 nodes)     │  └──────────────┘  │  │
              │  │   90-day hot, S3 cold TTL     │                     │  │
              │  └──────────────────────────────-┘                     │  │
              └───────────────────────────────────────────────────────┘  │
                                                                          │
              ┌───────────────────────────────────────────────────────────▼──┐
              │                    QUERY TIER                                  │
              │   ┌──────────────────────────────────────────────────┐        │
              │   │            Query API (×N pods)                    │        │
              │   │  • JWT auth  • 4-tier cache (L1/L2/MV/raw)       │        │
              │   │  • Partition pruning  • ClickHouse query builder  │        │
              │   └──────────────────────────────────────────────────┘        │
              └────────────────────────────────────────────────────────────────┘

              ┌──────────────────────────────────────────────────────────────────┐
              │                   SHARED INFRASTRUCTURE                           │
              │   PostgreSQL (Aurora HA)   Redis Cluster   MongoDB Atlas          │
              │   Kafka (MSK, 3 brokers)   Auth Service    Prometheus + Grafana   │
              │   OpenTelemetry Collector  Jaeger          MaxMind GeoIP DB        │
              └──────────────────────────────────────────────────────────────────┘
```

---

## 5. Component Overview

### 5.1 Gateway Service

**Role:** First line of defence + ingest fanout.

**Responsibilities:**
- Authenticate requests via X-API-Key (lookup PostgreSQL with warm cache).
- Rate-limit per API key using Redis token-bucket (Lua script, atomic).
- Two-stage deduplication: in-process Bloom filter (1B capacity, ~1µs) → Redis SET (for cross-pod dedup, ~1ms).
- Validate event schema and reject malformed payloads immediately (fail fast).
- Enrich with server-side timestamp and GeoIP (MaxMind in-process, ~1µs, no network hop).
- Archive raw payload to MongoDB (async goroutine, non-blocking for ingest path).
- Publish to `raw-events` Kafka topic (async, batched).

**Scaling:** Horizontally scalable — stateless (all state in Redis/Kafka). Auto-scale on CPU and request rate.

**SLO:** p99 < 50ms, error rate < 0.1%.

---

### 5.2 Enricher Service

**Role:** Transform raw events into analytics-ready enriched events.

**Responsibilities:**
- Consume `raw-events` Kafka topic (consumer group, 50 concurrent workers per pod).
- Parse User-Agent string → browser, OS, device type.
- Correct client timestamps (detect clock skew > 5 min, replace with server_time).
- Delegate session ID assignment to Session Engine (via Redis).
- Publish `EnrichedEvent` to `enriched-events` topic.
- On unrecoverable parse error → publish to `dlq-events` topic.

**Scaling:** Scale horizontally; each pod owns a Kafka partition subset. Partition count (12) sets upper bound on consumer parallelism.

---

### 5.3 Session Engine

**Role:** Stateful session tracking using Redis as session store.

**Responsibilities:**
- Receive events (via channel from Enricher or direct Kafka subscription).
- Lookup session key `session:{app_id}:{device_id}` in Redis.
- If no session or gap > 30 min → start new session (new UUID).
- Update SETEX with latest timestamp on every event.
- Emit `SessionEvent` (start/end) to `session-events` topic.

**State:** Redis is the only persistent state. Redis TTL auto-expires stale sessions.

---

### 5.4 Funnel Processor

**Role:** Track multi-step funnel conversions in real-time.

**Responsibilities:**

- Load funnel definitions from PostgreSQL on startup (cached in-memory, 30-second hot-reload).
- Consume `session-events` topic; for each event check if it matches any funnel step for the app.
- Track user's funnel progress in Redis ZSET (score = step index, member = user_id).
- Detect conversions within the time window.
- Publish conversion events to `agg-results` topic.

**Tradeoff:** Redis ZSET per funnel. At 1M concurrent funnel evaluations, Redis memory usage is ~500 MB. Acceptable.

---

### 5.5 CH-Writer

**Role:** High-throughput batch writer to ClickHouse.

**Responsibilities:**
- Consume `enriched-events`, `session-events`, `agg-results` Kafka topics.
- Buffer rows in memory (1-second window OR 500K rows, whichever comes first).
- Bulk insert using `INSERT INTO events FORMAT Native` (binary protocol, fastest path).
- Retry with exponential backoff on transient ClickHouse errors.
- Expose lag metric to Prometheus (consumer group lag alert).

**Scaling:** Multiple pods consume different Kafka partitions. ClickHouse accepts parallel inserts from multiple writers.

---

### 5.6 Query API

**Role:** Serve analytics queries from dashboards and external integrations.

**Responsibilities:**
- JWT authentication.
- Route to pre-aggregated materialized view or raw ClickHouse query.
- 4-tier cache: in-process L1 (60s TTL, stale-while-revalidate) → Redis L2 (5-min TTL) → ClickHouse Materialized Views → ClickHouse raw events table.
- **Single-flight:** concurrent identical queries are coalesced — only one ClickHouse call is issued; all waiting goroutines share the result.
- **Circuit breaker:** after 5 consecutive ClickHouse failures the breaker opens for 30 seconds, fast-failing subsequent calls to give ClickHouse time to recover.
- **Bulkhead:** per-tenant concurrency cap (20) + global cap (200) prevents noisy tenants from starving others.
- Partition pruning: always filter by `(app_id, date_range)` to avoid full-table scans.
- Expose: event counts, funnels, DAU/WAU/MAU, retention, session metrics.

**SLO:** p95 < 200ms (cached), p95 < 2s (ad-hoc on hot ClickHouse tier).

---

### 5.7 Alert Engine

**Role:** Background rule evaluator for threshold-based alerts.

**Responsibilities:**
- Load alert rules from PostgreSQL every 60 seconds.
- For each rule, run a ClickHouse query to evaluate the metric.
- Compare against threshold with condition operator.
- If triggered and not in Redis cooldown window (30 min), publish to `notifications` topic.
- Cooldown prevents alert spam.

---

### 5.8 Notification Service

**Role:** Dispatch notifications to external channels.

**Responsibilities:**
- Consume `notifications` Kafka topic.
- For each notification: send webhook HTTP POST or SMTP email.
- Retry up to 3 times with exponential backoff on failure.
- Record delivery status.

---

### 5.9 Auth Service

**Role:** Token issuance and validation.

**Responsibilities:**
- Exchange API key for JWT (HS256, configurable TTL).
- Validate API key against PostgreSQL `apps` table.
- Stateless JWT verification (Query API verifies locally without calling Auth Service).

---

## 6. Data Flow

### 6.1 Ingest Path (Write)

```
SDK → Gateway → (validate + auth + rate-limit + dedup)
              → MongoDB (async raw archive)
              → Kafka: raw-events
              → Enricher (UA, GeoIP, clock correction)
              → Kafka: enriched-events
              → [Session Engine] → Kafka: session-events
                                        → CH-Writer → ClickHouse (events + session_summaries)
                                        → Funnel Processor → Kafka: agg-results → CH-Writer → ClickHouse (funnel_conversions)
              → [Alert Engine polls ClickHouse] → Kafka: notifications → Notification Service → Webhook/Email
```

### 6.2 Query Path (Read)

```
Dashboard → Query API
          → Check in-process cache (L1) → HIT: return immediately
          → Check Redis cache (L2, 5-min TTL) → HIT: return
          → Build ClickHouse SQL with partition pruning
          → Query materialized view (DAU/hourly counts/revenue) → fast (~5ms)
          OR
          → Query events table with filters → slower (~200ms-2s)
          → Store result in Redis L2
          → Return response
```

### 6.3 Dead Letter Flow

```
Enricher parse failure → dlq-events Kafka topic
                       → Monitoring alert (Prometheus counter)
                       → Manual review / replay tooling
```

---

## 7. Scaling Strategy

### 7.1 Stateless Services (Horizontally Scalable)

All application services (Gateway, Enricher, CH-Writer, Query API, Funnel Processor, Alert Engine, Notification Service, Auth Service) are **stateless**. Shared state lives in Redis, Kafka, ClickHouse, or PostgreSQL.

Scale triggers (Kubernetes HPA):
- Gateway: CPU > 60% or RPS > 2,000 per pod → scale out
- Enricher/CH-Writer: Kafka consumer lag > 100K messages → scale out
- Query API: CPU > 70% or p95 latency > 150ms → scale out

### 7.2 Kafka Partitioning

- `raw-events`: 12 partitions. Key = `app_id + device_id` (ensures ordering per device, good distribution).
- `enriched-events`: 12 partitions. Same key.
- `session-events`: 6 partitions (lower volume). Key = `app_id`.
- `agg-results`: 4 partitions. Key = `app_id`.

Partition count can be increased (with rebalancing) as volume grows. Each partition maps to one consumer at a time within a consumer group → predictable scaling.

### 7.3 ClickHouse Sharding & Replication

```
3 Shards × 2 Replicas = 6 ClickHouse nodes per region

Application-level shard routing (internal/clickhouse.Pool):
  Shard index = FNV-1a(app_id) % numShards

  - Shard 0: apps whose FNV hash % 3 == 0
  - Shard 1: apps whose FNV hash % 3 == 1
  - Shard 2: apps whose FNV hash % 3 == 2

  Each app always lands on the same shard → per-app data locality,
  efficient ORDER BY within ClickHouse's MergeTree sort key.

CH-Writer implementation (internal/clickhouse.ShardedWriter):
  - One write-behind channel (500K-event buffer) per shard.
  - Incoming event batches are split by app_id and routed to the
    appropriate per-shard Writer.
  - Independent flush loops: a slow shard does not delay a fast one.

Read replicas (config.ClickHouseConfig.ReadHosts):
  - Query API reads via Pool.ReadConn() which round-robins across
    dedicated ReadHosts for SELECT queries.
  - Falls back to shards[0] when no ReadHosts configured.

Intra-shard replication: ReplicatedMergeTree via ClickHouse Keeper
Cross-shard queries: Distributed table engine on shard coordinators
```

Partition inside each shard: `(toYYYYMMDD(event_time), app_id)` — date-based pruning for time-range queries.

### 7.4 Redis Cluster

- Redis Cluster mode (6 nodes: 3 primary + 3 replica).
- `RouteByLatency: true` — driver picks the lowest-latency node for each command.
- `UniversalClient` interface: startup tries cluster mode first, falls back to single-node for dev environments.
- Key prefixes route to correct slot:
  - `session:{app_id}:{device_id}` → session namespace
  - `rl:{app_id}` → rate limit namespace
  - `qcache:{sha256}` → query cache namespace
- Redis Cluster provides automatic failover (replica promotion on primary death, ~10-30s).

### 7.5 PostgreSQL — Read/Write Splitting

```
Topology: 1 Primary (writer) + N read replicas

Application-level routing (internal/postgres.Client):

  Write pool  ← all INSERT / UPDATE / DELETE / TRANSACTION
  Read pool[] ← all SELECT queries, round-robin across replicas
                Falls back to primary when no replicas configured.

Read methods  (use c.read()):
  GetAppByAPIKey, GetApp, ListApps, ListFunnels, ListAlertRules,
  ListCohorts, ListExperiments, ListOrgs, GetCampaign,
  GetActiveCampaignsByTrigger, GetCampaignStats

Write methods (use c.write):
  CreateApp, UpdateApp, DeactivateApp, CreateOrgAndApp, RotateAPIKey,
  UpsertFunnel, CreateAlertRule, UpdateAlertRule, DeleteAlertRule,
  CreateCohort, DeleteCohort, CreateExperiment, UpdateExperiment,
  DeleteExperiment, CreateOrg, UpdateOrg, CreateCampaign, UpdateCampaign,
  SetCampaignActive

Transactions always go to primary (c.write.Begin).
Config: postgres.replicadsns (list of replica DSN strings).
```

Aurora storage auto-scales. No sharding needed (metadata <10 GB).

### 7.6 MongoDB — Read Preference Routing

```text
Topology: Replica set (1 Primary + 2 Secondaries)

Application-level routing (internal/mongo.Client):

  c.db     — primary read preference → writes (InsertRawBatch, UpsertUserProfile)
  c.readDB — configurable read preference → reads (GetRawEvents, GetUserProfile)

Default read preference: secondaryPreferred
  → Reads go to secondaries; fall back to primary if no secondary available.
  → Reduces primary load on write-heavy workloads.

Config: mongo.readpreference ("primary" | "primaryPreferred" |
        "secondary" | "secondaryPreferred" | "nearest")
        mongo.replicaset (replica set name for seed-list connections)

TTL index on created_at (90-day retention, auto-delete via MongoDB TTL).
Shard key = {app_id, event_time} for horizontal scale when needed.
```

### 7.7 Geographic Scaling

- Multi-region active-active: us-east-1, eu-west-1, ap-southeast-1.
- Ingest routed via latency-based DNS (Route 53) to nearest region.
- ClickHouse clusters are regional (data sovereignty, lower latency).
- Cross-region replication for global analytics dashboards via ClickHouse remote() queries or dedicated cross-region reader.

---

## 8. Single Points of Failure & Mitigation

| Component | SPOF Risk | Mitigation |
|-----------|-----------|------------|
| **Kafka** | Entire ingest pipeline stalls if Kafka is unavailable | 3-node MSK cluster (multi-AZ), topic replication factor 3, min.insync.replicas=2. Gateway retries with exponential backoff |
| **Redis** | Rate limiter and session state unavailable | Redis Cluster (3 primary + 3 replica). On Redis failure, Gateway falls back to in-process rate limiting (best-effort, less precise) |
| **ClickHouse** | No writes or queries possible | 3 shards × 2 replicas. One replica per shard can absorb writes during failover. Query API switches to replica on primary failure |
| **PostgreSQL (Aurora)** | Auth and metadata unavailable | Aurora Multi-AZ with automatic failover. API key validation cached in Redis for 5 minutes (configurable) to survive short outages |
| **Gateway pods** | No ingest | Kubernetes Deployment with minReplicas=3 across 3 AZs. ALB health checks remove unhealthy pods |
| **MaxMind GeoIP DB** | GeoIP enrichment fails | GeoIP is best-effort enrichment; missing fields are nullable. Bloom filter and Kafka publish continue unaffected |
| **Bloom filter (in-process)** | Lost on pod restart → some duplicate events slip through | Second-stage Redis dedup catches most duplicates. ClickHouse dedup via ReplacingMergeTree or `INSERT DEDUPLICATION` on event_id |
| **Alert Engine** | No alerts fire | Single pod with Kubernetes restartPolicy=Always. Alert delay = pod restart time (~30s). Alert cooldown in Redis prevents double-fire on restart |
| **ZooKeeper / CH Keeper** | ClickHouse replication coordination fails | CH Keeper cluster (3 nodes) with quorum. Writes still accepted by primary; replication catches up on restore |

---

## 9. Explicit Tradeoffs

### 9.1 Eventual Consistency vs Strong Consistency in Ingest

**Decision:** Kafka + async CH-Writer → eventual consistency (events land in ClickHouse 1–5 seconds after ingest).

**Why:** Strong consistency (synchronous ClickHouse write per HTTP request) would require ~5ms CH write latency per event at the gateway, which at 700K events/s means 3,500 concurrent CH connections. Kafka decouples ingest from storage, letting each tier scale independently.

**Implication:** Dashboard data lags by up to 5 seconds. Acceptable for analytics; unacceptable for billing.

---

### 9.2 Bloom Filter Deduplication: False Negatives Possible

**Decision:** Bloom filter is probabilistic. At 1% false-positive rate, ~1% of new unique events are incorrectly flagged as duplicates and dropped.

**Why:** Exact deduplication at 700K events/s would require a distributed set with ~100M entries/day — expensive. Bloom filter handles 99% of SDK retries (idempotent re-sends) with negligible memory (12 MB for 10M cells at 1% FPR).

**Implication:** ~1% of edge-case legitimate events may be dropped. Mitigated by: (1) Redis SET secondary dedup only runs when Bloom says "possible duplicate", (2) event_id is tracked per device, not globally.

---

### 9.3 ClickHouse vs Real-Time Stream Processing (Flink)

**Decision:** Use ClickHouse materialized views for pre-aggregation instead of Flink jobs.

**Why:** Flink adds operational complexity (cluster management, state checkpointing, exactly-once semantics). ClickHouse MVs auto-update on insert and cover DAU, hourly counts, revenue with zero additional infrastructure.

**Implication:** MVs cannot do stateful joins or windowed aggregations beyond what ClickHouse supports. Custom funnels require query-time computation. Accepted limitation.

---

### 9.4 MongoDB Raw Archive: Optional but Costly

**Decision:** Every raw event is archived to MongoDB before Kafka publish.

**Why:** Enables event replay (re-enrich, re-process on schema change), audit trail for debugging, and GDPR erasure capability.

**Implication:** 10 TB/day MongoDB writes. MongoDB is sharded but adds ~5ms per ingest path (async goroutine, non-blocking). Monthly cost: ~$3,000 extra. Considered worth it for data recovery. Hot retention is 90 days (TTL index on `created_at`).

**Alternative considered:** Write to S3 directly (cheaper, but harder to query for selective replay).

---

### 9.5 In-Process GeoIP vs External API

**Decision:** MaxMind GeoLite2 loaded into process memory (~100 MB mmdb file).

**Why:** External GeoIP API would add 10–50ms per request at the gateway, and 700K events/s × 50ms = non-starter. In-process lookup is ~1µs.

**Implication:** GeoIP DB must be refreshed weekly. Stale DB means IP-to-geo accuracy degrades (new IP ranges unrecognized). Acceptable for analytics use case.

---

### 9.6 JWT Stateless Auth vs Session-Based Auth

**Decision:** JWT tokens for Query API (stateless, verified locally).

**Why:** Stateless verification avoids Redis/DB lookup on every query request. At 43 req/s this is trivial, but consistent with scalability principles.

**Implication:** Cannot revoke a JWT before expiry (only option: short TTL + re-issue). Mitigation: configurable TTL (default 24h via `PULSE_AUTH_JWT_EXPIRY`; reduce for tighter revocation), API key invalidation flows through PostgreSQL (next JWT fetch fails).

---

### 9.7 Three-Tier Cache: Complexity vs Latency

**Decision:** L1 (in-process map) → L2 (Redis 5-min TTL) → L3 (ClickHouse query_cache).

**Why:** Analytics dashboards request the same time-range aggregations repeatedly (every 60s auto-refresh). Without caching, every analyst's refresh hits ClickHouse.

**Implication:** Cache staleness up to 5 minutes. Fine for analytics (not financial). Cache invalidation not implemented (TTL expiry is the only mechanism). Dashboard shows "last updated at" timestamp to set expectations.

---

## 10. Consistency, Availability & Latency Model

### CAP Position

PulseAnalytics makes **different CAP choices per tier**:

| Tier | CAP Choice | Reason |
|------|-----------|--------|
| Ingest (Gateway → Kafka) | AP (Availability + Partition tolerance) | Never drop events; prefer stale or duplicate over lost |
| ClickHouse writes (CH-Writer) | AP | Batch writes; prefer eventual durability over blocking on partition |
| Query reads (ClickHouse) | CP (Consistency + Partition tolerance) | Queries must return correct data; stale cache (5 min) is documented |
| Session state (Redis) | AP | Session tracking is best-effort; brief Redis partition means sessions don't start/end, no data loss |
| Metadata (PostgreSQL) | CP | Funnel definitions and alert rules must be consistent; failover may cause 10–30s downtime |

### Latency Targets

| Path | p50 | p95 | p99 |
|------|-----|-----|-----|
| Ingest (gateway response) | 5ms | 30ms | 50ms |
| Event to ClickHouse visible | 1s | 5s | 10s |
| Cached query (Redis hit) | 5ms | 15ms | 30ms |
| Ad-hoc ClickHouse query | 50ms | 200ms | 2s |
| Alert fire latency (from threshold breach) | 60s | 90s | 120s |

### Durability Guarantees

- Kafka: `acks=leader` (default; configurable to `all` per topic) → message survives one broker failure.
- ClickHouse: `ReplicatedMergeTree`, 2 replicas → one node failure = no data loss.
- MongoDB: write concern `w=majority` → raw archive survives one node failure.
- PostgreSQL: Aurora with Multi-AZ → synchronous standby = zero data loss on primary failure.

---

## 11. Bottlenecks & Mitigations

### Bottleneck 1: Gateway → Kafka Publish Latency

**Problem:** At 700K events/s, if Kafka is slow (network congestion, broker overload), Gateway builds up in-memory queue. If queue exceeds limit, events are dropped.

**Mitigation:**
- Kafka brokers on dedicated network (MSK, 10Gbps ENI).
- Gateway producer uses async batch publish with configurable queue depth (100K pending messages).
- Back-pressure signal: if queue > 80% full, gateway returns HTTP 429 to SDK (SDK retries with backoff).
- Alert on Kafka producer queue depth metric.

---

### Bottleneck 2: ClickHouse Insert Throughput

**Problem:** CH-Writer must sustain 700K enriched events/s into ClickHouse. Each INSERT is a merge operation; too many small inserts cause merge storms.

**Mitigation:**
- Buffer 1 second or 500K rows per batch → ~100 inserts/min per CH-Writer pod.
- Multiple CH-Writer pods each writing to a subset of shards.
- ClickHouse `max_insert_threads` tuned to 8 per node.
- Monitor `ReplicatedMergeTree` merge backlog; alert if parts > 300 (indicates write pressure).

---

### Bottleneck 3: Redis Memory for Sessions

**Problem:** 100M DAU × 500-byte session state = 50 GB Redis. Large Redis clusters are expensive and memory is limited.

**Mitigation:**
- Session TTL = 30 minutes → only active sessions are in Redis. At any instant, only sessions active in last 30 min count. Assume 20% of DAU active simultaneously = 20M sessions × 500B = 10 GB. Manageable.
- Redis cluster with 6 nodes × 16 GB = 96 GB capacity.

---

### Bottleneck 4: Kafka Consumer Lag (Enricher)

**Problem:** During traffic spikes (campaign launches), `raw-events` lag can grow if Enricher pods are too few.

**Mitigation:**
- Kubernetes HPA on Kafka consumer lag (KEDA + Prometheus adapter).
- Target: < 100K message lag. Scale Enricher from 3 → 12 pods in ~60s.
- Each pod runs 50 concurrent workers, so 12 pods = 600 parallel event processors.

---

### Bottleneck 5: PostgreSQL for API Key Lookups

**Problem:** Gateway validates API key on every request. At 14,000 req/s, that's 14,000 PostgreSQL reads/s — unsustainable.

**Mitigation:**
- Cache API key → rate limit config in Redis with 5-minute TTL (configurable via `PULSE_AUTH_API_KEY_TTL`).
- On cache miss: single PostgreSQL read, populate Redis.
- Actual PostgreSQL reads ≈ (new/rotated API keys) × 1/300s ≈ negligible.

---

### Bottleneck 6: ClickHouse Query Fan-out

**Problem:** Ad-hoc queries that scan 90 days × 10B events without proper filters will timeout or OOM ClickHouse.

**Mitigation:**
- Query API always enforces `app_id` + `date_range` filter before executing.
- Max date range = 90 days (hard limit in Query API).
- Query timeout = 30 seconds (ClickHouse query_settings).
- Materialized views serve most common queries (DAU, counts, revenue) without touching raw events.

---

### Bottleneck 7: Retry Storms

**Problem:** On transient ClickHouse failure, CH-Writer retries. Multiple pods retrying simultaneously can overwhelm a recovering ClickHouse node.

**Mitigation:**
- Exponential backoff with jitter: base=1s, max=30s, jitter=±20%.
- Circuit breaker: after 5 consecutive failures, CH-Writer stops retrying for 60 seconds.
- Events stay in Kafka (48-hour retention) — no data loss during retry pause.
- Dead letter queue only for truly unprocessable events (schema violations, not transient errors).

---

## 12. Cost vs Performance

### Infrastructure Cost Estimate (per region, monthly)

| Component | Configuration | Est. Monthly Cost |
|-----------|--------------|-------------------|
| Gateway (EKS) | 6 pods × c5.2xlarge | $900 |
| Enricher (EKS) | 6–12 pods × c5.xlarge | $600 |
| CH-Writer (EKS) | 4 pods × c5.xlarge | $400 |
| Query API (EKS) | 4 pods × c5.xlarge | $400 |
| Other services | ~10 pods × c5.large | $500 |
| Kafka (MSK) | 3 × kafka.m5.2xlarge | $1,800 |
| ClickHouse (EC2) | 6 × r5.4xlarge (128 GB RAM) | $9,000 |
| Redis (ElastiCache) | 6 × cache.r6g.2xlarge | $2,500 |
| PostgreSQL (Aurora) | 1 writer + 2 readers (db.r5.2xlarge) | $1,800 |
| MongoDB (Atlas) | M50 sharded, 3 shards | $3,000 |
| S3 (cold storage) | 365 TB × $0.023/GB | $8,400 |
| Data transfer | ~5 TB/month egress | $450 |
| **Total per region** | | **~$29,750/month** |
| **3 regions** | | **~$89,250/month** |

### Performance vs Cost Tradeoffs

| Choice | Cost Impact | Performance Gain |
|--------|------------|-----------------|
| ClickHouse r5.4xlarge (memory-optimized) | +40% vs c5 | Queries hit memory, not disk → 10× faster |
| Redis cluster (6 nodes) | +$2,500/mo | Eliminates ClickHouse cold hits → saves CH capacity |
| Kafka MSK managed | +$500/mo vs self-managed | Zero operational overhead for broker management |
| MongoDB archive | +$3,000/mo | Event replay, audit trail, GDPR capability |
| S3 cold tier | Cheap ($0.023/GB) | 10× cheaper than ClickHouse for year-old data |
| Bloom filter dedup | Zero cost (in-process) | Saves ~30% Redis SET calls |

### Cost Optimization Levers

1. **ClickHouse compression:** LZ4 (default) gives 10× compression → 90 TB hot = 9 TB actual disk. Reduce from r5.4xlarge to r5.2xlarge if headroom exists.
2. **Spot instances for CH-Writer and Enricher:** Stateless Kafka consumers. On spot interruption, consumer lag grows but no data loss. Save ~60% on compute.
3. **Kafka compaction + S3 offload (Tiered Storage):** MSK Tiered Storage can move old Kafka segments to S3. Reduces MSK broker disk cost by 70%.
4. **Cache hit rate improvement:** Every 1% improvement in Redis hit rate saves ~0.13 CH queries/s → at scale, this adds up.
5. **Free tier rate limiting:** Free-tier apps capped at 1,000 events/day — they don't contribute meaningfully to storage cost.

---

*Document version: 1.1 | Architecture owner: Platform Engineering | Last updated: 2026-04-17*
