// Package main boots the Funnel Processor service.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/postgres"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/funnel/internal/handler"
	"github.com/pulse-analytics/funnel/internal/repo"
	"github.com/pulse-analytics/funnel/internal/service"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "funnel-processor"
	cfg.Kafka.ConsumerGroup = "funnel-processor-group"

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

	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicSession}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	funnelRepo := repo.New(pg, log)
	initialFunnels, _ := funnelRepo.ListAllFunnels(ctx)
	funnelSvc := service.New(funnelRepo, redis, initialFunnels, m, log)

	// Hot-reload funnel definitions every 30 seconds
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				funnels, err := funnelRepo.ListAllFunnels(ctx)
				if err != nil {
					log.Warn("funnel reload failed", zap.Error(err))
					continue
				}
				funnelSvc.Reload(funnels)
				log.Debug("funnel definitions reloaded", zap.Int("count", len(funnels)))
			}
		}
	}()

	kafkaHandler := handler.New(funnelSvc, producer, cfg.Kafka.TopicAggResults, log)
	log.Info("funnel processor started")

	go func() {
		if err := consumer.ConsumeLoop(ctx, kafkaHandler.Handle); err != nil && ctx.Err() == nil {
			log.Error("consumer loop exited", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down funnel processor...")
	cancel()
	_ = producer.Flush(context.Background())
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
