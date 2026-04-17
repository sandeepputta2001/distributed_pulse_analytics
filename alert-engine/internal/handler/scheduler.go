// Package handler wires the alert scheduler and exposes health/metrics endpoints.
package handler

import (
	"context"
	"net/http"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/pulse-analytics/alert-engine/internal/service"
)

// Scheduler wraps cron and runs alert evaluations on a fixed cadence.
type Scheduler struct {
	svc  *service.AlertService
	cron *cron.Cron
	log  *zap.Logger
}

// NewScheduler creates a Scheduler that evaluates alerts every minute.
func NewScheduler(svc *service.AlertService, log *zap.Logger) *Scheduler {
	return &Scheduler{svc: svc, log: log}
}

// Start begins the evaluation loop.
func (s *Scheduler) Start() {
	s.cron = cron.New()
	s.cron.AddFunc("@every 1m", func() {
		ctx := context.Background()
		if err := s.svc.EvaluateAll(ctx); err != nil {
			s.log.Error("alert evaluation", zap.Error(err))
			return
		}
	})
	s.cron.Start()
	s.log.Info("alert scheduler started")
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cron != nil {
		s.cron.Stop()
	}
}

// Health returns 200 OK.
func (s *Scheduler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","service":"alert-engine"}`))
}
