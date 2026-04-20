package adapter

import (
	"sync"
	"time"
)

// State represents the circuit breaker's operational state.
type State int

const (
	StateClosed   State = iota // normal — requests pass through
	StateOpen                  // failing — requests are rejected immediately
	StateHalfOpen              // recovering — exactly one probe is allowed
)

// CircuitBreaker implements the three-state circuit breaker pattern:
//
//	Closed → Open (after 3 consecutive failures)
//	Open   → HalfOpen (after 30 s cooldown)
//	HalfOpen → Closed (probe success) | Open (probe failure)
type CircuitBreaker struct {
	mu        sync.Mutex
	state     State
	failures  int
	threshold int
	openUntil time.Time
	cooldown  time.Duration

	// NowFn returns the current time. Overridable in tests to simulate time passage.
	NowFn func() time.Time
}

// NewCircuitBreaker returns a breaker with production defaults (threshold=3, cooldown=30s).
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		threshold: 3,
		cooldown:  30 * time.Second,
		NowFn:     time.Now,
	}
}

// Allow returns true if the request may proceed.
//
//   - Closed: always true.
//   - Open: true only after the cooldown expires (transitions to HalfOpen).
//   - HalfOpen: true only for the first probe; subsequent callers are rejected.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		if cb.NowFn().After(cb.openUntil) {
			cb.state = StateHalfOpen
			return true // this call is the probe
		}
		return false

	case StateHalfOpen:
		// Only one concurrent probe is permitted; all others are rejected.
		return false
	}

	return false
}

// RecordSuccess resets the breaker to Closed and clears the failure counter.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
}

// RecordFailure increments the consecutive failure count.
// When the threshold is reached the circuit trips to Open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.threshold {
		cb.state = StateOpen
		cb.openUntil = cb.NowFn().Add(cb.cooldown)
	}
}

// GetState returns the current circuit state (safe for concurrent use).
func (cb *CircuitBreaker) GetState() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
