// Package main boots the ClickHouse Writer service.
// Consumes session-events, buffers them, and bulk-inserts to ClickHouse every 1s.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	chchlient "github.com/pulse-analytics/shared/pkg/clickhouse"
	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/chwriter/internal/handler"
	"github.com/pulse-analytics/chwriter/internal/repo"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "ch-writer"
	cfg.Kafka.ConsumerGroup = "ch-writer-group"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)

	chPool, err := chchlient.NewPool(&cfg.ClickHouse, log)
	if err != nil {
		log.Fatal("clickhouse pool", zap.Error(err))
	}
	defer chPool.Close()

	writer := chchlient.NewShardedWriter(chPool, log, m, 1*time.Second, 500_000)
	defer writer.Stop()

	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicSession}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	// Wire repo and handler
	chRepo := repo.New(writer, log)
	kafkaHandler := handler.New(chRepo, log)

	// Metrics + health HTTP server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", m.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		srv := &http.Server{Addr: ":9091", Handler: mux}
		if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
			log.Error("metrics server", zap.Error(err))
		}
	}()

	log.Info("ch-writer started", zap.String("topic", cfg.Kafka.TopicSession))

	go func() {
		if err := consumer.ConsumeLoop(ctx, kafkaHandler.Handle); err != nil && ctx.Err() == nil {
			log.Error("consumer loop exited", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down ch-writer, flushing buffer...")
	cancel()
	writer.Stop()
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
