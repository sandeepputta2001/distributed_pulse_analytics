// Package main implements the Notification & Campaign Service.
//
// Responsibilities:
//   - Consume Kafka topics (agg-results, session-events) for behavioural triggers
//   - Evaluate campaign rules stored in PostgreSQL
//   - Deliver notifications via webhook, email (SMTP), and mobile push (FCM)
//   - Expose a REST API for campaign CRUD and delivery analytics
//
// Endpoints:
//
//	POST   /v1/campaigns              Create campaign
//	GET    /v1/campaigns/{id}         Get campaign
//	PUT    /v1/campaigns/{id}         Update campaign
//	POST   /v1/campaigns/{id}/pause   Pause campaign
//	POST   /v1/campaigns/{id}/resume  Resume campaign
//	GET    /v1/campaigns/{id}/stats   Delivery statistics
//	GET    /health
//	GET    /metrics
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/auth"
	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/kafka"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	"github.com/pulse-analytics/internal/postgres"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/tracing"
)

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/base.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "notification-service"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())

	m := metrics.NewRegistry(cfg.Service.Name)

	redis, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer redis.Close()

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pg.Close()

	authSvc := auth.NewService(pg, redis, &cfg.Auth, log)

	// Kafka consumer — listens for triggerable events
	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{"agg-results", "session-events"}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}

	dispatcher := &notifDispatcher{pg: pg, redis: redis, log: log, m: m}

	// Consume triggers in background; MessageHandler receives (ctx, key, value []byte)
	go func() {
		if err := consumer.ConsumeLoop(ctx, func(ctx context.Context, _, value []byte) error {
			return dispatcher.handleKafkaMessage(ctx, value)
		}); err != nil {
			log.Error("kafka consumer stopped", zap.Error(err))
		}
	}()

	h := &notifHandler{pg: pg, m: m, log: log, authSvc: authSvc}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}))

	r.Get("/health", h.health)
	r.Handle("/metrics", m.Handler())

	r.Route("/v1/campaigns", func(r chi.Router) {
		r.Use(authSvc.JWTMiddleware)
		r.Post("/", h.create)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Post("/{id}/pause", h.pause)
		r.Post("/{id}/resume", h.resume)
		r.Get("/{id}/stats", h.stats)
	})

	addr := envOrDefault("LISTEN_ADDR", ":8084")
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("notification-service starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	cancel() // stop Kafka consumer
	log.Info("shutting down notification-service...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}

// ─── Dispatcher (Kafka trigger evaluation) ────────────────────────────────────

type notifDispatcher struct {
	pg    *postgres.Client
	redis *redisclient.Client
	log   *zap.Logger
	m     *metrics.Registry
}

// handleKafkaMessage receives every event from subscribed Kafka topics and
// evaluates it against all active campaigns matching the app_id + trigger type.
func (d *notifDispatcher) handleKafkaMessage(ctx context.Context, value []byte) error {
	var evt struct {
		AppID     string            `json:"app_id"`
		EventName string            `json:"event_name"`
		UserID    string            `json:"user_id"`
		DeviceID  string            `json:"device_id"`
		Props     map[string]string `json:"props"`
	}
	if err := json.Unmarshal(value, &evt); err != nil {
		return nil // skip malformed messages
	}

	campaigns, err := d.pg.GetActiveCampaignsByTrigger(ctx, evt.AppID, evt.EventName)
	if err != nil {
		d.log.Error("fetch campaigns", zap.Error(err))
		return nil
	}

	for _, c := range campaigns {
		// Dedup: skip if already notified this user in cooldown window (30 min)
		cooldownKey := fmt.Sprintf("notif:cooldown:%s:%s", c.ID, evt.UserID)
		if exists, _ := d.redis.Exists(ctx, cooldownKey); exists {
			continue
		}

		go func(camp *models.Campaign) {
			if err := d.dispatch(ctx, camp, evt.UserID, evt.Props); err != nil {
				d.log.Error("dispatch failed", zap.String("campaign", camp.ID), zap.Error(err))
				return
			}
			_ = d.redis.Set(ctx, cooldownKey, "1", 30*time.Minute)
			d.log.Info("notification dispatched",
				zap.String("campaign", camp.ID),
				zap.String("user", evt.UserID),
				zap.String("channel", camp.Channel),
			)
		}(c)
	}
	return nil
}

// dispatch routes the notification to the appropriate delivery channel.
func (d *notifDispatcher) dispatch(ctx context.Context, c *models.Campaign, userID string, props map[string]string) error {
	switch c.Channel {
	case "webhook":
		return d.sendWebhook(ctx, c, userID, props)
	case "email":
		return d.sendEmail(c, userID)
	case "push":
		return d.sendPush(c, userID)
	default:
		return fmt.Errorf("unknown channel: %s", c.Channel)
	}
}

func (d *notifDispatcher) sendWebhook(ctx context.Context, c *models.Campaign, userID string, props map[string]string) error {
	url := c.ChannelConf["url"]
	if url == "" {
		return errors.New("webhook url not configured")
	}
	payload, _ := json.Marshal(map[string]any{
		"campaign_id": c.ID,
		"app_id":      c.AppID,
		"user_id":     userID,
		"event":       c.TriggerConf["event_name"],
		"props":       props,
		"fired_at":    time.Now().UTC(),
	})

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if secret := c.ChannelConf["secret"]; secret != "" {
		req.Header.Set("X-Pulse-Signature", secret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (d *notifDispatcher) sendEmail(c *models.Campaign, _ string) error {
	smtpHost := c.ChannelConf["smtp_host"]
	smtpPort := c.ChannelConf["smtp_port"]
	from := c.ChannelConf["from"]
	to := c.ChannelConf["to_template"]
	subject := c.ChannelConf["subject"]
	body := c.ChannelConf["body"]

	if smtpHost == "" || from == "" || to == "" {
		return errors.New("incomplete email config")
	}

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", from, to, subject, body)
	addr := smtpHost + ":" + smtpPort
	return smtp.SendMail(addr, nil, from, []string{to}, []byte(msg))
}

func (d *notifDispatcher) sendPush(c *models.Campaign, userID string) error {
	fcmURL := "https://fcm.googleapis.com/fcm/send"
	serverKey := c.ChannelConf["fcm_server_key"]
	if serverKey == "" {
		return errors.New("fcm_server_key not configured")
	}

	payload, _ := json.Marshal(map[string]any{
		"to": "/topics/" + c.AppID + "-" + userID,
		"notification": map[string]string{
			"title": c.ChannelConf["title"],
			"body":  c.ChannelConf["body"],
		},
		"data": map[string]string{
			"campaign_id": c.ID,
			"app_id":      c.AppID,
		},
	})

	req, _ := http.NewRequest(http.MethodPost, fcmURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "key="+serverKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("FCM returned %d", resp.StatusCode)
	}
	return nil
}

// ─── HTTP Handlers ────────────────────────────────────────────────────────────

type notifHandler struct {
	pg      *postgres.Client
	m       *metrics.Registry
	log     *zap.Logger
	authSvc *auth.Service
}

func (h *notifHandler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "notification-service"})
}

// create — POST /v1/campaigns
func (h *notifHandler) create(w http.ResponseWriter, r *http.Request) {
	var c models.Campaign
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if c.AppID == "" || c.Name == "" {
		writeError(w, http.StatusBadRequest, "app_id and name required")
		return
	}
	c.Active = true
	c.CreatedAt = time.Now().UTC()

	id, err := h.pg.CreateCampaign(r.Context(), &c)
	if err != nil {
		h.log.Error("create campaign", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	c.ID = id
	writeJSON(w, http.StatusCreated, c)
}

// get — GET /v1/campaigns/{id}
func (h *notifHandler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := h.pg.GetCampaign(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// update — PUT /v1/campaigns/{id}
func (h *notifHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var c models.Campaign
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	c.ID = id
	if err := h.pg.UpdateCampaign(r.Context(), &c); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// pause — POST /v1/campaigns/{id}/pause
func (h *notifHandler) pause(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.pg.SetCampaignActive(r.Context(), id, false); err != nil {
		writeError(w, http.StatusInternalServerError, "pause failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// resume — POST /v1/campaigns/{id}/resume
func (h *notifHandler) resume(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.pg.SetCampaignActive(r.Context(), id, true); err != nil {
		writeError(w, http.StatusInternalServerError, "resume failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

// stats — GET /v1/campaigns/{id}/stats
func (h *notifHandler) stats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	stats, err := h.pg.GetCampaignStats(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats query failed")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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
