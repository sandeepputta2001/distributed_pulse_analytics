// Package repo provides data access for the Gateway service.
// Responsibilities: API key lookups (Postgres primary), raw event archival (MongoDB).
package repo

import (
	"context"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/mongo"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// Repo abstracts all data access for the gateway.
type Repo struct {
	pg    *postgres.Client
	mongo *mongo.Client
	log   *zap.Logger
}

// New creates a new gateway Repo.
func New(pg *postgres.Client, mongo *mongo.Client, log *zap.Logger) *Repo {
	return &Repo{pg: pg, mongo: mongo, log: log}
}

// GetAppByAPIKey fetches app metadata by API key (used for auth + rate-limit config).
func (r *Repo) GetAppByAPIKey(ctx context.Context, apiKey string) (*models.App, error) {
	return r.pg.GetAppByAPIKey(ctx, apiKey)
}

// InsertRawBatch archives raw events to MongoDB asynchronously.
// Called in a goroutine — never blocks the hot path.
func (r *Repo) InsertRawBatch(ctx context.Context, appID string, events []models.Event) error {
	if r.mongo == nil {
		return nil
	}
	return r.mongo.InsertRawBatch(ctx, appID, events)
}

// UpsertUserProfile upserts user traits in MongoDB.
func (r *Repo) UpsertUserProfile(ctx context.Context, appID, userID string, traits map[string]any) error {
	if r.mongo == nil {
		return nil
	}
	return r.mongo.UpsertUserProfile(ctx, appID, userID, traits)
}
