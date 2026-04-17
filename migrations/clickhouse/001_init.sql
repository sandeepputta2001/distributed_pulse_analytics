-- ============================================================
-- PulseAnalytics — ClickHouse Schema (local dev)
-- Uses MergeTree (not Replicated) for single-node dev setup.
-- ============================================================

CREATE DATABASE IF NOT EXISTS pulse;

USE pulse;

-- ─── Core Events Table ────────────────────────────────────────────────────────
-- Primary analytical store. MergeTree partitioned by date + app_id.
-- ORDER BY optimized for the most common query patterns.
CREATE TABLE IF NOT EXISTS events
(
    app_id        LowCardinality(String),
    event_id      UUID                DEFAULT generateUUIDv4(),
    user_id       String,
    device_id     String,
    event_name    LowCardinality(String),
    event_time    DateTime64(3, 'UTC'),
    server_time   DateTime64(3, 'UTC') DEFAULT now64(3),
    session_id    String,
    country_code  LowCardinality(FixedString(2)),
    platform      LowCardinality(String),  -- android | ios | web | react_native | flutter | server
    app_version   LowCardinality(String),
    os_family     LowCardinality(String),
    browser       LowCardinality(String),
    city          LowCardinality(String),
    revenue       Nullable(Float64),
    props         Map(String, String),      -- flexible event properties
    campaign_id   LowCardinality(String),
    install_source LowCardinality(String),

    -- Skip index on event_name for filtered scans
    INDEX event_name_minmax event_name TYPE set(1000) GRANULARITY 1
)
ENGINE = MergeTree()
PARTITION BY (toYYYYMMDD(event_time), app_id)
ORDER BY (app_id, event_name, toStartOfHour(event_time), user_id)
TTL toDateTime(event_time) + INTERVAL 365 DAY DELETE
SETTINGS
    index_granularity = 8192,
    merge_with_ttl_timeout = 3600;

-- ─── DAU Materialized View ────────────────────────────────────────────────────
-- Pre-aggregates daily active users using HyperLogLog.
-- Queries against dau_mv run in <10ms instead of scanning events.
CREATE MATERIALIZED VIEW IF NOT EXISTS dau_mv
ENGINE = AggregatingMergeTree()
PARTITION BY toYYYYMM(event_date)
ORDER BY (app_id, event_date, platform)
AS
SELECT
    app_id,
    toDate(event_time)       AS event_date,
    platform,
    uniqState(user_id)       AS dau_state,
    sumState(revenue)        AS revenue_state,
    countState()             AS event_count_state
FROM events
WHERE event_name = 'app_opened'
GROUP BY app_id, event_date, platform;

-- Query DAU from MV:
-- SELECT app_id, event_date, uniqMerge(dau_state) AS dau
-- FROM dau_mv WHERE app_id = ? GROUP BY app_id, event_date ORDER BY event_date;

-- ─── Hourly Event Counts MV ───────────────────────────────────────────────────
CREATE MATERIALIZED VIEW IF NOT EXISTS hourly_counts_mv
ENGINE = SummingMergeTree(event_count)
PARTITION BY toYYYYMM(hour)
ORDER BY (app_id, event_name, hour)
AS
SELECT
    app_id,
    event_name,
    toStartOfHour(event_time) AS hour,
    count()                   AS event_count
FROM events
GROUP BY app_id, event_name, hour;

-- ─── Revenue MV ───────────────────────────────────────────────────────────────
CREATE MATERIALIZED VIEW IF NOT EXISTS revenue_mv
ENGINE = SummingMergeTree(total_revenue)
PARTITION BY toYYYYMM(event_date)
ORDER BY (app_id, event_date, platform)
AS
SELECT
    app_id,
    toDate(event_time) AS event_date,
    platform,
    sum(revenue)       AS total_revenue,
    count()            AS purchase_count,
    uniqHLL12(user_id) AS paying_users
FROM events
WHERE event_name = 'purchase_completed' AND revenue > 0
GROUP BY app_id, event_date, platform;

-- ─── Session Summary Table ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS session_summaries
(
    app_id      LowCardinality(String),
    session_id  String,
    user_id     String,
    device_id   String,
    started_at  DateTime64(3, 'UTC'),
    ended_at    DateTime64(3, 'UTC'),
    duration_s  Int32,
    event_count Int32,
    country_code LowCardinality(FixedString(2)),
    platform    LowCardinality(String),
    entry_screen LowCardinality(String),
    exit_screen  LowCardinality(String)
)
ENGINE = MergeTree()
PARTITION BY (toYYYYMMDD(started_at), app_id)
ORDER BY (app_id, toStartOfDay(started_at), user_id)
SETTINGS index_granularity = 8192;

-- ─── Funnel Conversions Table ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS funnel_conversions
(
    app_id          LowCardinality(String),
    funnel_id       LowCardinality(String),
    user_id         String,
    steps_complete  Int8,
    total_steps     Int8,
    converted       Bool,
    duration_ms     Int64,
    converted_at    DateTime64(3, 'UTC'),
    campaign_id     LowCardinality(String)
)
ENGINE = MergeTree()
PARTITION BY (toYYYYMM(converted_at), app_id)
ORDER BY (app_id, funnel_id, toStartOfDay(converted_at))
SETTINGS index_granularity = 8192;

-- ─── Retention Events Table ───────────────────────────────────────────────────
-- Pre-computed by Flink RetentionJob.
CREATE TABLE IF NOT EXISTS retention_events
(
    app_id       LowCardinality(String),
    user_id      String,
    install_date Date,
    return_date  Date,
    day_n        Int16  -- days between install and return
)
ENGINE = MergeTree()
PARTITION BY (toYYYYMM(install_date), app_id)
ORDER BY (app_id, install_date, day_n)
SETTINGS index_granularity = 8192;
