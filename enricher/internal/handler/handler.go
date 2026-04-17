// Package handler implements the Kafka consumer loop for the Enricher service.
// It consumes raw-events, delegates enrichment to the service layer, and publishes
// enriched events back to Kafka.
package handler

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"

	"github.com/pulse-analytics/enricher/internal/service"
)

// KafkaHandler wires Kafka consumer → enrichment service → Kafka producer.
type KafkaHandler struct {
	svc      *service.EnricherService
	producer *kafka.Producer
	topicOut string
	metrics  *metrics.Registry
	log      *zap.Logger
}

// New creates a KafkaHandler.
func New(svc *service.EnricherService, producer *kafka.Producer, topicOut string, m *metrics.Registry, log *zap.Logger) *KafkaHandler {
	return &KafkaHandler{svc: svc, producer: producer, topicOut: topicOut, metrics: m, log: log}
}

// Handle processes a single Kafka message: unmarshal → enrich → publish.
func (h *KafkaHandler) Handle(ctx context.Context, _, value []byte) error {
	var msg service.IngestMessage
	if err := json.Unmarshal(value, &msg); err != nil {
		return nil // skip malformed messages — don't block partition
	}

	enriched := h.svc.Enrich(msg)
	if len(enriched) == 0 {
		return nil
	}

	for _, e := range enriched {
		partKey := []byte(e.AppID + ":" + e.DeviceID)
		if err := h.producer.PublishAsync(h.topicOut, partKey, e); err != nil {
			h.log.Error("publish enriched event", zap.String("event_id", e.EventID), zap.Error(err))
			h.metrics.KafkaProduceErrors.WithLabelValues(h.topicOut).Inc()
		}
	}
	return nil
}
