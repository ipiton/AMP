// Package llm provides LLM proxy client for alert classification with circuit breaker pattern.
package llm

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// CircuitBreakerState represents the state of the circuit breaker.
type CircuitBreakerState int

const (
	// StateClosed means circuit breaker is operational - all requests pass through.
	StateClosed CircuitBreakerState = iota
	// StateOpen means circuit breaker is open - requests fail-fast without calling LLM.
	StateOpen
	// StateHalfOpen means circuit breaker is testing if service recovered - limited requests allowed.
	StateHalfOpen
)

// String returns human-readable state name.
func (s CircuitBreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// callResult represents a single call result for sliding window calculation.
type callResult struct {
	timestamp time.Time
	success   bool
	duration  time.Duration
	slow      bool
}

// CircuitBreaker implements the circuit breaker pattern for LLM calls.
// It prevents cascading failures by failing fast when the LLM service is unavailable.
// Thread-safe for concurrent use.
type CircuitBreaker struct {
	// Configuration (immutable after creation)
	maxFailures      int
	resetTimeout     time.Duration
	failureThreshold float64
	timeWindow       time.Duration
	slowCallDuration time.Duration
	halfOpenMaxCalls int

	// State (protected by mutex)
	mu                   sync.RWMutex
	state                CircuitBreakerState
	failureCount         int
	successCount         int
	consecutiveFailures  int
	consecutiveSuccesses int
	lastStateChange      time.Time
	lastFailure          time.Time
	lastSuccess          time.Time
	halfOpenCalls        int

	// Sliding window for failure rate calculation
	callResults []callResult

	// Observability
	logger  *slog.Logger
	metrics *CircuitBreakerMetrics
}

// CircuitBreakerConfig holds configuration for circuit breaker.
type CircuitBreakerConfig struct {
	// MaxFailures is the threshold of consecutive failures before opening the circuit.
	MaxFailures int `mapstructure:"max_failures"`

	// ResetTimeout is the duration to wait in open state before transitioning to half-open.
	ResetTimeout time.Duration `mapstructure:"reset_timeout"`

	// FailureThreshold is the failure rate (0.0-1.0) to trigger opening the circuit.
	FailureThreshold float64 `mapstructure:"failure_threshold"`

	// TimeWindow is the duration for calculating failure rate.
	TimeWindow time.Duration `mapstructure:"time_window"`

	// SlowCallDuration is the threshold above which calls are considered slow (and counted as failures).
	SlowCallDuration time.Duration `mapstructure:"slow_call_duration"`

	// HalfOpenMaxCalls is the number of test calls allowed in half-open state.
	HalfOpenMaxCalls int `mapstructure:"half_open_max_calls"`

	// Enabled controls whether circuit breaker is active.
	Enabled bool `mapstructure:"enabled"`
}

// DefaultCircuitBreakerConfig returns production-ready default configuration.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxFailures:      5,
		ResetTimeout:     30 * time.Second,
		FailureThreshold: 0.5, // 50% failure rate
		TimeWindow:       60 * time.Second,
		SlowCallDuration: 3 * time.Second,
		HalfOpenMaxCalls: 1,
		Enabled:          true,
	}
}

// Validate checks if configuration is valid.
func (c CircuitBreakerConfig) Validate() error {
	if c.MaxFailures <= 0 {
		return errors.New("max_failures must be positive")
	}
	if c.ResetTimeout <= 0 {
		return errors.New("reset_timeout must be positive")
	}
	if c.FailureThreshold < 0 || c.FailureThreshold > 1 {
		return errors.New("failure_threshold must be between 0 and 1")
	}
	if c.TimeWindow <= 0 {
		return errors.New("time_window must be positive")
	}
	if c.SlowCallDuration <= 0 {
		return errors.New("slow_call_duration must be positive")
	}
	if c.HalfOpenMaxCalls <= 0 {
		return errors.New("half_open_max_calls must be positive")
	}
	return nil
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
func NewCircuitBreaker(config CircuitBreakerConfig, logger *slog.Logger, metrics *CircuitBreakerMetrics) (*CircuitBreaker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	cb := &CircuitBreaker{
		maxFailures:      config.MaxFailures,
		resetTimeout:     config.ResetTimeout,
		failureThreshold: config.FailureThreshold,
		timeWindow:       config.TimeWindow,
		slowCallDuration: config.SlowCallDuration,
		halfOpenMaxCalls: config.HalfOpenMaxCalls,
		state:            StateClosed,
		lastStateChange:  time.Now(),
		callResults:      make([]callResult, 0, 100), // Pre-allocate for performance
		logger:           logger,
		metrics:          metrics,
	}

	// Initialize metrics
	if metrics != nil {
		metrics.State.Set(float64(StateClosed))
	}

	return cb, nil
}

// Call executes the operation through circuit breaker.
// Returns ErrCircuitBreakerOpen if circuit is open.
func (cb *CircuitBreaker) Call(ctx context.Context, operation func(ctx context.Context) error) error {
	// Check if allowed (read lock for performance)
	if err := cb.beforeCall(); err != nil {
		return err
	}

	// Execute operation and measure duration
	startTime := time.Now()
	err := operation(ctx)
	duration := time.Since(startTime)

	// Record result (write lock)
	cb.afterCall(err, duration)

	return err
}

// beforeCall checks if request is allowed based on current state.
func (cb *CircuitBreaker) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateOpen:
		// Check if should transition to half-open
		if time.Since(cb.lastStateChange) >= cb.resetTimeout {
			cb.transitionToHalfOpenUnsafe()
			return nil // Allow test request
		}

		// Still in open state - fail fast
		if cb.metrics != nil {
			cb.metrics.RequestsBlocked.Inc()
		}

		cb.logger.Debug("Circuit breaker is open, request blocked",
			"time_since_open", time.Since(cb.lastStateChange),
			"reset_timeout", cb.resetTimeout,
		)

		return ErrCircuitBreakerOpen

	case StateHalfOpen:
		// In half-open, allow limited test calls
		if cb.halfOpenCalls >= cb.halfOpenMaxCalls {
			// Wait for ongoing test calls to complete
			if cb.metrics != nil {
				cb.metrics.RequestsBlocked.Inc()
			}
			return ErrCircuitBreakerOpen
		}

		cb.halfOpenCalls++
		if cb.metrics != nil {
			cb.metrics.HalfOpenRequests.Inc()
		}

		return nil

	case StateClosed:
		// Normal operation - allow request
		return nil
	}

	return nil
}

// afterCall records the result and updates state machine.
func (cb *CircuitBreaker) afterCall(err error, duration time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Determine if call was successful
	isSlow := duration >= cb.slowCallDuration
	isSuccess := err == nil && !isSlow

	// Record result in sliding window
	now := time.Now()
	cb.callResults = append(cb.callResults, callResult{
		timestamp: now,
		success:   isSuccess,
		duration:  duration,
		slow:      isSlow,
	})

	// Clean old results outside time window
	cb.cleanOldResultsUnsafe()

	// Update counters
	if isSuccess {
		cb.successCount++
		cb.consecutiveSuccesses++
		cb.consecutiveFailures = 0
		cb.lastSuccess = now

		if cb.metrics != nil {
			cb.metrics.Successes.Inc()
			cb.metrics.CallDuration.WithLabelValues("success").Observe(duration.Seconds())
		}

		cb.logger.Debug("Circuit breaker recorded success",
			"duration", duration,
			"consecutive_successes", cb.consecutiveSuccesses,
			"state", cb.state,
		)
	} else {
		cb.failureCount++
		cb.consecutiveFailures++
		cb.consecutiveSuccesses = 0
		cb.lastFailure = now

		if cb.metrics != nil {
			cb.metrics.Failures.Inc()
			if isSlow {
				cb.metrics.SlowCalls.Inc()
			}
			cb.metrics.CallDuration.WithLabelValues("failure").Observe(duration.Seconds())
		}

		cb.logger.Warn("Circuit breaker recorded failure",
			"error", err,
			"duration", duration,
			"slow", isSlow,
			"consecutive_failures", cb.consecutiveFailures,
			"state", cb.state,
		)
	}

	// State machine transitions
	switch cb.state {
	case StateClosed:
		if cb.shouldOpenUnsafe() {
			cb.transitionToOpenUnsafe()
		}

	case StateHalfOpen:
		if isSuccess {
			// First success in half-open → transition to closed
			cb.transitionToClosedUnsafe()
		} else {
			// Failure in half-open → back to open
			cb.transitionToOpenUnsafe()
		}
	}
}

// shouldOpenUnsafe determines if circuit should open (must be called with lock held).
func (cb *CircuitBreaker) shouldOpenUnsafe() bool {
	// Not enough data yet
	if len(cb.callResults) < cb.maxFailures {
		return false
	}

	// Check consecutive failures (fast path)
	if cb.consecutiveFailures >= cb.maxFailures {
		return true
	}

	// Check failure rate in time window
	totalCalls := len(cb.callResults)
	failures := 0
	for _, result := range cb.callResults {
		if !result.success {
			failures++
		}
	}

	failureRate := float64(failures) / float64(totalCalls)
	return failureRate >= cb.failureThreshold
}

// transitionToOpenUnsafe transitions to OPEN state (must be called with lock held).
func (cb *CircuitBreaker) transitionToOpenUnsafe() {
	oldState := cb.state
	cb.state = StateOpen
	cb.lastStateChange = time.Now()
	cb.halfOpenCalls = 0

	cb.logger.Warn("Circuit breaker opened",
		"previous_state", oldState,
		"failure_count", cb.failureCount,
		"consecutive_failures", cb.consecutiveFailures,
		"reset_timeout", cb.resetTimeout,
		"last_failure", cb.lastFailure.Format(time.RFC3339),
	)

	if cb.metrics != nil {
		cb.metrics.StateChanges.WithLabelValues(oldState.String(), "open").Inc()
		cb.metrics.State.Set(float64(StateOpen))
	}
}

// transitionToHalfOpenUnsafe transitions to HALF_OPEN state (must be called with lock held).
func (cb *CircuitBreaker) transitionToHalfOpenUnsafe() {
	oldState := cb.state
	cb.state = StateHalfOpen
	cb.lastStateChange = time.Now()
	cb.halfOpenCalls = 0

	cb.logger.Info("Circuit breaker entering half-open state",
		"previous_state", oldState,
		"time_since_open", time.Since(cb.lastFailure),
		"last_failure", cb.lastFailure.Format(time.RFC3339),
	)

	if cb.metrics != nil {
		cb.metrics.StateChanges.WithLabelValues(oldState.String(), "half_open").Inc()
		cb.metrics.State.Set(float64(StateHalfOpen))
	}
}

// transitionToClosedUnsafe transitions to CLOSED state (must be called with lock held).
func (cb *CircuitBreaker) transitionToClosedUnsafe() {
	oldState := cb.state
	cb.state = StateClosed
	cb.lastStateChange = time.Now()
	cb.halfOpenCalls = 0

	// Reset counters for fresh start
	cb.failureCount = 0
	cb.consecutiveFailures = 0
	cb.callResults = make([]callResult, 0, 100)

	cb.logger.Info("Circuit breaker closed",
		"previous_state", oldState,
		"success_count", cb.successCount,
		"time_since_last_failure", time.Since(cb.lastFailure),
	)

	if cb.metrics != nil {
		cb.metrics.StateChanges.WithLabelValues(oldState.String(), "closed").Inc()
		cb.metrics.State.Set(float64(StateClosed))
	}
}

// cleanOldResultsUnsafe removes results outside time window (must be called with lock held).
func (cb *CircuitBreaker) cleanOldResultsUnsafe() {
	cutoff := time.Now().Add(-cb.timeWindow)

	// Find first index within window
	firstValid := 0
	for i, result := range cb.callResults {
		if result.timestamp.After(cutoff) {
			firstValid = i
			break
		}
		// Mark old result for garbage collection
		cb.callResults[i] = callResult{}
	}

	// Slice to keep only recent results
	if firstValid > 0 {
		cb.callResults = cb.callResults[firstValid:]
	}
}

// GetState returns current state (thread-safe).
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// GetStats returns current statistics (thread-safe).
func (cb *CircuitBreaker) GetStats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var nextRetryAt time.Time
	if cb.state == StateOpen {
		nextRetryAt = cb.lastStateChange.Add(cb.resetTimeout)
	}

	return CircuitBreakerStats{
		State:                cb.state,
		FailureCount:         cb.failureCount,
		SuccessCount:         cb.successCount,
		ConsecutiveFailures:  cb.consecutiveFailures,
		ConsecutiveSuccesses: cb.consecutiveSuccesses,
		LastFailure:          cb.lastFailure,
		LastSuccess:          cb.lastSuccess,
		LastStateChange:      cb.lastStateChange,
		TotalCalls:           len(cb.callResults),
		NextRetryAt:          nextRetryAt,
	}
}

// Reset resets circuit breaker to initial closed state (for testing/manual intervention).
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	oldState := cb.state
	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses = 0
	cb.halfOpenCalls = 0
	cb.callResults = make([]callResult, 0, 100)
	cb.lastStateChange = time.Now()

	cb.logger.Info("Circuit breaker manually reset",
		"previous_state", oldState,
	)

	if cb.metrics != nil {
		cb.metrics.State.Set(float64(StateClosed))
	}
}

// CircuitBreakerStats holds circuit breaker statistics.
type CircuitBreakerStats struct {
	State                CircuitBreakerState
	FailureCount         int
	SuccessCount         int
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastFailure          time.Time
	LastSuccess          time.Time
	LastStateChange      time.Time
	TotalCalls           int
	NextRetryAt          time.Time
}
