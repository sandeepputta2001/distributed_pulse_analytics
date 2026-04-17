// Package service wraps the shared querying.Service, exposing a clean interface
// to the query-api handler layer.
package service

import (
	"context"

	"go.uber.org/zap"

	chchlient "github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/querying"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
)

// AnalyticsService delegates all query execution to the shared querying.Service.
type AnalyticsService struct {
	inner *querying.Service
}

// NewAnalyticsService creates an AnalyticsService.
func NewAnalyticsService(ch *chchlient.Client, redis *redisclient.Client, m *metrics.Registry, log *zap.Logger) *AnalyticsService {
	return &AnalyticsService{inner: querying.NewService(ch, redis, m, log)}
}

func (s *AnalyticsService) EventCount(ctx context.Context, req querying.EventCountRequest) (*querying.EventCountResponse, error) {
	return s.inner.EventCount(ctx, req)
}

func (s *AnalyticsService) Funnel(ctx context.Context, req querying.FunnelRequest) (*querying.FunnelResponse, error) {
	return s.inner.Funnel(ctx, req)
}

func (s *AnalyticsService) DAU(ctx context.Context, req querying.DAURequest) (*querying.EventCountResponse, error) {
	return s.inner.DAU(ctx, req)
}

func (s *AnalyticsService) Retention(ctx context.Context, req querying.RetentionRequest) (*querying.RetentionResponse, error) {
	return s.inner.Retention(ctx, req)
}

// RawQueryRow exposes ad-hoc row queries to handlers (e.g. session metrics).
func (s *AnalyticsService) RawQueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(...any) error } {
	return s.inner.RawQueryRow(ctx, sql, args...)
}

// Close frees resources owned by the analytics service (e.g. L1 cache janitor).
func (s *AnalyticsService) Close() {
	s.inner.Close()
}
