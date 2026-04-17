// Package handler wires the Kafka consumer loop and HTTP endpoints.
package handler

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/notification/internal/service"
)

// KafkaHandler consumes alert-fired events and delegates to NotificationService.
type KafkaHandler struct {
	consumer *kafka.Consumer
	svc      *service.NotificationService
	topic    string
	log      *zap.Logger
}

// NewKafkaHandler creates a KafkaHandler.
func NewKafkaHandler(c *kafka.Consumer, svc *service.NotificationService, topic string, log *zap.Logger) *KafkaHandler {
	return &KafkaHandler{consumer: c, svc: svc, topic: topic, log: log}
}

// Run starts the consume loop (blocking). Cancel ctx to stop.
func (h *KafkaHandler) Run(ctx context.Context) {
	if err := h.consumer.ConsumeLoop(ctx, func(ctx context.Context, _, value []byte) error {
		var evt models.AlertFiredEvent
		if err := json.Unmarshal(value, &evt); err != nil {
			h.log.Warn("unmarshal alert event", zap.Error(err))
			return nil // skip malformed messages
		}
		if err := h.svc.Deliver(ctx, evt); err != nil {
			h.log.Error("delivery failed", zap.String("rule", evt.RuleID), zap.Error(err))
			return err
		}
		return nil
	}); err != nil {
		h.log.Error("consume loop exited", zap.Error(err))
	}
}
