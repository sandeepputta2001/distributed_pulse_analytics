package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds all Prometheus metrics.
type Registry struct {
	reg *prometheus.Registry

	// ─── Gateway Metrics ──────────────────────────────────────────────────────
	IngestRequests   *prometheus.CounterVec
	IngestErrors     *prometheus.CounterVec
	IngestLatency    *prometheus.HistogramVec
	IngestEvents     prometheus.Counter
	IngestBatchSize  prometheus.Histogram
	DuplicatesFiltered prometheus.Counter

	// ─── Kafka Metrics ────────────────────────────────────────────────────────
	KafkaProduced      *prometheus.CounterVec
	KafkaProduceErrors *prometheus.CounterVec
	ConsumerProcessed  *prometheus.CounterVec
	ConsumerHandlerErrors *prometheus.CounterVec
	ConsumerLatency    *prometheus.HistogramVec
	ConsumerLag        *prometheus.GaugeVec

	// ─── ClickHouse Metrics ───────────────────────────────────────────────────
	CHInserted      prometheus.Counter
	CHInsertErrors  prometheus.Counter
	CHInsertLatency prometheus.Histogram
	CHQueryLatency  *prometheus.HistogramVec
	CHQueryErrors   *prometheus.CounterVec

	// ─── Redis Metrics ────────────────────────────────────────────────────────
	RedisCacheHits   *prometheus.CounterVec
	RedisCacheMisses *prometheus.CounterVec

	// ─── Session Metrics ──────────────────────────────────────────────────────
	SessionsOpened  prometheus.Counter
	SessionsClosed  prometheus.Counter
	SessionDuration prometheus.Histogram

	// ─── Query API Metrics ────────────────────────────────────────────────────
	QueryRequests *prometheus.CounterVec
	QueryLatency  *prometheus.HistogramVec
	QueryCacheHit *prometheus.CounterVec

	// ─── Circuit Breaker Metrics ──────────────────────────────────────────────
	// Labels: breaker (name), state (closed|open|half-open)
	CircuitBreakerState      *prometheus.GaugeVec
	CircuitBreakerTransitions *prometheus.CounterVec

	// ─── Bulkhead Metrics ─────────────────────────────────────────────────────
	BulkheadActive   prometheus.Gauge   // current active operations
	BulkheadRejected prometheus.Counter // cumulative rejections

	// ─── L1 Cache Metrics ─────────────────────────────────────────────────────
	L1CacheHits   prometheus.Counter
	L1CacheMisses prometheus.Counter
	L1CacheStales prometheus.Counter // stale-while-revalidate hits
	L1CacheSize   prometheus.Gauge

	// ─── Writer Backpressure Metrics ──────────────────────────────────────────
	WriterPending prometheus.Gauge   // events buffered in write-behind channel
	WriterDropped prometheus.Counter // events dropped due to buffer overflow
}

// NewRegistry creates and registers all metrics.
func NewRegistry(serviceName string) *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	factory := promauto.With(reg)
	labels := prometheus.Labels{"service": serviceName}
	_ = labels

	r := &Registry{reg: reg}

	// Gateway
	r.IngestRequests = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_ingest_requests_total",
		Help: "Total ingest HTTP requests",
	}, []string{"status"})

	r.IngestErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_ingest_errors_total",
		Help: "Total ingest errors",
	}, []string{"reason"})

	r.IngestLatency = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pulse_ingest_latency_seconds",
		Help:    "Ingest request latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"handler"})

	r.IngestEvents = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_ingest_events_total",
		Help: "Total events ingested",
	})

	r.IngestBatchSize = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "pulse_ingest_batch_size",
		Help:    "Events per ingest batch",
		Buckets: []float64{1, 5, 10, 50, 100, 250, 500},
	})

	r.DuplicatesFiltered = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_ingest_duplicates_filtered_total",
		Help: "Events filtered as duplicates",
	})

	// Kafka
	r.KafkaProduced = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_kafka_produced_total",
		Help: "Records produced to Kafka",
	}, []string{"topic"})

	r.KafkaProduceErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_kafka_produce_errors_total",
		Help: "Kafka produce errors",
	}, []string{"topic"})

	r.ConsumerProcessed = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_consumer_processed_total",
		Help: "Records processed by consumers",
	}, []string{"topic"})

	r.ConsumerHandlerErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_consumer_handler_errors_total",
		Help: "Consumer handler errors",
	}, []string{"topic"})

	r.ConsumerLatency = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pulse_consumer_latency_seconds",
		Help:    "Consumer processing latency per record",
		Buckets: []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
	}, []string{"topic"})

	r.ConsumerLag = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pulse_consumer_lag",
		Help: "Kafka consumer group lag",
	}, []string{"topic", "partition"})

	// ClickHouse
	r.CHInserted = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_clickhouse_inserted_total",
		Help: "Rows inserted into ClickHouse",
	})

	r.CHInsertErrors = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_clickhouse_insert_errors_total",
		Help: "ClickHouse insert errors",
	})

	r.CHInsertLatency = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "pulse_clickhouse_insert_latency_seconds",
		Help:    "ClickHouse batch insert latency",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10},
	})

	r.CHQueryLatency = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pulse_clickhouse_query_latency_seconds",
		Help:    "ClickHouse query latency",
		Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10},
	}, []string{"query_type"})

	r.CHQueryErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_clickhouse_query_errors_total",
		Help: "ClickHouse query errors",
	}, []string{"query_type"})

	// Redis
	r.RedisCacheHits = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_redis_cache_hits_total",
		Help: "Redis cache hits",
	}, []string{"cache"})

	r.RedisCacheMisses = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_redis_cache_misses_total",
		Help: "Redis cache misses",
	}, []string{"cache"})

	// Sessions
	r.SessionsOpened = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_sessions_opened_total",
		Help: "Sessions opened",
	})

	r.SessionsClosed = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_sessions_closed_total",
		Help: "Sessions closed",
	})

	r.SessionDuration = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "pulse_session_duration_seconds",
		Help:    "Session duration distribution",
		Buckets: []float64{10, 30, 60, 120, 300, 600, 1800, 3600},
	})

	// Query API
	r.QueryRequests = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_query_requests_total",
		Help: "Query API requests",
	}, []string{"type", "status"})

	r.QueryLatency = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pulse_query_latency_seconds",
		Help:    "Query API latency",
		Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10},
	}, []string{"type"})

	r.QueryCacheHit = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_query_cache_hits_total",
		Help: "Query result cache hits",
	}, []string{"cache_tier"})

	// Circuit Breaker
	r.CircuitBreakerState = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pulse_circuit_breaker_state",
		Help: "Current circuit breaker state (0=closed, 1=open, 2=half-open)",
	}, []string{"breaker"})

	r.CircuitBreakerTransitions = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "pulse_circuit_breaker_transitions_total",
		Help: "Total circuit breaker state transitions",
	}, []string{"breaker", "from", "to"})

	// Bulkhead
	r.BulkheadActive = factory.NewGauge(prometheus.GaugeOpts{
		Name: "pulse_bulkhead_active_operations",
		Help: "Currently active bulkhead operations",
	})

	r.BulkheadRejected = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_bulkhead_rejected_total",
		Help: "Operations rejected by the bulkhead due to capacity",
	})

	// L1 Cache
	r.L1CacheHits = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_l1_cache_hits_total",
		Help: "L1 in-process cache hits",
	})

	r.L1CacheMisses = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_l1_cache_misses_total",
		Help: "L1 in-process cache misses",
	})

	r.L1CacheStales = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_l1_cache_stales_total",
		Help: "L1 stale-while-revalidate hits (served stale, triggered refresh)",
	})

	r.L1CacheSize = factory.NewGauge(prometheus.GaugeOpts{
		Name: "pulse_l1_cache_size",
		Help: "Number of entries in the L1 in-process cache",
	})

	// Write-Behind Backpressure
	r.WriterPending = factory.NewGauge(prometheus.GaugeOpts{
		Name: "pulse_writer_pending_events",
		Help: "Events currently buffered in the ClickHouse write-behind channel",
	})

	r.WriterDropped = factory.NewCounter(prometheus.CounterOpts{
		Name: "pulse_writer_dropped_total",
		Help: "Events dropped due to write-behind buffer overflow",
	})

	return r
}

// Handler returns an HTTP handler for Prometheus scraping.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
