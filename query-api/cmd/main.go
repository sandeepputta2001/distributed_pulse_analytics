// main.go — Query API service entrypoint.
package main

import (
	"context"
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
	"github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/postgres"
	"github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/query-api/internal/handler"
	"github.com/pulse-analytics/query-api/internal/repo"
	"github.com/pulse-analytics/query-api/internal/service"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg, err := config.Load(envOr("CONFIG_PATH", "configs/config.yaml"))
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}

	// ── Tracing ──────────────────────────────────────────────────────────────
	tp, err := tracing.Init(context.Background(), &cfg.Telemetry, log)
	if err != nil {
		log.Warn("tracing init", zap.Error(err))
	} else {
		defer tp.Shutdown(context.Background())
	}

	// ── Metrics ───────────────────────────────────────────────────────────────
	m := metrics.NewRegistry("query_api")

	// ── Postgres ──────────────────────────────────────────────────────────────
	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres connect", zap.Error(err))
	}
	defer pg.Close()

	// ── ClickHouse ────────────────────────────────────────────────────────────
	ch, err := clickhouse.NewClient(&cfg.ClickHouse, log)
	if err != nil {
		log.Fatal("clickhouse connect", zap.Error(err))
	}
	defer ch.Close()

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb, err := redis.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis connect", zap.Error(err))
	}
	defer rdb.Close()

	// ── Services / Repos ──────────────────────────────────────────────────────
	pgRepo := repo.NewPostgresRepo(pg, log)
	svc := service.NewAnalyticsService(ch, rdb, m, log)
	defer svc.Close()

	analyticsH := handler.NewAnalyticsHandler(svc, m, log)
	mgmtH := handler.NewMgmtHandler(pgRepo, svc, m, log)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
	}))

	authSvc := auth.NewService(pg, rdb, &cfg.Auth, log)
	jwtMW := authSvc.JWTMiddleware

	// Health
	r.Get("/health", analyticsH.Health)
	r.Get("/ready", analyticsH.Health)
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// Metrics
	r.Handle("/metrics", m.Handler())

	r.Route("/v1", func(r chi.Router) {
		r.Use(jwtMW)

		// ── Analytics endpoints ───────────────────────────────────────────
		r.Get("/events/count", analyticsH.EventCount)
		r.Post("/funnels/query", analyticsH.FunnelQuery)
		r.Get("/dau", analyticsH.DAU)
		r.Post("/retention", analyticsH.Retention)
		r.Get("/sessions/metrics", analyticsH.SessionMetrics)

		// ── Management endpoints ──────────────────────────────────────────
		// Funnels
		r.Post("/funnels", mgmtH.CreateFunnel)
		r.Get("/apps/{app_id}/funnels", mgmtH.ListFunnels)

		// Apps
		r.Get("/apps", mgmtH.ListApps)
		r.Get("/apps/{id}", mgmtH.GetApp)
		r.Put("/apps/{id}", mgmtH.UpdateApp)
		r.Delete("/apps/{id}", mgmtH.DeleteApp)

		// Alerts
		r.Get("/alerts", mgmtH.ListAlerts)
		r.Post("/alerts", mgmtH.CreateAlert)
		r.Put("/alerts/{id}", mgmtH.UpdateAlert)
		r.Delete("/alerts/{id}", mgmtH.DeleteAlert)

		// Cohorts
		r.Get("/cohorts", mgmtH.ListCohorts)
		r.Post("/cohorts", mgmtH.CreateCohort)
		r.Delete("/cohorts/{id}", mgmtH.DeleteCohort)
		r.Post("/cohorts/{id}/recompute", mgmtH.RecomputeCohort)

		// Orgs
		r.Get("/orgs", mgmtH.ListOrgs)
		r.Post("/orgs", mgmtH.CreateOrg)
		r.Put("/orgs/{id}", mgmtH.UpdateOrg)

		// Experiments
		r.Get("/experiments", mgmtH.ListExperiments)
		r.Post("/experiments", mgmtH.CreateExperiment)
		r.Put("/experiments/{id}", mgmtH.UpdateExperiment)
		r.Delete("/experiments/{id}", mgmtH.DeleteExperiment)
	})

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("query-api listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("graceful shutdown", zap.Error(err))
	}
	log.Info("query-api stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
