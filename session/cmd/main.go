// Package main boots the Session Engine service.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/session/internal/handler"
	"github.com/pulse-analytics/session/internal/service"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "session-engine"
	cfg.Kafka.ConsumerGroup = "session-engine-group"

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

	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicEnriched}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	sessionSvc := service.New(redis, m, log)
	kafkaHandler := handler.New(sessionSvc, producer, cfg.Kafka.TopicSession, log)

	log.Info("session engine started")

	go func() {
		if err := consumer.ConsumeLoop(ctx, kafkaHandler.Handle); err != nil && ctx.Err() == nil {
			log.Error("consumer loop exited", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down session engine...")
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
