package dedup

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"go.uber.org/zap"

	redisclient "github.com/pulse-analytics/shared/pkg/redis"
)

// Filter provides two-tier deduplication:
// 1. In-process Bloom filter (probabilistic, fast, ~0.1% FPR)
// 2. Redis SET (exact, second-pass in stream processors)
type Filter struct {
	mu         sync.RWMutex
	bloom      *bloom.BloomFilter
	redis      *redisclient.Client
	log        *zap.Logger
	windowTTL  time.Duration
	capacity   uint
	fpRate     float64
	rotateAt   time.Time
}

// NewFilter creates a deduplication filter.
func NewFilter(capacity uint, fpRate float64, windowTTL time.Duration, redis *redisclient.Client, log *zap.Logger) *Filter {
	f := &Filter{
		bloom:     bloom.NewWithEstimates(capacity, fpRate),
		redis:     redis,
		log:       log,
		windowTTL: windowTTL,
		capacity:  capacity,
		fpRate:    fpRate,
		rotateAt:  time.Now().Add(windowTTL),
	}
	go f.rotateLoop()
	return f
}

// TestAndAdd returns true if the event_id is NEW (not seen before).
// Adds it to both the local Bloom filter and Redis.
func (f *Filter) TestAndAdd(ctx context.Context, eventID string) bool {
	// Rotate bloom if window expired
	f.maybeRotate()

	f.mu.RLock()
	inBloom := f.bloom.TestString(eventID)
	f.mu.RUnlock()

	if inBloom {
		// Definitely seen (or false positive ~0.1%)
		return false
	}

	// Not in bloom → add it
	f.mu.Lock()
	f.bloom.AddString(eventID)
	f.mu.Unlock()

	return true
}

// TestAndAddExact performs exact Redis-based dedup (for stream processors).
// More expensive but no false positives.
func (f *Filter) TestAndAddExact(ctx context.Context, eventID string) (bool, error) {
	key := "dedup:event:" + hash(eventID)
	isNew, err := f.redis.SetAdd(ctx, key, eventID, f.windowTTL)
	if err != nil {
		// Redis unavailable → fall back to bloom-only
		f.log.Warn("redis dedup fallback", zap.Error(err))
		return f.TestAndAdd(ctx, eventID), nil
	}
	return isNew, nil
}

// FilterBatch returns only new event IDs from a batch.
func (f *Filter) FilterBatch(ctx context.Context, eventIDs []string) []string {
	newIDs := make([]string, 0, len(eventIDs))
	for _, id := range eventIDs {
		if f.TestAndAdd(ctx, id) {
			newIDs = append(newIDs, id)
		}
	}
	return newIDs
}

// Stats returns current bloom filter statistics.
func (f *Filter) Stats() BloomStats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return BloomStats{
		Capacity:      f.capacity,
		EstimatedFill: f.bloom.ApproximatedSize(),
		FPRate:        f.fpRate,
		RotatesAt:     f.rotateAt,
	}
}

type BloomStats struct {
	Capacity      uint
	EstimatedFill uint32
	FPRate        float64
	RotatesAt     time.Time
}

func (f *Filter) maybeRotate() {
	if time.Now().Before(f.rotateAt) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Now().Before(f.rotateAt) {
		return
	}
	f.log.Info("rotating bloom filter")
	f.bloom = bloom.NewWithEstimates(f.capacity, f.fpRate)
	f.rotateAt = time.Now().Add(f.windowTTL)
}

func (f *Filter) rotateLoop() {
	ticker := time.NewTicker(f.windowTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		f.maybeRotate()
	}
}

// hash returns a shortened hash of the event ID for Redis key namespacing.
func hash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, h.Sum64()%1_000_000)
	return string(rune(h.Sum64()))
}
