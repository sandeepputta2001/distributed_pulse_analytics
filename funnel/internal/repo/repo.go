// Package repo provides data access for the Funnel Processor service.
package repo

import (
	"context"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// Repo loads funnel definitions from Postgres.
type Repo struct {
	pg  *postgres.Client
	log *zap.Logger
}

// New creates a funnel Repo.
func New(pg *postgres.Client, log *zap.Logger) *Repo {
	return &Repo{pg: pg, log: log}
}

// ListAllFunnels loads all funnel definitions across all apps (used at startup + hot-reload).
func (r *Repo) ListAllFunnels(ctx context.Context) ([]*models.FunnelDefinition, error) {
	// In production, list all apps then load their funnels.
	// Simplified: returns an empty slice and logs; real implementation queries Postgres.
	r.log.Debug("loading funnel definitions")
	return []*models.FunnelDefinition{}, nil
}

// ListFunnelsByApp loads funnels for a specific app.
func (r *Repo) ListFunnelsByApp(ctx context.Context, appID string) ([]*models.FunnelDefinition, error) {
	return r.pg.ListFunnels(ctx, appID)
}
