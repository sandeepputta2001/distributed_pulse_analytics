// Package main implements the Ingest Gateway service.
//
// @title        PulseAnalytics — Ingest Gateway API
// @version      1.0.0
// @description  High-throughput event ingestion API. Accepts batches of up to 500 events per request at up to 100M events/second across the cluster.
//
// @contact.name   PulseAnalytics Support
// @contact.url    https://pulse-analytics.io/support
// @contact.email  support@pulse-analytics.io
//
// @license.name  MIT
//
// @host      localhost:8080
// @BasePath  /
// @schemes   http https
//
// @securityDefinitions.apikey  ApiKeyAuth
// @in                          header
// @name                        X-API-Key
// @description                 API key issued per application. Obtain from the PulseAnalytics dashboard.
//
// @tag.name         ingest
// @tag.description  Event ingestion endpoints
// @tag.name         system
// @tag.description  Health and readiness checks
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	httpSwagger "github.com/swaggo/http-swagger"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/auth"
	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/dedup"
	_ "github.com/pulse-analytics/docs/gateway" // swagger docs
	"github.com/pulse-analytics/internal/geo"
	"github.com/pulse-analytics/internal/health"
	"github.com/pulse-analytics/internal/kafka"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	"github.com/pulse-analytics/internal/mongo"
	"github.com/pulse-analytics/internal/postgres"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/ratelimit"
	"github.com/pulse-analytics/internal/tracing"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/gateway.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("failed to load config", zap.Error(err))
	}
	cfg.Service.Name = "gateway"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ─── Telemetry ────────────────────────────────────────────────────────────
	tp, err := tracing.Init(ctx, &cfg.Telemetry, log)
	if err != nil {
		log.Warn("tracing init failed", zap.Error(err))
	}
	defer tp.Shutdown(context.Background())

	m := metrics.NewRegistry(cfg.Service.Name)

	// ─── Infrastructure Clients ───────────────────────────────────────────────
	redis, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis connect", zap.Error(err))
	}
	defer redis.Close()

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres connect", zap.Error(err))
	}
	defer pg.Close()

	mgo, err := mongo.NewClient(&cfg.Mongo, log)
	if err != nil {
		log.Warn("mongo connect failed (non-fatal)", zap.Error(err))
	}
	if mgo != nil {
		if err := mgo.EnsureIndexes(ctx); err != nil {
			log.Warn("mongo ensure indexes", zap.Error(err))
		}
		defer mgo.Close(ctx)
	}

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	// ─── Application Services ─────────────────────────────────────────────────
	authSvc := auth.NewService(pg, redis, &cfg.Auth, log)
	limiter := ratelimit.NewLimiter(redis, log, cfg.RateLimit.CleanupInterval)
	defer limiter.Close()

	filter := dedup.NewFilter(
		cfg.Bloom.Capacity,
		cfg.Bloom.FalsePositive,
		cfg.Bloom.WindowTTL,
		redis,
		log,
	)

	geoResolver, err := geo.NewResolver(cfg.GeoIP.DBPath, log)
	if err != nil {
		log.Warn("geoip resolver", zap.Error(err))
	}
	if geoResolver != nil {
		defer geoResolver.Close()
	}

	// ─── Deep Readiness Checker ───────────────────────────────────────────────
	readyChecker := health.New(3 * time.Second)
	readyChecker.AddCritical("kafka", func(ctx context.Context) error {
		return producer.PublishSync(ctx, "_health", []byte("ping"), "pong")
	})
	readyChecker.AddCritical("redis", func(ctx context.Context) error {
		return redis.Ping(ctx)
	})
	readyChecker.AddCritical("postgres", func(ctx context.Context) error {
		return pg.Ping(ctx)
	})
	if mgo != nil {
		readyChecker.AddOptional("mongo", func(ctx context.Context) error {
			return mgo.Ping(ctx)
		})
	}

	// ─── HTTP Router ──────────────────────────────────────────────────────────
	handler := &gatewayHandler{
		cfg:     cfg,
		kafka:   producer,
		auth:    authSvc,
		limiter: limiter,
		dedup:   filter,
		geo:     geoResolver,
		mongo:   mgo,
		metrics: m,
		log:     log,
		ready:   readyChecker,
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key", "Content-Encoding"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(prometheusMiddleware(m))

	// Health + readiness
	r.Get("/health", handler.health)
	r.Get("/ready", handler.readinessCheck)

	// Metrics scrape endpoint
	r.Handle("/metrics", m.Handler())

	// Swagger UI — accessible at /swagger/index.html
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
		httpSwagger.DeepLinking(true),
		httpSwagger.DocExpansion("list"),
		httpSwagger.DomID("swagger-ui"),
	))

	// Ingest routes — all require API key auth
	r.Group(func(r chi.Router) {
		r.Use(authSvc.APIKeyMiddleware(limiter))
		r.Post("/v1/events", handler.handleIngest)
		r.Post("/v1/identify", handler.handleIdentify)
		r.Post("/v1/track", handler.handleTrack)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		Handler:      r,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// ─── Start ────────────────────────────────────────────────────────────────
	go func() {
		log.Info("gateway starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gateway...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer shutdownCancel()

	if err := producer.Flush(shutdownCtx); err != nil {
		log.Error("kafka flush failed", zap.Error(err))
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown failed", zap.Error(err))
	}
}

// ─── Handler ──────────────────────────────────────────────────────────────────

type gatewayHandler struct {
	cfg     *config.Config
	kafka   *kafka.Producer
	auth    *auth.Service
	limiter *ratelimit.Limiter
	dedup   *dedup.Filter
	geo     *geo.Resolver
	mongo   *mongo.Client
	metrics *metrics.Registry
	log     *zap.Logger
	ready   *health.Checker
}

// health godoc
// @Summary     Health check
// @Description Returns service health status. Used by load balancer and readiness probes.
// @Tags        system
// @Produce     json
// @Success     200 {object} map[string]string "ok"
// @Router      /health [get]
func (h *gatewayHandler) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "gateway"})
}

// handleIngest godoc
// @Summary     Ingest event batch
// @Description Accepts a batch of up to 500 analytics events from SDK clients. Authenticates via X-API-Key, applies per-app rate limiting (token bucket), deduplicates via Bloom filter, and publishes to Kafka raw-events topic asynchronously. Returns 202 immediately.
// @Tags        ingest
// @Accept      json
// @Produce     json
// @Param       Content-Encoding  header    string            false  "Set to 'gzip' when body is gzip-compressed"
// @Param       batch             body      models.EventBatch true   "Event batch (max 500 events)"
// @Success     202               {object}  map[string]interface{}   "Accepted — events queued for processing"
// @Failure     400               {object}  map[string]string        "Invalid payload or batch size exceeded"
// @Failure     401               {object}  map[string]string        "Missing or invalid X-API-Key"
// @Failure     429               {object}  map[string]string        "Rate limit exceeded for this app"
// @Failure     500               {object}  map[string]string        "Internal server error"
// @Security    ApiKeyAuth
// @Router      /v1/events [post]
func (h *gatewayHandler) handleIngest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, span := tracing.Tracer("gateway").Start(r.Context(), "ingest")
	defer span.End()

	app, _ := auth.AppFromContext(ctx)

	// Decompress body
	body, err := readBody(r, h.cfg.HTTP.MaxBodyBytes)
	if err != nil {
		h.metrics.IngestErrors.WithLabelValues("body_read").Inc()
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	// Parse batch
	var batch models.EventBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		h.metrics.IngestErrors.WithLabelValues("parse").Inc()
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if len(batch.Events) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(batch.Events) > 500 {
		http.Error(w, `{"error":"max 500 events per batch"}`, http.StatusBadRequest)
		return
	}

	// Override app_id with authenticated app
	batch.AppID = app.ID

	// Enrich with server-side IP geolocation
	clientIP := geo.ExtractIP(r.Header.Get("X-Forwarded-For"), r.RemoteAddr)
	span.SetAttributes(
		attribute.String("app_id", app.ID),
		attribute.Int("event_count", len(batch.Events)),
		attribute.String("client_ip", clientIP),
	)

	// Deduplicate events via Bloom filter
	originalCount := len(batch.Events)
	newEvents := make([]models.Event, 0, len(batch.Events))
	for _, e := range batch.Events {
		if e.EventID == "" {
			e.EventID = models.NewEventID()
		}
		if h.dedup.TestAndAdd(ctx, e.EventID) {
			newEvents = append(newEvents, e)
		}
	}
	filtered := originalCount - len(newEvents)
	if filtered > 0 {
		h.metrics.DuplicatesFiltered.Add(float64(filtered))
		h.log.Debug("dedup filtered",
			zap.String("app_id", app.ID),
			zap.Int("filtered", filtered),
			zap.Int("remaining", len(newEvents)),
		)
	}

	if len(newEvents) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	batch.Events = newEvents

	// Attach client IP and server time to batch metadata
	// (enrichment service will do full GeoIP + UA parsing)
	batchWithMeta := map[string]any{
		"batch":     batch,
		"client_ip": clientIP,
		"server_ts": time.Now().UnixMilli(),
	}

	// Publish to Kafka raw-events (partitioned by app_id:device_id for ordering)
	partitionKey := []byte(app.ID + ":" + batch.DeviceID)
	if err := h.kafka.PublishAsync(h.cfg.Kafka.TopicRawEvents, partitionKey, batchWithMeta); err != nil {
		h.log.Error("kafka publish failed",
			zap.String("app_id", app.ID),
			zap.Error(err),
		)
		h.metrics.IngestErrors.WithLabelValues("kafka").Inc()
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Store raw events in MongoDB (async, non-blocking for latency)
	if h.mongo != nil {
		go func() {
			mgoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.mongo.InsertRawBatch(mgoCtx, app.ID, batch.Events); err != nil {
				h.log.Warn("mongo insert failed", zap.Error(err))
			}
		}()
	}

	// Record metrics
	h.metrics.IngestEvents.Add(float64(len(newEvents)))
	h.metrics.IngestBatchSize.Observe(float64(len(newEvents)))
	h.metrics.IngestRequests.WithLabelValues("202").Inc()
	h.metrics.IngestLatency.WithLabelValues("ingest").Observe(time.Since(start).Seconds())

	// Acknowledge immediately — don't wait for Kafka ack
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted": len(newEvents),
		"filtered": filtered,
	})
}

// handleIdentify godoc
// @Summary     Identify / update user profile
// @Description Updates user traits (email, plan, custom properties). Synthesizes a user_updated event and upserts the user document in MongoDB for segmentation.
// @Tags        ingest
// @Accept      json
// @Produce     json
// @Param       payload  body      object  true  "Identity payload with user_id and traits map"
// @Success     202      "Accepted"
// @Failure     400      {object}  map[string]string  "Missing user_id or invalid JSON"
// @Failure     401      {object}  map[string]string  "Unauthorized"
// @Security    ApiKeyAuth
// @Router      /v1/identify [post]
func (h *gatewayHandler) handleIdentify(w http.ResponseWriter, r *http.Request) {
	app, _ := auth.AppFromContext(r.Context())

	var payload struct {
		UserID string         `json:"user_id"`
		Traits map[string]any `json:"traits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Synthesize user_updated event
	evt := models.Event{
		EventID:   models.NewEventID(),
		EventName: "user_updated",
		EventTime: models.NowMs(),
		Props:     payload.Traits,
	}
	batch := map[string]any{
		"batch": models.EventBatch{
			AppID:  app.ID,
			UserID: payload.UserID,
			Events: []models.Event{evt},
		},
		"server_ts": models.NowMs(),
	}

	key := []byte(app.ID + ":" + payload.UserID)
	if err := h.kafka.PublishAsync(h.cfg.Kafka.TopicRawEvents, key, batch); err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Upsert in Mongo
	if h.mongo != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.mongo.UpsertUserProfile(ctx, app.ID, payload.UserID, payload.Traits)
		}()
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleTrack godoc
// @Summary     Track a single event
// @Description Single-event shorthand. Equivalent to POST /v1/events with a one-event batch. Useful for server-side tracking where batching is not needed.
// @Tags        ingest
// @Accept      json
// @Produce     json
// @Param       event  body      models.Event  true  "Single event object"
// @Success     202    "Accepted"
// @Failure     400    {object}  map[string]string  "Invalid JSON"
// @Failure     401    {object}  map[string]string  "Unauthorized"
// @Security    ApiKeyAuth
// @Router      /v1/track [post]
func (h *gatewayHandler) handleTrack(w http.ResponseWriter, r *http.Request) {
	app, _ := auth.AppFromContext(r.Context())

	var evt models.Event
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if evt.EventID == "" {
		evt.EventID = models.NewEventID()
	}
	if evt.EventTime == 0 {
		evt.EventTime = models.NowMs()
	}

	batch := map[string]any{
		"batch": models.EventBatch{
			AppID:  app.ID,
			Events: []models.Event{evt},
		},
		"server_ts": models.NowMs(),
	}
	if err := h.kafka.PublishAsync(h.cfg.Kafka.TopicRawEvents, []byte(app.ID), batch); err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// readinessCheck runs all registered dependency checks in parallel and
// returns 200 when all critical dependencies are healthy, 503 otherwise.
// The JSON body includes per-dependency latency and error detail.
func (h *gatewayHandler) readinessCheck(w http.ResponseWriter, r *http.Request) {
	h.ready.Handler()(w, r)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	var reader io.Reader = http.MaxBytesReader(nil, r.Body, maxBytes)

	switch r.Header.Get("Content-Encoding") {
	case "gzip":
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}

	return io.ReadAll(reader)
}

func prometheusMiddleware(m *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			status := fmt.Sprintf("%d", ww.Status())
			m.IngestRequests.WithLabelValues(status).Inc()
			m.IngestLatency.WithLabelValues(r.URL.Path).Observe(time.Since(start).Seconds())
		})
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newLogger() *zap.Logger {
	env := os.Getenv("PULSE_SERVICE_ENVIRONMENT")
	if env == "production" {
		l, _ := zap.NewProduction()
		return l
	}
	l, _ := zap.NewDevelopment()
	return l
}
