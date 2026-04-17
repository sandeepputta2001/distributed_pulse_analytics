// Package repo provides Postgres data access for the Auth service.
package repo

import (
	"context"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// Repo abstracts all database operations for the auth service.
type Repo struct {
	pg  *postgres.Client
	log *zap.Logger
}

// New creates a new auth Repo.
func New(pg *postgres.Client, log *zap.Logger) *Repo {
	return &Repo{pg: pg, log: log}
}

// GetAppByAPIKey fetches an app record by its API key.
func (r *Repo) GetAppByAPIKey(ctx context.Context, apiKey string) (*models.App, error) {
	return r.pg.GetAppByAPIKey(ctx, apiKey)
}

// CreateOrgAndApp creates a new org + default app in a single transaction.
// Returns orgID, appID, apiKey.
func (r *Repo) CreateOrgAndApp(ctx context.Context, orgName, appName, email string) (string, string, string, error) {
	return r.pg.CreateOrgAndApp(ctx, orgName, appName, email)
}

// RotateAPIKey updates the API key for the given app.
func (r *Repo) RotateAPIKey(ctx context.Context, appID, newKey string) error {
	return r.pg.RotateAPIKey(ctx, appID, newKey)
}
