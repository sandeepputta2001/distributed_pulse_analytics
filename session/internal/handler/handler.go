// Package handler implements the Kafka consumer for the Session service.
package handler

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/session/internal/service"
)

// KafkaHandler consumes enriched events, assigns session IDs, and publishes to session-events.
type KafkaHandler struct {
	svc      *service.SessionService
	producer *kafka.Producer
	topicOut string
	log      *zap.Logger
}

// New creates a KafkaHandler.
func New(svc *service.SessionService, producer *kafka.Producer, topicOut string, log *zap.Logger) *KafkaHandler {
	return &KafkaHandler{svc: svc, producer: producer, topicOut: topicOut, log: log}
}

// Handle processes one Kafka message.
func (h *KafkaHandler) Handle(ctx context.Context, _, value []byte) error {
	var event models.EnrichedEvent
	if err := json.Unmarshal(value, &event); err != nil {
		return nil
	}

	result, err := h.svc.Process(ctx, event)
	if err != nil {
		h.log.Error("session process failed", zap.Error(err))
		return nil
	}

	partKey := []byte(result.UpdatedEvent.AppID + ":" + result.UpdatedEvent.DeviceID)
	if err := h.producer.PublishAsync(h.topicOut, partKey, result.UpdatedEvent); err != nil {
		h.log.Error("publish session event", zap.Error(err))
	}
	for _, se := range result.SessionEvts {
		if err := h.producer.PublishAsync(h.topicOut, partKey, se); err != nil {
			h.log.Error("publish synthetic session event", zap.Error(err))
		}
	}
	return nil
}
