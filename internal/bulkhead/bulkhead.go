// Package bulkhead implements the Bulkhead resilience pattern.
//
// A Bulkhead partitions a shared resource (e.g. ClickHouse query workers) into
// per-tenant pools so that a single noisy tenant cannot exhaust the resource
// and starve all other tenants.
//
// Design:
//   - Each tenant gets its own semaphore of size maxConcurrent.
//   - Semaphores are created lazily on first use and are never destroyed
//     (memory is O(distinct tenants), which is bounded by the number of apps).
//   - Acquire returns ErrCapacityExceeded immediately when the semaphore is
//     full (non-blocking) so the caller can return HTTP 429 without queuing.
//   - A global semaphore imposes a hard cap across all tenants to protect the
//     downstream system from total overload.
package bulkhead

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrCapacityExceeded is returned when a tenant's or global concurrency limit
// is reached.
var ErrCapacityExceeded = errors.New("bulkhead capacity exceeded")

// Bulkhead manages per-tenant concurrency limits backed by buffered channels.
type Bulkhead struct {
	mu             sync.Mutex
	semaphores     map[string]chan struct{}
	global         chan struct{} // hard cap across all tenants
	maxPerTenant   int
	maxGlobal      int
	activeTotal    atomic.Int64
	rejectedTotal  atomic.Int64
}

// New creates a Bulkhead.
//
//	maxPerTenant — max concurrent operations for a single tenant (default 50)
//	maxGlobal    — absolute max across all tenants combined (default 500)
func New(maxPerTenant, maxGlobal int) *Bulkhead {
	if maxPerTenant <= 0 {
		maxPerTenant = 50
	}
	if maxGlobal <= 0 {
		maxGlobal = 500
	}
	return &Bulkhead{
		semaphores:   make(map[string]chan struct{}),
		global:       make(chan struct{}, maxGlobal),
		maxPerTenant: maxPerTenant,
		maxGlobal:    maxGlobal,
	}
}

// Acquire attempts to reserve a slot for tenantID.
// Returns nil on success. Returns ErrCapacityExceeded if either the per-tenant
// or the global limit is already at capacity.
// The caller MUST call Release(tenantID) after the operation completes.
func (b *Bulkhead) Acquire(tenantID string) error {
	// Check global limit first (cheaper — no per-tenant map lookup).
	select {
	case b.global <- struct{}{}:
	default:
		b.rejectedTotal.Add(1)
		return fmt.Errorf("%w: global limit %d reached", ErrCapacityExceeded, b.maxGlobal)
	}

	// Check per-tenant limit.
	sem := b.getOrCreate(tenantID)
	select {
	case sem <- struct{}{}:
	default:
		// Release global slot we just acquired.
		<-b.global
		b.rejectedTotal.Add(1)
		return fmt.Errorf("%w: tenant %q limit %d reached", ErrCapacityExceeded, tenantID, b.maxPerTenant)
	}

	b.activeTotal.Add(1)
	return nil
}

// Release frees the slot previously reserved by Acquire.
func (b *Bulkhead) Release(tenantID string) {
	sem := b.getOrCreate(tenantID)
	select {
	case <-sem:
	default:
	}
	select {
	case <-b.global:
	default:
	}
	b.activeTotal.Add(-1)
}

// Do is a convenience wrapper: acquires a slot, runs fn, then releases.
func (b *Bulkhead) Do(tenantID string, fn func() error) error {
	if err := b.Acquire(tenantID); err != nil {
		return err
	}
	defer b.Release(tenantID)
	return fn()
}

// ActiveCount returns the number of currently active operations across all tenants.
func (b *Bulkhead) ActiveCount() int64 { return b.activeTotal.Load() }

// RejectedCount returns the cumulative number of rejected operations.
func (b *Bulkhead) RejectedCount() int64 { return b.rejectedTotal.Load() }

// TenantActive returns the number of active operations for a specific tenant.
func (b *Bulkhead) TenantActive(tenantID string) int {
	b.mu.Lock()
	sem, ok := b.semaphores[tenantID]
	b.mu.Unlock()
	if !ok {
		return 0
	}
	return len(sem)
}

func (b *Bulkhead) getOrCreate(tenantID string) chan struct{} {
	b.mu.Lock()
	sem, ok := b.semaphores[tenantID]
	if !ok {
		sem = make(chan struct{}, b.maxPerTenant)
		b.semaphores[tenantID] = sem
	}
	b.mu.Unlock()
	return sem
}
