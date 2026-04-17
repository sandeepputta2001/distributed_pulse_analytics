// Package main boots the Ingest Gateway service.
//
// @title        PulseAnalytics — Ingest Gateway
// @version      1.0.0
// @description  High-throughput event ingestion. Accepts batches up to 500 events, deduplicates, rate-limits, and publishes to Kafka raw-events topic.
// @host         localhost:8080
// @BasePath     /
// @schemes      http https
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	httpSwagger "github.com/swaggo/http-swagger"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/auth"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/dedup"
	"github.com/pulse-analytics/shared/pkg/geo"
	"github.com/pulse-analytics/shared/pkg/health"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/mongo"
	"github.com/pulse-analytics/shared/pkg/postgres"
	"github.com/pulse-analytics/shared/pkg/ratelimit"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/gateway/internal/handler"
	"github.com/pulse-analytics/gateway/internal/repo"
	"github.com/pulse-analytics/gateway/internal/service"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "gateway"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Telemetry ─────────────────────────────────────────────────────────────
	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)

	// ── Infrastructure ────────────────────────────────────────────────────────
	redisClient, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer redisClient.Close()

	pgClient, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pgClient.Close()

	mgoClient, err := mongo.NewClient(&cfg.Mongo, log)
	if err != nil {
		log.Warn("mongo (non-fatal)", zap.Error(err))
	}
	if mgoClient != nil {
		_ = mgoClient.EnsureIndexes(ctx)
		defer mgoClient.Close(ctx)
	}

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	// ── Application Services ──────────────────────────────────────────────────
	authSvc := auth.NewService(pgClient, redisClient, &cfg.Auth, log)
	limiter := ratelimit.NewLimiter(redisClient, log, cfg.RateLimit.CleanupInterval)
	defer limiter.Close()

	dedupFilter := dedup.NewFilter(cfg.Bloom.Capacity, cfg.Bloom.FalsePositive, cfg.Bloom.WindowTTL, redisClient, log)
	geoResolver, _ := geo.NewResolver(cfg.GeoIP.DBPath, log)
	if geoResolver != nil {
		defer geoResolver.Close()
	}

	// ── Health Checker ────────────────────────────────────────────────────────
	readyChecker := health.New(3 * time.Second)
	readyChecker.AddCritical("kafka", func(ctx context.Context) error {
		return producer.PublishSync(ctx, "_health", []byte("ping"), "pong")
	})
	readyChecker.AddCritical("redis", func(ctx context.Context) error { return redisClient.Ping(ctx) })
	readyChecker.AddCritical("postgres", func(ctx context.Context) error { return pgClient.Ping(ctx) })
	if mgoClient != nil {
		readyChecker.AddOptional("mongo", func(ctx context.Context) error { return mgoClient.Ping(ctx) })
	}

	// ── Wire up handler/service/repo ──────────────────────────────────────────
	gatewayRepo := repo.New(pgClient, mgoClient, log)
	ingestSvc := service.New(producer, dedupFilter, limiter, m, log, cfg.Kafka.TopicRawEvents)
	h := handler.New(ingestSvc, gatewayRepo, geoResolver, authSvc, m, log, readyChecker, cfg.HTTP.MaxBodyBytes)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.RequestID, middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "X-API-Key", "Content-Encoding"},
		MaxAge:         300,
	}))
	r.Use(handler.PrometheusMiddleware(m))

	r.Get("/health", h.Health)
	r.Get("/ready", h.Ready)
	r.Handle("/metrics", m.Handler())
	r.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	r.Group(func(r chi.Router) {
		r.Use(authSvc.APIKeyMiddleware(limiter))
		r.Post("/v1/events", h.HandleIngest)
		r.Post("/v1/identify", h.HandleIdentify)
		r.Post("/v1/track", h.HandleTrack)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		Handler:      r,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	go func() {
		log.Info("gateway starting", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gateway...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer shutCancel()
	_ = producer.Flush(shutCtx)
	_ = srv.Shutdown(shutCtx)
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
