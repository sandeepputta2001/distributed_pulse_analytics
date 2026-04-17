package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Service    ServiceConfig
	HTTP       HTTPConfig
	GRPC       GRPCConfig
	Kafka      KafkaConfig
	Redis      RedisConfig
	ClickHouse ClickHouseConfig
	Postgres   PostgresConfig
	Mongo      MongoConfig
	GeoIP      GeoIPConfig
	Auth       AuthConfig
	RateLimit  RateLimitConfig
	Bloom      BloomConfig
	Telemetry  TelemetryConfig
}

type ServiceConfig struct {
	Name        string
	Environment string // development | staging | production
	LogLevel    string
}

type HTTPConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
}

type GRPCConfig struct {
	Host string
	Port int
}

type KafkaConfig struct {
	Brokers         []string
	TopicRawEvents  string
	TopicEnriched   string
	TopicSession    string
	TopicAggResults string
	TopicDLQ        string
	TopicNotify     string
	ConsumerGroup   string
	ProducerBatch   int
	ProducerLinger  time.Duration
	Acks            string // leader | all
	Compression     string // snappy | lz4 | zstd
	TLS             bool
	SASLUser        string
	SASLPass        string
}

type RedisConfig struct {
	Addrs        []string // cluster mode
	Password     string
	DB           int
	PoolSize     int
	MaxRetries   int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type ClickHouseConfig struct {
	Hosts       []string
	Database    string
	Username    string
	Password    string
	MaxOpenConn int
	MaxIdleConn int
	DialTimeout time.Duration
	TLS         bool
	Debug       bool
	// Sharding: additional shard hosts for write distribution.
	// Each entry in ShardHosts is a shard coordinator; writes are routed by
	// FNV-1a(app_id) % len(ShardHosts). Leave empty to use Hosts[0] only.
	ShardHosts []string
	// ReadHosts: dedicated ClickHouse replicas for read queries.
	// If empty, reads fall back to Hosts[0].
	ReadHosts []string
}

type PostgresConfig struct {
	DSN         string
	MaxOpenConn int
	MaxIdleConn int
	MaxLifetime time.Duration
	// ReplicaDSNs: optional list of read-replica connection strings.
	// SELECT queries are load-balanced across these pools in round-robin order.
	// If empty, all queries use the primary (DSN).
	ReplicaDSNs []string
}

type MongoConfig struct {
	URI      string
	Database string
	Timeout  time.Duration
	// ReadPreference controls which replica set members are used for reads.
	// Valid values: "primary" | "primaryPreferred" | "secondary" |
	// "secondaryPreferred" | "nearest".  Default: "secondaryPreferred".
	ReadPreference string
	// ReplicaSet name, required when connecting to a replica set by seed list.
	ReplicaSet string
}

type GeoIPConfig struct {
	DBPath string // path to GeoLite2-City.mmdb
}

type AuthConfig struct {
	JWTSecret      string
	APIKeyCacheTTL time.Duration
	JWTExpiry      time.Duration
}

type RateLimitConfig struct {
	Enabled          bool
	DefaultRPS       float64
	DefaultBurst     int
	WindowSize       time.Duration
	CleanupInterval  time.Duration
}

type BloomConfig struct {
	Capacity    uint
	FalsePositive float64
	WindowTTL   time.Duration
}

type TelemetryConfig struct {
	OTLPEndpoint    string
	PrometheusPort  int
	ServiceName     string
	SamplingRate    float64
	MetricsEnabled  bool
	TracingEnabled  bool
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("PULSE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// Service
	v.SetDefault("service.name", "pulse-analytics")
	v.SetDefault("service.environment", "development")
	v.SetDefault("service.loglevel", "info")

	// HTTP
	v.SetDefault("http.host", "0.0.0.0")
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.readtimeout", "10s")
	v.SetDefault("http.writetimeout", "30s")
	v.SetDefault("http.idletimeout", "60s")
	v.SetDefault("http.shutdowntimeout", "15s")
	v.SetDefault("http.maxbodybytes", 10<<20) // 10MB

	// gRPC
	v.SetDefault("grpc.host", "0.0.0.0")
	v.SetDefault("grpc.port", 9090)

	// Kafka
	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.topicrawevents", "raw-events")
	v.SetDefault("kafka.topicenriched", "enriched-events")
	v.SetDefault("kafka.topicsession", "session-events")
	v.SetDefault("kafka.topicaggresults", "agg-results")
	v.SetDefault("kafka.topicdlq", "dlq-events")
	v.SetDefault("kafka.topicnotify", "notifications")
	v.SetDefault("kafka.consumergroup", "pulse-consumer")
	v.SetDefault("kafka.producerbatch", 1000)
	v.SetDefault("kafka.producerlinger", "5ms")
	v.SetDefault("kafka.acks", "leader")
	v.SetDefault("kafka.compression", "snappy")

	// Redis
	v.SetDefault("redis.addrs", []string{"localhost:6379"})
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.poolsize", 50)
	v.SetDefault("redis.maxretries", 3)
	v.SetDefault("redis.dialtimeout", "5s")
	v.SetDefault("redis.readtimeout", "3s")
	v.SetDefault("redis.writetimeout", "3s")

	// ClickHouse
	v.SetDefault("clickhouse.hosts", []string{"localhost:9000"})
	v.SetDefault("clickhouse.database", "pulse")
	v.SetDefault("clickhouse.username", "pulse")
	v.SetDefault("clickhouse.password", "")
	v.SetDefault("clickhouse.maxopenconn", 20)
	v.SetDefault("clickhouse.maxidleconn", 5)
	v.SetDefault("clickhouse.dialtimeout", "10s")

	// Postgres
	v.SetDefault("postgres.dsn", "postgres://pulse:pulse@localhost:5432/pulse?sslmode=disable")
	v.SetDefault("postgres.maxopenconn", 25)
	v.SetDefault("postgres.maxidleconn", 5)
	v.SetDefault("postgres.maxlifetime", "30m")
	v.SetDefault("postgres.replicadsns", []string{})

	// Mongo
	v.SetDefault("mongo.uri", "mongodb://localhost:27017")
	v.SetDefault("mongo.database", "pulse")
	v.SetDefault("mongo.timeout", "10s")
	v.SetDefault("mongo.readpreference", "secondaryPreferred")
	v.SetDefault("mongo.replicaset", "")

	// GeoIP
	v.SetDefault("geoip.dbpath", "/data/GeoLite2-City.mmdb")

	// Auth
	v.SetDefault("auth.jwtsecret", "change-me-in-production")
	v.SetDefault("auth.apikeycachettl", "5m")
	v.SetDefault("auth.jwtexpiry", "24h")

	// Rate Limit
	v.SetDefault("ratelimit.enabled", true)
	v.SetDefault("ratelimit.defaultrps", 10000.0)
	v.SetDefault("ratelimit.defaultburst", 50000)
	v.SetDefault("ratelimit.windowsize", "1s")
	v.SetDefault("ratelimit.cleanupinterval", "5m")

	// Bloom
	v.SetDefault("bloom.capacity", 1_000_000_000) // 1B
	v.SetDefault("bloom.falsepositive", 0.001)
	v.SetDefault("bloom.windowttl", "24h")

	// Telemetry
	v.SetDefault("telemetry.otlpendpoint", "localhost:4317")
	v.SetDefault("telemetry.prometheusport", 9091)
	v.SetDefault("telemetry.servicename", "pulse-analytics")
	v.SetDefault("telemetry.samplingrate", 0.01)
	v.SetDefault("telemetry.metricsenabled", true)
	v.SetDefault("telemetry.tracingenabled", true)
}
