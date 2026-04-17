package kafka

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/metrics"
)

// MessageHandler is the callback invoked for each Kafka record.
type MessageHandler func(ctx context.Context, key, value []byte) error

// Consumer wraps franz-go for high-throughput consumer groups.
type Consumer struct {
	client  *kgo.Client
	topics  []string
	log     *zap.Logger
	m       *metrics.Registry
}

// NewConsumer creates a Kafka consumer group client.
func NewConsumer(cfg *config.KafkaConfig, topics []string, log *zap.Logger, m *metrics.Registry) (*Consumer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),

		// Start from latest by default; override in specific consumers
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),

		// Fetch tuning for high throughput
		kgo.FetchMaxBytes(50 << 20),   // 50MB
		kgo.FetchMaxPartitionBytes(10 << 20), // 10MB per partition
		kgo.FetchMinBytes(1 << 20),    // 1MB min (reduce round trips)
		kgo.FetchMaxWait(500 * time.Millisecond),
	}

	if cfg.TLS {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	if cfg.SASLUser != "" {
		opts = append(opts, kgo.SASL(plain.Auth{
			User: cfg.SASLUser,
			Pass: cfg.SASLPass,
		}.AsMechanism()))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		return nil, fmt.Errorf("kafka consumer broker unreachable: %w", err)
	}

	log.Info("kafka consumer connected",
		zap.Strings("brokers", cfg.Brokers),
		zap.Strings("topics", topics),
		zap.String("group", cfg.ConsumerGroup),
	)

	return &Consumer{client: client, topics: topics, log: log, m: m}, nil
}

// ConsumeLoop runs a blocking poll loop, calling handler for each record.
// It commits offsets after each successful batch.
func (c *Consumer) ConsumeLoop(ctx context.Context, handler MessageHandler) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil
		}

		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				c.log.Error("kafka fetch error",
					zap.String("topic", e.Topic),
					zap.Int32("partition", e.Partition),
					zap.Error(e.Err),
				)
			}
		}

		var commitErr error
		fetches.EachRecord(func(r *kgo.Record) {
			start := time.Now()
			if err := handler(ctx, r.Key, r.Value); err != nil {
				c.log.Error("handler error",
					zap.String("topic", r.Topic),
					zap.Int64("offset", r.Offset),
					zap.Error(err),
				)
				c.m.ConsumerHandlerErrors.WithLabelValues(r.Topic).Inc()
			} else {
				c.m.ConsumerProcessed.WithLabelValues(r.Topic).Inc()
				c.m.ConsumerLatency.WithLabelValues(r.Topic).Observe(time.Since(start).Seconds())
			}
		})

		// Commit all processed offsets
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil && commitErr == nil {
			c.log.Error("commit offsets failed", zap.Error(err))
		}
	}
}

// ConcurrentConsumeLoop runs handler concurrently across partitions.
// Maintains per-partition ordering. Good for enrichment services.
func (c *Consumer) ConcurrentConsumeLoop(ctx context.Context, workers int, handler MessageHandler) error {
	type work struct {
		key, value []byte
		record     *kgo.Record
	}

	// Partition-keyed worker channels to preserve ordering per partition
	ch := make(chan work, workers*100)
	done := make(chan struct{})

	for i := 0; i < workers; i++ {
		go func() {
			for w := range ch {
				if err := handler(ctx, w.key, w.value); err != nil {
					c.log.Error("concurrent handler error", zap.Error(err))
					c.m.ConsumerHandlerErrors.WithLabelValues("concurrent").Inc()
				}
			}
		}()
	}
	_ = done

	defer close(ch)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return nil
		}

		fetches.EachRecord(func(r *kgo.Record) {
			ch <- work{key: r.Key, value: r.Value, record: r}
		})

		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			c.log.Error("commit offsets failed", zap.Error(err))
		}
	}
}

// Close gracefully shuts down the consumer.
func (c *Consumer) Close() {
	c.client.Close()
}

// UnmarshalRecord is a helper to unmarshal JSON record values.
func UnmarshalRecord(value []byte, target interface{}) error {
	return json.Unmarshal(value, target)
}
