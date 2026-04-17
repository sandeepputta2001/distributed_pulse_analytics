// Package handler implements the Kafka consumer for the Funnel Processor service.
package handler

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/funnel/internal/service"
)

// KafkaHandler consumes enriched events, evaluates funnel steps, publishes conversions.
type KafkaHandler struct {
	svc      *service.FunnelService
	producer *kafka.Producer
	topicOut string
	log      *zap.Logger
}

// New creates a KafkaHandler.
func New(svc *service.FunnelService, producer *kafka.Producer, topicOut string, log *zap.Logger) *KafkaHandler {
	return &KafkaHandler{svc: svc, producer: producer, topicOut: topicOut, log: log}
}

// Handle processes one Kafka message.
func (h *KafkaHandler) Handle(ctx context.Context, _, value []byte) error {
	var event models.EnrichedEvent
	if err := json.Unmarshal(value, &event); err != nil {
		return nil
	}

	conversions, err := h.svc.Process(ctx, event)
	if err != nil {
		h.log.Error("funnel process failed", zap.Error(err))
		return nil
	}

	for _, conv := range conversions {
		partKey := []byte(conv.AppID + ":" + conv.UserID)
		if err := h.producer.PublishAsync(h.topicOut, partKey, conv); err != nil {
			h.log.Error("publish conversion", zap.Error(err))
		}
	}
	return nil
}
