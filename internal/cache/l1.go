// Package cache provides an in-process (L1) cache used as the first tier in
// the 3-tier query caching strategy:
//
//	L1 (this package) — in-process sync.Map, sub-microsecond reads, no network
//	L2               — Redis cluster, shared across pods, ~1ms reads
//	L3               — ClickHouse materialized views, ~10ms reads
//	L4               — ClickHouse raw scan, ~100ms+ reads
//
// # Stale-While-Revalidate (SWR)
//
// Each entry has two deadlines:
//   - StaleAt  — after this time the entry is "stale but usable"
//   - ExpiresAt — after this time the entry is gone (hard expiry)
//
// When a stale-but-not-expired entry is returned, the cache sets a revalidation
// flag for that key so the caller can trigger a background refresh without every
// concurrent reader triggering their own refresh (prevents thundering herd).
//
// # Eviction
//
// The cache is bounded by maxEntries. When the limit is exceeded, a background
// janitor evicts expired entries. If the map is still over the limit after
// expiry eviction, the oldest entries are evicted (approximated by insertion
// order via a separate slice — O(n) but runs in background, not on hot path).
package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// Entry is a single cached value.
type Entry struct {
	Value     []byte
	ExpiresAt time.Time
	StaleAt   time.Time
}

// IsExpired returns true if the entry has passed its hard expiry.
func (e *Entry) IsExpired() bool { return time.Now().After(e.ExpiresAt) }

// IsStale returns true if the entry is past its freshness window but still
// within the grace period (stale-while-revalidate zone).
func (e *Entry) IsStale() bool {
	now := time.Now()
	return now.After(e.StaleAt) && now.Before(e.ExpiresAt)
}

// GetResult is returned by L1Cache.Get.
type GetResult struct {
	Value        []byte
	Found        bool
	NeedsRefresh bool // true when the entry is stale → caller should async-refresh
}

// L1Cache is a bounded, TTL-aware, stale-while-revalidate in-process cache.
// It is safe for concurrent use.
type L1Cache struct {
	mu         sync.RWMutex
	entries    map[string]*Entry
	order      []string // insertion order for LRU-style eviction
	maxEntries int

	// revalidating tracks keys that already have an in-flight background
	// refresh so we don't fan-out multiple refresh goroutines for the same key.
	revalidating sync.Map

	hits   atomic.Int64
	misses atomic.Int64
	stales atomic.Int64

	stopCh chan struct{}
}

// New creates an L1Cache with the given capacity and starts the background
// janitor.  Call Close() to stop the janitor goroutine.
func New(maxEntries int, janitorInterval time.Duration) *L1Cache {
	c := &L1Cache{
		entries:    make(map[string]*Entry, maxEntries),
		maxEntries: maxEntries,
		stopCh:     make(chan struct{}),
	}
	go c.janitor(janitorInterval)
	return c
}

// Set stores value under key with the given TTL.
//
// staleTTL controls when the entry enters the stale-while-revalidate window.
// It must be <= ttl. A value of 0 disables SWR (staleAt = expiresAt).
func (c *L1Cache) Set(key string, value []byte, ttl, staleTTL time.Duration) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	staleAt := expiresAt
	if staleTTL > 0 && staleTTL < ttl {
		staleAt = now.Add(staleTTL)
	}

	e := &Entry{
		Value:     value,
		ExpiresAt: expiresAt,
		StaleAt:   staleAt,
	}

	c.mu.Lock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = e
	c.mu.Unlock()

	// Clear any revalidation lock — fresh value just written.
	c.revalidating.Delete(key)
}

// Get retrieves an entry by key.
//
// Returns a GetResult with:
//   - Found=false  → cache miss
//   - Found=true, NeedsRefresh=false → fresh hit
//   - Found=true, NeedsRefresh=true  → stale hit; caller should async-refresh
func (c *L1Cache) Get(key string) GetResult {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return GetResult{}
	}
	if e.IsExpired() {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		c.misses.Add(1)
		return GetResult{}
	}

	if e.IsStale() {
		c.stales.Add(1)
		// Only one goroutine should refresh; use CAS to elect it.
		_, alreadyRefreshing := c.revalidating.LoadOrStore(key, struct{}{})
		return GetResult{
			Value:        e.Value,
			Found:        true,
			NeedsRefresh: !alreadyRefreshing,
		}
	}

	c.hits.Add(1)
	return GetResult{Value: e.Value, Found: true}
}

// Delete removes a key immediately.
func (c *L1Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
	c.revalidating.Delete(key)
}

// Stats returns cache statistics.
func (c *L1Cache) Stats() Stats {
	c.mu.RLock()
	size := len(c.entries)
	c.mu.RUnlock()
	return Stats{
		Size:   size,
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
		Stales: c.stales.Load(),
	}
}

// Close stops the background janitor.
func (c *L1Cache) Close() {
	close(c.stopCh)
}

// Stats is a snapshot of cache counters.
type Stats struct {
	Size   int
	Hits   int64
	Misses int64
	Stales int64
}

// ── janitor ───────────────────────────────────────────────────────────────────

func (c *L1Cache) janitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

func (c *L1Cache) evictExpired() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove hard-expired entries.
	for k, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, k)
		}
	}

	// If still over capacity, evict oldest entries (front of insertion-order slice).
	for len(c.entries) > c.maxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}

	// Rebuild order slice to remove keys that were deleted above.
	filtered := c.order[:0]
	for _, k := range c.order {
		if _, ok := c.entries[k]; ok {
			filtered = append(filtered, k)
		}
	}
	c.order = filtered
}
