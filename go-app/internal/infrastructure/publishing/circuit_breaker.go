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
	config           CircuitBreakerConfig
	state            CircuitBreakerState
	failureCount     int
	successCount     int
	lastFailureTime  time.Time
	targetName       string
	metrics          *PublishingMetrics
	mu               sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	return NewCircuitBreakerWithMetrics(config, "", nil)
}

// NewCircuitBreakerWithMetrics creates a new circuit breaker with metrics
func NewCircuitBreakerWithMetrics(config CircuitBreakerConfig, targetName string, metrics *PublishingMetrics) *CircuitBreaker {
	cb := &CircuitBreaker{
		config:     config,
		state:      StateClosed,
		targetName: targetName,
		metrics:    metrics,
	}

	// Initialize metric
	if cb.metrics != nil && cb.targetName != "" {
		cb.metrics.UpdateCircuitBreakerState(cb.targetName, StateClosed)
	}

	return cb
}

// CanAttempt checks if a request can be attempted
func (cb *CircuitBreaker) CanAttempt() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if timeout elapsed to transition to half-open
		if time.Since(cb.lastFailureTime) > cb.config.Timeout {
			// Transition will happen on next attempt
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
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
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			// Transition to closed
			cb.state = StateClosed
			cb.failureCount = 0
			cb.successCount = 0
			// Record recovery metric
			if cb.metrics != nil && cb.targetName != "" {
				cb.metrics.RecordCircuitBreakerRecovery(cb.targetName)
				cb.metrics.UpdateCircuitBreakerState(cb.targetName, StateClosed)
			}
		}
	case StateOpen:
		// Transition to half-open on first success after timeout
		if time.Since(cb.lastFailureTime) > cb.config.Timeout {
			cb.state = StateHalfOpen
			cb.successCount = 1
			cb.failureCount = 0
		}
	}
}

// RecordFailure records a failed attempt
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	oldState := cb.state

	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			// Transition to open
			cb.state = StateOpen
			if cb.metrics != nil && cb.targetName != "" {
				cb.metrics.RecordCircuitBreakerTrip(cb.targetName)
			}
		}
	case StateHalfOpen:
		// Go back to open on any failure in half-open
		cb.state = StateOpen
		cb.successCount = 0
	}

	// Update metric if state changed
	if cb.metrics != nil && cb.targetName != "" && oldState != cb.state {
		cb.metrics.UpdateCircuitBreakerState(cb.targetName, cb.state)
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
