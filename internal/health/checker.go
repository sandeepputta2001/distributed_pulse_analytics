// Package health provides a deep health aggregator that checks all
// downstream dependencies and returns a structured status report.
//
// Liveness vs Readiness:
//
//	/health  — liveness: is the process alive? Always returns 200 unless the
//	            process is wedged (e.g. OOM, deadlock). No dependency checks.
//	/ready   — readiness: can the process serve traffic? Checks all critical
//	            dependencies. Returns 503 if any critical dependency is down so
//	            the load balancer removes the pod from rotation.
//
// This aggregator is used for the /ready endpoint. It runs checks in parallel
// with a shared timeout and returns both an HTTP status code and a JSON body
// with per-dependency detail (useful for dashboards and alerting).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Status represents the health of a single dependency.
type Status struct {
	Name    string        `json:"name"`
	OK      bool          `json:"ok"`
	Latency time.Duration `json:"latency_ms"`
	Error   string        `json:"error,omitempty"`
}

// MarshalJSON customises the latency field to milliseconds.
func (s Status) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Name      string  `json:"name"`
		OK        bool    `json:"ok"`
		LatencyMs float64 `json:"latency_ms"`
		Error     string  `json:"error,omitempty"`
	}
	return json.Marshal(Alias{
		Name:      s.Name,
		OK:        s.OK,
		LatencyMs: float64(s.Latency.Milliseconds()),
		Error:     s.Error,
	})
}

// Report is the full readiness report.
type Report struct {
	OK           bool              `json:"ok"`
	Checks       []Status          `json:"checks"`
	GeneratedAt  time.Time         `json:"generated_at"`
}

// CheckFn is a function that performs a single dependency health check.
// It should respect the context deadline.
type CheckFn func(ctx context.Context) error

// Checker aggregates multiple health check functions.
type Checker struct {
	checks  []namedCheck
	timeout time.Duration
}

type namedCheck struct {
	name     string
	fn       CheckFn
	critical bool // if false, failure degrades but does not mark overall !OK
}

// New creates a Checker with the given per-check timeout.
func New(timeout time.Duration) *Checker {
	return &Checker{timeout: timeout}
}

// AddCritical registers a critical dependency check.
// If this check fails, the overall readiness report is marked not-OK (→ 503).
func (c *Checker) AddCritical(name string, fn CheckFn) {
	c.checks = append(c.checks, namedCheck{name: name, fn: fn, critical: true})
}

// AddOptional registers a non-critical dependency check.
// Failures appear in the report but do not affect the overall OK status.
func (c *Checker) AddOptional(name string, fn CheckFn) {
	c.checks = append(c.checks, namedCheck{name: name, fn: fn, critical: false})
}

// Run executes all checks in parallel and returns the aggregated report.
func (c *Checker) Run(ctx context.Context) Report {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	type result struct {
		idx    int
		status Status
	}
	results := make([]result, len(c.checks))
	ch := make(chan result, len(c.checks))

	var wg sync.WaitGroup
	for i, chk := range c.checks {
		wg.Add(1)
		go func(idx int, chk namedCheck) {
			defer wg.Done()
			start := time.Now()
			err := chk.fn(ctx)
			s := Status{
				Name:    chk.name,
				OK:      err == nil,
				Latency: time.Since(start),
			}
			if err != nil {
				s.Error = err.Error()
			}
			ch <- result{idx: idx, status: s}
		}(i, chk)
	}

	wg.Wait()
	close(ch)

	for r := range ch {
		results[r.idx] = r
	}

	overallOK := true
	statuses := make([]Status, len(c.checks))
	for i, r := range results {
		statuses[i] = r.status
		if !r.status.OK && c.checks[i].critical {
			overallOK = false
		}
	}

	return Report{
		OK:          overallOK,
		Checks:      statuses,
		GeneratedAt: time.Now(),
	}
}

// Handler returns an http.HandlerFunc that runs all checks and responds with
// 200 (OK) or 503 (not ready), plus a JSON body with per-check detail.
func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := c.Run(r.Context())

		w.Header().Set("Content-Type", "application/json")
		if report.OK {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(report)
	}
}
