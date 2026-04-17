// Package service implements funnel step tracking and conversion detection.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"

	"github.com/pulse-analytics/funnel/internal/repo"
)

// FunnelConversion is emitted when a user completes all funnel steps.
type FunnelConversion struct {
	AppID     string `json:"app_id"`
	UserID    string `json:"user_id"`
	FunnelID  string `json:"funnel_id"`
	Steps     int    `json:"steps_completed"`
	Converted bool   `json:"converted"`
	TimeMs    int64  `json:"time_ms"`
}

// FunnelService tracks per-user funnel state and detects conversions.
type FunnelService struct {
	repo    *repo.Repo
	redis   *redisclient.Client
	metrics *metrics.Registry
	log     *zap.Logger

	mu      sync.RWMutex
	funnels []*models.FunnelDefinition
}

// New creates a FunnelService and loads initial funnel definitions.
func New(r *repo.Repo, redis *redisclient.Client, funnels []*models.FunnelDefinition, m *metrics.Registry, log *zap.Logger) *FunnelService {
	return &FunnelService{repo: r, redis: redis, metrics: m, log: log, funnels: funnels}
}

// Reload updates funnel definitions (hot-reload every 30s).
func (s *FunnelService) Reload(funnels []*models.FunnelDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.funnels = funnels
}

// Process evaluates an event against all funnel definitions for the app.
// Returns any conversions triggered by this event.
func (s *FunnelService) Process(ctx context.Context, event models.EnrichedEvent) ([]FunnelConversion, error) {
	s.mu.RLock()
	funnels := s.funnels
	s.mu.RUnlock()

	var conversions []FunnelConversion
	for _, f := range funnels {
		if f.AppID != event.AppID {
			continue
		}
		conv, err := s.evaluateFunnel(ctx, f, event)
		if err != nil {
			s.log.Warn("funnel evaluate failed", zap.String("funnel_id", f.FunnelID), zap.Error(err))
			continue
		}
		if conv != nil {
			conversions = append(conversions, *conv)
		}
	}
	return conversions, nil
}

func (s *FunnelService) evaluateFunnel(ctx context.Context, f *models.FunnelDefinition, event models.EnrichedEvent) (*FunnelConversion, error) {
	stateKey := fmt.Sprintf("funnel:%s:%s", f.FunnelID, event.UserID)

	var state models.FunnelState
	if err := s.redis.GetJSON(ctx, stateKey, &state); err != nil {
		state = models.FunnelState{FunnelID: f.FunnelID, UserID: event.UserID}
	}

	if state.Converted {
		return nil, nil // already converted
	}

	nextStep := state.CompletedSteps
	if nextStep >= len(f.Steps) {
		return nil, nil
	}

	if f.Steps[nextStep] != event.EventName {
		return nil, nil
	}

	state.CompletedSteps++
	state.StepTimestamps = append(state.StepTimestamps, event.EventTime)

	// Check if window expired
	if len(state.StepTimestamps) > 1 {
		windowMs := f.WindowSeconds * 1000
		if event.EventTime-state.StepTimestamps[0] > windowMs {
			// Reset funnel state
			state = models.FunnelState{FunnelID: f.FunnelID, UserID: event.UserID}
			data, _ := json.Marshal(state)
			_ = s.redis.Set(ctx, stateKey, data, 7*24*time.Hour)
			return nil, nil
		}
	}

	state.Converted = state.CompletedSteps == len(f.Steps)
	data, _ := json.Marshal(state)
	_ = s.redis.Set(ctx, stateKey, data, 7*24*time.Hour)

	return &FunnelConversion{
		AppID:     event.AppID,
		UserID:    event.UserID,
		FunnelID:  f.FunnelID,
		Steps:     state.CompletedSteps,
		Converted: state.Converted,
		TimeMs:    event.EventTime,
	}, nil
}
