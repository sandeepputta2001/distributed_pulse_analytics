-- ─── Migration 002: Industry Analytics Tables ─────────────────────────────────
-- Adds: experiments, segments, campaigns, notification_deliveries
-- Depends on: 001_init.sql (orgs, apps, funnel_definitions)
-- ──────────────────────────────────────────────────────────────────────────────

BEGIN;

-- ─── A/B Experiments ──────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS experiments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'running', 'paused', 'concluded')),
    goal_event      TEXT NOT NULL,
    variants        JSONB NOT NULL DEFAULT '[]',
    start_at        TIMESTAMPTZ,
    end_at          TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_experiments_app_id ON experiments(app_id);
CREATE INDEX IF NOT EXISTS idx_experiments_status ON experiments(status);

CREATE TABLE IF NOT EXISTS experiment_user_assignments (
    experiment_id   UUID NOT NULL REFERENCES experiments(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    variant         TEXT NOT NULL,
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (experiment_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_exp_assignments_variant ON experiment_user_assignments(experiment_id, variant);

-- ─── User Segments ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS segments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    rules           JSONB NOT NULL DEFAULT '{}',
    cached_count    BIGINT,
    count_refreshed_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_segments_app_id ON segments(app_id);

-- ─── Campaigns ────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS campaigns (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    trigger_type    TEXT NOT NULL
                        CHECK (trigger_type IN ('event', 'funnel_drop', 'session_end', 'scheduled', 'goal_reached')),
    trigger_conf    JSONB NOT NULL DEFAULT '{}',
    channel         TEXT NOT NULL
                        CHECK (channel IN ('webhook', 'email', 'push')),
    channel_conf    JSONB NOT NULL DEFAULT '{}',
    active          BOOLEAN NOT NULL DEFAULT true,
    segment_id      UUID REFERENCES segments(id) ON DELETE SET NULL,
    cooldown_seconds INT NOT NULL DEFAULT 1800,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_campaigns_app_id ON campaigns(app_id);
CREATE INDEX IF NOT EXISTS idx_campaigns_active ON campaigns(active) WHERE active = true;
CREATE INDEX IF NOT EXISTS idx_campaigns_trigger ON campaigns(trigger_type) WHERE active = true;

-- ─── Notification Delivery Log ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id     UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    app_id          TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    channel         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'sent'
                        CHECK (status IN ('sent', 'delivered', 'failed', 'opened', 'clicked')),
    error_msg       TEXT,
    sent_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_deliveries_campaign ON notification_deliveries(campaign_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_user     ON notification_deliveries(app_id, user_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_sent_at  ON notification_deliveries(sent_at);

-- ─── Attribution / UTM Tracking ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_attribution (
    app_id              TEXT NOT NULL,
    user_id             TEXT NOT NULL,
    first_utm_source    TEXT,
    first_utm_medium    TEXT,
    first_utm_campaign  TEXT,
    first_utm_term      TEXT,
    first_utm_content   TEXT,
    first_referrer      TEXT,
    first_seen          TIMESTAMPTZ,
    last_utm_source     TEXT,
    last_utm_medium     TEXT,
    last_utm_campaign   TEXT,
    last_seen           TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_attribution_source   ON user_attribution(app_id, first_utm_source);
CREATE INDEX IF NOT EXISTS idx_attribution_campaign ON user_attribution(app_id, first_utm_campaign);

-- ─── Organisations: add email and plan columns ────────────────────────────────
ALTER TABLE orgs
    ADD COLUMN IF NOT EXISTS email       TEXT,
    ADD COLUMN IF NOT EXISTS plan        TEXT NOT NULL DEFAULT 'free'
                                             CHECK (plan IN ('free', 'growth', 'enterprise')),
    ADD COLUMN IF NOT EXISTS created_at  TIMESTAMPTZ NOT NULL DEFAULT now();

-- ─── Apps: add org_id and API key management columns ─────────────────────────
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS org_id          TEXT,
    ADD COLUMN IF NOT EXISTS app_name        TEXT,
    ADD COLUMN IF NOT EXISTS previous_api_key TEXT,
    ADD COLUMN IF NOT EXISTS rotated_at      TIMESTAMPTZ;

-- ─── Helper function: updated_at auto-update trigger ─────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['experiments', 'segments', 'campaigns', 'notification_deliveries']
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM pg_trigger
            WHERE tgname = format('trg_%s_updated_at', t)
        ) THEN
            EXECUTE format('
                CREATE TRIGGER trg_%s_updated_at
                BEFORE UPDATE ON %s
                FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t, t);
        END IF;
    END LOOP;
END;
$$;

COMMIT;
