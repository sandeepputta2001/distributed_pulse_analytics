// Package main implements the ClickHouse Writer service.
// Consumes session-events (enriched + sessionized), accumulates them in a buffer,
// and performs bulk inserts to ClickHouse every 1 second (100K–1M rows/batch).
// Uses async inserts with deduplication by event_id.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	chchlient "github.com/pulse-analytics/internal/clickhouse"
	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/enricher"
	"github.com/pulse-analytics/internal/kafka"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/models"
	"github.com/pulse-analytics/internal/tracing"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/chwriter.yaml")
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

	// ClickHouse shard pool — routes writes by FNV(app_id) % numShards.
	// Falls back to single-node when ShardHosts is empty.
	chPool, err := chchlient.NewPool(&cfg.ClickHouse, log)
	if err != nil {
		log.Fatal("clickhouse pool", zap.Error(err))
	}
	defer chPool.Close()

	// Sharded batch writer: one write-behind buffer per shard, flush every 1s.
	writer := chchlient.NewShardedWriter(chPool, log, m, 1*time.Second, 500_000)
	defer writer.Stop()

	// Kafka consumer: session-events (enriched+sessionized events)
	consumer, err := kafka.NewConsumer(&cfg.Kafka, []string{cfg.Kafka.TopicSession}, log, m)
	if err != nil {
		log.Fatal("kafka consumer", zap.Error(err))
	}
	defer consumer.Close()

	// DLQ producer for failed events
	dlqProducer, err := kafka.NewProducer(&cfg.Kafka, log, m)
	if err != nil {
		log.Fatal("kafka producer", zap.Error(err))
	}
	defer dlqProducer.Close()

	// Metrics HTTP server
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", m.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		srv := &http.Server{Addr: ":9091", Handler: mux}
		if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
			log.Error("metrics server", zap.Error(err))
		}
	}()

	log.Info("ch-writer started",
		zap.String("consumer_topic", cfg.Kafka.TopicSession),
	)

	go func() {
		if err := consumer.ConsumeLoop(ctx, func(ctx context.Context, key, value []byte) error {
			var event models.EnrichedEvent
			if err := json.Unmarshal(value, &event); err != nil {
				// Try to unmarshal as session event (synthetic events)
				var se models.SessionEvent
				if err2 := json.Unmarshal(value, &se); err2 != nil {
					return nil // skip malformed
				}
				// Convert session event → CH row
				chEvt := sessionEventToCH(se)
				writer.Write([]models.CHEvent{chEvt})
				return nil
			}

			// Convert enriched event → CH row
			chEvt := enricher.ToCHEvent(event)
			writer.Write([]models.CHEvent{chEvt})
			return nil
		}); err != nil && ctx.Err() == nil {
			log.Error("consumer loop", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down ch-writer, flushing buffer...")
	cancel()
	writer.Stop()
	_ = dlqProducer.Flush(context.Background())
}

// sessionEventToCH maps a synthetic session event to a ClickHouse row.
func sessionEventToCH(se models.SessionEvent) models.CHEvent {
	props := map[string]string{
		"session_duration_s": durationToStr(se.DurationS),
		"event_count":        intToStr(se.EventCount),
		"entry_screen":       se.EntryScreen,
		"exit_screen":        se.ExitScreen,
		"exit_reason":        se.ExitReason,
	}
	return models.CHEvent{
		AppID:     se.AppID,
		EventID:   se.SessionID + "-" + se.Type,
		UserID:    se.UserID,
		DeviceID:  se.DeviceID,
		EventName: se.Type, // "session_start" | "session_end"
		EventTime: time.UnixMilli(se.StartTimeMs).UTC(),
		ServerTime: time.UnixMilli(se.StartTimeMs).UTC(),
		SessionID: se.SessionID,
		Props:     props,
	}
}

func durationToStr(s int64) string {
	return time.Duration(s * int64(time.Second)).String()
}

func intToStr(n int) string {
	return strconv.Itoa(n)
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
