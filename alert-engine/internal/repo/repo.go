// Package repo provides data access for the alert-engine.
package repo

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// Repo provides data access for alert rules and event aggregations.
type Repo struct {
	pg  *postgres.Client
	ch  *clickhouse.Client
	log *zap.Logger
}

// New creates a Repo.
func New(pg *postgres.Client, ch *clickhouse.Client, log *zap.Logger) *Repo {
	return &Repo{pg: pg, ch: ch, log: log}
}

// ListActiveAlerts returns all active alert rules.
func (r *Repo) ListActiveAlerts(ctx context.Context) ([]*models.AlertRule, error) {
	return r.pg.ListAlertRules(ctx, "")
}

// UpdateAlertLastFired stamps the last_fired_at timestamp on a rule — primary.
func (r *Repo) UpdateAlertLastFired(ctx context.Context, id string, t time.Time) error {
	return r.pg.UpdateAlertLastFired(ctx, id, t)
}

// GetApp returns app details for a given ID.
func (r *Repo) GetApp(ctx context.Context, appID string) (*models.App, error) {
	return r.pg.GetApp(ctx, appID)
}

// QueryMetricValue runs the ClickHouse aggregation for a given metric and time window.
// It returns the scalar result so the alert service can compare against thresholds.
func (r *Repo) QueryMetricValue(ctx context.Context, appID, metric string, windowMinutes int) (float64, error) {
	sql := metricSQL(metric)
	if sql == "" {
		return 0, nil
	}
	row := r.ch.QueryRow(ctx, sql, appID, windowMinutes)
	var val float64
	if err := row.Scan(&val); err != nil {
		return 0, err
	}
	return val, nil
}

// metricSQL returns a parameterised ClickHouse query for the named metric.
// Parameters: $1 = app_id, $2 = window_minutes.
func metricSQL(metric string) string {
	switch metric {
	case "event_rate":
		return `SELECT count() / ? AS v FROM events WHERE app_id = ? AND event_time >= now() - toIntervalMinute(?)`
	case "error_rate":
		return `SELECT countIf(event_name = 'error') / count() AS v FROM events WHERE app_id = ? AND event_time >= now() - toIntervalMinute(?)`
	case "p99_latency":
		return `SELECT quantile(0.99)(toFloat64(props['latency_ms'])) AS v FROM events WHERE app_id = ? AND event_name = 'api_call' AND event_time >= now() - toIntervalMinute(?)`
	case "active_users":
		return `SELECT uniq(device_id) AS v FROM events WHERE app_id = ? AND event_time >= now() - toIntervalMinute(?)`
	default:
		return ""
	}
}
