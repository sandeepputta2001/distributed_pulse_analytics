// Package circuitbreaker implements the Circuit Breaker resilience pattern.
//
// States:
//
//	Closed   — normal operation; requests pass through
//	Open     — dependency is unhealthy; requests are rejected immediately (fail-fast)
//	HalfOpen — cooldown elapsed; a single probe request is allowed through to test recovery
//
// Transition rules:
//
//	Closed  → Open     : consecutive failures >= maxFailures
//	Open    → HalfOpen : openTimeout elapsed
//	HalfOpen → Closed  : probe succeeds (successThreshold times)
//	HalfOpen → Open    : probe fails → reset timeout
package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrOpen is returned when the breaker is open and requests are being rejected.
var ErrOpen = errors.New("circuit breaker open")

// State represents the breaker state machine.
type State int

const (
	StateClosed   State = iota // healthy — pass requests through
	StateOpen                  // unhealthy — fail fast
	StateHalfOpen              // probing — allow one test request
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds tuning parameters for a Breaker.
type Config struct {
	// MaxFailures is the number of consecutive failures before opening (default 5).
	MaxFailures int
	// OpenTimeout is how long the breaker stays open before moving to half-open (default 30s).
	OpenTimeout time.Duration
	// SuccessThreshold is the number of consecutive successes in half-open needed to close (default 2).
	SuccessThreshold int
	// OnStateChange is called whenever the state transitions (optional).
	OnStateChange func(name string, from, to State)
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.MaxFailures <= 0 {
		out.MaxFailures = 5
	}
	if out.OpenTimeout <= 0 {
		out.OpenTimeout = 30 * time.Second
	}
	if out.SuccessThreshold <= 0 {
		out.SuccessThreshold = 2
	}
	return out
}

// Counts tracks rolling success/failure counts.
type Counts struct {
	Requests             uint64
	TotalSuccesses       uint64
	TotalFailures        uint64
	ConsecutiveSuccesses uint64
	ConsecutiveFailures  uint64
}

// Breaker is a thread-safe circuit breaker.
type Breaker struct {
	name    string
	cfg     Config
	mu      sync.Mutex
	state   State
	counts  Counts
	openAt  time.Time // when the breaker opened (used to compute half-open eligibility)
}

// New creates a named Breaker with the given config.
func New(name string, cfg Config) *Breaker {
	return &Breaker{
		name:  name,
		cfg:   cfg.withDefaults(),
		state: StateClosed,
	}
}

// Execute runs fn through the breaker.
//
//   - If open, returns ErrOpen immediately (fail-fast).
//   - If closed or half-open, calls fn and records the outcome.
//   - Wraps non-nil errors with context so callers can identify them.
func (b *Breaker) Execute(fn func() error) error {
	if err := b.allow(); err != nil {
		return err
	}

	err := fn()
	b.record(err)
	return err
}

// State returns the current breaker state (safe for concurrent read).
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeTransitionToHalfOpen()
	return b.state
}

// Counts returns a snapshot of the current counters.
func (b *Breaker) Counts() Counts {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.counts
}

// Reset forces the breaker back to Closed (useful in tests or manual recovery).
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transition(StateClosed)
}

// ── internal ──────────────────────────────────────────────────────────────────

func (b *Breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.maybeTransitionToHalfOpen()

	switch b.state {
	case StateClosed:
		return nil
	case StateHalfOpen:
		// Allow only the first probe through.
		// Subsequent callers are rejected until the probe resolves.
		if b.counts.Requests == 0 {
			return nil
		}
		return fmt.Errorf("%w: %s (half-open, probe in progress)", ErrOpen, b.name)
	default: // StateOpen
		return fmt.Errorf("%w: %s", ErrOpen, b.name)
	}
}

func (b *Breaker) record(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.counts.Requests++

	if err != nil {
		b.counts.TotalFailures++
		b.counts.ConsecutiveFailures++
		b.counts.ConsecutiveSuccesses = 0

		switch b.state {
		case StateClosed:
			if int(b.counts.ConsecutiveFailures) >= b.cfg.MaxFailures {
				b.transition(StateOpen)
			}
		case StateHalfOpen:
			// Probe failed — reopen.
			b.transition(StateOpen)
		}
	} else {
		b.counts.TotalSuccesses++
		b.counts.ConsecutiveSuccesses++
		b.counts.ConsecutiveFailures = 0

		if b.state == StateHalfOpen && int(b.counts.ConsecutiveSuccesses) >= b.cfg.SuccessThreshold {
			b.transition(StateClosed)
		}
	}
}

// maybeTransitionToHalfOpen moves an open breaker to half-open when the
// timeout has elapsed. Must be called with b.mu held.
func (b *Breaker) maybeTransitionToHalfOpen() {
	if b.state == StateOpen && time.Since(b.openAt) >= b.cfg.OpenTimeout {
		b.transition(StateHalfOpen)
	}
}

// transition changes state and resets relevant counters. Must be called with b.mu held.
func (b *Breaker) transition(next State) {
	if b.state == next {
		return
	}
	prev := b.state
	b.state = next
	b.counts = Counts{} // reset on every transition

	if next == StateOpen {
		b.openAt = time.Now()
	}

	if b.cfg.OnStateChange != nil {
		// Call without holding the lock to avoid deadlocks in the callback.
		go b.cfg.OnStateChange(b.name, prev, next)
	}
}
