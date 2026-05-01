# PulseAnalytics — Low Level Design (LLD)

> Detailed component-by-component design derived from the HLD. Covers algorithms, data schemas, API contracts, caching, retry logic, error handling, and internal concurrency models.

---

## Table of Contents

1. [Gateway Service — Detailed Design](#1-gateway-service--detailed-design)
2. [Enricher Service — Detailed Design](#2-enricher-service--detailed-design)
3. [Session Engine — Detailed Design](#3-session-engine--detailed-design)
4. [Funnel Processor — Detailed Design](#4-funnel-processor--detailed-design)
5. [CH-Writer — Detailed Design](#5-ch-writer--detailed-design)
6. [Query API — Detailed Design](#6-query-api--detailed-design)
7. [Alert Engine — Detailed Design](#7-alert-engine--detailed-design)
8. [Notification Service — Detailed Design](#8-notification-service--detailed-design)
9. [Auth Service — Detailed Design](#9-auth-service--detailed-design)
10. [Kafka Topic Design](#10-kafka-topic-design)
11. [ClickHouse Schema Design](#11-clickhouse-schema-design)
12. [PostgreSQL Schema Design](#12-postgresql-schema-design)
13. [Redis Key Design](#13-redis-key-design)
14. [MongoDB Schema Design](#14-mongodb-schema-design)
15. [Deduplication Design](#15-deduplication-design)
16. [Rate Limiting Design](#16-rate-limiting-design)
17. [Caching Design](#17-caching-design)
18. [Retry & Circuit Breaker Design](#18-retry--circuit-breaker-design)
19. [Observability Design — LGTM Stack](#19-observability-design--lgtm-stack)
20. [Missing Data & Edge Cases](#20-missing-data--edge-cases)
21. [Sharding & Replication — Detailed Design](#21-sharding--replication--detailed-design)
22. [Minikube Deployment — Detailed Design](#22-minikube-deployment--detailed-design)
23. [Load Testing — Detailed Design](#23-load-testing--detailed-design)

---

## 1. Gateway Service — Detailed Design

### 1.1 Request Processing Pipeline

Every ingest request passes through a fixed middleware chain. Order matters — auth runs before rate limiting (no wasted Redis calls on unauthenticated requests); dedup runs after validation (no wasted Redis calls on invalid payloads).

```
HTTP Request
     │
     ▼
[1] RequestID injection (UUID, added to response header + logs)
     │
     ▼
[2] Gzip decompression (if Content-Encoding: gzip)
     │
     ▼
[3] Body size check (reject if > 5 MB; prevents OOM)
     │
     ▼
[4] JSON decode → EventBatch struct
     │
     ▼
[5] Schema validation
     │   • app_id non-empty
     │   • device_id non-empty
     │   • events array non-empty, len <= 500
     │   • each event: event_name non-empty, event_time within ±7 days
     │   REJECT → 400 Bad Request with field-level error message
     │
     ▼
[6] API Key authentication
     │   • Extract X-API-Key header
     │   • Check Redis cache: GET apikey:{key} → AppConfig{app_id, rps, burst, active}
     │   • On cache miss: query PostgreSQL apps table (indexed on api_key)
     │   • Populate Redis cache (TTL 5 min, configurable via PULSE_AUTH_API_KEY_TTL)
     │   • If not found or active=false → 401 Unauthorized
     │
     ▼
[7] Rate Limiting (per API key)
     │   • Redis Lua token-bucket (atomic, no race condition)
     │   • On limit exceeded → 429 Too Many Requests
     │       Response headers: Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining
     │
     ▼
[8] Deduplication
     │   • Per event: compute dedup key = sha256(app_id + device_id + event_id)
     │   • Bloom filter lookup (in-process, ~1µs): if "definitely not seen" → proceed
     │   • If "possibly seen": Redis SET NX with TTL 24h (authoritative check)
     │   • Skip event if Redis confirms duplicate; add to bloom filter if new
     │
     ▼
[9] GeoIP enrichment
     │   • Extract IP from X-Forwarded-For (first IP, after removing known proxies)
     │   • MaxMind lookup → CountryCode, Region, City, Lat, Lon (nullable on failure)
     │
     ▼
[10] Server timestamp injection
     │   • Set server_time = time.Now().UTC() on each event
     │   • Preserve client event_time as-is
     │
     ▼
[11] Mongo archive (async)
     │   • Spawn goroutine; does NOT block response
     │   • On Mongo failure: log error + Prometheus counter (no ingest failure)
     │
     ▼
[12] Kafka publish
     │   • Serialize each event to JSON (or Protobuf if configured)
     │   • Partition key = app_id + ":" + device_id (consistent routing)
     │   • Async producer: place on channel, return when acknowledged by local queue
     │   • On Kafka unavailable: respond 503 Service Unavailable (data not lost yet;
     │     SDK should retry; Gateway queue has 30s buffer)
     │
     ▼
[13] HTTP 200 OK {"accepted": N, "rejected": M, "request_id": "..."}
```

### 1.2 EventBatch API Contract

```
POST /v1/events
Headers:
  X-API-Key: pk_live_...
  Content-Type: application/json
  Content-Encoding: gzip (optional)

Body:
{
  "app_id":     "app_abc123",          // required; tenant identifier
  "device_id":  "d_xyz789",            // required; unique device/browser identifier
  "user_id":    "u_123",               // optional; logged-in user identity
  "sdk_version":"pulse-js-2.1.0",      // optional; for debugging
  "sent_at_ms": 1712830000000,         // optional; client send timestamp (ms)
  "events": [
    {
      "event_id":   "evt_abc",         // required; client-generated UUID for dedup
      "event_name": "Button Clicked",  // required; max 256 chars
      "event_time": 1712829999000,     // required; epoch ms (client-side)
      "props": {                       // optional; arbitrary string k/v
        "button_id": "cta_signup",
        "page": "/landing"
      },
      "revenue": 9.99,                 // optional; purchase value in USD
      "device": {                      // optional; device context
        "platform":    "web",
        "os":          "macOS",
        "browser":     "Chrome",
        "app_version": "1.0.0"
      }
    }
  ]
}

Response 200:
{
  "accepted":   48,
  "rejected":   2,
  "duplicates": 1,
  "request_id": "req_7f3a..."
}

Response 400:
{ "error": "validation_failed", "details": [{"field": "events[2].event_name", "message": "required"}] }

Response 401: { "error": "invalid_api_key" }
Response 429: { "error": "rate_limit_exceeded", "retry_after": 2 }
Response 503: { "error": "service_unavailable", "retry_after": 5 }
```

### 1.3 Bloom Filter Configuration

```
Target: deduplicate SDK retries within 24-hour window
Estimated unique events/day per pod: 10M (assuming 10 Gateway pods)
False positive rate target: 0.1%

Bloom filter sizing:
  n = 10,000,000 items
  p = 0.001 (0.1% FPR)
  m = -n * ln(p) / (ln(2))^2 ≈ 143,775,827 bits ≈ 17 MB per pod
  k = m/n * ln(2) ≈ 10 hash functions

Implementation: bits-and-blooms/bloom v3
Reset schedule: daily at 00:00 UTC (new bloom for new day)
Persistence: not persisted; Redis SET is the authoritative dedup store
```

### 1.4 MongoDB Archive Document

```javascript
// Collection: raw_events
// Indexes: { app_id: 1, event_time: -1 }, { device_id: 1 }, { user_id: 1 }
//          { app_id: 1, event_name: 1, event_time: -1 }
// TTL index: { created_at: 1 }, expireAfterSeconds: 7776000 (90 days)
{
  "_id":          ObjectId("..."),      // auto-generated
  "app_id":       "app_abc123",
  "device_id":    "d_xyz789",
  "user_id":      "u_123",             // null if not provided
  "event_id":     "evt_abc",
  "event_name":   "Button Clicked",
  "event_time":   ISODate("2026-04-11T10:00:00Z"),
  "server_time":  ISODate("2026-04-11T10:00:00.042Z"),
  "props":        { "button_id": "cta_signup", "page": "/landing" },
  "revenue":      9.99,
  "ip":           "203.0.113.42",
  "country_code": "US",
  "sdk_version":  "pulse-js-2.1.0",
  "raw_payload":  { /* full original JSON */ },
  "created_at":   ISODate("2026-04-11T10:00:00.050Z")  // TTL field (90-day expiry)
}
```

---

## 2. Enricher Service — Detailed Design

### 2.1 Consumer Architecture

```
Kafka Consumer (franz-go)
  Consumer Group: enricher-group
  Topics: raw-events (12 partitions)
  
Per-pod setup:
  - 1 Kafka consumer (handles assigned partitions)
  - Worker pool: 50 goroutines (configurable via ENRICHER_WORKERS env)
  - Channel buffer: 10,000 events (backpressure signal)

Processing flow per event:
  poll batch from Kafka
       │
       ▼
  for each event in batch → send to worker channel
       │
       ▼ (goroutine pool)
  enrich(event) → enrichedEvent
       │
  publish to enriched-events Kafka topic
       │
  commit offset (only after all events in batch are published)
```

### 2.2 Enrichment Operations

**User-Agent Parsing:**
```
Input:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 ..."
Output: { browser: "Chrome", browser_version: "124.0", os_family: "macOS",
          platform: "web", is_mobile: false, is_tablet: false }

Library: mssola/useragent or ua-parser/uap-go
Fallback: all fields null (no error propagation)
```

**Timestamp Correction:**
```go
// Clock skew detection
const maxSkewSeconds = 300 // 5 minutes

func correctTimestamp(clientMs int64, serverTime time.Time) time.Time {
    client := time.UnixMilli(clientMs).UTC()
    skew := serverTime.Sub(client).Abs()
    if skew > maxSkewSeconds*time.Second {
        // Client clock is drifted; use server time
        return serverTime
    }
    return client
}

// client_offset_ms is always recorded for diagnostic purposes
clientOffsetMs = serverTime.UnixMilli() - clientMs
```

**GeoIP (Secondary Enrichment — Enricher has own copy):**
- Same MaxMind mmdb loaded in Enricher process.
- Enricher re-runs GeoIP in case Gateway omitted it (e.g., request came via internal SDK without X-Forwarded-For).

### 2.3 EnrichedEvent Schema (Kafka message)

```json
{
  "event_id":        "evt_abc",
  "app_id":          "app_abc123",
  "device_id":       "d_xyz789",
  "user_id":         "u_123",
  "event_name":      "Button Clicked",
  "event_time":      "2026-04-11T10:00:00Z",
  "server_time":     "2026-04-11T10:00:00.042Z",
  "client_offset_ms": 42,
  "session_id":      "sess_7f3a...",
  "country_code":    "US",
  "region":          "California",
  "city":            "San Francisco",
  "lat":             37.7749,
  "lon":             -122.4194,
  "browser":         "Chrome",
  "browser_version": "124.0",
  "os_family":       "macOS",
  "platform":        "web",
  "is_mobile":       false,
  "app_version":     "1.0.0",
  "props":           { "button_id": "cta_signup" },
  "revenue":         9.99,
  "install_source":  null,
  "campaign_id":     null
}
```

### 2.4 Dead Letter Queue (DLQ) Policy

```
Conditions that route to DLQ:
  1. event_id or event_name is empty after unmarshal (should not happen; gateway validates)
  2. Protobuf unmarshal failure (schema incompatibility)
  3. Any panic/recover in enrichment goroutine

DLQ message format:
  { "original_payload": "base64...", "error": "unmarshal failed: ...", "ts": "..." }

DLQ consumer: monitoring dashboard reads dlq-events
  - Alert if DLQ rate > 0.01% of total events
  - Replay tool: re-publish DLQ events to raw-events after schema fix
```

---

## 3. Session Engine — Detailed Design

### 3.1 Session State Machine

```
States: NONE → ACTIVE → ENDED

Transition rules:
  NONE → ACTIVE:   First event for device_id in this app, OR gap > 30 min since last event
  ACTIVE → ACTIVE: Event arrives within 30 min of last event (update Redis TTL)
  ACTIVE → ENDED:  Redis TTL expires (30 min of inactivity)

Session end detection: passive (TTL expiry triggers no Kafka message directly)
  Instead: when next event arrives after gap > 30 min, Session Engine creates
  synthetic ENDED event for the previous session before starting new one.
```

### 3.2 Redis Session Key Design

```
Key:   session:{app_id}:{device_id}
Type:  Redis Hash
TTL:   1800 seconds (30 minutes, reset on every event)

Fields:
  session_id:      "sess_7f3a..."     (UUID)
  started_at:      "1712830000000"    (epoch ms)
  last_event_at:   "1712830120000"    (epoch ms)
  event_count:     "42"               (string, HINCRBY)
  entry_event:     "App Opened"       (first event in session)
  exit_event:      "Button Clicked"   (last event seen so far)

Commands used:
  HGETALL session:{app_id}:{device_id}
  HMSET session:{app_id}:{device_id} last_event_at {ts} exit_event {name}
  EXPIRE session:{app_id}:{device_id} 1800
  HINCRBY session:{app_id}:{device_id} event_count 1
  DEL session:{app_id}:{device_id}   (on explicit session end)
```

### 3.3 Session Event Schema (Kafka: session-events)

```json
// Session START
{
  "type":        "session_start",
  "session_id":  "sess_7f3a...",
  "app_id":      "app_abc123",
  "device_id":   "d_xyz789",
  "user_id":     "u_123",
  "started_at":  "2026-04-11T10:00:00Z",
  "country_code":"US",
  "platform":    "web"
}

// Session END (emitted when next session starts after gap)
{
  "type":         "session_end",
  "session_id":   "sess_7f3a...",
  "app_id":       "app_abc123",
  "device_id":    "d_xyz789",
  "user_id":      "u_123",
  "started_at":   "2026-04-11T10:00:00Z",
  "ended_at":     "2026-04-11T10:30:15Z",
  "duration_sec": 1815,
  "event_count":  42,
  "entry_event":  "App Opened",
  "exit_event":   "Button Clicked"
}
```

---

## 4. Funnel Processor — Detailed Design

### 4.1 Funnel Definition (PostgreSQL → In-Memory Cache)

```sql
-- funnel_definitions table row
funnel_id:      "funnel_abc"
app_id:         "app_abc123"
name:           "Signup Flow"
steps:          ["App Opened", "Signup Page Viewed", "Signup Completed"]
window_seconds: 86400   -- user must complete all steps within 24 hours
created_at:     "..."
```

In-memory cache structure (per Funnel Processor pod):
```go
type FunnelIndex map[string][]*FunnelDefinition  // key: app_id
// Refreshed every 30 seconds from PostgreSQL (hot-reload ticker)
```

### 4.2 User Progress Tracking in Redis

```
Key:   funnel:state:{app_id}:{funnel_id}:{user_id}
Type:  Redis Sorted Set (ZSET)
TTL:   8 days (stateEvictTTL — fixed eviction window, independent of funnel window)

Members: step index as string ("0", "1", "2", ...)
Score:   event timestamp in ms (server_time)

Algorithm:
  On each session-event for app_id:
    1. Find all funnels for this app where event_name matches any step
    2. For each matching funnel:
       a. ZADD funnel:state:{app_id}:{funnel_id}:{user_id} {score=ts_ms} {member=stepIdx}
       b. EXPIRE key 8 days
       c. If this is NOT the last step → done (wait for more events)
       d. If last step reached:
            ZRANGEBYSCORE key [now_ms - window_ms] [now_ms] → check all prior steps exist
            If all steps present within window → emit ConversionEvent, DEL key
            Else → no conversion yet
```

### 4.3 Funnel Conversion Event (Kafka: agg-results)

```json
{
  "funnel_id":      "funnel_abc",
  "app_id":         "app_abc123",
  "user_id":        "u_123",
  "converted":      true,
  "steps_complete": 3,
  "total_steps":    3,
  "duration_ms":    2700000,
  "converted_at":   1712831700000
}
```

---

## 5. CH-Writer — Detailed Design

**Input topics:** `enriched-events`, `session-events`, `agg-results`. Handles `EnrichedEvent` rows (from enriched-events), `SessionEvent` rows (session_start / session_end from session-events), and funnel conversion rows (from agg-results) by attempting to unmarshal each message as the appropriate type per topic.

### 5.1 Batching Algorithm

```go
// Per topic, one batcher goroutine:
type Batcher struct {
    buf         []CHEvent
    flushTicker *time.Ticker    // 1-second tick
    maxRows     int              // 500,000
    chClient    *ClickHouseConn
}

func (b *Batcher) run() {
    for {
        select {
        case event := <-b.inputCh:
            b.buf = append(b.buf, event)
            if len(b.buf) >= b.maxRows {
                b.flush()  // size-triggered flush
            }
        case <-b.flushTicker.C:
            if len(b.buf) > 0 {
                b.flush()  // time-triggered flush
            }
        }
    }
}

func (b *Batcher) flush() {
    batch := b.buf
    b.buf = b.buf[:0]  // reset (reuse underlying array)
    go b.insertWithRetry(batch)  // non-blocking: start insert goroutine
}
```

### 5.2 ClickHouse Insert (Native Protocol)

```go
// Using clickhouse-go driver
func (b *Batcher) insertWithRetry(events []CHEvent) {
    backoff := 1 * time.Second
    for attempt := 0; attempt < 5; attempt++ {
        err := b.chClient.InsertBatch(ctx, events)
        if err == nil {
            metrics.InsertSuccess.Add(float64(len(events)))
            return
        }
        if isUnrecoverable(err) {
            // Schema mismatch, type error — don't retry
            metrics.InsertDead.Add(float64(len(events)))
            log.Error("unrecoverable insert error", zap.Error(err))
            return
        }
        // Transient error: backoff + jitter
        jitter := time.Duration(rand.Intn(200)) * time.Millisecond
        time.Sleep(backoff + jitter)
        backoff = min(backoff*2, 30*time.Second)
    }
    // All retries exhausted: circuit breaker open
    b.circuitBreaker.RecordFailure()
    metrics.InsertExhausted.Add(float64(len(events)))
    // Events stay in Kafka; consumer offset NOT committed → will be reprocessed
}
```

**Insert SQL template:**
```sql
INSERT INTO events
  (app_id, event_id, user_id, device_id, event_name,
   event_time, server_time, session_id, country_code,
   platform, app_version, os_family, browser, city,
   revenue, props, campaign_id, install_source)
VALUES ...
FORMAT Native
```

### 5.3 Consumer Offset Commit Strategy

```
Commit mode: MANUAL after successful batch insert
  - If insert succeeds → commit offsets for all events in batch
  - If insert fails after all retries → do NOT commit offsets
  - On pod restart: re-consume from last committed offset
  - Consequence: at-least-once delivery to ClickHouse
  - ClickHouse dedup: INSERT DEDUPLICATION enabled on ReplicatedMergeTree
    (deduplicates based on full row hash for identical inserts)

Note: ClickHouse INSERT DEDUPLICATION is per-block (not per-row).
For row-level dedup, event_id is stored and FINAL keyword is used in queries:
  SELECT ... FROM events FINAL WHERE ...  (applies ReplacingMergeTree semantics)
```

---

## 6. Query API — Detailed Design

### 6.1 Query Routing Decision Tree

```
Incoming query request
        │
        ├── Is granularity = DAU/WAU/MAU?
        │       YES → query dau_mv materialized view (HyperLogLog merge)
        │             estimated latency: 5–20ms
        │
        ├── Is query type = event_count with hourly granularity?
        │       YES → query hourly_counts_mv materialized view (SummingMergeTree)
        │             estimated latency: 5–15ms
        │
        ├── Is query type = retention?
        │       YES → query retention_events table (pre-computed cohort rows)
        │             estimated latency: 10–50ms
        │
        ├── Is query type = session metrics?
        │       YES → query session_summaries table
        │             estimated latency: 20–100ms
        │
        ├── Is query type = funnel?
        │       YES → query funnel_conversions table
        │             estimated latency: 10–50ms
        │
        └── Ad-hoc event query (arbitrary filters on events table)
                    → Build SQL with mandatory app_id + date_range filter
                    → Apply property filters from request
                    → Execute with 30s timeout
                    estimated latency: 50ms–2s
```

### 6.2 ClickHouse Query Builder (Internal)

```go
// All queries enforce partition pruning
type EventCountQuery struct {
    AppID       string
    StartDate   time.Time  // inclusive
    EndDate     time.Time  // inclusive, max 90 days from StartDate
    EventNames  []string   // optional filter
    Filters     []Filter   // optional property filters
    Granularity string     // "hour", "day", "week", "month"
}

func (q *EventCountQuery) ToSQL() string {
    // MANDATORY clauses (partition pruning):
    //   WHERE app_id = '{AppID}'
    //   AND toDate(event_time) BETWEEN '{StartDate}' AND '{EndDate}'
    //
    // GROUP BY:
    //   toStartOf{Granularity}(event_time) AS ts
    //
    // Property filters (safe parameterized via bindvars):
    //   AND props['key'] = 'value'   (uses bloom filter index on props)
    //
    // Limit: LIMIT 10000 (prevent runaway result sets)
}
```

### 6.3 Cache Key Design

```go
func queryCacheKey(req QueryRequest) string {
    // Canonical JSON of request (sorted keys) → SHA-256 → hex
    canonical, _ := json.Marshal(req)  // struct with sorted fields
    hash := sha256.Sum256(canonical)
    return "qcache:" + hex.EncodeToString(hash[:])
}

// Example key: qcache:a3f7b2c1d4e5...
// TTL: 300 seconds (5 minutes)
// Value: JSON-encoded query response (gzip-compressed in Redis)
```

### 6.4 DAU Query (HyperLogLog)

```sql
-- From dau_mv (materialized view using uniqState)
SELECT
    toDate(day)                                AS date,
    uniqMerge(user_hll)                        AS dau
FROM dau_mv
WHERE
    app_id    = {app_id: String}
    AND day  >= {start: Date}
    AND day  <= {end: Date}
GROUP BY date
ORDER BY date

-- HyperLogLog error rate: ±2% at 95th percentile
-- Much faster than COUNT(DISTINCT user_id) on raw events table
```

### 6.5 Funnel Query (Query-Time Computation)

```sql
-- Step-by-step conversion analysis
-- Uses funnel_conversions pre-computed table for speed
SELECT
    steps_completed,
    count()              AS users
FROM funnel_conversions
WHERE
    funnel_id = {funnel_id: String}
    AND app_id = {app_id: String}
    AND toDate(started_at) BETWEEN {start: Date} AND {end: Date}
GROUP BY steps_completed
ORDER BY steps_completed

-- For ad-hoc funnels (not pre-computed):
-- windowFunnel() ClickHouse function on raw events table
SELECT
    level,
    count() AS users
FROM (
    SELECT
        user_id,
        windowFunnel(86400)(event_time,
            event_name = 'App Opened',
            event_name = 'Signup Page Viewed',
            event_name = 'Signup Completed'
        ) AS level
    FROM events
    WHERE app_id = {app_id} AND toDate(event_time) BETWEEN {start} AND {end}
    GROUP BY user_id
)
GROUP BY level
```

### 6.6 JWT Validation

```go
// Middleware: runs on every /v1/* request except /v1/auth/login
func JWTMiddleware(jwtSecret string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            if !strings.HasPrefix(authHeader, "Bearer ") {
                http.Error(w, `{"error":"missing_token"}`, 401)
                return
            }
            tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
            claims, err := parseJWT(tokenStr, jwtSecret)
            if err != nil {
                http.Error(w, `{"error":"invalid_token"}`, 401)
                return
            }
            // Inject app_id, org_id into context
            ctx := context.WithValue(r.Context(), ctxKeyAppID, claims.AppID)
            ctx = context.WithValue(ctx, ctxKeyOrgID, claims.OrgID)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// JWT Claims structure
type Claims struct {
    AppID     string `json:"app_id"`
    OrgID     string `json:"org_id"`
    IssuedAt  int64  `json:"iat"`
    ExpiresAt int64  `json:"exp"`
}
// Algorithm: HS256 (HMAC-SHA256), shared secret
// TTL: 86400 seconds (24 hours) default, configurable via PULSE_AUTH_JWT_EXPIRY
```

---

## 7. Alert Engine — Detailed Design

### 7.1 Evaluation Loop

```
Startup:
  1. Load all alert_rules from PostgreSQL WHERE active = true
  2. Schedule each rule for evaluation every 60 seconds (configurable per rule)

Per tick (every 60 seconds):
  For each alert rule:
    1. Build ClickHouse query based on rule.metric_name and time window
    2. Execute query (timeout 10s)
    3. Compare result against rule.threshold using rule.condition operator
    4. If threshold breached:
         a. Check Redis: GET alert_cooldown:{rule_id}
         b. If key exists → skip (cooldown active, 30-min default)
         c. If key absent:
              - SET alert_cooldown:{rule_id} "fired" EX 1800
              - Publish to notifications Kafka topic
    5. Record evaluation metric (success/failure/latency)
```

### 7.2 Alert Rule Schema (PostgreSQL)

```sql
CREATE TABLE alert_rules (
    rule_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id        UUID NOT NULL REFERENCES apps(app_id),
    name          TEXT NOT NULL,
    metric_name   TEXT NOT NULL,  -- e.g., "event_count", "revenue", "dau"
    event_filter  TEXT,           -- optional event_name filter
    condition     TEXT NOT NULL,  -- "gt", "lt", "eq", "gte", "lte"
    threshold     FLOAT8 NOT NULL,
    window_minutes INT NOT NULL DEFAULT 60,
    cooldown_minutes INT NOT NULL DEFAULT 30,
    channels      TEXT[] NOT NULL DEFAULT '{}',  -- ["webhook", "email"]
    webhook_url   TEXT,
    email_to      TEXT[],
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 7.3 Alert Notification Kafka Message

```json
{
  "rule_id":     "rule_abc",
  "app_id":      "app_abc123",
  "rule_name":   "High Error Rate",
  "metric":      "event_count",
  "actual":      15234.5,
  "threshold":   10000.0,
  "condition":   "gt",
  "fired_at":    "2026-04-11T10:00:00Z",
  "channels":    ["webhook", "email"],
  "webhook_url": "https://hooks.customer.com/alerts",
  "email_to":    ["ops@customer.com"]
}
```

---

## 8. Notification Service — Detailed Design

### 8.1 Webhook Delivery

```go
func sendWebhook(url string, payload []byte) error {
    client := &http.Client{Timeout: 10 * time.Second}
    req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Pulse-Signature", hmacSignature(payload, webhookSecret))
    req.Header.Set("User-Agent", "PulseAnalytics-Webhooks/1.0")

    resp, err := client.Do(req)
    if err != nil || resp.StatusCode >= 500 {
        return fmt.Errorf("webhook failed: %w", err)
    }
    if resp.StatusCode >= 400 {
        // Client error (400-499): do NOT retry (customer endpoint issue)
        log.Warn("webhook client error", zap.Int("status", resp.StatusCode))
        return nil  // treat as delivered
    }
    return nil
}
```

**Retry policy for webhooks:**
```
Attempt 1: immediate
Attempt 2: 30 seconds later
Attempt 3: 5 minutes later
Give up: mark delivery failed, alert ops team
```

**Webhook security:** HMAC-SHA256 signature in `X-Pulse-Signature` header. Customer verifies against their shared secret.

### 8.2 Email Delivery

```go
// SMTP via AWS SES
func sendEmail(to []string, subject, body string) error {
    // SES SDK call
    // Retry: 3 attempts with 10s backoff
    // On throttle (429 from SES): 60s backoff
}
```

---

## 9. Auth Service — Detailed Design

### 9.1 API Key → JWT Exchange

```
POST /v1/auth/login
Body: { "api_key": "pk_live_..." }

Flow:
  1. Query PostgreSQL: SELECT app_id, org_id, active FROM apps WHERE api_key = $1
  2. If not found or active=false → 401
  3. Generate JWT:
       Claims { AppID, OrgID, IssuedAt: now, ExpiresAt: now+TTL }
       Sign with HS256 and PULSE_AUTH_JWT_SECRET
  4. Return { "token": "eyJ...", "expires_at": "2026-04-11T11:00:00Z" }

Auth Service does NOT validate JWTs — Query API validates locally (stateless).
Auth Service is only called to ISSUE tokens.
```

### 9.2 API Key Format

```
Format: pk_{env}_{random_base62_32chars}
Example: pk_live_7f3aB2cD4eF5gH6iJ7kL8mN9oP0qR1sT

Generation: crypto/rand → base62 encoding
Storage: bcrypt hash in PostgreSQL (api_key_hash column)
Lookup: full key stored temporarily in Redis for rate limiting; PostgreSQL has full key for initial validation
```

**Tradeoff:** Storing plaintext API key in PostgreSQL (not hashed) for exact lookup. Hashed storage would require bcrypt comparison (slow). Mitigated by: TLS in transit, DB access controls, API key rotation capability.

---

## 10. Kafka Topic Design

### 10.1 Topic Configurations

| Topic | Partitions | Replicas | Retention | Min ISR | Compression | Key |
|-------|-----------|---------|-----------|---------|-------------|-----|
| `raw-events` | 12 | 3 | 48h | 2 | snappy | `{app_id}:{device_id}` |
| `enriched-events` | 12 | 3 | 48h | 2 | snappy | `{app_id}:{device_id}` |
| `session-events` | 6 | 3 | 24h | 2 | snappy | `{app_id}` |
| `agg-results` | 4 | 3 | 24h | 2 | snappy | `{app_id}` |
| `dlq-events` | 2 | 3 | 7 days | 1 | gzip | none |
| `notifications` | 2 | 3 | 24h | 1 | snappy | `{app_id}` |

### 10.2 Producer Configuration

```yaml
# Gateway producer (franz-go)
producer:
  acks: "leader"            # default: ack from partition leader only (lower latency)
                            # set to "all" for stronger durability on critical topics
  compression: "snappy"
  batch_max_bytes: 1048576  # 1 MB max batch
  linger_ms: 5              # 5ms batching delay (latency vs throughput tradeoff)
  max_in_flight: 5          # pipeline 5 unacked batches
  retry_max: 10
  retry_backoff_ms: 100
```

### 10.3 Consumer Group Configuration

```yaml
# Enricher consumer group
consumer:
  group_id: "enricher-group"
  auto_offset_reset: "latest"   # on first run, start from latest
  max_poll_records: 1000
  session_timeout_ms: 30000
  heartbeat_interval_ms: 3000
  enable_auto_commit: false     # manual offset commit after processing
  isolation_level: "read_committed"
```

### 10.4 Partition Lag Monitoring

```
Alert thresholds (Prometheus → Grafana):
  enricher-group lag on raw-events: > 100,000 messages → WARNING
  enricher-group lag on raw-events: > 500,000 messages → CRITICAL (auto-scale Enricher)
  ch-writer-group lag on enriched-events: > 200,000 messages → WARNING
```

---

## 11. ClickHouse Schema Design

### 11.1 Core Events Table

```sql
CREATE TABLE events ON CLUSTER pulse_cluster
(
    -- Partition and ordering keys
    app_id          LowCardinality(String),
    event_time      DateTime64(3, 'UTC'),
    
    -- Identity
    event_id        UUID,
    user_id         String,
    device_id       String,
    session_id      String,
    
    -- Event data
    event_name      LowCardinality(String),
    server_time     DateTime64(3, 'UTC'),
    client_offset_ms Int32,         -- server_time - client event_time in ms
    
    -- Geo
    country_code    LowCardinality(String),
    region          LowCardinality(String),
    city            LowCardinality(String),
    lat             Nullable(Float32),
    lon             Nullable(Float32),
    
    -- Device / UA
    platform        LowCardinality(String),   -- web, ios, android, server
    os_family       LowCardinality(String),
    browser         LowCardinality(String),
    browser_version LowCardinality(String),
    is_mobile       UInt8,
    app_version     LowCardinality(String),
    
    -- Attribution
    install_source  LowCardinality(String),
    campaign_id     LowCardinality(String),
    
    -- Revenue
    revenue         Nullable(Float64),
    
    -- Flexible properties
    props           Map(String, String),
    
    -- Metadata
    ingested_at     DateTime DEFAULT now()
)
ENGINE = ReplicatedMergeTree(
    '/clickhouse/tables/{shard}/events',   -- ZooKeeper path
    '{replica}'                             -- replica name from macros
)
PARTITION BY (toYYYYMMDD(event_time), app_id)
ORDER BY (app_id, event_name, toStartOfHour(event_time), user_id)
SETTINGS
    index_granularity = 8192,
    -- Deduplication: deduplicate identical inserts within 100ms window
    replicated_deduplication_window = 1000,
    -- TTL: move to cold after 90 days, delete after 365 days
    storage_policy = 'hot_cold_s3';

-- TTL rules
ALTER TABLE events MODIFY TTL
    event_time + INTERVAL 90 DAY TO DISK 'cold_s3',
    event_time + INTERVAL 365 DAY DELETE;

-- Skip indices for fast property lookups
ALTER TABLE events ADD INDEX props_bloom_idx props TYPE bloom_filter(0.01) GRANULARITY 4;
ALTER TABLE events ADD INDEX event_name_idx event_name TYPE set(200) GRANULARITY 4;
ALTER TABLE events ADD INDEX user_id_idx user_id TYPE bloom_filter(0.01) GRANULARITY 4;

-- Distributed table (query router across shards)
CREATE TABLE events_dist ON CLUSTER pulse_cluster AS events
ENGINE = Distributed(pulse_cluster, default, events, cityHash64(app_id));
```

### 11.2 DAU Materialized View

```sql
-- State table (stores HyperLogLog intermediate state)
CREATE TABLE dau_state ON CLUSTER pulse_cluster
(
    app_id       LowCardinality(String),
    day          Date,
    platform     LowCardinality(String),
    user_hll     AggregateFunction(uniq, String)   -- HyperLogLog state
)
ENGINE = ReplicatedAggregatingMergeTree(...)
PARTITION BY toYYYYMM(day)
ORDER BY (app_id, day, platform);

-- MV that populates dau_state on every insert to events
CREATE MATERIALIZED VIEW dau_mv ON CLUSTER pulse_cluster
TO dau_state AS
SELECT
    app_id,
    toDate(event_time)          AS day,
    platform,
    uniqState(user_id)          AS user_hll
FROM events
GROUP BY app_id, day, platform;

-- Query (merges HLL states):
SELECT
    day,
    uniqMerge(user_hll) AS dau
FROM dau_state
WHERE app_id = 'app_abc123'
  AND day BETWEEN '2026-04-01' AND '2026-04-11'
GROUP BY day ORDER BY day;
```

### 11.3 Hourly Counts Materialized View

```sql
CREATE TABLE hourly_counts ON CLUSTER pulse_cluster
(
    app_id       LowCardinality(String),
    event_name   LowCardinality(String),
    hour         DateTime,
    count        UInt64
)
ENGINE = ReplicatedSummingMergeTree(count)
PARTITION BY toYYYYMMDD(hour)
ORDER BY (app_id, event_name, hour);

CREATE MATERIALIZED VIEW hourly_counts_mv ON CLUSTER pulse_cluster
TO hourly_counts AS
SELECT
    app_id,
    event_name,
    toStartOfHour(event_time) AS hour,
    count()                   AS count
FROM events
GROUP BY app_id, event_name, hour;
```

### 11.4 Session Summaries Table

```sql
CREATE TABLE session_summaries ON CLUSTER pulse_cluster
(
    app_id         LowCardinality(String),
    session_id     String,
    user_id        String,
    device_id      String,
    started_at     DateTime64(3, 'UTC'),
    ended_at       DateTime64(3, 'UTC'),
    duration_sec   UInt32,
    event_count    UInt32,
    entry_event    LowCardinality(String),
    exit_event     LowCardinality(String),
    country_code   LowCardinality(String),
    platform       LowCardinality(String)
)
ENGINE = ReplicatedMergeTree(...)
PARTITION BY (toYYYYMMDD(started_at), app_id)
ORDER BY (app_id, toDate(started_at), session_id);
```

### 11.5 Funnel Conversions Table

```sql
CREATE TABLE funnel_conversions ON CLUSTER pulse_cluster
(
    app_id          LowCardinality(String),
    funnel_id       String,
    user_id         String,
    steps_completed UInt8,
    total_steps     UInt8,
    completed       UInt8,  -- 0 or 1
    started_at      DateTime64(3, 'UTC'),
    completed_at    Nullable(DateTime64(3, 'UTC')),
    duration_sec    Nullable(UInt32)
)
ENGINE = ReplicatedMergeTree(...)
PARTITION BY (toYYYYMMDD(started_at), app_id)
ORDER BY (app_id, funnel_id, started_at, user_id);
```

### 11.6 Storage Policy (Hot/Cold Tiering)

```xml
<!-- /etc/clickhouse-server/config.d/storage_policy.xml -->
<storage_configuration>
    <disks>
        <hot>
            <type>local</type>
            <path>/var/lib/clickhouse/</path>
        </hot>
        <cold_s3>
            <type>s3</type>
            <endpoint>https://s3.us-east-1.amazonaws.com/pulse-analytics-cold/</endpoint>
            <access_key_id>...</access_key_id>
            <secret_access_key>...</secret_access_key>
        </cold_s3>
    </disks>
    <policies>
        <hot_cold_s3>
            <volumes>
                <hot_volume>
                    <disk>hot</disk>
                    <max_data_part_size_bytes>10737418240</max_data_part_size_bytes>
                </hot_volume>
                <cold_volume>
                    <disk>cold_s3</disk>
                </cold_volume>
            </volumes>
            <move_factor>0.2</move_factor>
        </hot_cold_s3>
    </policies>
</storage_configuration>
```

---

## 12. PostgreSQL Schema Design

```sql
-- Organizations (top-level billing entities)
CREATE TABLE orgs (
    org_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    plan        TEXT NOT NULL CHECK (plan IN ('free', 'growth', 'enterprise')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Applications (one org has many apps)
CREATE TABLE apps (
    app_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    api_key     TEXT NOT NULL UNIQUE,          -- plaintext for lookup; protect at DB level
    rps         INT NOT NULL DEFAULT 1000,      -- rate limit: requests per second
    burst       INT NOT NULL DEFAULT 2000,      -- burst allowance
    active      BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX apps_api_key_idx ON apps(api_key) WHERE active = true;

-- Funnel definitions
CREATE TABLE funnel_definitions (
    funnel_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    steps           TEXT[] NOT NULL,             -- ordered event names
    window_seconds  INT NOT NULL DEFAULT 86400,  -- conversion time window
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX funnel_app_idx ON funnel_definitions(app_id);

-- Cohort definitions
CREATE TABLE cohort_definitions (
    cohort_id    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    filter_sql   TEXT NOT NULL,        -- SQL WHERE clause fragment for ClickHouse
    user_count   BIGINT,               -- denormalized, updated by background job
    computed_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Alert rules
CREATE TABLE alert_rules (
    rule_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id           UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    metric_name      TEXT NOT NULL,
    event_filter     TEXT,
    condition        TEXT NOT NULL CHECK (condition IN ('gt','lt','eq','gte','lte')),
    threshold        FLOAT8 NOT NULL,
    window_minutes   INT NOT NULL DEFAULT 60,
    cooldown_minutes INT NOT NULL DEFAULT 30,
    channels         TEXT[] NOT NULL DEFAULT '{}',
    webhook_url      TEXT,
    email_to         TEXT[],
    active           BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX alert_rules_app_idx ON alert_rules(app_id) WHERE active = true;

-- Dashboards (JSONB layout)
CREATE TABLE dashboards (
    dashboard_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    layout       JSONB NOT NULL DEFAULT '[]',  -- array of widget configs
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Experiments (A/B testing)
CREATE TABLE experiments (
    experiment_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id        UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('draft','running','completed','stopped')),
    variants      JSONB NOT NULL,    -- [{"name":"control","traffic_pct":50}, ...]
    goal_event    TEXT NOT NULL,     -- event_name that measures success
    started_at    TIMESTAMPTZ,
    ended_at      TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Campaigns (push/email/webhook triggers)
CREATE TABLE campaigns (
    campaign_id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id        UUID NOT NULL REFERENCES apps(app_id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    trigger_type  TEXT NOT NULL,   -- "event", "scheduled", "cohort"
    trigger_conf  JSONB NOT NULL,
    channel       TEXT NOT NULL,   -- "push", "email", "webhook"
    channel_conf  JSONB NOT NULL,
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Async query job tracking
CREATE TABLE query_jobs (
    job_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(app_id),
    query_hash   TEXT NOT NULL,    -- SHA-256 of query parameters
    status       TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed')),
    result_key   TEXT,             -- Redis key where result is stored
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX query_jobs_hash_idx ON query_jobs(query_hash, status);
```

---

## 13. Redis Key Design

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `apikey:{api_key}` | Hash | 5 min (configurable) | API key → AppConfig cache (rps, burst, app_id) |
| `dedup:{sha256_of_event_dedup_key}` | String | 86400s | Event deduplication (24h window) |
| `session:{app_id}:{device_id}` | Hash | 1800s | Active session state |
| `rl:{api_key}:{window}` | String | window_s | Rate limit token bucket counter |
| `qcache:{sha256_of_query}` | String | 300s | Query result cache (gzip-compressed JSON) |
| `alert_cooldown:{rule_id}` | String | cooldown_s | Alert cooldown lock |
| `funnel_progress:{funnel_id}:{user_id}` | Hash | window_s+60 | User funnel state |
| `circuit:{service}:{target}` | String | 60s | Circuit breaker open state |

### Redis Memory Estimate

```
apikey cache:    5,000 apps × 200B = 1 MB
dedup keys:      10M events/hour × 64B key = 640 MB per pod (shared cluster)
session state:   20M concurrent × 512B = 10 GB
rate limit:      5,000 apps × 50B = 250 KB
query cache:     10K unique queries × 50KB avg = 500 MB
alert cooldown:  500 rules × 50B = 25 KB
funnel progress: 1M active funnels × 200B = 200 MB
---
Total: ~11.3 GB → 6-node cluster (16 GB each) = 96 GB total, ~12% utilization
```

---

## 14. MongoDB Schema Design

### Raw Events Collection

```javascript
// Collection: raw_events
// Shard key: { app_id: 1, event_time: 1 }  (range-based sharding)
// Indexes:
//   { app_id: 1, event_time: -1 }  (compound, primary query pattern)
//   { app_id: 1, event_name: 1, event_time: -1 }
//   { device_id: 1, event_time: -1 }
//   { user_id: 1, event_time: -1 }
//   { created_at: 1 }, TTL 90 days (7776000 seconds) — matches internal/mongo/client.go

{
  "_id":          ObjectId,
  "event_id":     "evt_abc",            // indexed for replay lookups
  "app_id":       "app_abc123",
  "device_id":    "d_xyz789",
  "user_id":      "u_123",
  "event_name":   "Button Clicked",
  "event_time":   ISODate,
  "server_time":  ISODate,
  "props":        {},                   // full original props map
  "device":       {},                   // full DeviceContext
  "ip":           "203.0.113.42",
  "country_code": "US",
  "raw_payload":  {},                   // complete original request event object
  "sdk_version":  "pulse-js-2.1.0",
  "created_at":   ISODate              // TTL index field (90-day expiry)
}
```

---

## 15. Deduplication Design

### Stage 1: In-Process Bloom Filter (Gateway)

```
Purpose: Reject SDK retries before hitting Redis
False-positive rate: 0.1% (1 in 1000 legitimate new events incorrectly rejected)
Cell count: 10M items per pod (17 MB memory)
Hash functions: 10
Reset: Daily at midnight UTC (new filter for new day's events)
Thread safety: sync.RWMutex around bloom operations

Dedup key: SHA-256(app_id + ":" + device_id + ":" + event_id)
  Note: event_id is client-generated UUID — SDK must send stable event_id on retry
```

### Stage 2: Redis SET NX (Cross-Pod Dedup)

```
Only runs if Bloom filter says "possibly seen" (not "definitely not seen")
Key: dedup:{hex(sha256(dedup_key))}
Command: SET dedup:{key} "1" EX 86400 NX
  NX = only set if not exists
  Returns: 1 (new) or 0 (duplicate)

Cross-pod guarantee: Redis Cluster distributes by hash slot of key
  → same dedup key always hits same Redis primary → no race condition
```

### Stage 3: ClickHouse INSERT DEDUPLICATION

```
Last resort: identical data blocks inserted multiple times are deduplicated
  by ClickHouse ReplicatedMergeTree within 100 block window.
  
For row-level dedup (e.g., in case of CH-Writer retry after crash):
  Query with FINAL keyword or SELECT ... FROM events WHERE event_id = ?
  ClickHouse retains only one copy via ReplacingMergeTree semantics.
```

---

## 16. Rate Limiting Design

### Token Bucket Algorithm (Redis Lua)

```lua
-- lua/token_bucket.lua
local key        = KEYS[1]          -- "rl:{api_key}"
local capacity   = tonumber(ARGV[1]) -- max tokens (burst)
local rate       = tonumber(ARGV[2]) -- tokens per second (rps)
local now        = tonumber(ARGV[3]) -- current time in ms
local requested  = tonumber(ARGV[4]) -- tokens requested (= 1 per HTTP request)

local data = redis.call('HMGET', key, 'tokens', 'last_refill_ms')
local tokens       = tonumber(data[1]) or capacity
local last_refill  = tonumber(data[2]) or now

-- Refill tokens based on elapsed time
local elapsed_ms  = math.max(0, now - last_refill)
local refill      = elapsed_ms * rate / 1000
tokens = math.min(capacity, tokens + refill)

if tokens >= requested then
    -- Allow: consume tokens
    tokens = tokens - requested
    redis.call('HMSET', key, 'tokens', tokens, 'last_refill_ms', now)
    redis.call('EXPIRE', key, 3600)  -- reset expiry
    return {1, math.floor(tokens), capacity}  -- allowed, remaining, capacity
else
    -- Reject: not enough tokens
    redis.call('HMSET', key, 'tokens', tokens, 'last_refill_ms', now)
    redis.call('EXPIRE', key, 3600)
    local retry_after = math.ceil((requested - tokens) / rate)
    return {0, 0, capacity, retry_after}  -- denied, remaining=0, retry_after_sec
end
```

Rate limit configuration per app (from `apps` table):
- Free tier: 100 RPS, burst 200
- Growth tier: 1,000 RPS, burst 3,000
- Enterprise: 10,000 RPS, burst 30,000

---

## 17. Caching Design

### Four-Tier Cache Flow (with Stale-While-Revalidate)

```go
// internal/querying/service.go — simplified view
func (s *Service) query(ctx context.Context, req QueryRequest) (Result, error) {
    key := cacheKey(req)

    // L1: In-process cache (60s TTL, stale-while-revalidate)
    // SWR window: entries aged 45–60s are returned stale while an async
    // background goroutine refreshes from L2/L3/L4.
    if val, state := s.l1.Get(key); state == cache.Hit {
        metrics.L1CacheHits.Inc()
        return val, nil
    } else if state == cache.Stale {
        metrics.L1CacheStales.Inc()
        go s.refresh(key, req)  // async revalidation — caller gets stale response now
        return val, nil
    }

    // Single-flight: coalesce concurrent identical queries
    result, err, _ := s.sf.Do(key, func() (any, error) {
        // L2: Redis distributed cache (5-min TTL, shared across pods)
        if val, err := s.redis.Get(ctx, key); err == nil {
            s.l1.Set(key, val)
            metrics.L2CacheHits.Inc()
            return val, nil
        }

        // L3/L4: ClickHouse (wrapped in circuit breaker)
        result, err := s.chBreaker.Call(func() (any, error) {
            return s.chQuery(ctx, req)  // hits MV first, falls back to raw table
        })
        if err != nil {
            return nil, err
        }

        // Populate L2 and L1
        s.redis.SetEX(ctx, key, result, 5*time.Minute)
        s.l1.Set(key, result)
        return result, nil
    })
    return result.(Result), err
}
```

**Tier summary:**

| Tier | Store | TTL | Scope |
|------|-------|-----|-------|
| L1 | In-process `L1Cache` | 60s (SWR at 45s) | Per pod |
| L2 | Redis cluster | 5 min | Shared across all Query API pods |
| L3 | ClickHouse Materialized Views | — | Pre-aggregated hourly/daily rollups |
| L4 | ClickHouse raw `events` table | — | Full scan, worst case |

### Cache Invalidation

- **TTL-based only**: 5-minute Redis TTL, no explicit invalidation.
- **Why:** Analytics dashboards tolerate 5-minute staleness. Event-driven invalidation (invalidate on new event insert) would require tracking which cache keys are affected by which app/event combinations — too complex.
- **User expectation:** Dashboard shows "Last updated: N minutes ago" timestamp.

---

## 18. Retry & Circuit Breaker Design

### Exponential Backoff with Jitter

```go
type RetryConfig struct {
    MaxAttempts int
    BaseDelay   time.Duration
    MaxDelay    time.Duration
    JitterPct   float64  // e.g., 0.2 for ±20% jitter
}

func retry(cfg RetryConfig, fn func() error) error {
    var err error
    delay := cfg.BaseDelay
    for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
        if attempt > 0 {
            jitter := time.Duration(float64(delay) * cfg.JitterPct * (2*rand.Float64() - 1))
            time.Sleep(delay + jitter)
            delay = min(delay*2, cfg.MaxDelay)
        }
        err = fn()
        if err == nil {
            return nil
        }
        if isUnrecoverable(err) {
            return err  // no retry for schema errors, auth errors
        }
    }
    return err
}

// Default configs per service:
var (
    KafkaPublishRetry = RetryConfig{MaxAttempts: 10, BaseDelay: 100*time.Millisecond, MaxDelay: 5*time.Second, JitterPct: 0.2}
    ClickHouseRetry   = RetryConfig{MaxAttempts: 5, BaseDelay: 1*time.Second, MaxDelay: 30*time.Second, JitterPct: 0.2}
    WebhookRetry      = RetryConfig{MaxAttempts: 3, BaseDelay: 30*time.Second, MaxDelay: 300*time.Second, JitterPct: 0.1}
    PostgresRetry     = RetryConfig{MaxAttempts: 3, BaseDelay: 500*time.Millisecond, MaxDelay: 5*time.Second, JitterPct: 0.2}
)
```

### Circuit Breaker States

```
States: CLOSED → OPEN → HALF_OPEN → CLOSED

CLOSED (normal):
  - All requests pass through
  - Count consecutive failures
  - Threshold: 5 failures → → OPEN

OPEN (fast-fail):
  - All requests immediately return error (no downstream call)
  - Timer: 60 seconds → → HALF_OPEN
  - Key in Redis: circuit:{service}:{target} = "open" EX 60

HALF_OPEN (probe):
  - Allow 1 request through
  - If success: → CLOSED (reset counter)
  - If failure: → OPEN (reset 60s timer)

Implementation: per CH-Writer pod, per ClickHouse shard
  On circuit open: CH-Writer pauses consuming from Kafka (backpressure)
    → events stay in Kafka (48h retention buffer)
  ClickHouse recovery → circuit half-opens → test write → success → resume
```

---

## 19. Observability Design — LGTM Stack

The observability stack was upgraded from a Prometheus + Jaeger setup to the full **Grafana LGTM** (Loki + Grafana + Tempo + Mimir) stack with OpenTelemetry as the unified collection layer. All three signal types — traces, metrics, and logs — flow through a single OTel Collector pipeline.

### 19.1 OpenTelemetry Collector Pipeline

```yaml
# Receivers
receivers:
  otlp:                          # All services push OTLP gRPC to :4317
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
      http: { endpoint: 0.0.0.0:4318 }
  filelog:                       # Collect structured JSON logs from pod stdout
    include: [/var/log/pods/pulse_*/*/*.log]
    operators:
      - type: json_parser        # Parse JSON log lines
      - type: regex_parser       # Extract k8s metadata from file path
        regex: '^.*/(?P<namespace>[^_]+)_(?P<pod_name>[^_]+)_...'
  prometheus:                    # Scrape OTel Collector's own metrics
    config:
      scrape_configs:
        - job_name: otel-self
          static_configs: [{ targets: [localhost:8888] }]

# Processors (applied in order)
processors:
  memory_limiter:                # Hard cap at 400 MiB — prevents OOM
    limit_mib: 400
    check_interval: 1s
  k8sattributes:                 # Enrich with pod/namespace/node metadata
    auth_type: serviceAccount
    extract:
      metadata: [k8s.pod.name, k8s.namespace.name, k8s.deployment.name, k8s.node.name]
      labels:
        - { tag_name: app, key: app, from: pod }
  resource:                      # Add environment tags
    attributes:
      - { action: insert, key: service.environment, value: minikube }
  batch:                         # Batch for efficiency
    timeout: 5s
    send_batch_size: 512
  tail_sampling:                 # Keep 100% errors, 100% slow (>200ms), 10% rest
    decision_wait: 10s
    policies:
      - { name: errors, type: status_code, status_code: { status_codes: [ERROR] } }
      - { name: slow,   type: latency,     latency: { threshold_ms: 200 } }
      - { name: sample, type: probabilistic, probabilistic: { sampling_percentage: 10 } }

# Exporters
exporters:
  otlp/tempo:                    # Traces → Tempo
    endpoint: tempo.monitoring.svc.cluster.local:4317
    tls: { insecure: true }
  prometheusremotewrite:         # Metrics → Mimir
    endpoint: http://mimir.monitoring.svc.cluster.local:9009/api/v1/push
    headers: { X-Scope-OrgID: pulse }
  prometheus:                    # Metrics scrape endpoint (Prometheus pulls :8889)
    endpoint: 0.0.0.0:8889
  loki:                          # Logs → Loki
    endpoint: http://loki.monitoring.svc.cluster.local:3100/loki/api/v1/push
    labels:
      resource:
        service.name: service_name
        k8s.namespace.name: namespace
        k8s.pod.name: pod

# Pipelines
service:
  pipelines:
    traces:  { receivers: [otlp],              processors: [memory_limiter, k8sattributes, resource, batch, tail_sampling], exporters: [otlp/tempo] }
    metrics: { receivers: [otlp, prometheus],  processors: [memory_limiter, k8sattributes, resource, batch],               exporters: [prometheusremotewrite, prometheus] }
    logs:    { receivers: [filelog],           processors: [memory_limiter, k8sattributes, resource, batch],               exporters: [loki] }
```

### 19.2 Metrics (Prometheus / Mimir)

All services expose `/metrics` on port `9091`. Prometheus scrapes them every 15s and remote_writes to Mimir for long-term storage.

| Metric Name | Type | Labels | Description |
|-------------|------|--------|-------------|
| `pulse_ingest_events_total` | Counter | `app_id`, `status` | Events accepted/rejected at gateway |
| `pulse_ingest_errors_total` | Counter | `app_id` | Ingest errors |
| `pulse_ingest_latency_seconds` | Histogram | `status_code` | Gateway HTTP latency |
| `pulse_ingest_duplicates_filtered_total` | Counter | `app_id`, `stage` | Dedup hits (bloom/redis) |
| `pulse_ingest_batch_size` | Histogram | — | Events per batch |
| `pulse_kafka_publish_duration_seconds` | Histogram | `topic` | Kafka produce latency |
| `pulse_consumer_lag` | Gauge | `topic`, `group`, `partition` | Consumer group lag |
| `pulse_clickhouse_insert_duration_seconds` | Histogram | `table` | CH insert latency |
| `pulse_clickhouse_inserted_total` | Counter | `table` | Rows inserted to ClickHouse |
| `pulse_clickhouse_query_latency_seconds` | Histogram | `query_type`, `cache_level` | Query API latency |
| `pulse_query_requests_total` | Counter | `query_type`, `status` | Query API request count |
| `pulse_redis_cache_hits_total` | Counter | `level` | Cache hits (l1/l2) |
| `pulse_redis_cache_misses_total` | Counter | `level` | Cache misses |
| `pulse_redis_operation_duration_seconds` | Histogram | `operation` | Redis latency |
| `pulse_sessions_opened_total` | Counter | `app_id` | New sessions started |
| `pulse_session_duration_seconds` | Histogram | `app_id` | Session duration distribution |
| `pulse_alert_fired_total` | Counter | `app_id`, `rule_id` | Alert fires |
| `pulse_circuit_breaker_state` | Gauge | `service`, `target` | 0=closed, 1=open |

**Prometheus alert rules** (defined in `deployments/k8s/monitoring/prometheus-rules.yaml`):
- `HighIngestErrorRate`: error rate > 1% for 2 min → warning
- `HighIngestLatency`: P95 > 200ms for 5 min → warning
- `KafkaConsumerLagHigh`: lag > 1M messages for 5 min → critical
- `ClickHouseInsertLatencyHigh`: P95 insert > 5s for 5 min → warning

### 19.3 Distributed Tracing (Tempo)

All services initialise the OTel SDK in `main()` via `tracing.Init()`:

```go
// internal/tracing/otel.go
func Init(ctx context.Context, cfg *config.TelemetryConfig, log *zap.Logger) (*sdktrace.TracerProvider, error) {
    exporter, err := otlptracegrpc.New(ctx,
        otlptracegrpc.WithEndpoint(cfg.Endpoint),   // otel-collector:4317
        otlptracegrpc.WithInsecure(),
    )
    sampler := sdktrace.ParentBased(
        sdktrace.TraceIDRatioBased(cfg.SampleRate),  // 1.0 in dev, 0.01 in prod
    )
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithSampler(sampler),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(cfg.ServiceName),
            attribute.String("deployment.environment", cfg.Environment),
        )),
    )
    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))
    return tp, nil
}
```

**Spans created per service:**

```
Gateway:
  gateway.handle_request          (root span, HTTP method + path)
  ├── auth.validate_api_key
  ├── ratelimit.check
  ├── dedup.bloom_check
  ├── dedup.redis_check
  ├── geoip.lookup
  ├── kafka.publish                (raw-events)
  └── mongo.archive                (async)

Enricher:
  enricher.process_event
  ├── ua.parse
  ├── geoip.lookup
  ├── session.assign               (Redis HGETALL + HMSET)
  └── kafka.publish                (enriched-events)

Query API:
  queryapi.handle_request
  ├── cache.l1_lookup
  ├── cache.l2_lookup              (Redis GET)
  ├── clickhouse.query             (with query_type attribute)
  └── cache.l2_store               (Redis SETEX)
```

**Trace-to-log correlation:** Every log line includes `trace_id` and `span_id` fields (injected by the OTel SDK). Grafana Loki datasource is configured with a derived field that extracts `trace_id` and links to Tempo.

**Trace-to-metric correlation:** Tempo's metrics generator produces `traces_spanmetrics_*` and `traces_service_graph_*` metrics, remote-written to Mimir. These power the service map and RED metrics in Grafana.

### 19.4 Log Collection (Loki)

Logs flow: pod stdout (JSON) → filelog receiver in OTel Collector → Loki exporter → Loki.

**Log format** (all services use `go.uber.org/zap` in JSON mode):
```json
{
  "level":      "info",
  "ts":         "2026-05-01T10:00:00.042Z",
  "caller":     "gateway/handler.go:142",
  "msg":        "batch ingested",
  "service":    "gateway",
  "request_id": "req_7f3a...",
  "app_id":     "app_abc123",
  "accepted":   48,
  "rejected":   2,
  "latency_ms": 23.4,
  "trace_id":   "abc123def456...",
  "span_id":    "7890abcd..."
}
```

**Loki label scheme:**
```
{namespace="pulse", pod="gateway-xxx", container="gateway", service_name="gateway"}
```

**LogQL query examples:**
```logql
# All gateway errors in last 5 min
{namespace="pulse", container="gateway"} | json | level="error"

# Slow requests (>200ms)
{namespace="pulse", container="gateway"} | json | latency_ms > 200

# Trace correlation: find logs for a specific trace
{namespace="pulse"} | json | trace_id="abc123def456"
```

### 19.5 Grafana Datasource Configuration

```yaml
# Auto-provisioned via ConfigMap grafana-datasources
datasources:
  - name: Mimir          # Primary metrics (default)
    type: prometheus
    url: http://mimir.monitoring.svc.cluster.local:9009/prometheus
    headers: { X-Scope-OrgID: pulse }

  - name: Prometheus     # Direct scrape fallback
    type: prometheus
    url: http://prometheus.monitoring.svc.cluster.local:9090

  - name: Loki           # Logs
    type: loki
    url: http://loki.monitoring.svc.cluster.local:3100
    derivedFields:
      - matcherRegex: '"trace_id":"(\w+)"'
        name: TraceID
        url: "${__value.raw}"
        datasourceUid: tempo

  - name: Tempo          # Traces
    type: tempo
    url: http://tempo.monitoring.svc.cluster.local:3200
    tracesToLogsV2:
      datasourceUid: loki
      query: '{namespace="${__span.tags.namespace}", pod="${__span.tags.pod}"} |= "${__trace.traceId}"'
    tracesToMetrics:
      datasourceUid: mimir
    serviceMap:
      datasourceUid: mimir
    nodeGraph:
      enabled: true
```

### 19.6 Health Check Endpoints

```
GET /health  → 200 OK {"status":"ok"}
GET /ready   → 200 OK {"status":"ready", "checks": {...}}
            OR 503    {"status":"not_ready", "checks": {"kafka": "disconnected"}}

OTel Collector health:
  GET http://otel-collector:13133/  → 200 OK (health_check extension)
  GET http://otel-collector:55679/  → zpages debug UI

Grafana health:
  GET http://grafana:3000/api/health → {"commit":"...","database":"ok","version":"11.1.0"}
```

---

## 20. Missing Data & Edge Cases

### 20.1 Missing user_id

**Scenario:** SDK sends event without user_id (anonymous pre-login user).

**Handling:**
- Gateway accepts event. user_id is nullable.
- device_id is mandatory and used as session key.
- On identify call (POST /v1/identify): user_id is linked to device_id.
- Retroactive user association: NOT implemented (anonymous events before login remain unlinked).
- **Implication:** DAU counts device_id, not user_id, if user_id is absent. Could double-count users who use multiple devices.
- **Mitigation:** Clients should call identify() as early as possible. SDK alias() method (not yet built) would support retroactive linking.

### 20.2 Clock Skew > 5 Minutes

**Scenario:** Mobile device has wrong system clock.

**Handling:**
- Enricher detects: `abs(server_time - client_event_time) > 5 min`
- Replaces event_time with server_time for analytics.
- Records `client_offset_ms` for diagnostics.
- Events older than 7 days or in the future by > 24h are rejected at Gateway (validation step).

### 20.3 Kafka Broker Unavailability

**Scenario:** Kafka MSK broker restarts or network partition.

**Handling:**
- Gateway: async producer buffers up to 30 seconds of events in memory.
- If buffer full: Gateway returns HTTP 503 to SDK.
- SDK retries with exponential backoff (built into Go and JS SDKs).
- On broker recovery: buffered events are published automatically.
- MongoDB archive is the last resort — events already archived before Kafka publish can be replayed from MongoDB.

### 20.4 ClickHouse Node Failure

**Scenario:** One ClickHouse replica goes down.

**Handling:**
- ClickHouse: writes auto-route to surviving replica within shard.
- Reads: Query API connects to healthy nodes. ClickHouse Distributed table engine handles routing.
- CH-Writer: detects connection error, circuit breaker opens.
- Events buffer in Kafka (48h retention).
- On node recovery: replica syncs from ZooKeeper and catches up via replication.
- **No data loss** assuming at least one replica per shard is alive.

### 20.5 Enricher Worker Panic

**Scenario:** Malformed event causes panic in enrichment goroutine.

**Handling:**
```go
func (w *worker) process(event RawEvent) {
    defer func() {
        if r := recover(); r != nil {
            log.Error("enricher panic", zap.Any("error", r))
            metrics.PanicTotal.Inc()
            // Route to DLQ instead of crashing entire worker pool
            publishToDLQ(event, fmt.Sprintf("panic: %v", r))
        }
    }()
    // ... enrichment logic
}
```

### 20.6 Redis Unavailability

**Scenario:** Redis cluster experiences network partition.

**Impact per feature:**
| Feature | Degraded Behavior |
|---------|------------------|
| Rate limiting | Fall back to in-process token bucket (less precise, per-pod basis) |
| API key cache | Fall back to PostgreSQL lookup on every request (higher latency, but correct) |
| Deduplication | Stage 1 (Bloom) still works; Stage 2 (Redis SET) unavailable → some duplicates may pass through to ClickHouse (Stage 3 catches at query time via FINAL) |
| Session tracking | New sessions start on every event (sessions cannot be continued) → session metrics degraded but event ingestion continues |
| Query cache | Every query hits ClickHouse directly → higher ClickHouse load but correct results |

### 20.7 Event Property Size

**Scenario:** SDK sends event with 10 MB props map.

**Handling:**
- Gateway body size limit: 5 MB per batch (HTTP 413 if exceeded).
- Per-event props validation: max 50 keys, max 256 chars per key, max 1024 chars per value.
- Excess keys are truncated with a warning logged.
- `props` in ClickHouse is Map(String, String) — key and value are strings, ensuring type safety.

### 20.8 Missing GeoIP Data

**Scenario:** IP address is private (10.x.x.x, 192.168.x.x) or MaxMind DB doesn't recognize it.

**Handling:**
- All geo fields are Nullable in ClickHouse.
- MaxMind returns error for private IPs → set country_code = "", city = "" (not null).
- `LowCardinality(String)` handles empty string efficiently.
- Dashboard shows "Unknown" for unknown geos.

### 20.9 Funnel Step Order Violations

**Scenario:** User completes step 2 before step 1 (out-of-order events due to network delay).

**Handling:**
- Funnel Processor checks Redis state: step N-1 must be recorded before step N is accepted.
- Out-of-order event for step N is buffered in Redis for 60 seconds (short window) to handle minor ordering issues.
- After 60 seconds, if step N-1 still not seen, step N is discarded.
- **Implication:** Network delays > 60 seconds can cause missed funnel steps. Acceptable edge case.

### 20.10 Alert Engine Restart During Cooldown

**Scenario:** Alert Engine pod restarts. Does it double-fire alerts?

**Handling:**
- Cooldown state is in Redis (not in-process), so pod restart doesn't lose cooldown.
- On restart, Alert Engine reloads rules from PostgreSQL and checks Redis cooldown before firing.
- Result: no double-fire after restart.

---

## 21. Sharding & Replication — Detailed Design

### 21.1 PostgreSQL — Read/Write Splitting

**Package:** `internal/postgres`

**Struct layout:**

```go
type Client struct {
    write    *pgxpool.Pool   // primary — all mutations
    reads    []*pgxpool.Pool // replicas — SELECT-only, round-robin
    rrCursor atomic.Uint64   // monotonically increasing; idx = rrCursor % len(reads)
    log      *zap.Logger
}
```

**Startup behaviour:**

1. Connect to primary DSN (`postgres.dsn`). Fatal on failure.
2. For each DSN in `postgres.replicadsns`: connect, ping. On error → log warning + skip (degraded mode, not fatal).
3. Log replica count. If zero → all traffic goes to primary (single-pool mode, backward-compatible).

**Read-pool selection algorithm:**

```
func (c *Client) read() *pgxpool.Pool {
    if len(c.reads) == 0 { return c.write }
    idx = atomic.Add(&rrCursor, 1) % len(c.reads)
    return c.reads[idx]
}
```

- Atomic increment ensures lock-free, thread-safe round-robin.
- No weighted distribution; all replicas assumed equal capacity.

**Method routing table:**

| Method | Pool | Reason |
| --- | --- | --- |
| `GetAppByAPIKey`, `GetApp`, `ListApps` | `read()` | Pure SELECT |
| `ListFunnels`, `ListAlertRules`, `ListCohorts` | `read()` | Pure SELECT |
| `ListExperiments`, `ListOrgs` | `read()` | Pure SELECT |
| `GetCampaign`, `GetActiveCampaignsByTrigger` | `read()` | Pure SELECT |
| `GetCampaignStats` | `read()` | Aggregate SELECT |
| `CreateApp`, `UpdateApp`, `DeactivateApp` | `write` | Mutation |
| `CreateOrgAndApp`, `RotateAPIKey` | `write` (tx) | Transaction |
| `UpsertFunnel`, `CreateAlertRule`, `UpdateAlertRule` | `write` | Mutation |
| `DeleteAlertRule`, `CreateCohort`, `DeleteCohort` | `write` | Mutation |
| `CreateExperiment`, `UpdateExperiment`, `DeleteExperiment` | `write` | Mutation |
| `CreateOrg`, `UpdateOrg` | `write` | Mutation |
| `CreateCampaign`, `UpdateCampaign`, `SetCampaignActive` | `write` | Mutation |

**Replica lag:** Async streaming replication means replicas can be 10–100ms behind the primary. Read-your-writes consistency is not guaranteed for the same request; callers that immediately read after a write should either use `write` pool directly or accept eventual consistency.

**Failover:** If a replica pool returns errors, the application returns the error to the caller. No automatic failover to primary on read error (intentional: masks replica failures). Kubernetes liveness probes ensure bad replicas are restarted.

---

### 21.2 ClickHouse — Application-Level Sharding

**Packages:** `internal/clickhouse` (Pool, ShardedWriter)

#### Pool — Connection Management

```go
type Pool struct {
    shards  []*Client       // one connection per shard write host
    readers []*Client       // read-replica connections (ReadHosts config)
    rrRead  atomic.Uint64
}
```

**Startup:** `NewPool` iterates `ShardHosts`; fatal if any shard fails to connect. `ReadHosts` failures are non-fatal (replica skipped with warning). Falls back to `Hosts[0]` when `ShardHosts` is empty.

#### Shard-routing algorithm

```text
func ShardFor(appID string) *Client:
    h = FNV-1a(appID)          // 64-bit non-crypto hash, ~2 ns/op
    return shards[ h % len(shards) ]
```

**Why FNV-1a over sipHash64:**

- Available in Go stdlib (`hash/fnv`) — no external dependency.
- Sufficient distribution uniformity for O(1000) apps per shard.
- sipHash64 provides better DoS resistance (secret key), irrelevant for internal routing.

**Shard affinity:** Every event for a given `app_id` always goes to the same shard. This means ClickHouse's `ORDER BY (app_id, event_time)` within each shard sees fully sorted data for that app, enabling maximum merge efficiency and compressed storage.

#### ShardedWriter — Write Fan-Out

```go
type ShardedWriter struct {
    writers []*Writer   // one Writer (write-behind channel) per shard
}
```

**Write path for a batch of events:**

```text
ShardedWriter.Write(events):
    if single shard → delegate directly (zero overhead)
    else:
        for each event e:
            idx = FNV-1a(e.AppID) % numShards
            buckets[idx] = append(buckets[idx], e)
        for each shard bucket:
            writers[idx].Write(bucket)   // non-blocking channel send
```

Each per-shard `Writer` has its own independent:

- Write-behind channel (500K events)
- Flush ticker (1s interval)
- Backpressure thresholds (soft 80%, hard 100%)
- Prometheus metrics (pending, dropped)

This means a slow shard (e.g. CH node under disk pressure) only affects events for apps routed to that shard, not the entire write path.

#### Read routing

```text
Pool.ReadConn():
    if no readers configured → return shards[0]
    idx = atomic.Add(&rrRead, 1) % len(readers)
    return readers[idx]
```

The Query API uses `Pool.ReadConn()` for all SELECT queries, keeping read traffic off the write shard connections.

---

### 21.3 MongoDB — Read Preference Routing

**Package:** `internal/mongo`

**Struct layout:**

```go
type Client struct {
    client *mongo.Client
    db     *mongo.Database   // primary read pref — writes
    readDB *mongo.Database   // configured read pref — reads
}
```

Both `db` and `readDB` share the same underlying `*mongo.Client` (single connection pool). The difference is the `ReadPreference` option set on each database handle.

**Read preference parsing:**

| Config value | `readpref` result | Use case |
| --- | --- | --- |
| `"primary"` | `readpref.Primary()` | Strongest consistency |
| `"primaryPreferred"` | `readpref.PrimaryPreferred()` | Primary if available, secondary as fallback |
| `"secondary"` | `readpref.Secondary()` | Always read from secondary |
| `"secondaryPreferred"` | `readpref.SecondaryPreferred()` | **Default** — secondary load spreading |
| `"nearest"` | `readpref.Nearest()` | Lowest latency node |

**Method routing:**

| Method | Handle | Reason |
| --- | --- | --- |
| `InsertRawBatch` | `db` (primary) | Write — must go to primary |
| `UpsertUserProfile` | `db` (primary) | Write — upsert |
| `EnsureIndexes` | `db` (primary) | DDL — must go to primary |
| `GetRawEvents` | `readDB` | Read — time-range scan |
| `GetUserProfile` | `readDB` | Read — point lookup |

**Replica set config:**

```yaml
mongo:
  uri: "mongodb://host1:27017,host2:27017,host3:27017"
  database: "pulse"
  read_preference: "secondaryPreferred"
  replica_set: "rs0"
```

The driver discovers all members from the seed list and routes according to the read preference. The `replica_set` field prevents accidentally connecting to a standalone node when a replica set is expected.

---

### 21.4 Redis — Cluster Mode

**Package:** `internal/redis`

**Client strategy:** `redis.UniversalClient` interface — tries `ClusterClient` first; on failure closes the cluster client and falls back to `redis.Client` (single-node). This enables local development with a single Redis instance while production runs a 6-node cluster.

**Cluster configuration:**

```yaml
redis:
  addrs: ["redis-0:6379", "redis-1:6379", "redis-2:6379",
          "redis-3:6379", "redis-4:6379", "redis-5:6379"]
  pool_size: 50
  max_retries: 3
```

Redis Cluster handles hash-slot routing internally; the driver (`go-redis/v9`) automatically routes commands to the correct shard based on the key's CRC16 hash slot. `RouteByLatency: true` picks the lowest-latency node when multiple replicas serve the same slot.

---

### 21.5 Config Reference for Sharding/Replication

```yaml
postgres:
  dsn: "postgres://pulse:pass@primary:5432/pulse"
  replica_dsns:
    - "postgres://pulse:pass@replica-1:5432/pulse"
    - "postgres://pulse:pass@replica-2:5432/pulse"
  max_open_conn: 25
  max_idle_conn: 5
  max_lifetime: 30m

clickhouse:
  hosts: ["ch-shard0:9000"]
  shard_hosts:
    - "ch-shard0:9000"
    - "ch-shard1:9000"
    - "ch-shard2:9000"
  read_hosts:
    - "ch-replica0:9000"
    - "ch-replica1:9000"
  database: "pulse"
  username: "pulse"

mongo:
  uri: "mongodb://mongo-0:27017,mongo-1:27017,mongo-2:27017"
  database: "pulse"
  read_preference: "secondaryPreferred"
  replica_set: "rs0"

redis:
  addrs:
    - "redis-0:6379"
    - "redis-1:6379"
    - "redis-2:6379"
  pool_size: 50
  max_retries: 3
```

---

*Document version: 1.2 | Architecture owner: Platform Engineering | Last updated: 2026-05-01*

---

## 22. Minikube Deployment — Detailed Design

### 22.1 File Structure

```
deployments/minikube/
├── namespace.yaml          # pulse + monitoring namespaces
├── secrets.yaml            # dev-safe plaintext secrets for all services
├── configmap.yaml          # all 9 service configs (in-cluster DNS names)
├── ingress.yaml            # nginx ingress: pulse.local, api.pulse.local, grafana.pulse.local
├── deploy.sh               # one-shot bash deploy script
│
├── infra/
│   ├── kafka.yaml          # Strimzi single-broker + 6 KafkaTopic resources
│   ├── redis.yaml          # Redis 7.2 standalone + redis_exporter sidecar
│   ├── clickhouse.yaml     # ClickHouse 24.3 + Prometheus metrics endpoint
│   ├── postgres.yaml       # Postgres 16 single pod
│   └── mongo.yaml          # MongoDB 7.0 single pod
│
├── monitoring/
│   ├── otel-collector.yaml # OTel Collector with full LGTM pipeline + RBAC
│   ├── loki.yaml           # Grafana Loki 3.1 (single-binary, filesystem)
│   ├── tempo.yaml          # Grafana Tempo 2.5 (single-binary, emptyDir)
│   ├── mimir.yaml          # Grafana Mimir 2.13 (single-binary, emptyDir)
│   ├── prometheus.yaml     # Prometheus 2.53 (scrapes all services, remote_write to Mimir)
│   ├── grafana.yaml        # Grafana 11.1 (all 4 datasources pre-wired)
│   └── grafana-dashboards.yaml  # 6 pre-built dashboards as ConfigMap
│
├── services/
│   ├── gateway.yaml        # 1 replica, imagePullPolicy: Never, NodePort
│   ├── enricher.yaml       # 1 replica, imagePullPolicy: Never
│   ├── session.yaml        # 1 replica
│   ├── funnel.yaml         # 1 replica
│   ├── chwriter.yaml       # 1 replica
│   ├── query-api.yaml      # 1 replica, NodePort
│   ├── alertengine.yaml    # 1 replica
│   ├── auth-service.yaml   # 1 replica, NodePort
│   └── notification-service.yaml  # 1 replica
│
└── loadtest/
    └── locust.yaml         # Locust master (1 pod) + workers (2 pods), NodePort UI
```

### 22.2 Image Build Strategy

Images are built directly into Minikube's Docker daemon using `eval $(minikube docker-env)`. This avoids the need for a container registry:

```bash
eval $(minikube docker-env -p minikube)
docker build -t pulse-gateway:dev -f deployments/docker/gateway.Dockerfile .
```

All service deployments use `imagePullPolicy: Never` so Kubernetes uses the locally built image without attempting a registry pull.

### 22.3 Service Discovery

All services communicate via Kubernetes ClusterIP DNS:

| Service | DNS Name | Port |
|---------|----------|------|
| Kafka bootstrap | `pulse-kafka-kafka-bootstrap.pulse.svc.cluster.local` | 9092 |
| Redis | `redis-master.pulse.svc.cluster.local` | 6379 |
| ClickHouse | `clickhouse.pulse.svc.cluster.local` | 9000 |
| Postgres | `postgres.pulse.svc.cluster.local` | 5432 |
| MongoDB | `mongo.pulse.svc.cluster.local` | 27017 |
| OTel Collector | `otel-collector.monitoring.svc.cluster.local` | 4317 |
| Loki | `loki.monitoring.svc.cluster.local` | 3100 |
| Tempo | `tempo.monitoring.svc.cluster.local` | 3200 |
| Mimir | `mimir.monitoring.svc.cluster.local` | 9009 |
| Grafana | `grafana.monitoring.svc.cluster.local` | 3000 |

### 22.4 ConfigMap Design

A single `pulse-configs` ConfigMap in the `pulse` namespace contains YAML config files for all 9 services. Each service mounts it at `/app/configs` and reads its own file via `CONFIG_PATH` env var:

```yaml
# In each service deployment:
env:
  - name: CONFIG_PATH
    value: /app/configs/gateway.yaml   # or enricher.yaml, queryapi.yaml, etc.
volumeMounts:
  - name: configs
    mountPath: /app/configs
    readOnly: true
volumes:
  - name: configs
    configMap:
      name: pulse-configs
```

All configs point to in-cluster DNS names and use `telemetry.endpoint: otel-collector.monitoring.svc.cluster.local:4317` with `insecure: true`.

### 22.5 Kafka Setup (Strimzi)

Strimzi Operator is installed cluster-wide before deploying the Kafka resource:

```bash
kubectl apply -f "https://strimzi.io/install/latest?namespace=pulse"
```

The Minikube Kafka config uses:
- 1 broker (vs 3 in production)
- `replication.factor: 1` and `min.insync.replicas: 1` (vs 3/2 in production)
- `storage.type: ephemeral` (vs persistent-claim in production)
- Reduced resource limits (512Mi RAM vs 8Gi in production)

KafkaTopic resources are applied alongside the Kafka cluster and managed by Strimzi's Entity Operator.

### 22.6 Observability Stack Startup Order

The LGTM stack must start in this order to avoid connection errors:

```
1. Loki    (Tempo and OTel Collector depend on it)
2. Tempo   (OTel Collector depends on it)
3. Mimir   (Prometheus and OTel Collector depend on it)
4. OTel Collector  (services depend on it for trace export)
5. Prometheus      (depends on Mimir for remote_write)
6. Grafana         (depends on all datasources)
```

The `deploy.sh` script applies them in this order and waits for each rollout before proceeding.

### 22.7 Ingress Configuration

Requires the Minikube nginx ingress addon:

```bash
minikube addons enable ingress
```

Add to `/etc/hosts`:
```
$(minikube ip)  pulse.local api.pulse.local grafana.pulse.local
```

| Host | Backend Service | Port |
|------|----------------|------|
| `pulse.local` | `gateway` | 8080 |
| `api.pulse.local` | `query-api` | 8082 |
| `grafana.pulse.local` | `grafana` | 3000 |

---

## 23. Load Testing — Detailed Design

### 23.1 Architecture

Locust runs in distributed mode inside the `loadtest` namespace:

```
┌─────────────────────────────────────────────────────────────────┐
│  Namespace: loadtest                                             │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  locust-master (1 pod)                                   │   │
│  │  • Coordinates workers                                   │   │
│  │  • Serves web UI on :8089 (NodePort)                     │   │
│  │  • Aggregates stats from workers                         │   │
│  │  • Exposes Prometheus metrics on :9646                   │   │
│  └──────────────────────────────────────────────────────────┘   │
│                          │ :5557/:5558                           │
│         ┌────────────────┴────────────────┐                     │
│         ▼                                 ▼                     │
│  ┌─────────────┐                  ┌─────────────┐               │
│  │locust-worker│                  │locust-worker│               │
│  │  (pod 1)    │                  │  (pod 2)    │               │
│  └──────┬──────┘                  └──────┬──────┘               │
│         │                                │                       │
│         └──────────────┬─────────────────┘                      │
│                        │ HTTP requests                           │
│                        ▼                                         │
│         gateway.pulse.svc.cluster.local:8080                    │
└─────────────────────────────────────────────────────────────────┘
```

### 23.2 Locustfile Design (`loadtest/locustfile.py`)

The locustfile defines two `HttpUser` subclasses:

**`SDKUser`** (weight=80, wait_time=0.1–1.0s):
- Simulates mobile/web SDK clients sending event batches
- `on_start()`: picks a random `app_id` from pool of 5, sets `X-API-Key` header
- Task distribution: 70% small (1–10 events), 20% medium (25–100), 5% large (200–500), 5% health

**`DashboardUser`** (weight=20, wait_time=1.0–5.0s):
- Simulates analysts running analytics queries
- `on_start()`: picks a random `app_id`, sets `Authorization: Bearer dev-token`
- Task distribution: 40% event count, 30% active users, 20% retention, 10% health

**Shared test data:**
```python
APP_IDS  = ["app_0001", "app_0002", "app_0003", "app_0004", "app_0005"]
USER_IDS = [str(uuid.uuid4()) for _ in range(500)]  # pool of 500 user IDs
```

**Event generation** (`make_event()`):
- Generates realistic event payloads with random `event_type`, `platform`, `country`, `page`, `referrer`, `value`
- Each event gets a unique `event_id` (UUID) and current timestamp

### 23.3 Prometheus Metrics from Locust

Locust exposes metrics on `:9646/metrics` (scraped by Prometheus):

| Metric | Description |
|--------|-------------|
| `locust_requests_current_rps` | Current requests per second per endpoint |
| `locust_requests_current_fail_per_sec` | Current failures per second |
| `locust_requests_avg_response_time` | Average response time in ms |
| `locust_requests_num_failures` | Total failure count |
| `locust_users` | Current number of active users |

These are displayed in the **Load Test** Grafana dashboard alongside gateway metrics, enabling direct correlation between load parameters and system behaviour.

### 23.4 Test Execution

**Via Locust web UI (in-cluster):**
```bash
make minikube-locust
# Opens browser to Locust UI
# Set: Number of users = 50, Spawn rate = 5/s, Host = (pre-filled)
# Click "Start swarming"
```

**Headless (local):**
```bash
make loadtest-headless
# Runs: locust --headless --users=50 --spawn-rate=5 --run-time=5m
# Saves HTML report to loadtest/report.html
```

**Recommended progression:**
```
1. Smoke:    5 users,  1/s,  2 min  → verify all endpoints respond
2. Baseline: 20 users, 2/s,  5 min  → establish baseline metrics
3. Load:     50 users, 5/s,  10 min → normal expected load
4. Stress:   100 users,10/s, 10 min → find breaking point
5. Soak:     30 users, 3/s,  30 min → detect memory leaks / drift
```

### 23.5 Interpreting Results

During a load test, watch these Grafana panels:

| Panel | What to look for |
|-------|-----------------|
| Gateway Events/sec | Should scale linearly with Locust RPS |
| P95 Ingest Latency | Should stay < 50ms under normal load |
| Kafka Consumer Lag | Should stay near 0 (enricher keeping up) |
| Pod CPU (pulse ns) | Should not hit limits (would cause throttling) |
| Locust Failures/sec | Should be 0 unless rate limiting kicks in |
| Redis Memory | Should grow slowly (session state) then plateau |

A spike in Kafka consumer lag with stable gateway throughput indicates the enricher is the bottleneck — scale up enricher replicas.

---

*Document version: 1.2 | Architecture owner: Platform Engineering | Last updated: 2026-05-01*
