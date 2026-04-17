// Package main implements the Enrichment Service.
// Consumes raw-events from Kafka, enriches each event with:
//   - GeoIP lookup (MaxMind in-process, ~1µs)
//   - User-Agent parsing
//   - Server-side timestamp correction
//   - Session ID assignment (delegated to session-engine)
//
// Outputs enriched events to enriched-events Kafka topic.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/enricher"
	"github.com/pulse-analytics/internal/geo"
	"github.com/pulse-analytics/internal/kafka"
	"github.com/pulse-analytics/internal/metrics"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/tracing"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/enricher.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "enricher"
	cfg.Kafka.ConsumerGroup = "enricher-group"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Telemetry
	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())
	m := metrics.NewRegistry(cfg.Service.Name)

	// Infrastructure
	redis, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis", zap.Error(err))
	}
	defer redis.Close()

	geoResolver, err := geo.NewResolver(cfg.GeoIP.DBPath, log)
	if err != nil {
		log.Warn("geoip unavailable", zap.Error(err))
	}
	if geoResolver != nil {
		defer geoResolver.Close()
	}

	// Kafka consumer: raw-events → enriched-events
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

	// Enrichment service
	svc := enricher.NewService(geoResolver, log)

	log.Info("enricher service started",
		zap.String("consumer_group", cfg.Kafka.ConsumerGroup),
		zap.String("input_topic", cfg.Kafka.TopicRawEvents),
		zap.String("output_topic", cfg.Kafka.TopicEnriched),
	)

	// Concurrent consume loop: 50 workers, preserves per-partition ordering
	go func() {
		if err := consumer.ConcurrentConsumeLoop(ctx, 50, func(ctx context.Context, key, value []byte) error {
			var msg enricher.IngestMessage
			if err := json.Unmarshal(value, &msg); err != nil {
				return nil // skip malformed messages
			}

			enrichedEvents := svc.Enrich(msg)
			if len(enrichedEvents) == 0 {
				return nil
			}

			// Publish each enriched event to enriched-events topic
			// Partition by app_id:device_id for downstream session ordering
			for _, e := range enrichedEvents {
				partKey := []byte(e.AppID + ":" + e.DeviceID)
				if err := producer.PublishAsync(cfg.Kafka.TopicEnriched, partKey, e); err != nil {
					log.Error("publish enriched", zap.Error(err))
				}
			}
			return nil
		}); err != nil {
			if ctx.Err() == nil {
				log.Error("consumer loop exited", zap.Error(err))
			}
		}
	}()

	// Graceful shutdown
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
