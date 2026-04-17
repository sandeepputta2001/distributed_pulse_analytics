// Package repo provides Postgres and ClickHouse data access for the Query API.
package repo

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// PostgresRepo handles all CRUD operations on Postgres metadata tables.
type PostgresRepo struct {
	pg  *postgres.Client
	log *zap.Logger
}

// NewPostgresRepo creates a PostgresRepo.
func NewPostgresRepo(pg *postgres.Client, log *zap.Logger) *PostgresRepo {
	return &PostgresRepo{pg: pg, log: log}
}

// ─── Funnel ────────────────────────────────────────────────────────────────

func (r *PostgresRepo) ListFunnels(ctx context.Context, appID string) ([]*models.FunnelDefinition, error) {
	return r.pg.ListFunnels(ctx, appID)
}

func (r *PostgresRepo) UpsertFunnel(ctx context.Context, f *models.FunnelDefinition) error {
	return r.pg.UpsertFunnel(ctx, f)
}

// ─── Apps ─────────────────────────────────────────────────────────────────

func (r *PostgresRepo) ListApps(ctx context.Context) ([]*models.App, error) {
	return r.pg.ListApps(ctx)
}

func (r *PostgresRepo) GetApp(ctx context.Context, appID string) (*models.App, error) {
	return r.pg.GetApp(ctx, appID)
}

func (r *PostgresRepo) UpdateApp(ctx context.Context, id, name string, rps float64, burst int) error {
	return r.pg.UpdateApp(ctx, id, name, rps, burst)
}

func (r *PostgresRepo) DeactivateApp(ctx context.Context, id string) error {
	return r.pg.DeactivateApp(ctx, id)
}

// ─── Orgs ─────────────────────────────────────────────────────────────────

func (r *PostgresRepo) ListOrgs(ctx context.Context) ([]*models.Org, error) {
	return r.pg.ListOrgs(ctx)
}

func (r *PostgresRepo) CreateOrg(ctx context.Context, o *models.Org) error {
	return r.pg.CreateOrg(ctx, o)
}

func (r *PostgresRepo) UpdateOrg(ctx context.Context, o *models.Org) error {
	return r.pg.UpdateOrg(ctx, o)
}

// ─── Alerts ────────────────────────────────────────────────────────────────

func (r *PostgresRepo) ListAlertRules(ctx context.Context, appID string) ([]*models.AlertRule, error) {
	return r.pg.ListAlertRules(ctx, appID)
}

func (r *PostgresRepo) CreateAlertRule(ctx context.Context, rule *models.AlertRule) error {
	return r.pg.CreateAlertRule(ctx, rule)
}

func (r *PostgresRepo) UpdateAlertRule(ctx context.Context, rule *models.AlertRule) error {
	return r.pg.UpdateAlertRule(ctx, rule)
}

func (r *PostgresRepo) DeleteAlertRule(ctx context.Context, id string) error {
	return r.pg.DeleteAlertRule(ctx, id)
}

// ─── Cohorts ───────────────────────────────────────────────────────────────

func (r *PostgresRepo) ListCohorts(ctx context.Context, appID string) ([]*models.CohortDefinition, error) {
	return r.pg.ListCohorts(ctx, appID)
}

func (r *PostgresRepo) CreateCohort(ctx context.Context, co *models.CohortDefinition) error {
	return r.pg.CreateCohort(ctx, co)
}

func (r *PostgresRepo) DeleteCohort(ctx context.Context, id string) error {
	return r.pg.DeleteCohort(ctx, id)
}

func (r *PostgresRepo) GetCohort(ctx context.Context, id string) (*models.CohortDefinition, error) {
	return r.pg.GetCohort(ctx, id)
}

func (r *PostgresRepo) UpdateCohortCount(ctx context.Context, id string, count int64, computedAt time.Time) error {
	return r.pg.UpdateCohortCount(ctx, id, count, computedAt)
}

// ─── Experiments ──────────────────────────────────────────────────────────

func (r *PostgresRepo) ListExperiments(ctx context.Context, appID string) ([]*models.Experiment, error) {
	return r.pg.ListExperiments(ctx, appID)
}

func (r *PostgresRepo) CreateExperiment(ctx context.Context, e *models.Experiment) error {
	return r.pg.CreateExperiment(ctx, e)
}

func (r *PostgresRepo) UpdateExperiment(ctx context.Context, e *models.Experiment) error {
	return r.pg.UpdateExperiment(ctx, e)
}

func (r *PostgresRepo) DeleteExperiment(ctx context.Context, id string) error {
	return r.pg.DeleteExperiment(ctx, id)
}

