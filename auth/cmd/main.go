// Package main boots the Auth service.
// Endpoints: POST /v1/auth/register, /token, /refresh, GET /v1/auth/validate, POST /v1/auth/apikey/rotate
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
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/auth"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/postgres"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/auth/internal/handler"
	"github.com/pulse-analytics/auth/internal/repo"
	"github.com/pulse-analytics/auth/internal/service"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "auth"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)

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

	// Wire up handler/service/repo
	authRepo := repo.New(pgClient, log)
	jwtSvc := auth.NewService(pgClient, redisClient, &cfg.Auth, log)
	authSvc := service.New(authRepo, jwtSvc, redisClient, &cfg.Auth, log)
	h := handler.New(authSvc, m, log)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.RequestID, middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}))

	r.Get("/health", h.Health)
	r.Handle("/metrics", m.Handler())

	r.Route("/v1/auth", func(r chi.Router) {
		// Public
		r.Post("/register", h.Register)
		r.Post("/token", h.Token)
		r.Post("/refresh", h.Refresh)
		// Protected — JWT required
		r.Group(func(r chi.Router) {
			r.Use(jwtSvc.JWTMiddleware)
			r.Get("/validate", h.Validate)
			r.Post("/apikey/rotate", h.RotateAPIKey)
		})
	})

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	if cfg.HTTP.Port == 0 {
		addr = ":8083"
	}
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("auth service starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down auth service...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
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
