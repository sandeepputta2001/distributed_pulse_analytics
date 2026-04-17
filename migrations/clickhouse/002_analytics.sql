-- ─── ClickHouse Migration 002: Industry Analytics Tables ──────────────────────
-- Local dev version: uses plain MergeTree engines (no Replicated, no Distributed)
-- ──────────────────────────────────────────────────────────────────────────────

USE pulse;

-- ─── Experiment Assignments ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS experiment_assignments
(
    experiment_id   String,
    user_id         String,
    app_id          String,
    variant         String,
    assigned_at     DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree(assigned_at)
PARTITION BY toYYYYMM(assigned_at)
ORDER BY (experiment_id, user_id)
TTL assigned_at + INTERVAL 365 DAY DELETE
SETTINGS index_granularity = 8192;

-- ─── Revenue Hourly Aggregation ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS revenue_hourly
(
    app_id          String,
    hour            DateTime,
    total_revenue   AggregateFunction(sum,   Float64),
    tx_count        AggregateFunction(count, UInt64),
    unique_payors   AggregateFunction(uniq,  String)
)
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(hour)
ORDER BY (app_id, hour)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS revenue_hourly_mv
TO revenue_hourly
AS SELECT
    app_id,
    toStartOfHour(toDateTime(event_time)) AS hour,
    sumState(toFloat64OrZero(props['revenue'])) AS total_revenue,
    countState() AS tx_count,
    uniqState(user_id) AS unique_payors
FROM events
WHERE event_name = 'purchase'
GROUP BY app_id, hour;

-- ─── Attribution Daily Aggregation ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS attribution_daily
(
    app_id       String,
    day          Date,
    utm_source   String,
    utm_medium   String,
    utm_campaign String,
    users        AggregateFunction(uniq,  String),
    conversions  AggregateFunction(count, UInt64),
    revenue      AggregateFunction(sum,   Float64)
)
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(day)
ORDER BY (app_id, day, utm_source, utm_medium, utm_campaign)
TTL day + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS attribution_daily_mv
TO attribution_daily
AS SELECT
    app_id,
    toDate(event_time) AS day,
    props['utm_source']   AS utm_source,
    props['utm_medium']   AS utm_medium,
    props['utm_campaign'] AS utm_campaign,
    uniqState(user_id) AS users,
    countStateIf(event_name = 'purchase') AS conversions,
    sumStateIf(toFloat64OrZero(props['revenue']), event_name = 'purchase') AS revenue
FROM events
WHERE notEmpty(props['utm_source'])
GROUP BY app_id, day, utm_source, utm_medium, utm_campaign;

-- ─── User-Level Lifetime Stats ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_lifetime_stats
(
    app_id         String,
    user_id        String,
    first_seen     AggregateFunction(min,     DateTime64(3, 'UTC')),
    last_seen      AggregateFunction(max,     DateTime64(3, 'UTC')),
    total_events   AggregateFunction(count,   UInt64),
    total_sessions AggregateFunction(uniq,    String),
    total_revenue  AggregateFunction(sum,     Float64),
    last_country   AggregateFunction(anyLast, String),
    last_city      AggregateFunction(anyLast, String)
)
ENGINE = AggregatingMergeTree()
PARTITION BY app_id
ORDER BY (app_id, user_id)
SETTINGS index_granularity = 4096;

CREATE MATERIALIZED VIEW IF NOT EXISTS user_lifetime_stats_mv
TO user_lifetime_stats
AS SELECT
    app_id,
    user_id,
    minState(event_time)    AS first_seen,
    maxState(event_time)    AS last_seen,
    countState()            AS total_events,
    uniqState(session_id)   AS total_sessions,
    sumStateIf(toFloat64OrZero(props['revenue']), event_name = 'purchase') AS total_revenue,
    anyLastState(toString(country_code)) AS last_country,
    anyLastState(toString(city))         AS last_city
FROM events
GROUP BY app_id, user_id;

-- ─── Session Paths ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS session_paths
(
    app_id       String,
    session_id   String,
    user_id      String,
    path         Array(String),
    session_date Date,
    created_at   DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(session_date)
ORDER BY (app_id, session_id)
TTL session_date + INTERVAL 90 DAY DELETE
SETTINGS index_granularity = 8192;

-- ─── Real-Time Active Users Buffer ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS realtime_buffer
(
    app_id     String,
    user_id    String,
    event_name String,
    event_time DateTime
)
ENGINE = Buffer(currentDatabase(), events, 8, 1, 60, 100, 10000, 10000000, 100000000);
