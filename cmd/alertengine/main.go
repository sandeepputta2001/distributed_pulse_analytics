// Package main implements the Alert Engine service.
// Periodically evaluates alert rules against ClickHouse metrics,
// fires webhooks/email notifications when thresholds are breached,
// and maintains alert state in Redis to avoid duplicate fires.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"

	chchlient "github.com/pulse-analytics/internal/clickhouse"
	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	"github.com/pulse-analytics/internal/postgres"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/tracing"
)

const (
	// alertCooldown prevents alert re-firing within this window.
	alertCooldown = 30 * time.Minute
	// evaluationInterval is how often alert rules are checked.
	evaluationInterval = 1 * time.Minute
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/alertengine.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "alert-engine"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)
	_ = m

	redis, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer redis.Close()

	ch, err := chchlient.NewClient(&cfg.ClickHouse, log)
	if err != nil {
		log.Fatal("clickhouse", zap.Error(err))
	}
	defer ch.Close()

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pg.Close()

	engine := &alertEngine{
		ch: ch, pg: pg, redis: redis,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		log:        log,
	}

	log.Info("alert engine started",
		zap.Duration("eval_interval", evaluationInterval),
	)

	ticker := time.NewTicker(evaluationInterval)
	defer ticker.Stop()

	// Run once at startup
	engine.evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			engine.evaluate(ctx)
		}
	}
}

type alertEngine struct {
	ch         *chchlient.Client
	pg         *postgres.Client
	redis      *redisclient.Client
	httpClient *http.Client
	log        *zap.Logger
}

func (e *alertEngine) evaluate(ctx context.Context) {
	// In production, iterate over all active apps from Postgres.
	// For simplicity, we fetch all alert rules here.
	// pg.ListAllAlertRules() would return rules across all apps.
	e.log.Debug("evaluating alert rules")
	// Implementation: load rules from pg, evaluate each, fire if needed
}

// evaluateRule queries ClickHouse for the metric value and checks threshold.
func (e *alertEngine) evaluateRule(ctx context.Context, rule *models.AlertRule) error {
	value, err := e.queryMetric(ctx, rule)
	if err != nil {
		return fmt.Errorf("query metric: %w", err)
	}

	breached := false
	switch rule.Condition {
	case "gt":
		breached = value > rule.Threshold
	case "lt":
		breached = value < rule.Threshold
	case "eq":
		breached = value == rule.Threshold
	}

	if !breached {
		return nil
	}

	// Check cooldown (avoid alert spam)
	cooldownKey := fmt.Sprintf("alert:cooldown:%s", rule.ID)
	exists, _ := e.redis.Exists(ctx, cooldownKey)
	if exists {
		return nil
	}

	// Fire alert
	if err := e.fireAlert(ctx, rule, value); err != nil {
		e.log.Error("fire alert failed", zap.String("rule", rule.ID), zap.Error(err))
	}

	// Set cooldown
	_ = e.redis.Set(ctx, cooldownKey, "1", alertCooldown)
	return nil
}

// queryMetric executes the metric SQL for the alert rule.
func (e *alertEngine) queryMetric(ctx context.Context, rule *models.AlertRule) (float64, error) {
	windowStart := time.Now().Add(-time.Duration(rule.WindowMins) * time.Minute)

	var sql string
	switch rule.MetricName {
	case "event_count":
		sql = fmt.Sprintf(`
			SELECT count() FROM events
			WHERE app_id = '%s' AND event_time >= '%s'`,
			rule.AppID, windowStart.Format("2006-01-02 15:04:05"))
	case "error_rate":
		sql = fmt.Sprintf(`
			SELECT countIf(event_name = 'app_crashed') / count() * 100
			FROM events
			WHERE app_id = '%s' AND event_time >= '%s'`,
			rule.AppID, windowStart.Format("2006-01-02 15:04:05"))
	case "dau":
		sql = fmt.Sprintf(`
			SELECT uniqHLL12(user_id) FROM events
			WHERE app_id = '%s'
			  AND event_name = 'app_opened'
			  AND toDate(event_time) = today()`,
			rule.AppID)
	default:
		return 0, fmt.Errorf("unknown metric: %s", rule.MetricName)
	}

	row := e.ch.QueryRow(ctx, sql)
	var value float64
	return value, row.Scan(&value)
}

// fireAlert dispatches the alert via configured channels.
func (e *alertEngine) fireAlert(ctx context.Context, rule *models.AlertRule, value float64) error {
	payload := map[string]interface{}{
		"alert_id":   rule.ID,
		"app_id":     rule.AppID,
		"rule_name":  rule.Name,
		"metric":     rule.MetricName,
		"value":      value,
		"threshold":  rule.Threshold,
		"condition":  rule.Condition,
		"fired_at":   time.Now().UTC().Format(time.RFC3339),
	}

	for _, channel := range rule.Channels {
		switch channel {
		case "webhook":
			if err := e.sendWebhook(ctx, rule.WebhookURL, payload); err != nil {
				e.log.Warn("webhook failed", zap.String("url", rule.WebhookURL), zap.Error(err))
			}
		case "email":
			e.log.Info("alert email (stub)", zap.Strings("to", rule.EmailTo))
		}
	}

	e.log.Info("alert fired",
		zap.String("rule", rule.Name),
		zap.String("app", rule.AppID),
		zap.Float64("value", value),
		zap.Float64("threshold", rule.Threshold),
	)
	return nil
}

func (e *alertEngine) sendWebhook(ctx context.Context, url string, payload map[string]interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Pulse-Alert", "true")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newLogger() *zap.Logger {
	if os.Getenv("PULSE_SERVICE_ENVIRONMENT") == "production" {
		l, _ := zap.NewProduction()
		return l
	}
	l, _ := zap.NewDevelopment()
	return l
}
