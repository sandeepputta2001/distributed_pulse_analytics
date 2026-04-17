// Package repo provides data access for the notification service.
package repo

import (
	"context"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/mongo"
	"github.com/pulse-analytics/shared/pkg/postgres"
)

// Repo handles reads for notification channels and writes for delivery history.
type Repo struct {
	pg    *postgres.Client
	mongo *mongo.Client
	log   *zap.Logger
}

// New creates a Repo.
func New(pg *postgres.Client, m *mongo.Client, log *zap.Logger) *Repo {
	return &Repo{pg: pg, mongo: m, log: log}
}

// GetAlertRule returns the alert rule for a given ID (used to look up channels).
func (r *Repo) GetAlertRule(ctx context.Context, ruleID string) (*models.AlertRule, error) {
	return r.pg.GetAlertRule(ctx, ruleID)
}

// GetApp returns the app, which carries the notification contact config.
func (r *Repo) GetApp(ctx context.Context, appID string) (*models.App, error) {
	return r.pg.GetApp(ctx, appID)
}

// SaveDelivery records a notification delivery attempt in MongoDB for audit.
func (r *Repo) SaveDelivery(ctx context.Context, d *models.NotificationDelivery) error {
	return r.mongo.InsertOne(ctx, "notification_deliveries", d)
}
