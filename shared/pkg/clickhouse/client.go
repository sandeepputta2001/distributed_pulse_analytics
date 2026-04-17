package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"io"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
)

// Client wraps the ClickHouse driver.
type Client struct {
	conn driver.Conn
	log  *zap.Logger
	cfg  *config.ClickHouseConfig
}

// NewClient creates a ClickHouse connection.
func NewClient(cfg *config.ClickHouseConfig, log *zap.Logger) (*Client, error) {
	opts := &clickhouse.Options{
		Addr: cfg.Hosts,
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Settings: clickhouse.Settings{
			// Tune for bulk inserts
			"async_insert":                  1,
			"wait_for_async_insert":         0,
			"async_insert_max_data_size":    "100000000", // 100MB
			"async_insert_busy_timeout_ms":  1000,
			"async_insert_stale_timeout_ms": 5000,
			"max_insert_block_size":         1_000_000,
			"insert_deduplicate":            1,
		},
		DialTimeout:          cfg.DialTimeout,
		MaxOpenConns:         cfg.MaxOpenConn,
		MaxIdleConns:         cfg.MaxIdleConn,
		ConnMaxLifetime:      30 * time.Minute,
		ConnOpenStrategy:     clickhouse.ConnOpenInOrder,
		BlockBufferSize:      10,
		MaxCompressionBuffer: 10_000_000, // 10MB
	}

	if cfg.TLS {
		opts.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if cfg.Debug {
		opts.Debug = true
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}

	log.Info("clickhouse connected", zap.Strings("hosts", cfg.Hosts), zap.String("db", cfg.Database))
	return &Client{conn: conn, log: log, cfg: cfg}, nil
}

// Conn returns the raw driver connection.
func (c *Client) Conn() driver.Conn { return c.conn }

// Exec runs a DDL or non-query statement.
func (c *Client) Exec(ctx context.Context, query string, args ...any) error {
	return c.conn.Exec(ctx, query, args...)
}

// Query runs a SELECT and returns rows.
func (c *Client) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return c.conn.Query(ctx, query, args...)
}

// QueryRow runs a SELECT that returns a single row.
func (c *Client) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return c.conn.QueryRow(ctx, query, args...)
}

// PrepareBatch prepares a batch insert.
func (c *Client) PrepareBatch(ctx context.Context, query string) (driver.Batch, error) {
	return c.conn.PrepareBatch(ctx, query)
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.conn.Ping(ctx)
}

// Close closes the connection pool.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Stats returns connection pool statistics.
func (c *Client) Stats() driver.Stats {
	return c.conn.Stats()
}

// WithQueryID runs a query with a specific query ID (useful for cancellation).
func (c *Client) WithQueryID(ctx context.Context, queryID string) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithQueryID(queryID))
}

// WithSettings runs a query with custom settings.
func (c *Client) WithSettings(ctx context.Context, settings clickhouse.Settings) context.Context {
	return clickhouse.Context(ctx, clickhouse.WithSettings(settings))
}

// ClickHouseSettings alias for convenience.
type Settings = clickhouse.Settings

// ─── Shard Pool ───────────────────────────────────────────────────────────────

// Pool manages a set of shard write connections and optional read-replica
// connections for ClickHouse.
//
// # Write Sharding
//
// Writes are routed by FNV-1a(app_id) % numShards so every app's events land
// on a consistent shard, enabling per-shard ORDER BY locality.  When
// config.ClickHouseConfig.ShardHosts is empty, all writes go to Hosts[0]
// (single-node / distributed-table mode).
//
// # Read Replicas
//
// Reads use ReadHosts in round-robin order.  Falls back to shards[0] when
// ReadHosts is empty.
type Pool struct {
	shards  []*Client
	readers []*Client
	rrRead  atomic.Uint64
	log     *zap.Logger
}

// NewPool creates a Pool from config.  Each ShardHost and ReadHost gets its own
// connection; a shard host that fails to connect aborts startup, while a read
// host failure is demoted to a warning and that replica is skipped.
func NewPool(cfg *config.ClickHouseConfig, log *zap.Logger) (*Pool, error) {
	shardHosts := cfg.ShardHosts
	if len(shardHosts) == 0 {
		shardHosts = []string{cfg.Hosts[0]}
	}

	var shards []*Client
	for _, host := range shardHosts {
		sc := *cfg
		sc.Hosts = []string{host}
		c, err := NewClient(&sc, log)
		if err != nil {
			for _, s := range shards {
				_ = s.Close()
			}
			return nil, fmt.Errorf("clickhouse shard %s: %w", host, err)
		}
		shards = append(shards, c)
	}

	var readers []*Client
	for _, host := range cfg.ReadHosts {
		rc := *cfg
		rc.Hosts = []string{host}
		c, err := NewClient(&rc, log)
		if err != nil {
			log.Warn("clickhouse read host skipped", zap.String("host", host), zap.Error(err))
			continue
		}
		readers = append(readers, c)
	}

	log.Info("clickhouse pool ready",
		zap.Int("shards", len(shards)),
		zap.Int("readers", len(readers)),
	)
	return &Pool{shards: shards, readers: readers, log: log}, nil
}

// ShardFor returns the shard Client for the given appID.
// Uses FNV-1a(appID) % numShards for consistent shard assignment.
func (p *Pool) ShardFor(appID string) *Client {
	if len(p.shards) == 1 {
		return p.shards[0]
	}
	h := fnv.New64a()
	_, _ = io.WriteString(h, appID)
	idx := h.Sum64() % uint64(len(p.shards))
	return p.shards[idx]
}

// ReadConn returns a read-replica Client via round-robin, falling back to
// shards[0] when no read replicas are configured.
func (p *Pool) ReadConn() *Client {
	if len(p.readers) == 0 {
		return p.shards[0]
	}
	idx := p.rrRead.Add(1) % uint64(len(p.readers))
	return p.readers[idx]
}

// Shards returns all shard clients (used for health checks).
func (p *Pool) Shards() []*Client { return p.shards }

// Close closes all shard and reader connections.
func (p *Pool) Close() {
	for _, c := range p.shards {
		_ = c.Close()
	}
	for _, c := range p.readers {
		_ = c.Close()
	}
}
