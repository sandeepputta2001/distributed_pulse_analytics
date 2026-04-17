package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/config"
)

// Client wraps go-redis with convenience helpers.
// It transparently supports both cluster mode and single-node mode;
// the raw field is a redis.UniversalClient that works for both.
type Client struct {
	raw redis.UniversalClient
	log *zap.Logger
}

// NewClient creates a Redis client.
// It first attempts a cluster connection; if that fails it falls back to a
// single-node client (useful for local development).
func NewClient(cfg *config.RedisConfig, log *zap.Logger) (*Client, error) {
	cluster := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:          cfg.Addrs,
		Password:       cfg.Password,
		PoolSize:       cfg.PoolSize,
		MaxRetries:     cfg.MaxRetries,
		DialTimeout:    cfg.DialTimeout,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		RouteByLatency: true,
		RouteRandomly:  false,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := cluster.Ping(ctx).Err(); err == nil {
		log.Info("redis cluster connected", zap.Strings("addrs", cfg.Addrs))
		return &Client{raw: cluster, log: log}, nil
	}

	// Cluster unavailable — fall back to single-node (e.g. local dev / sentinel).
	if err := cluster.Close(); err != nil {
		log.Warn("redis cluster close error during fallback", zap.Error(err))
	}

	single := redis.NewClient(&redis.Options{
		Addr:         cfg.Addrs[0],
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MaxRetries:   cfg.MaxRetries,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err2 := single.Ping(ctx2).Err(); err2 != nil {
		return nil, fmt.Errorf("redis connect failed (cluster and single-node): single: %w", err2)
	}
	log.Warn("redis cluster unavailable, using single-node fallback", zap.String("addr", cfg.Addrs[0]))
	return &Client{raw: single, log: log}, nil
}

// ─── Key-Value ────────────────────────────────────────────────────────────────

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.raw.Get(ctx, key).Result()
}

func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return c.raw.Set(ctx, key, value, ttl).Err()
}

func (c *Client) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.raw.Set(ctx, key, data, ttl).Err()
}

func (c *Client) GetJSON(ctx context.Context, key string, target any) error {
	data, err := c.raw.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.raw.Del(ctx, keys...).Err()
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.raw.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.raw.TTL(ctx, key).Result()
}

func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.raw.Expire(ctx, key, ttl).Err()
}

// ─── Hash ─────────────────────────────────────────────────────────────────────

func (c *Client) HSet(ctx context.Context, key string, values ...any) error {
	return c.raw.HSet(ctx, key, values...).Err()
}

func (c *Client) HGet(ctx context.Context, key, field string) (string, error) {
	return c.raw.HGet(ctx, key, field).Result()
}

func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.raw.HGetAll(ctx, key).Result()
}

func (c *Client) HDel(ctx context.Context, key string, fields ...string) error {
	return c.raw.HDel(ctx, key, fields...).Err()
}

// ─── Counter ──────────────────────────────────────────────────────────────────

func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	return c.raw.Incr(ctx, key).Result()
}

func (c *Client) IncrBy(ctx context.Context, key string, n int64) (int64, error) {
	return c.raw.IncrBy(ctx, key, n).Result()
}

func (c *Client) IncrByFloat(ctx context.Context, key string, f float64) (float64, error) {
	return c.raw.IncrByFloat(ctx, key, f).Result()
}

// ─── Sorted Set ───────────────────────────────────────────────────────────────

func (c *Client) ZAdd(ctx context.Context, key string, members ...redis.Z) error {
	return c.raw.ZAdd(ctx, key, members...).Err()
}

func (c *Client) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) ([]string, error) {
	return c.raw.ZRangeByScore(ctx, key, opt).Result()
}

func (c *Client) ZCard(ctx context.Context, key string) (int64, error) {
	return c.raw.ZCard(ctx, key).Result()
}

func (c *Client) ZRemRangeByScore(ctx context.Context, key, min, max string) (int64, error) {
	return c.raw.ZRemRangeByScore(ctx, key, min, max).Result()
}

// ─── Rate Limiting (Token Bucket via Lua) ────────────────────────────────────

const rateLimitScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local fill_time = burst / rate
local ttl = math.floor(fill_time * 2)

local last_tokens = tonumber(redis.call("GET", key))
if last_tokens == nil then
    last_tokens = burst
end

local last_refreshed = tonumber(redis.call("GET", key..":ts"))
if last_refreshed == nil then
    last_refreshed = now
end

local delta = math.max(0, now - last_refreshed)
local filled_tokens = math.min(burst, last_tokens + (delta * rate))
local allowed = filled_tokens >= requested

if allowed then
    redis.call("SET", key, filled_tokens - requested, "EX", ttl)
else
    redis.call("SET", key, filled_tokens, "EX", ttl)
end
redis.call("SET", key..":ts", now, "EX", ttl)

return { allowed and 1 or 0, math.floor(filled_tokens) }
`

// AllowRate checks if a rate limit allows the request (token bucket).
func (c *Client) AllowRate(ctx context.Context, key string, rps float64, burst int) (bool, error) {
	now := float64(time.Now().UnixNano()) / 1e9
	results, err := c.raw.Eval(ctx, rateLimitScript, []string{key},
		rps, burst, now, 1,
	).Slice()
	if err != nil {
		return true, nil // fail open
	}
	allowed, _ := results[0].(int64)
	return allowed == 1, nil
}

// ─── Pub/Sub ──────────────────────────────────────────────────────────────────

func (c *Client) Publish(ctx context.Context, channel string, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.raw.Publish(ctx, channel, data).Err()
}

func (c *Client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.raw.Subscribe(ctx, channels...)
}

// ─── Set (exact dedup) ────────────────────────────────────────────────────────

// SetAdd adds a member to a set (used as exact dedup in processing layer).
func (c *Client) SetAdd(ctx context.Context, key string, member string, ttl time.Duration) (bool, error) {
	pipe := c.raw.Pipeline()
	added := pipe.SAdd(ctx, key, member)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return added.Val() == 1, nil
}

// SetIsMember checks exact dedup set membership.
func (c *Client) SetIsMember(ctx context.Context, key, member string) (bool, error) {
	return c.raw.SIsMember(ctx, key, member).Result()
}

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error {
	return c.raw.Ping(ctx).Err()
}

// Close closes all connections.
func (c *Client) Close() error {
	return c.raw.Close()
}

// IsNotFound checks if error is redis.Nil (key not found).
func IsNotFound(err error) bool {
	return err == redis.Nil
}
