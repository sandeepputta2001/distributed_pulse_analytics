// Package service implements alert evaluation logic.
package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/alert-engine/internal/repo"
)

// AlertService evaluates alert rules against live ClickHouse metrics
// and publishes fired alerts to Kafka.
type AlertService struct {
	repo      *repo.Repo
	publisher *kafka.Producer
	topic     string
	log       *zap.Logger
}

// New creates an AlertService.
func New(r *repo.Repo, pub *kafka.Producer, topic string, log *zap.Logger) *AlertService {
	return &AlertService{repo: r, publisher: pub, topic: topic, log: log}
}

// EvaluateAll loads all active alert rules and evaluates each one.
// Fired alerts are published to Kafka for the notification service.
func (s *AlertService) EvaluateAll(ctx context.Context) error {
	rules, err := s.repo.ListActiveAlerts(ctx)
	if err != nil {
		return fmt.Errorf("list alerts: %w", err)
	}

	for _, rule := range rules {
		if !rule.Active {
			continue
		}
		if err := s.evaluate(ctx, rule); err != nil {
			s.log.Warn("alert eval failed", zap.String("rule_id", rule.ID), zap.Error(err))
		}
	}
	return nil
}

// evaluate checks a single alert rule and fires if threshold is breached.
func (s *AlertService) evaluate(ctx context.Context, rule *models.AlertRule) error {
	val, err := s.repo.QueryMetricValue(ctx, rule.AppID, rule.MetricName, rule.WindowMins)
	if err != nil {
		return fmt.Errorf("query metric %s: %w", rule.MetricName, err)
	}

	fired := compare(val, rule.Condition, rule.Threshold)
	if !fired {
		return nil
	}

	s.log.Info("alert fired",
		zap.String("rule", rule.ID),
		zap.String("metric", rule.MetricName),
		zap.Float64("value", val),
		zap.Float64("threshold", rule.Threshold),
	)

	now := time.Now()
	event := models.AlertFiredEvent{
		RuleID:    rule.ID,
		AppID:     rule.AppID,
		Metric:    rule.MetricName,
		Value:     val,
		Threshold: rule.Threshold,
		Operator:  rule.Condition,
		FiredAt:   now,
	}
	if err := s.publisher.PublishJSON(ctx, s.topic, rule.AppID, event); err != nil {
		return err
	}
	// Persist last_fired_at so the query-api can surface it to the frontend.
	if err := s.repo.UpdateAlertLastFired(ctx, rule.ID, now); err != nil {
		s.log.Warn("update last_fired_at", zap.String("rule_id", rule.ID), zap.Error(err))
	}
	return nil
}

// compare evaluates value op threshold.
func compare(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	}
	return false
}
