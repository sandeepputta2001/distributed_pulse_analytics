// Package funnel tracks multi-step funnel conversions per user.
// State is stored in Redis sorted sets (score = event_timestamp ms).
// On each step event, it checks if all previous steps exist within
// the configured time window and emits funnel_conversion when complete.
package funnel

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	redisclient "github.com/pulse-analytics/internal/redis"
)

const (
	funnelKeyPrefix = "funnel:state:"
	stateEvictTTL   = 8 * 24 * time.Hour // keep state 8 days max
)

// ConversionEvent is emitted when a user completes all funnel steps.
type ConversionEvent struct {
	FunnelID      string  `json:"funnel_id"`
	AppID         string  `json:"app_id"`
	UserID        string  `json:"user_id"`
	StepsComplete int     `json:"steps_complete"`
	TotalSteps    int     `json:"total_steps"`
	Converted     bool    `json:"converted"`
	DurationMs    int64   `json:"duration_ms"`
	ConvertedAt   int64   `json:"converted_at"`
}

// Processor tracks funnel step completions per user.
type Processor struct {
	redis   *redisclient.Client
	funnels []*models.FunnelDefinition
	m       *metrics.Registry
	log     *zap.Logger
}

// NewProcessor creates a Funnel Processor.
func NewProcessor(redis *redisclient.Client, funnels []*models.FunnelDefinition, m *metrics.Registry, log *zap.Logger) *Processor {
	return &Processor{redis: redis, funnels: funnels, m: m, log: log}
}

// UpdateFunnels updates the funnel definitions (hot reload).
func (p *Processor) UpdateFunnels(funnels []*models.FunnelDefinition) {
	p.funnels = funnels
}

// Process checks if an event advances any funnel for the user.
// Returns any conversion events produced.
func (p *Processor) Process(ctx context.Context, event models.EnrichedEvent) ([]ConversionEvent, error) {
	if event.UserID == "" {
		return nil, nil
	}

	var conversions []ConversionEvent

	for _, funnel := range p.funnels {
		if funnel.AppID != event.AppID {
			continue
		}

		conv, err := p.processForFunnel(ctx, event, funnel)
		if err != nil {
			p.log.Warn("funnel process error",
				zap.String("funnel", funnel.FunnelID),
				zap.Error(err),
			)
			continue
		}
		if conv != nil {
			conversions = append(conversions, *conv)
		}
	}

	return conversions, nil
}

func (p *Processor) processForFunnel(ctx context.Context, event models.EnrichedEvent, funnel *models.FunnelDefinition) (*ConversionEvent, error) {
	// Find which step this event corresponds to
	stepIdx := -1
	for i, step := range funnel.Steps {
		if step == event.EventName {
			stepIdx = i
			break
		}
	}
	if stepIdx < 0 {
		return nil, nil // event not in this funnel
	}

	// Redis key: funnel state per user per funnel
	key := fmt.Sprintf("%s%s:%s:%s", funnelKeyPrefix, funnel.AppID, funnel.FunnelID, event.UserID)
	score := float64(event.ServerTime)
	member := strconv.Itoa(stepIdx)

	// Add this step to the sorted set (score = timestamp)
	if err := p.redis.ZAdd(ctx, key, redis.Z{Score: score, Member: member}); err != nil {
		return nil, fmt.Errorf("zadd: %w", err)
	}
	// Set TTL for state eviction
	if err := p.redis.Expire(ctx, key, stateEvictTTL); err != nil {
		p.log.Warn("expire funnel key failed", zap.Error(err))
	}

	// If this is NOT the last step, we're done for now
	if stepIdx < len(funnel.Steps)-1 {
		return nil, nil
	}

	// Last step reached — check if all previous steps exist within the time window
	windowMs := funnel.WindowSeconds * 1000
	windowMin := strconv.FormatFloat(score-float64(windowMs), 'f', 0, 64)
	windowMax := strconv.FormatFloat(score, 'f', 0, 64)

	// Check each required step
	var firstStepTs float64
	for i := 0; i < len(funnel.Steps); i++ {
		members, err := p.redis.ZRangeByScore(ctx, key, &redis.ZRangeBy{
			Min:    windowMin,
			Max:    windowMax,
			Offset: 0,
			Count:  1,
		})
		if err != nil || len(members) == 0 {
			return nil, nil // step not found in window → no conversion
		}
		if i == 0 {
			// Get timestamp of first step
			results, err := p.redis.ZRangeByScore(ctx, key, &redis.ZRangeBy{
				Min: windowMin,
				Max: windowMax,
			})
			if err == nil && len(results) > 0 {
				// Approximate first step score from range
				firstStepTs, _ = strconv.ParseFloat(results[0], 64)
			}
		}
	}

	// All steps present → conversion!
	durationMs := int64(score) - int64(firstStepTs)
	if durationMs < 0 {
		durationMs = 0
	}

	conv := &ConversionEvent{
		FunnelID:      funnel.FunnelID,
		AppID:         event.AppID,
		UserID:        event.UserID,
		StepsComplete: len(funnel.Steps),
		TotalSteps:    len(funnel.Steps),
		Converted:     true,
		DurationMs:    durationMs,
		ConvertedAt:   event.ServerTime,
	}

	// Clean up state after conversion to avoid re-counting
	if err := p.redis.Del(ctx, key); err != nil {
		p.log.Warn("del funnel state failed", zap.Error(err))
	}

	p.log.Info("funnel conversion",
		zap.String("funnel", funnel.FunnelID),
		zap.String("user", event.UserID),
		zap.Duration("duration", time.Duration(durationMs)*time.Millisecond),
	)

	return conv, nil
}

// PruneExpiredSteps removes steps outside the window for a user.
func (p *Processor) PruneExpiredSteps(ctx context.Context, key string, windowMs int64, nowMs int64) error {
	cutoff := strconv.FormatInt(nowMs-windowMs, 10)
	_, err := p.redis.ZRemRangeByScore(ctx, key, "-inf", cutoff)
	return err
}
