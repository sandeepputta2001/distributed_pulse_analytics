// Package main boots the Enricher service.
// Consumes raw-events from Kafka, enriches each event with GeoIP + UA + timestamp,
// and publishes to enriched-events topic.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/geo"
	"github.com/pulse-analytics/shared/pkg/kafka"
	"github.com/pulse-analytics/shared/pkg/metrics"
	redisclient "github.com/pulse-analytics/shared/pkg/redis"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/enricher/internal/handler"
	"github.com/pulse-analytics/enricher/internal/service"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "enricher"
	cfg.Kafka.ConsumerGroup = "enricher-group"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)

	// Redis (optional — used by geo cache if needed)
	_, err = redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Warn("redis unavailable (non-fatal)", zap.Error(err))
	}

	geoResolver, _ := geo.NewResolver(cfg.GeoIP.DBPath, log)
	if geoResolver != nil {
		defer geoResolver.Close()
	}

	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicRawEvents}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	producer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer producer.Close()

	// Wire handler/service
	enrichSvc := service.New(geoResolver, log)
	kafkaHandler := handler.New(enrichSvc, producer, cfg.Kafka.TopicEnriched, m, log)

	log.Info("enricher started",
		zap.String("consume", cfg.Kafka.TopicRawEvents),
		zap.String("publish", cfg.Kafka.TopicEnriched),
	)

	go func() {
		if err := consumer.ConcurrentConsumeLoop(ctx, 50, kafkaHandler.Handle); err != nil && ctx.Err() == nil {
			log.Error("consumer loop exited", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down enricher...")
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
