// Package resilience provides resilience patterns (retry, circuit breaker, etc.)
//
// Note: Retry logic migrated to pkg/retry (TN-057).
// This package now wraps pkg/retry for backward compatibility.
package resilience

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/pkg/retry"
	"github.com/prometheus/client_golang/prometheus"
)

// ================================================================================
// Retry Policy
// ================================================================================

// RetryPolicy defines retry behavior
type RetryPolicy struct {
	MaxRetries    int
	BaseDelay     time.Duration
	MaxDelay      time.Duration
	Multiplier    float64
	Jitter        bool
	ErrorChecker  ErrorChecker
	Logger        *slog.Logger
	Metrics       *prometheus.CounterVec
	OperationName string
}

// ErrorChecker determines if an error is retryable
type ErrorChecker interface {
	IsRetryable(error) bool
}

// DefaultErrorChecker retries all errors
type DefaultErrorChecker struct{}

func (d *DefaultErrorChecker) IsRetryable(err error) bool {
	return err != nil
}

// ================================================================================
// Retry Function
// ================================================================================

// WithRetryFunc executes a function with retry logic.
//
// Migrated to use pkg/retry for unified retry strategy (TN-057).
// This function now wraps retry.Do for backward compatibility.
func WithRetryFunc[T any](ctx context.Context, policy *RetryPolicy, fn func() (T, error)) (T, error) {
	// Apply defaults
	if policy.MaxRetries == 0 {
		policy.MaxRetries = 3
	}
	if policy.BaseDelay == 0 {
		policy.BaseDelay = 100 * time.Millisecond
	}
	if policy.MaxDelay == 0 {
		policy.MaxDelay = 30 * time.Second
	}
	if policy.Multiplier == 0 {
		policy.Multiplier = 2.0
	}
	if policy.ErrorChecker == nil {
		policy.ErrorChecker = &DefaultErrorChecker{}
	}

	// Convert RetryPolicy to retry.Strategy
	strategy := retry.Strategy{
		MaxAttempts:     policy.MaxRetries + 1, // +1 because retry.Strategy counts attempts, not retries
		BaseDelay:       policy.BaseDelay,
		MaxDelay:        policy.MaxDelay,
		Multiplier:      policy.Multiplier,
		JitterRatio:     0.15, // 15% jitter (was controlled by policy.Jitter, now always on)
		ErrorClassifier: &errorCheckerAdapter{policy.ErrorChecker},
		Logger:          policy.Logger,
		OperationName:   policy.OperationName,
		Metrics:         policy.Metrics,
	}

	// Execute with unified retry logic
	return retry.Do(ctx, strategy, fn)
}

// errorCheckerAdapter adapts resilience.ErrorChecker to retry.ErrorClassifier
type errorCheckerAdapter struct {
	checker ErrorChecker
}

func (a *errorCheckerAdapter) IsRetryable(err error) bool {
	if a.checker == nil {
		return true // Default: retry all errors
	}
	return a.checker.IsRetryable(err)
}

// calculateDelay is deprecated - use pkg/retry instead.
//
// Deprecated: This function is kept for backward compatibility only.
// New code should use pkg/retry.Strategy.
// Note: Jitter is now handled by pkg/retry, not here.
func calculateDelay(attempt int, policy *RetryPolicy) time.Duration {
	// This function is no longer used internally but kept for API compatibility
	// Simplified implementation (jitter now handled by pkg/retry)
	delay := float64(policy.BaseDelay) * float64(attempt+1)

	// Cap at max delay
	if delay > float64(policy.MaxDelay) {
		delay = float64(policy.MaxDelay)
	}

	return time.Duration(delay)
}

// ================================================================================
// Circuit Breaker (stub for future implementation)
// ================================================================================

// CircuitBreaker provides circuit breaker pattern - stub
type CircuitBreaker struct {
	// TODO: Implement circuit breaker
}

// NewCircuitBreaker creates a new circuit breaker - stub
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{}
}

// ================================================================================
// Bulkhead (stub for future implementation)
// ================================================================================

// Bulkhead provides bulkhead pattern - stub
type Bulkhead struct {
	// TODO: Implement bulkhead
}

// NewBulkhead creates a new bulkhead - stub
func NewBulkhead() *Bulkhead {
	return &Bulkhead{}
}
