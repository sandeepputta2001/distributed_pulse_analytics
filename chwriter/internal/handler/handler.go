// Package handler implements the Kafka consumer for the CHWriter service.
package handler

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/chwriter/internal/repo"
	"github.com/pulse-analytics/chwriter/internal/service"
)

// KafkaHandler consumes session-events and writes them to ClickHouse via the repo.
type KafkaHandler struct {
	repo *repo.Repo
	log  *zap.Logger
}

// New creates a KafkaHandler.
func New(r *repo.Repo, log *zap.Logger) *KafkaHandler {
	return &KafkaHandler{repo: r, log: log}
}

// Handle processes one Kafka message (EnrichedEvent or SessionEvent).
func (h *KafkaHandler) Handle(_ context.Context, _, value []byte) error {
	// Try EnrichedEvent first
	var enriched models.EnrichedEvent
	if err := json.Unmarshal(value, &enriched); err == nil && enriched.EventID != "" {
		chEvt := service.EnrichedToCHEvent(enriched)
		h.repo.WriteEvents([]models.CHEvent{chEvt})
		return nil
	}

	// Try SessionEvent (synthetic)
	var se models.SessionEvent
	if err := json.Unmarshal(value, &se); err == nil && se.SessionID != "" {
		chEvt := service.SessionToCHEvent(se)
		h.repo.WriteEvents([]models.CHEvent{chEvt})
		return nil
	}

	h.log.Warn("unrecognised message format, skipping")
	return nil
}
