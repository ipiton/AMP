package publishing

import (
	"sync"
	"time"
)

// CircuitBreakerState represents the current state of circuit breaker
type CircuitBreakerState int

const (
	// StateClosed - circuit is working normally
	StateClosed CircuitBreakerState = iota
	// StateOpen - circuit is broken, rejecting requests
	StateOpen
	// StateHalfOpen - circuit is testing if service recovered
	StateHalfOpen
)

func (s CircuitBreakerState) String() string {
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

// CircuitBreakerConfig holds configuration for circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           // Number of failures before opening
	SuccessThreshold int           // Number of successes before closing from half-open
	Timeout          time.Duration // Time to wait before trying half-open
}

// CircuitBreaker implements circuit breaker pattern per target
type CircuitBreaker struct {
	config          CircuitBreakerConfig
	state           CircuitBreakerState
	failureCount    int
	successCount    int
	lastFailureTime time.Time
	targetName      string
	mu              sync.RWMutex

	// probeInFlight marks the single half-open probe slot as taken;
	// probeStartedAt bounds it. A probe whose outcome is never recorded (an
	// abandoned job that says nothing about the target, see
	// PublishingQueue.processJob) frees the slot after config.Timeout instead
	// of pinning the breaker in half-open forever.
	probeInFlight  bool
	probeStartedAt time.Time
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// NewCircuitBreakerWithName creates a new circuit breaker with target name for logging
func NewCircuitBreakerWithName(config CircuitBreakerConfig, targetName string) *CircuitBreaker {
	return &CircuitBreaker{
		config:     config,
		state:      StateClosed,
		targetName: targetName,
	}
}

// CanAttempt checks if a request can be attempted.
//
// Open lets nothing through until config.Timeout has passed since the last
// failure; then it moves to half-open and admits exactly ONE probe
// (CIRCUIT-BREAKER-HALF-OPEN). Before, it only returned true without changing
// state, so every caller after the timeout went through — the whole backlog
// hit a target that had just been failing — while State() still said "open".
// A write lock, because admitting the probe is a state change.
func (cb *CircuitBreaker) CanAttempt() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.lastFailureTime) <= cb.config.Timeout {
			return false
		}
		cb.state = StateHalfOpen
		cb.successCount = 0
		return cb.takeProbe()
	case StateHalfOpen:
		if cb.probeInFlight && time.Since(cb.probeStartedAt) <= cb.config.Timeout {
			return false
		}
		return cb.takeProbe()
	default:
		return false
	}
}

// takeProbe occupies the half-open probe slot. Caller holds cb.mu.
func (cb *CircuitBreaker) takeProbe() bool {
	cb.probeInFlight = true
	cb.probeStartedAt = time.Now()
	return true
}

// RecordSuccess records a successful attempt
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		// Reset failure count on success
		cb.failureCount = 0
	case StateHalfOpen:
		cb.probeInFlight = false
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			// Transition to closed
			cb.state = StateClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	}
	// StateOpen: a success reported by an attempt admitted before the breaker
	// opened says nothing about recovery; only a half-open probe closes it.
}

// RecordFailure records a failed attempt
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			// Transition to open
			cb.state = StateOpen
		}
	case StateHalfOpen:
		// Go back to open on any failure in half-open
		cb.state = StateOpen
		cb.successCount = 0
		cb.probeInFlight = false
	}
}

// State returns current circuit breaker state
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.probeInFlight = false
}

// GetFailureCount returns current failure count
func (cb *CircuitBreaker) GetFailureCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCount
}

// GetSuccessCount returns current success count (in half-open state)
func (cb *CircuitBreaker) GetSuccessCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.successCount
}

// TargetName returns the target name for this circuit breaker
func (cb *CircuitBreaker) TargetName() string {
	return cb.targetName
}
