// Package resilience provides resilience patterns (retry, circuit breaker, etc.)
package resilience

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"time"

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

// WithRetryFunc executes a function with retry logic
func WithRetryFunc[T any](ctx context.Context, policy *RetryPolicy, fn func() (T, error)) (T, error) {
	var result T
	var lastErr error

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

	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		// Check context
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("context cancelled: %w", err)
		}

		// Execute function
		result, lastErr = fn()

		// Success
		if lastErr == nil {
			if policy.Metrics != nil && policy.OperationName != "" {
				policy.Metrics.WithLabelValues(policy.OperationName, "success").Inc()
			}
			return result, nil
		}

		// Check if error is retryable
		if !policy.ErrorChecker.IsRetryable(lastErr) {
			if policy.Logger != nil {
				policy.Logger.Debug("Error not retryable",
					"operation", policy.OperationName,
					"attempt", attempt,
					"error", lastErr)
			}
			if policy.Metrics != nil && policy.OperationName != "" {
				policy.Metrics.WithLabelValues(policy.OperationName, "non_retryable").Inc()
			}
			return result, lastErr
		}

		// Last attempt failed
		if attempt == policy.MaxRetries {
			if policy.Logger != nil {
				policy.Logger.Warn("Max retries exceeded",
					"operation", policy.OperationName,
					"attempts", attempt+1,
					"error", lastErr)
			}
			if policy.Metrics != nil && policy.OperationName != "" {
				policy.Metrics.WithLabelValues(policy.OperationName, "max_retries").Inc()
			}
			return result, fmt.Errorf("max retries (%d) exceeded: %w", policy.MaxRetries, lastErr)
		}

		// Calculate delay with exponential backoff
		delay := calculateDelay(attempt, policy)

		if policy.Logger != nil {
			policy.Logger.Debug("Retrying after error",
				"operation", policy.OperationName,
				"attempt", attempt+1,
				"delay", delay,
				"error", lastErr)
		}

		if policy.Metrics != nil && policy.OperationName != "" {
			policy.Metrics.WithLabelValues(policy.OperationName, "retry").Inc()
		}

		// Wait before retry
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled during retry: %w", ctx.Err())
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return result, lastErr
}

// calculateDelay calculates the delay for the next retry with exponential backoff
func calculateDelay(attempt int, policy *RetryPolicy) time.Duration {
	// Exponential backoff: baseDelay * multiplier^attempt
	delay := float64(policy.BaseDelay) * math.Pow(policy.Multiplier, float64(attempt))

	// Cap at max delay
	if delay > float64(policy.MaxDelay) {
		delay = float64(policy.MaxDelay)
	}

	// Add jitter if enabled (randomize ±25%)
	if policy.Jitter {
		jitterRange := delay * 0.25
		jitter := (rand.Float64() * 2 * jitterRange) - jitterRange
		delay += jitter
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
