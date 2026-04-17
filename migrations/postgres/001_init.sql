-- ============================================================
-- PulseAnalytics — PostgreSQL Schema (metadata / OLTP)
-- Run via migrate or psql on the Aurora PostgreSQL instance.
-- ============================================================

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ─── Organizations ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orgs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    plan       TEXT        NOT NULL DEFAULT 'free'  CHECK (plan IN ('free', 'growth', 'enterprise')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Apps (Tenants) ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS apps (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    api_key    TEXT        NOT NULL UNIQUE,
    -- Rate limit settings per app
    rps        FLOAT       NOT NULL DEFAULT 10000,   -- events/second
    burst      INT         NOT NULL DEFAULT 50000,
    active     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS apps_api_key_idx ON apps(api_key) WHERE active = TRUE;
CREATE INDEX IF NOT EXISTS apps_org_id_idx  ON apps(org_id);

-- ─── Funnel Definitions ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS funnel_definitions (
    funnel_id      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id         UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    steps          TEXT[]      NOT NULL,            -- ordered event names
    window_seconds BIGINT      NOT NULL DEFAULT 604800,  -- 7 days
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS funnel_app_id_idx ON funnel_definitions(app_id);

-- ─── Cohort Definitions ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS cohort_definitions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    description TEXT,
    -- SQL WHERE clause evaluated against events table
    filter_sql  TEXT        NOT NULL,
    -- Recomputed on a schedule
    last_computed_at TIMESTAMPTZ,
    user_count  BIGINT      DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS cohort_app_id_idx ON cohort_definitions(app_id);

-- ─── Dashboard Configs ────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS dashboards (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id     UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    config     JSONB       NOT NULL DEFAULT '{}',  -- widget layout
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Alert Rules ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS alert_rules (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    metric_name TEXT        NOT NULL,
    condition   TEXT        NOT NULL CHECK (condition IN ('gt', 'lt', 'eq', 'gte', 'lte')),
    threshold   FLOAT       NOT NULL,
    window_mins INT         NOT NULL DEFAULT 60,
    channels    TEXT[]      NOT NULL DEFAULT '{}',
    webhook_url TEXT,
    email_to    TEXT[]      DEFAULT '{}',
    active      BOOLEAN     NOT NULL DEFAULT TRUE,
    last_fired_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS alert_rules_app_id_idx ON alert_rules(app_id) WHERE active = TRUE;

-- ─── Async Query Jobs ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS query_jobs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id     UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status     TEXT        NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    query_type TEXT        NOT NULL,
    params     JSONB       NOT NULL DEFAULT '{}',
    result_url TEXT,                               -- S3 pre-signed URL
    error_msg  TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS query_jobs_app_status ON query_jobs(app_id, status);

-- ─── A/B Test Definitions ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS experiments (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    description  TEXT,
    status       TEXT        NOT NULL DEFAULT 'draft'
                             CHECK (status IN ('draft', 'running', 'paused', 'completed')),
    variants     JSONB       NOT NULL DEFAULT '[]',  -- [{name, weight, config}]
    metric_goals JSONB       NOT NULL DEFAULT '[]',  -- primary/guardrail metrics
    started_at   TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── User Identity Map ────────────────────────────────────────────────────────
-- Maps anonymous device_ids to authenticated user_ids (identity resolution)
CREATE TABLE IF NOT EXISTS identity_map (
    app_id     UUID        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    device_id  TEXT        NOT NULL,
    user_id    TEXT        NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, device_id)
);

CREATE INDEX IF NOT EXISTS identity_user_idx ON identity_map(app_id, user_id);

-- ─── Automatic updated_at trigger ────────────────────────────────────────────
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER update_apps_updated_at
    BEFORE UPDATE ON apps
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE OR REPLACE TRIGGER update_funnel_updated_at
    BEFORE UPDATE ON funnel_definitions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE OR REPLACE TRIGGER update_dashboards_updated_at
    BEFORE UPDATE ON dashboards
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
