package postgres

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/models"
)

// Client wraps pgxpool with primary/replica read-write splitting.
//
// # Read/Write Splitting
//
// All mutating operations (INSERT, UPDATE, DELETE, transactions) are routed to
// the primary pool.  SELECT-only methods call read() which round-robins across
// the replica pools.  If no replicas are configured, read() falls back to the
// primary so the behaviour is identical to the single-pool case.
//
// Replicas are registered in config.PostgresConfig.ReplicaDSNs.  Each replica
// gets its own pgxpool.Pool sized the same as the primary.
//
// # Failover
//
// If a replica pool fails to connect at startup it is skipped with a warning
// rather than aborting the process.  At runtime, errors from a replica are
// returned to the caller; the caller (or a higher-level retry layer) decides
// whether to retry on the primary.
type Client struct {
	write    *pgxpool.Pool
	reads    []*pgxpool.Pool // empty when no replicas configured
	rrCursor atomic.Uint64  // round-robin index for replica selection
	log      *zap.Logger
}

// NewClient creates the primary pool and, optionally, one pool per replica DSN.
func NewClient(cfg *config.PostgresConfig, log *zap.Logger) (*Client, error) {
	primary, err := newPool(cfg.DSN, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres primary: %w", err)
	}
	log.Info("postgres primary connected", zap.String("dsn", maskDSN(cfg.DSN)))

	c := &Client{write: primary, log: log}

	for _, dsn := range cfg.ReplicaDSNs {
		pool, err := newPool(dsn, cfg)
		if err != nil {
			log.Warn("postgres replica skipped (connect failed)",
				zap.String("dsn", maskDSN(dsn)), zap.Error(err))
			continue
		}
		log.Info("postgres replica connected", zap.String("dsn", maskDSN(dsn)))
		c.reads = append(c.reads, pool)
	}

	if len(c.reads) == 0 {
		log.Info("postgres: no replicas configured — all reads use primary")
	} else {
		log.Info("postgres read replicas ready", zap.Int("count", len(c.reads)))
	}

	return c, nil
}

// newPool constructs a pgxpool with the shared sizing config.
func newPool(dsn string, cfg *config.PostgresConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.MaxOpenConn)
	poolCfg.MinConns = int32(cfg.MaxIdleConn)
	poolCfg.MaxConnLifetime = cfg.MaxLifetime
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// read returns a replica pool selected via round-robin, falling back to the
// primary when no replicas are available.
func (c *Client) read() *pgxpool.Pool {
	if len(c.reads) == 0 {
		return c.write
	}
	idx := c.rrCursor.Add(1) % uint64(len(c.reads))
	return c.reads[idx]
}

// WritePool returns the primary (write) pool — used for health checks and
// components that need direct pool access (e.g. pgx transactions).
func (c *Client) WritePool() *pgxpool.Pool { return c.write }

// Pool returns the primary pool (backwards-compat alias used by gateway main).
func (c *Client) Pool() *pgxpool.Pool { return c.write }

// ─── App Operations ───────────────────────────────────────────────────────────

// GetAppByAPIKey fetches an App by its API key — read replica.
func (c *Client) GetAppByAPIKey(ctx context.Context, apiKey string) (*models.App, error) {
	const q = `
		SELECT id, org_id, name, api_key, rps, burst, active, created_at
		FROM apps
		WHERE api_key = $1 AND active = true
		LIMIT 1`

	var app models.App
	row := c.read().QueryRow(ctx, q, apiKey)
	if err := row.Scan(&app.ID, &app.OrgID, &app.Name, &app.APIKey,
		&app.RPS, &app.Burst, &app.Active, &app.CreatedAt); err != nil {
		return nil, fmt.Errorf("get app by api key: %w", err)
	}
	return &app, nil
}

// GetApp fetches an App by ID — read replica.
func (c *Client) GetApp(ctx context.Context, appID string) (*models.App, error) {
	const q = `
		SELECT id, org_id, name, api_key, rps, burst, active, created_at
		FROM apps
		WHERE id = $1`

	var app models.App
	row := c.read().QueryRow(ctx, q, appID)
	if err := row.Scan(&app.ID, &app.OrgID, &app.Name, &app.APIKey,
		&app.RPS, &app.Burst, &app.Active, &app.CreatedAt); err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	return &app, nil
}

// CreateApp inserts a new app — primary.
func (c *Client) CreateApp(ctx context.Context, app *models.App) error {
	const q = `
		INSERT INTO apps (id, org_id, name, api_key, rps, burst, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := c.write.Exec(ctx, q,
		app.ID, app.OrgID, app.Name, app.APIKey,
		app.RPS, app.Burst, app.Active, app.CreatedAt)
	return err
}

// ─── Funnel Operations ────────────────────────────────────────────────────────

// ListFunnels returns all funnel definitions for an app — read replica.
func (c *Client) ListFunnels(ctx context.Context, appID string) ([]*models.FunnelDefinition, error) {
	const q = `
		SELECT funnel_id, app_id, name, steps, window_seconds, created_at
		FROM funnel_definitions
		WHERE app_id = $1
		ORDER BY created_at DESC`

	rows, err := c.read().Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var funnels []*models.FunnelDefinition
	for rows.Next() {
		var f models.FunnelDefinition
		if err := rows.Scan(&f.FunnelID, &f.AppID, &f.Name,
			&f.Steps, &f.WindowSeconds, &f.CreatedAt); err != nil {
			return nil, err
		}
		funnels = append(funnels, &f)
	}
	return funnels, rows.Err()
}

// UpsertFunnel inserts or updates a funnel definition — primary.
func (c *Client) UpsertFunnel(ctx context.Context, f *models.FunnelDefinition) error {
	const q = `
		INSERT INTO funnel_definitions (funnel_id, app_id, name, steps, window_seconds, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (funnel_id) DO UPDATE
		SET name = EXCLUDED.name,
		    steps = EXCLUDED.steps,
		    window_seconds = EXCLUDED.window_seconds`

	_, err := c.write.Exec(ctx, q,
		f.FunnelID, f.AppID, f.Name, f.Steps, f.WindowSeconds, f.CreatedAt)
	return err
}

// ListApps returns all active apps — read replica.
func (c *Client) ListApps(ctx context.Context) ([]*models.App, error) {
	const q = `SELECT id, org_id, name, api_key, rps, burst, active, created_at FROM apps WHERE active = true ORDER BY created_at DESC`
	rows, err := c.read().Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var apps []*models.App
	for rows.Next() {
		var a models.App
		if err := rows.Scan(&a.ID, &a.OrgID, &a.Name, &a.APIKey, &a.RPS, &a.Burst, &a.Active, &a.CreatedAt); err != nil {
			return nil, err
		}
		apps = append(apps, &a)
	}
	return apps, rows.Err()
}

// UpdateApp updates mutable app fields — primary.
func (c *Client) UpdateApp(ctx context.Context, id, name string, rps float64, burst int) error {
	_, err := c.write.Exec(ctx,
		`UPDATE apps SET name = COALESCE(NULLIF($2,''), name), rps = CASE WHEN $3 > 0 THEN $3 ELSE rps END, burst = CASE WHEN $4 > 0 THEN $4 ELSE burst END WHERE id = $1`,
		id, name, rps, burst)
	return err
}

// DeactivateApp soft-deletes an app — primary.
func (c *Client) DeactivateApp(ctx context.Context, id string) error {
	_, err := c.write.Exec(ctx, `UPDATE apps SET active = false WHERE id = $1`, id)
	return err
}

// CreateOrgAndApp creates an org and a default app — primary (transaction).
func (c *Client) CreateOrgAndApp(ctx context.Context, orgName, appName, email string) (orgID, appID, apiKey string, err error) {
	orgID = models.NewEventID()
	appID = models.NewEventID()
	apiKey = "pk_live_" + models.NewEventID()[:32]

	tx, err := c.write.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO orgs (id, name, plan) VALUES ($1, $2, 'free')`,
		orgID, orgName)
	if err != nil {
		return
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO apps (id, org_id, name, api_key, rps, burst, active) VALUES ($1, $2, $3, $4, 10000, 50000, true)`,
		appID, orgID, appName, apiKey)
	if err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}

// RotateAPIKey sets a new API key for an app — primary.
func (c *Client) RotateAPIKey(ctx context.Context, appID, newKey string) error {
	_, err := c.write.Exec(ctx, `UPDATE apps SET api_key = $2 WHERE id = $1`, appID, newKey)
	return err
}

// ─── Alert Rule Operations ────────────────────────────────────────────────────

// ListAlertRules returns active alert rules — read replica.
func (c *Client) ListAlertRules(ctx context.Context, appID string) ([]*models.AlertRule, error) {
	const q = `
		SELECT id, app_id, name, metric_name, condition, threshold,
		       window_mins, channels, webhook_url, email_to, active, last_fired_at, created_at
		FROM alert_rules
		WHERE app_id = $1 AND active = true`

	rows, err := c.read().Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*models.AlertRule
	for rows.Next() {
		var r models.AlertRule
		if err := rows.Scan(&r.ID, &r.AppID, &r.Name, &r.MetricName,
			&r.Condition, &r.Threshold, &r.WindowMins, &r.Channels,
			&r.WebhookURL, &r.EmailTo, &r.Active, &r.LastFiredAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, &r)
	}
	return rules, rows.Err()
}

// UpdateAlertLastFired stamps last_fired_at on an alert rule — primary.
func (c *Client) UpdateAlertLastFired(ctx context.Context, id string, t time.Time) error {
	_, err := c.write.Exec(ctx,
		`UPDATE alert_rules SET last_fired_at = $2 WHERE id = $1`, id, t)
	return err
}

// CreateAlertRule inserts a new alert rule — primary.
func (c *Client) CreateAlertRule(ctx context.Context, r *models.AlertRule) error {
	_, err := c.write.Exec(ctx,
		`INSERT INTO alert_rules (id, app_id, name, metric_name, condition, threshold, window_mins, channels, webhook_url, email_to, active, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		r.ID, r.AppID, r.Name, r.MetricName, r.Condition, r.Threshold,
		r.WindowMins, r.Channels, r.WebhookURL, r.EmailTo, r.Active, r.CreatedAt)
	return err
}

// UpdateAlertRule updates an existing alert rule — primary.
func (c *Client) UpdateAlertRule(ctx context.Context, r *models.AlertRule) error {
	_, err := c.write.Exec(ctx,
		`UPDATE alert_rules SET name=$2, metric_name=$3, condition=$4, threshold=$5, window_mins=$6, channels=$7, webhook_url=$8, email_to=$9, active=$10 WHERE id=$1`,
		r.ID, r.Name, r.MetricName, r.Condition, r.Threshold,
		r.WindowMins, r.Channels, r.WebhookURL, r.EmailTo, r.Active)
	return err
}

// DeleteAlertRule removes an alert rule — primary.
func (c *Client) DeleteAlertRule(ctx context.Context, id string) error {
	_, err := c.write.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	return err
}

// ─── Cohort Operations ────────────────────────────────────────────────────────

// ListCohorts returns cohort definitions for an app — read replica.
func (c *Client) ListCohorts(ctx context.Context, appID string) ([]*models.CohortDefinition, error) {
	const q = `SELECT id, app_id, name, description, filter_sql, user_count, last_computed_at, created_at FROM cohort_definitions WHERE app_id = $1 ORDER BY created_at DESC`
	rows, err := c.read().Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cohorts []*models.CohortDefinition
	for rows.Next() {
		var co models.CohortDefinition
		if err := rows.Scan(&co.ID, &co.AppID, &co.Name, &co.Description, &co.FilterSQL, &co.UserCount, &co.LastComputedAt, &co.CreatedAt); err != nil {
			return nil, err
		}
		cohorts = append(cohorts, &co)
	}
	return cohorts, rows.Err()
}

// GetCohort returns a single cohort definition by ID — read replica.
func (c *Client) GetCohort(ctx context.Context, id string) (*models.CohortDefinition, error) {
	const q = `SELECT id, app_id, name, description, filter_sql, user_count, last_computed_at, created_at FROM cohort_definitions WHERE id = $1`
	var co models.CohortDefinition
	err := c.read().QueryRow(ctx, q, id).Scan(&co.ID, &co.AppID, &co.Name, &co.Description, &co.FilterSQL, &co.UserCount, &co.LastComputedAt, &co.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get cohort: %w", err)
	}
	return &co, nil
}

// UpdateCohortCount updates user_count and last_computed_at for a cohort — primary.
func (c *Client) UpdateCohortCount(ctx context.Context, id string, count int64, computedAt time.Time) error {
	_, err := c.write.Exec(ctx,
		`UPDATE cohort_definitions SET user_count = $2, last_computed_at = $3 WHERE id = $1`,
		id, count, computedAt)
	return err
}

// CreateCohort inserts a cohort definition — primary.
func (c *Client) CreateCohort(ctx context.Context, co *models.CohortDefinition) error {
	_, err := c.write.Exec(ctx,
		`INSERT INTO cohort_definitions (id, app_id, name, description, filter_sql, user_count, created_at) VALUES ($1,$2,$3,$4,$5,0,$6)`,
		co.ID, co.AppID, co.Name, co.Description, co.FilterSQL, co.CreatedAt)
	return err
}

// DeleteCohort removes a cohort definition — primary.
func (c *Client) DeleteCohort(ctx context.Context, id string) error {
	_, err := c.write.Exec(ctx, `DELETE FROM cohort_definitions WHERE id = $1`, id)
	return err
}

// ─── Experiment Operations ────────────────────────────────────────────────────

// ListExperiments returns experiments for an app — read replica.
func (c *Client) ListExperiments(ctx context.Context, appID string) ([]*models.Experiment, error) {
	const q = `SELECT id, app_id, name, description, status, goal_event, variants, start_at, end_at, created_at FROM experiments WHERE app_id = $1 ORDER BY created_at DESC`
	rows, err := c.read().Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var exps []*models.Experiment
	for rows.Next() {
		var e models.Experiment
		if err := rows.Scan(&e.ID, &e.AppID, &e.Name, &e.Description, &e.Status, &e.GoalEvent, &e.Variants, &e.StartAt, &e.EndAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		exps = append(exps, &e)
	}
	return exps, rows.Err()
}

// CreateExperiment inserts an experiment — primary.
func (c *Client) CreateExperiment(ctx context.Context, e *models.Experiment) error {
	variants := e.Variants
	if variants == nil {
		variants = []byte("[]")
	}
	_, err := c.write.Exec(ctx,
		`INSERT INTO experiments (id, app_id, name, description, status, goal_event, variants, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.AppID, e.Name, e.Description, e.Status, e.GoalEvent, variants, e.CreatedAt)
	return err
}

// UpdateExperiment updates experiment status and config — primary.
func (c *Client) UpdateExperiment(ctx context.Context, e *models.Experiment) error {
	_, err := c.write.Exec(ctx,
		`UPDATE experiments SET name=COALESCE(NULLIF($2,''),name), status=COALESCE(NULLIF($3,''),status), goal_event=COALESCE(NULLIF($4,''),goal_event) WHERE id=$1`,
		e.ID, e.Name, e.Status, e.GoalEvent)
	return err
}

// DeleteExperiment removes an experiment — primary.
func (c *Client) DeleteExperiment(ctx context.Context, id string) error {
	_, err := c.write.Exec(ctx, `DELETE FROM experiments WHERE id = $1`, id)
	return err
}

// ─── Org Operations ───────────────────────────────────────────────────────────

// ListOrgs returns all organisations — read replica.
func (c *Client) ListOrgs(ctx context.Context) ([]*models.Org, error) {
	const q = `SELECT id, name, plan, created_at FROM orgs ORDER BY created_at DESC`
	rows, err := c.read().Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orgs []*models.Org
	for rows.Next() {
		var o models.Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Plan, &o.CreatedAt); err != nil {
			return nil, err
		}
		orgs = append(orgs, &o)
	}
	return orgs, rows.Err()
}

// CreateOrg inserts a new organisation — primary.
func (c *Client) CreateOrg(ctx context.Context, o *models.Org) error {
	_, err := c.write.Exec(ctx,
		`INSERT INTO orgs (id, name, plan, created_at) VALUES ($1,$2,$3,$4)`,
		o.ID, o.Name, o.Plan, o.CreatedAt)
	return err
}

// UpdateOrg updates an organisation's name and/or plan — primary.
func (c *Client) UpdateOrg(ctx context.Context, o *models.Org) error {
	_, err := c.write.Exec(ctx,
		`UPDATE orgs SET name=COALESCE(NULLIF($2,''),name), plan=COALESCE(NULLIF($3,''),plan) WHERE id=$1`,
		o.ID, o.Name, o.Plan)
	return err
}

// ─── Campaign Operations ──────────────────────────────────────────────────────

// CreateCampaign inserts a new campaign and returns its generated ID — primary.
func (c *Client) CreateCampaign(ctx context.Context, camp *models.Campaign) (string, error) {
	id := models.NewEventID()
	_, err := c.write.Exec(ctx,
		`INSERT INTO campaigns (id, app_id, name, trigger_type, trigger_conf, channel, channel_conf, active, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, camp.AppID, camp.Name, camp.TriggerType, camp.TriggerConf,
		camp.Channel, camp.ChannelConf, camp.Active, camp.CreatedAt)
	return id, err
}

// GetCampaign fetches a campaign by ID — read replica.
func (c *Client) GetCampaign(ctx context.Context, id string) (*models.Campaign, error) {
	const q = `SELECT id, app_id, name, trigger_type, trigger_conf, channel, channel_conf, active, created_at
	           FROM campaigns WHERE id = $1`
	var camp models.Campaign
	row := c.read().QueryRow(ctx, q, id)
	if err := row.Scan(&camp.ID, &camp.AppID, &camp.Name, &camp.TriggerType, &camp.TriggerConf,
		&camp.Channel, &camp.ChannelConf, &camp.Active, &camp.CreatedAt); err != nil {
		return nil, fmt.Errorf("get campaign: %w", err)
	}
	return &camp, nil
}

// UpdateCampaign updates mutable campaign fields — primary.
func (c *Client) UpdateCampaign(ctx context.Context, camp *models.Campaign) error {
	_, err := c.write.Exec(ctx,
		`UPDATE campaigns SET name=COALESCE(NULLIF($2,''),name),
		 trigger_type=COALESCE(NULLIF($3,''),trigger_type),
		 trigger_conf=$4, channel=COALESCE(NULLIF($5,''),channel), channel_conf=$6
		 WHERE id=$1`,
		camp.ID, camp.Name, camp.TriggerType, camp.TriggerConf, camp.Channel, camp.ChannelConf)
	return err
}

// SetCampaignActive activates or deactivates a campaign — primary.
func (c *Client) SetCampaignActive(ctx context.Context, id string, active bool) error {
	_, err := c.write.Exec(ctx, `UPDATE campaigns SET active=$2 WHERE id=$1`, id, active)
	return err
}

// GetActiveCampaignsByTrigger returns active campaigns matching a trigger topic — read replica.
func (c *Client) GetActiveCampaignsByTrigger(ctx context.Context, appID, topic string) ([]*models.Campaign, error) {
	const q = `SELECT id, app_id, name, trigger_type, trigger_conf, channel, channel_conf, active, created_at
	           FROM campaigns WHERE app_id=$1 AND active=true AND trigger_conf->>'topic'=$2`
	rows, err := c.read().Query(ctx, q, appID, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var camps []*models.Campaign
	for rows.Next() {
		var camp models.Campaign
		if err := rows.Scan(&camp.ID, &camp.AppID, &camp.Name, &camp.TriggerType, &camp.TriggerConf,
			&camp.Channel, &camp.ChannelConf, &camp.Active, &camp.CreatedAt); err != nil {
			return nil, err
		}
		camps = append(camps, &camp)
	}
	return camps, rows.Err()
}

// GetCampaignStats returns delivery statistics for a campaign — read replica.
func (c *Client) GetCampaignStats(ctx context.Context, campaignID string) (*models.CampaignStats, error) {
	const q = `SELECT
		COUNT(*) FILTER (WHERE status='sent')      AS sent,
		COUNT(*) FILTER (WHERE status='delivered') AS delivered,
		COUNT(*) FILTER (WHERE status='failed')    AS failed
		FROM notification_deliveries WHERE campaign_id=$1`
	var s models.CampaignStats
	s.CampaignID = campaignID
	row := c.read().QueryRow(ctx, q, campaignID)
	if err := row.Scan(&s.Sent, &s.Delivered, &s.Failed); err != nil {
		return nil, fmt.Errorf("campaign stats: %w", err)
	}
	if s.Sent > 0 {
		s.OpenRate = float64(s.Delivered) / float64(s.Sent) * 100
	}
	return &s, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// GetAlertRule returns a single alert rule by ID — read replica.
func (c *Client) GetAlertRule(ctx context.Context, id string) (*models.AlertRule, error) {
	const q = `
		SELECT id, app_id, name, metric_name, condition, threshold,
		       window_mins, channels, webhook_url, email_to, active, last_fired_at, created_at
		FROM alert_rules
		WHERE id = $1`

	var r models.AlertRule
	err := c.read().QueryRow(ctx, q, id).Scan(
		&r.ID, &r.AppID, &r.Name, &r.MetricName,
		&r.Condition, &r.Threshold, &r.WindowMins, &r.Channels,
		&r.WebhookURL, &r.EmailTo, &r.Active, &r.LastFiredAt, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.write.Ping(ctx)
}

func (c *Client) Close() {
	c.write.Close()
	for _, r := range c.reads {
		r.Close()
	}
}

func maskDSN(dsn string) string {
	if len(dsn) > 20 {
		return dsn[:20] + "..."
	}
	return dsn
}
