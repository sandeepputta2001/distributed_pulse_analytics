// Package service implements notification delivery logic.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/notification/internal/repo"
)

// NotificationService delivers alerts via configured channels (webhook, email stub).
type NotificationService struct {
	repo       *repo.Repo
	httpClient *http.Client
	log        *zap.Logger
}

// New creates a NotificationService.
func New(r *repo.Repo, log *zap.Logger) *NotificationService {
	return &NotificationService{
		repo:       r,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		log:        log,
	}
}

// Deliver handles an AlertFiredEvent: looks up the app's notification config
// and dispatches via the appropriate channel(s).
func (s *NotificationService) Deliver(ctx context.Context, evt models.AlertFiredEvent) error {
	app, err := s.repo.GetApp(ctx, evt.AppID)
	if err != nil {
		return fmt.Errorf("get app: %w", err)
	}

	delivery := &models.NotificationDelivery{
		ID:        fmt.Sprintf("nd-%d", time.Now().UnixNano()),
		RuleID:    evt.RuleID,
		AppID:     evt.AppID,
		Channel:   app.NotificationChannel,
		SentAt:    time.Now(),
		Payload:   evt,
	}

	var deliveryErr error
	switch app.NotificationChannel {
	case "webhook":
		deliveryErr = s.sendWebhook(ctx, app.WebhookURL, evt)
	case "email":
		deliveryErr = s.sendEmail(ctx, app.NotificationEmail, evt)
	default:
		s.log.Warn("unknown notification channel", zap.String("channel", app.NotificationChannel))
	}

	if deliveryErr != nil {
		delivery.Status = "failed"
		delivery.Error = deliveryErr.Error()
		s.log.Error("notification delivery failed",
			zap.String("app_id", evt.AppID),
			zap.String("channel", app.NotificationChannel),
			zap.Error(deliveryErr),
		)
	} else {
		delivery.Status = "delivered"
	}

	if err := s.repo.SaveDelivery(ctx, delivery); err != nil {
		s.log.Warn("save delivery record", zap.Error(err))
	}
	return deliveryErr
}

// sendWebhook POSTs the fired alert as JSON to the configured URL.
func (s *NotificationService) sendWebhook(ctx context.Context, url string, evt models.AlertFiredEvent) error {
	if url == "" {
		return fmt.Errorf("webhook URL not configured")
	}
	body, _ := json.Marshal(evt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// sendEmail is a stub — integrate with SendGrid / SES in production.
func (s *NotificationService) sendEmail(ctx context.Context, to string, evt models.AlertFiredEvent) error {
	s.log.Info("email notification (stub)",
		zap.String("to", to),
		zap.String("rule_id", evt.RuleID),
		zap.String("metric", evt.Metric),
		zap.Float64("value", evt.Value),
	)
	return nil
}
