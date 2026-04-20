package embed

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the circuit breaker is open and a call is attempted.
var ErrCircuitOpen = errors.New("embed: circuit breaker is open")

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota
	StateOpen
	StateHalfOpen
)

const (
	defaultFailureThreshold = 3
	defaultOpenDuration     = 30 * time.Second
)

// CircuitBreaker guards EmbedSvc calls.
// Three consecutive failures open the circuit for openDuration; after that
// one probe is allowed (half-open). A successful probe closes the circuit.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	failureThreshold int
	openDuration     time.Duration
	openedAt         time.Time
}

// Option configures a CircuitBreaker.
type Option func(*CircuitBreaker)

// WithOpenDuration overrides the 30-second default open window.
func WithOpenDuration(d time.Duration) Option {
	return func(cb *CircuitBreaker) {
		cb.openDuration = d
	}
}

// NewCircuitBreaker returns a closed CircuitBreaker ready for use.
func NewCircuitBreaker(opts ...Option) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: defaultFailureThreshold,
		openDuration:     defaultOpenDuration,
	}
	for _, o := range opts {
		o(cb)
	}
	return cb
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Allow returns nil if a call is permitted, ErrCircuitOpen otherwise.
// When the open window expires the circuit transitions to half-open and
// allows exactly one probe call through.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.openDuration {
			cb.state = StateHalfOpen
			return nil
		}
		return ErrCircuitOpen
	default:
		return nil
	}
}

// RecordFailure increments the failure counter and opens the circuit if the
// threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	if cb.failures >= cb.failureThreshold {
		cb.state = StateOpen
		cb.openedAt = time.Now()
	}
}

// RecordSuccess resets the failure counter and closes the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = StateClosed
}
