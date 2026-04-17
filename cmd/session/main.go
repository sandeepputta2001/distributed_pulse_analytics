// Package main implements the Session Engine service.
// Consumes enriched-events, assigns/manages session state via Redis,
// emits session_start and session_end synthetic events.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/kafka"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/session"
	"github.com/pulse-analytics/internal/tracing"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/session.yaml")
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

	engine := session.NewEngine(redis, m, log)

	log.Info("session engine started")

	go func() {
		if err := consumer.ConsumeLoop(ctx, func(ctx context.Context, key, value []byte) error {
			var event models.EnrichedEvent
			if err := json.Unmarshal(value, &event); err != nil {
				return nil
			}

			updated, sessionEvts, err := engine.Process(ctx, event)
			if err != nil {
				log.Error("session process failed", zap.Error(err))
				return nil
			}

			// Re-publish enriched event (now with session_id) to session-events topic
			partKey := []byte(updated.AppID + ":" + updated.DeviceID)
			if err := producer.PublishAsync(cfg.Kafka.TopicSession, partKey, updated); err != nil {
				log.Error("publish session event", zap.Error(err))
			}

			// Publish synthetic session_start/session_end events
			for _, se := range sessionEvts {
				if err := producer.PublishAsync(cfg.Kafka.TopicSession, partKey, se); err != nil {
					log.Error("publish synthetic session event", zap.Error(err))
				}
			}

			return nil
		}); err != nil && ctx.Err() == nil {
			log.Error("consumer loop", zap.Error(err))
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
