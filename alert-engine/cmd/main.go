// main.go — Alert Engine service entrypoint.
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
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/postgres"

	"github.com/pulse-analytics/alert-engine/internal/handler"
	"github.com/pulse-analytics/alert-engine/internal/repo"
	"github.com/pulse-analytics/alert-engine/internal/service"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg, err := config.Load(envOr("CONFIG_PATH", "configs/config.yaml"))
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}

	m := metrics.NewRegistry("alert_engine")

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pg.Close()

	ch, err := clickhouse.NewClient(&cfg.ClickHouse, log)
	if err != nil {
		log.Fatal("clickhouse", zap.Error(err))
	}
	defer ch.Close()

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	r := repo.New(pg, ch, log)
	svc := service.New(r, producer, cfg.Kafka.TopicNotify, log)
	sched := handler.NewScheduler(svc, log)
	sched.Start()
	defer sched.Stop()

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	mux := chi.NewRouter()
	mux.Get("/health", sched.Health)
	mux.Get("/ready", sched.Health)
	mux.Handle("/metrics", m.Handler())

	srv := &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("alert-engine listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Info("alert-engine stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
