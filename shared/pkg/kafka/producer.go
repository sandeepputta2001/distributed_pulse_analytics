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

	"github.com/pulse-analytics/shared/pkg/config"
	"github.com/pulse-analytics/shared/pkg/metrics"
)

// Producer wraps franz-go for high-throughput async publishing.
type Producer struct {
	client *kgo.Client
	log    *zap.Logger
	m      *metrics.Registry
}

// NewProducer creates a kafka producer tuned for high throughput.
func NewProducer(cfg *config.KafkaConfig, log *zap.Logger, m *metrics.Registry) (*Producer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),

		// Batching for throughput
		kgo.ProducerBatchMaxBytes(int32(cfg.ProducerBatch) * 200), // ~200B per event
		kgo.ProducerLinger(cfg.ProducerLinger),
		kgo.RecordRetries(5),
		kgo.RetryBackoffFn(func(tries int) time.Duration {
			return time.Duration(tries*tries) * 100 * time.Millisecond
		}),

		kgo.RequiredAcks(ackFromString(cfg.Acks)),
		kgo.ProducerBatchCompression(compressionFromString(cfg.Compression)),
		kgo.DisableIdempotentWrite(),

		// Buffer up to 1M records before applying back-pressure
		kgo.MaxBufferedRecords(1_000_000),
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
		return nil, fmt.Errorf("kafka producer: %w", err)
	}

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		return nil, fmt.Errorf("kafka broker unreachable: %w", err)
	}

	log.Info("kafka producer connected", zap.Strings("brokers", cfg.Brokers))
	return &Producer{client: client, log: log, m: m}, nil
}

// PublishAsync publishes a record without waiting for broker ack.
// Errors are handled via the promise callback.
func (p *Producer) PublishAsync(topic string, key []byte, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshalling record: %w", err)
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: data,
	}

	p.client.Produce(context.Background(), record, func(r *kgo.Record, err error) {
		if err != nil {
			p.log.Error("kafka produce failed",
				zap.String("topic", topic),
				zap.Error(err),
			)
			p.m.KafkaProduceErrors.WithLabelValues(topic).Inc()
		} else {
			p.m.KafkaProduced.WithLabelValues(topic).Inc()
		}
	})

	return nil
}

// PublishSync publishes and waits for acknowledgment.
func (p *Producer) PublishSync(ctx context.Context, topic string, key []byte, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshalling record: %w", err)
	}

	if err := p.client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   key,
		Value: data,
	}).FirstErr(); err != nil {
		p.m.KafkaProduceErrors.WithLabelValues(topic).Inc()
		return fmt.Errorf("kafka sync produce: %w", err)
	}

	p.m.KafkaProduced.WithLabelValues(topic).Inc()
	return nil
}

// PublishBatch publishes multiple records in a single batch.
func (p *Producer) PublishBatch(ctx context.Context, topic string, records []BatchRecord) error {
	kRecords := make([]*kgo.Record, len(records))
	for i, r := range records {
		data, err := json.Marshal(r.Value)
		if err != nil {
			return fmt.Errorf("marshalling record %d: %w", i, err)
		}
		kRecords[i] = &kgo.Record{Topic: topic, Key: r.Key, Value: data}
	}

	results := p.client.ProduceSync(ctx, kRecords...)
	for _, res := range results {
		if res.Err != nil {
			p.m.KafkaProduceErrors.WithLabelValues(topic).Inc()
			return fmt.Errorf("batch produce: %w", res.Err)
		}
	}

	p.m.KafkaProduced.WithLabelValues(topic).Add(float64(len(records)))
	return nil
}

type BatchRecord struct {
	Key   []byte
	Value interface{}
}

// Flush waits for all pending async records to be sent.
func (p *Producer) Flush(ctx context.Context) error {
	return p.client.Flush(ctx)
}

// Close gracefully shuts down the producer.
// PublishJSON JSON-encodes value and publishes synchronously with the given key.
func (p *Producer) PublishJSON(ctx context.Context, topic, key string, value interface{}) error {
	return p.PublishSync(ctx, topic, []byte(key), value)
}

func (p *Producer) Close() {
	p.client.Close()
}

func ackFromString(s string) kgo.Acks {
	switch s {
	case "all", "-1":
		return kgo.AllISRAcks()
	case "leader", "1":
		return kgo.LeaderAck()
	default:
		return kgo.LeaderAck()
	}
}

func compressionFromString(s string) kgo.CompressionCodec {
	switch s {
	case "snappy":
		return kgo.SnappyCompression()
	case "lz4":
		return kgo.Lz4Compression()
	case "zstd":
		return kgo.ZstdCompression()
	case "gzip":
		return kgo.GzipCompression()
	default:
		return kgo.SnappyCompression()
	}
}
