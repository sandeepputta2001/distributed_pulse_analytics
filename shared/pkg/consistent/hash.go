// Package consistent implements a consistent hashing ring with virtual nodes.
//
// Consistent hashing ensures that when a node is added or removed, only
// K/N keys (K = total keys, N = number of nodes) are remapped on average —
// compared to K keys for simple modular hashing.
//
// Virtual nodes (vnodes): each physical node is mapped to `replicas` points
// on the ring. More vnodes → more even key distribution, especially with
// heterogeneous node capacities.
//
// Use cases in PulseAnalytics:
//   - Distributing cache invalidation work across gateway pods
//   - Routing tenant write traffic to specific ClickHouse shards
//   - Assigning Kafka partition ownership for session/funnel processing
package consistent

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

const defaultReplicas = 150 // virtual nodes per physical node

// Ring is a consistent hash ring. It is safe for concurrent use.
type Ring struct {
	mu       sync.RWMutex
	ring     map[uint32]string // hash point → node name
	sorted   []uint32          // sorted hash points for binary search
	nodes    map[string]int    // node name → vnode count
	replicas int
}

// New creates a consistent hash ring.
// replicas controls the number of virtual nodes per physical node (default 150).
func New(replicas int) *Ring {
	if replicas <= 0 {
		replicas = defaultReplicas
	}
	return &Ring{
		ring:     make(map[uint32]string),
		nodes:    make(map[string]int),
		replicas: replicas,
	}
}

// Add registers a node with the ring using the default vnode count.
func (r *Ring) Add(node string) {
	r.AddWeighted(node, r.replicas)
}

// AddWeighted registers a node with a custom vnode count.
// Higher weight means the node receives a proportionally larger share of keys.
func (r *Ring) AddWeighted(node string, vnodes int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[node]; exists {
		return // already present
	}
	r.nodes[node] = vnodes

	for i := 0; i < vnodes; i++ {
		h := hashKey(fmt.Sprintf("%s:%d", node, i))
		r.ring[h] = node
		r.sorted = append(r.sorted, h)
	}
	sort.Slice(r.sorted, func(i, j int) bool { return r.sorted[i] < r.sorted[j] })
}

// Remove deregisters a node and all its virtual nodes from the ring.
func (r *Ring) Remove(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	vnodes, ok := r.nodes[node]
	if !ok {
		return
	}
	delete(r.nodes, node)

	toRemove := make(map[uint32]struct{}, vnodes)
	for i := 0; i < vnodes; i++ {
		h := hashKey(fmt.Sprintf("%s:%d", node, i))
		delete(r.ring, h)
		toRemove[h] = struct{}{}
	}

	filtered := r.sorted[:0]
	for _, h := range r.sorted {
		if _, removed := toRemove[h]; !removed {
			filtered = append(filtered, h)
		}
	}
	r.sorted = filtered
}

// Get returns the node responsible for the given key.
// Returns "" if the ring is empty.
func (r *Ring) Get(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.sorted) == 0 {
		return ""
	}

	h := hashKey(key)
	// Find the first vnode whose hash >= h (clockwise search on the ring).
	idx := sort.Search(len(r.sorted), func(i int) bool {
		return r.sorted[i] >= h
	})
	if idx == len(r.sorted) {
		idx = 0 // wrap around
	}
	return r.ring[r.sorted[idx]]
}

// GetN returns the first n distinct nodes responsible for the key (for
// replication). The list is ordered by ring proximity.
func (r *Ring) GetN(key string, n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.nodes) == 0 {
		return nil
	}
	if n > len(r.nodes) {
		n = len(r.nodes)
	}

	h := hashKey(key)
	idx := sort.Search(len(r.sorted), func(i int) bool {
		return r.sorted[i] >= h
	})

	seen := make(map[string]struct{}, n)
	result := make([]string, 0, n)

	for len(result) < n {
		node := r.ring[r.sorted[idx%len(r.sorted)]]
		if _, dup := seen[node]; !dup {
			seen[node] = struct{}{}
			result = append(result, node)
		}
		idx++
	}
	return result
}

// Nodes returns the list of physical nodes in the ring.
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.nodes))
	for n := range r.nodes {
		out = append(out, n)
	}
	return out
}

// Size returns the number of physical nodes.
func (r *Ring) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// hashKey converts a string key to a uint32 ring position using SHA-256.
// SHA-256 gives excellent uniformity with negligible collision probability.
func hashKey(key string) uint32 {
	sum := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(sum[:4])
}
