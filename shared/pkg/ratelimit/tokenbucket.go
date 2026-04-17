package ratelimit

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"

	redisclient "github.com/pulse-analytics/shared/pkg/redis"
)

// Limiter provides per-tenant rate limiting with Redis backend
// and an in-process fallback.
type Limiter struct {
	redis   *redisclient.Client
	local   map[string]*rate.Limiter
	mu      sync.RWMutex
	log     *zap.Logger
	cleanup *time.Ticker
}

// NewLimiter creates a rate limiter.
func NewLimiter(redis *redisclient.Client, log *zap.Logger, cleanupInterval time.Duration) *Limiter {
	l := &Limiter{
		redis:   redis,
		local:   make(map[string]*rate.Limiter),
		log:     log,
		cleanup: time.NewTicker(cleanupInterval),
	}
	go l.runCleanup()
	return l
}

// Allow checks if a request is allowed for the given tenant.
// Uses Redis token bucket for distributed limiting.
// Falls back to local limiter if Redis is unavailable.
func (l *Limiter) Allow(ctx context.Context, tenantID string, rps float64, burst int) (bool, error) {
	// Try distributed rate limit via Redis Lua script
	allowed, err := l.redis.AllowRate(ctx, "rl:"+tenantID, rps, burst)
	if err != nil {
		// Redis unavailable - fall back to local in-process limiter
		l.log.Warn("redis rate limit fallback", zap.String("tenant", tenantID), zap.Error(err))
		return l.localAllow(tenantID, rps, burst), nil
	}
	return allowed, nil
}

// AllowN checks if n requests are allowed.
func (l *Limiter) AllowN(ctx context.Context, tenantID string, n int, rps float64, burst int) (bool, error) {
	allowed, err := l.redis.AllowRate(ctx, "rl:"+tenantID, rps, burst)
	if err != nil {
		return l.localAllowN(tenantID, n, rps, burst), nil
	}
	return allowed, nil
}

func (l *Limiter) localAllow(tenantID string, rps float64, burst int) bool {
	return l.getLocal(tenantID, rps, burst).Allow()
}

func (l *Limiter) localAllowN(tenantID string, n int, rps float64, burst int) bool {
	return l.getLocal(tenantID, rps, burst).AllowN(time.Now(), n)
}

func (l *Limiter) getLocal(tenantID string, rps float64, burst int) *rate.Limiter {
	l.mu.RLock()
	if lim, ok := l.local[tenantID]; ok {
		l.mu.RUnlock()
		return lim
	}
	l.mu.RUnlock()

	l.mu.Lock()
	defer l.mu.Unlock()
	if lim, ok := l.local[tenantID]; ok {
		return lim
	}
	lim := rate.NewLimiter(rate.Limit(rps), burst)
	l.local[tenantID] = lim
	return lim
}

func (l *Limiter) runCleanup() {
	for range l.cleanup.C {
		l.mu.Lock()
		// Simple cleanup: clear all local limiters periodically
		// They'll be re-created on next request
		if len(l.local) > 10000 {
			l.local = make(map[string]*rate.Limiter)
		}
		l.mu.Unlock()
	}
}

// Close stops the cleanup goroutine.
func (l *Limiter) Close() {
	l.cleanup.Stop()
}
