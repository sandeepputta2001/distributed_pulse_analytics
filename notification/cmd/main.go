// main.go — Notification service entrypoint.
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

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/mongo"
	"github.com/pulse-analytics/shared/pkg/postgres"

	"github.com/pulse-analytics/notification/internal/handler"
	"github.com/pulse-analytics/notification/internal/repo"
	"github.com/pulse-analytics/notification/internal/service"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync()

	cfg, err := config.Load(envOr("CONFIG_PATH", "configs/config.yaml"))
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}

	m := metrics.NewRegistry("notification")

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres", zap.Error(err))
	}
	defer pg.Close()

	mdb, err := mongo.NewClient(&cfg.Mongo, log)
	if err != nil {
		log.Fatal("mongo", zap.Error(err))
	}
	defer mdb.Close(context.Background())

	cfg.Kafka.ConsumerGroup = "notification-svc"
	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicNotify}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	r := repo.New(pg, mdb, log)
	svc := service.New(r, log)

	kafkaH := handler.NewKafkaHandler(consumer, svc, cfg.Kafka.TopicNotify, log)
	httpH := handler.NewHTTPHandler(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Kafka consumer goroutine
	go kafkaH.Run(ctx)

	// HTTP server for health / metrics
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	mux := chi.NewRouter()
	mux.Get("/health", httpH.Health)
	mux.Get("/ready", httpH.Health)
	mux.Handle("/metrics", m.Handler())

	srv := &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("notification listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	cancel() // stop Kafka consumer
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	log.Info("notification stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
