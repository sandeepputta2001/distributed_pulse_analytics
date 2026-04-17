// Package service implements the core ingest business logic for the Gateway.
// It orchestrates deduplication, rate-limiting, Kafka publishing, and MongoDB archival.
package service

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/dedup"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/ratelimit"
)

// IngestService handles the core ingest pipeline: dedup → kafka publish.
type IngestService struct {
	kafka   *kafka.Producer
	dedup   *dedup.Filter
	limiter *ratelimit.Limiter
	metrics *metrics.Registry
	log     *zap.Logger

	topicRawEvents string
}

// New creates an IngestService.
func New(
	producer *kafka.Producer,
	filter *dedup.Filter,
	limiter *ratelimit.Limiter,
	m *metrics.Registry,
	log *zap.Logger,
	topicRawEvents string,
) *IngestService {
	return &IngestService{
		kafka:          producer,
		dedup:          filter,
		limiter:        limiter,
		metrics:        m,
		log:            log,
		topicRawEvents: topicRawEvents,
	}
}

// IngestResult contains the outcome of a single ingest call.
type IngestResult struct {
	Accepted int
	Filtered int
}

// ProcessBatch deduplicates and publishes an event batch to Kafka.
// Returns accepted/filtered counts.
func (s *IngestService) ProcessBatch(ctx context.Context, appID, deviceID, clientIP string, events []models.Event) (*IngestResult, error) {
	original := len(events)

	// Two-stage deduplication
	unique := make([]models.Event, 0, len(events))
	for _, e := range events {
		if e.EventID == "" {
			e.EventID = models.NewEventID()
		}
		if s.dedup.TestAndAdd(ctx, e.EventID) {
			unique = append(unique, e)
		}
	}
	filtered := original - len(unique)
	if filtered > 0 {
		s.metrics.DuplicatesFiltered.Add(float64(filtered))
		s.log.Debug("dedup filtered", zap.String("app_id", appID),
			zap.Int("filtered", filtered), zap.Int("remaining", len(unique)))
	}

	if len(unique) == 0 {
		return &IngestResult{Accepted: 0, Filtered: filtered}, nil
	}

	payload := map[string]any{
		"batch": models.EventBatch{
			AppID:    appID,
			DeviceID: deviceID,
			Events:   unique,
		},
		"client_ip": clientIP,
		"server_ts": time.Now().UnixMilli(),
	}

	partKey := []byte(appID + ":" + deviceID)
	if err := s.kafka.PublishAsync(s.topicRawEvents, partKey, payload); err != nil {
		s.metrics.IngestErrors.WithLabelValues("kafka").Inc()
		return nil, err
	}

	s.metrics.IngestEvents.Add(float64(len(unique)))
	s.metrics.IngestBatchSize.Observe(float64(len(unique)))
	return &IngestResult{Accepted: len(unique), Filtered: filtered}, nil
}

// PublishIdentify publishes a user_updated event for identify calls.
func (s *IngestService) PublishIdentify(ctx context.Context, appID, userID string, traits map[string]any) error {
	evt := models.Event{
		EventID:   models.NewEventID(),
		EventName: "user_updated",
		EventTime: models.NowMs(),
		Props:     traits,
	}
	payload := map[string]any{
		"batch": models.EventBatch{
			AppID:  appID,
			UserID: userID,
			Events: []models.Event{evt},
		},
		"server_ts": models.NowMs(),
	}
	return s.kafka.PublishAsync(s.topicRawEvents, []byte(appID+":"+userID), payload)
}

// PublishTrack publishes a single tracked event.
func (s *IngestService) PublishTrack(ctx context.Context, appID string, evt models.Event) error {
	if evt.EventID == "" {
		evt.EventID = models.NewEventID()
	}
	if evt.EventTime == 0 {
		evt.EventTime = models.NowMs()
	}
	payload := map[string]any{
		"batch": models.EventBatch{
			AppID:  appID,
			Events: []models.Event{evt},
		},
		"server_ts": models.NowMs(),
	}
	return s.kafka.PublishAsync(s.topicRawEvents, []byte(appID), payload)
}
