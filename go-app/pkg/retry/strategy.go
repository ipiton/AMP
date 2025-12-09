// Package retry provides unified retry strategy with exponential backoff and jitter.
//
// This package consolidates 6 different retry implementations across the codebase:
//   - pkg/httperror/retry.go (was unused)
//   - internal/core/resilience/resilience.go (LLM)
//   - internal/infrastructure/k8s/client.go (K8s discovery)
//   - internal/infrastructure/publishing/queue.go (critical path!)
//   - internal/business/publishing/refresh_retry.go (refresh manager)
//   - internal/infrastructure/migrations/errors.go (DB migrations)
//
// Usage:
//
//	strategy := retry.Default()
//	result, err := retry.Do(ctx, strategy, func() (string, error) {
//	    return someOperation()
//	})
//
// Or for operations without return value:
//
//	err := retry.DoSimple(ctx, strategy, func() error {
//	    return someOperation()
//	})
package retry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/prometheus/client_golang/prometheus"
)

// Strategy defines retry configuration with exponential backoff and jitter.
//
// Thread-safe: All methods are safe for concurrent use.
type Strategy struct {
	// MaxAttempts is the maximum number of attempts (including initial attempt).
	// Default: 3 (1 initial + 2 retries)
	MaxAttempts int

	// BaseDelay is the initial delay between retries.
	// Default: 100ms
	// Formula: delay = BaseDelay * Multiplier^attempt
	BaseDelay time.Duration

	// MaxDelay is the maximum delay between retries (cap).
	// Default: 30s
	MaxDelay time.Duration

	// Multiplier is the exponential backoff multiplier.
	// Default: 2.0 (doubles each retry)
	// Common values: 1.5 (moderate), 2.0 (standard), 3.0 (aggressive)
	Multiplier float64

	// JitterRatio adds randomness to prevent thundering herd.
	// Value range: 0.0 - 1.0
	// Default: 0.15 (±15% randomness)
	// Formula: delay ± (delay * JitterRatio * random(-1..1))
	JitterRatio float64

	// ErrorClassifier determines if an error is retryable.
	// If nil, defaults to HTTPErrorClassifier.
	ErrorClassifier ErrorClassifier

	// Metrics is an optional Prometheus counter for retry attempts.
	// Labels: {operation, result} where result is "success"|"retry"|"max_retries"|"non_retryable"
	Metrics *prometheus.CounterVec

	// Logger is an optional structured logger.
	// If nil, no logging is performed.
	Logger *slog.Logger

	// OperationName is used for logging and metrics labels.
	// Optional, defaults to "unknown"
	OperationName string
}

// ErrorClassifier determines if an error should trigger a retry.
//
// Implementations must be thread-safe.
type ErrorClassifier interface {
	// IsRetryable returns true if the error is transient and retry should be attempted.
	// Examples of retryable errors:
	//   - Network timeouts
	//   - 5xx server errors
	//   - 429 rate limits
	//   - Connection refused
	//
	// Examples of non-retryable errors:
	//   - 4xx client errors (except 429)
	//   - Invalid input/validation errors
	//   - Authorization errors
	IsRetryable(err error) bool
}

// HTTPErrorClassifier classifies HTTP API errors using pkg/httperror.
//
// Thread-safe: Stateless implementation.
type HTTPErrorClassifier struct{}

// IsRetryable implements ErrorClassifier for HTTP errors.
func (c *HTTPErrorClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Delegate to httperror package (handles HTTPAPIError + network errors)
	return httperror.IsRetryable(err)
}

// Default returns a recommended strategy for most use cases.
//
// Configuration:
//   - MaxAttempts: 3 (1 initial + 2 retries)
//   - BaseDelay: 100ms
//   - MaxDelay: 30s
//   - Multiplier: 2.0 (exponential)
//   - JitterRatio: 0.15 (±15%)
//   - ErrorClassifier: HTTPErrorClassifier
func Default() Strategy {
	return Strategy{
		MaxAttempts:     3,
		BaseDelay:       100 * time.Millisecond,
		MaxDelay:        30 * time.Second,
		Multiplier:      2.0,
		JitterRatio:     0.15,
		ErrorClassifier: &HTTPErrorClassifier{},
	}
}

// Aggressive returns a strategy with more frequent retries for critical operations.
//
// Configuration:
//   - MaxAttempts: 5 (1 initial + 4 retries)
//   - BaseDelay: 50ms (faster start)
//   - MaxDelay: 10s (lower cap)
//   - Multiplier: 1.5 (slower growth)
//   - JitterRatio: 0.2 (±20%)
func Aggressive() Strategy {
	return Strategy{
		MaxAttempts:     5,
		BaseDelay:       50 * time.Millisecond,
		MaxDelay:        10 * time.Second,
		Multiplier:      1.5,
		JitterRatio:     0.2,
		ErrorClassifier: &HTTPErrorClassifier{},
	}
}

// Conservative returns a strategy with fewer, longer-spaced retries.
//
// Configuration:
//   - MaxAttempts: 2 (1 initial + 1 retry)
//   - BaseDelay: 500ms
//   - MaxDelay: 60s
//   - Multiplier: 3.0 (faster growth)
//   - JitterRatio: 0.1 (±10%)
func Conservative() Strategy {
	return Strategy{
		MaxAttempts:     2,
		BaseDelay:       500 * time.Millisecond,
		MaxDelay:        60 * time.Second,
		Multiplier:      3.0,
		JitterRatio:     0.1,
		ErrorClassifier: &HTTPErrorClassifier{},
	}
}

// NoRetry returns a strategy that never retries (useful for testing).
func NoRetry() Strategy {
	return Strategy{
		MaxAttempts:     1,
		ErrorClassifier: &HTTPErrorClassifier{},
	}
}

// WithLogger returns a copy of the strategy with the specified logger.
func (s Strategy) WithLogger(logger *slog.Logger) Strategy {
	s.Logger = logger
	return s
}

// WithMetrics returns a copy of the strategy with the specified metrics.
func (s Strategy) WithMetrics(metrics *prometheus.CounterVec, operationName string) Strategy {
	s.Metrics = metrics
	s.OperationName = operationName
	return s
}

// WithMaxAttempts returns a copy of the strategy with the specified max attempts.
func (s Strategy) WithMaxAttempts(max int) Strategy {
	s.MaxAttempts = max
	return s
}

// WithErrorClassifier returns a copy of the strategy with the specified classifier.
func (s Strategy) WithErrorClassifier(classifier ErrorClassifier) Strategy {
	s.ErrorClassifier = classifier
	return s
}

// Do executes operation with retry logic, returning the result or error.
//
// Parameters:
//   - ctx: Context for cancellation/timeout
//   - strategy: Retry configuration
//   - operation: Function to execute (can return any type T)
//
// Returns:
//   - T: Result from successful operation
//   - error: Last error if all attempts failed, or context error
//
// Behavior:
//   1. Execute operation
//   2. If success, return result
//   3. If error is non-retryable, return error immediately
//   4. If retryable, wait with exponential backoff + jitter
//   5. Repeat until max attempts or success
//   6. Check context cancellation before each attempt
//
// Example:
//
//	result, err := retry.Do(ctx, retry.Default(), func() (*http.Response, error) {
//	    return http.Get("https://api.example.com/data")
//	})
func Do[T any](ctx context.Context, strategy Strategy, operation func() (T, error)) (T, error) {
	var result T
	var lastErr error

	// Apply defaults
	if strategy.MaxAttempts == 0 {
		strategy.MaxAttempts = 3
	}
	if strategy.BaseDelay == 0 {
		strategy.BaseDelay = 100 * time.Millisecond
	}
	if strategy.MaxDelay == 0 {
		strategy.MaxDelay = 30 * time.Second
	}
	if strategy.Multiplier == 0 {
		strategy.Multiplier = 2.0
	}
	if strategy.ErrorClassifier == nil {
		strategy.ErrorClassifier = &HTTPErrorClassifier{}
	}

	operationName := strategy.OperationName
	if operationName == "" {
		operationName = "unknown"
	}

	for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			if strategy.Logger != nil {
				strategy.Logger.WarnContext(ctx, "Operation cancelled",
					slog.String("operation", operationName),
					slog.Int("attempt", attempt+1))
			}
			return result, ctx.Err()
		default:
		}

		// Execute operation
		result, lastErr = operation()

		// Success!
		if lastErr == nil {
			if strategy.Metrics != nil {
				strategy.Metrics.WithLabelValues(operationName, "success").Inc()
			}
			if strategy.Logger != nil && attempt > 0 {
				strategy.Logger.InfoContext(ctx, "Operation succeeded after retry",
					slog.String("operation", operationName),
					slog.Int("attempt", attempt+1))
			}
			return result, nil
		}

		// Check if error is retryable
		if !strategy.ErrorClassifier.IsRetryable(lastErr) {
			if strategy.Metrics != nil {
				strategy.Metrics.WithLabelValues(operationName, "non_retryable").Inc()
			}
			if strategy.Logger != nil {
				strategy.Logger.WarnContext(ctx, "Non-retryable error",
					slog.String("operation", operationName),
					slog.Int("attempt", attempt+1),
					slog.String("error", lastErr.Error()))
			}
			return result, fmt.Errorf("non-retryable error: %w", lastErr)
		}

		// Last attempt - return error
		if attempt == strategy.MaxAttempts-1 {
			if strategy.Metrics != nil {
				strategy.Metrics.WithLabelValues(operationName, "max_retries").Inc()
			}
			if strategy.Logger != nil {
				strategy.Logger.ErrorContext(ctx, "Max retries exceeded",
					slog.String("operation", operationName),
					slog.Int("attempts", attempt+1),
					slog.String("error", lastErr.Error()))
			}
			return result, fmt.Errorf("max retries (%d) exceeded: %w", strategy.MaxAttempts, lastErr)
		}

		// Calculate delay with exponential backoff + jitter
		delay := strategy.calculateDelay(attempt)

		// Record retry attempt
		if strategy.Metrics != nil {
			strategy.Metrics.WithLabelValues(operationName, "retry").Inc()
		}

		// Log retry
		if strategy.Logger != nil {
			strategy.Logger.WarnContext(ctx, "Operation failed, retrying",
				slog.String("operation", operationName),
				slog.Int("attempt", attempt+1),
				slog.Int("max_attempts", strategy.MaxAttempts),
				slog.Duration("delay", delay),
				slog.String("error", lastErr.Error()))
		}

		// Wait with backoff
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return result, fmt.Errorf("cancelled during backoff: %w", ctx.Err())
		}
	}

	// Should not reach here, but handle gracefully
	return result, fmt.Errorf("unexpected retry loop exit: %w", lastErr)
}

// DoSimple is a convenience wrapper for operations without return value.
//
// This is equivalent to Do[struct{}, error] but with cleaner syntax.
//
// Example:
//
//	err := retry.DoSimple(ctx, retry.Default(), func() error {
//	    return publisher.Publish(ctx, alert, target)
//	})
func DoSimple(ctx context.Context, strategy Strategy, operation func() error) error {
	_, err := Do(ctx, strategy, func() (struct{}, error) {
		return struct{}{}, operation()
	})
	return err
}

// calculateDelay calculates exponential backoff delay with jitter.
//
// Formula:
//
//	base_delay = BaseDelay * Multiplier^attempt
//	capped_delay = min(base_delay, MaxDelay)
//	jitter = capped_delay * JitterRatio * random(-1.0 to 1.0)
//	final_delay = capped_delay + jitter
//
// The jitter prevents thundering herd problem when multiple clients retry simultaneously.
//
// Performance: Uses bit shift instead of math.Pow when Multiplier is 2.0 (common case).
func (s Strategy) calculateDelay(attempt int) time.Duration {
	var delay float64

	// Optimize for common case (Multiplier = 2.0)
	if s.Multiplier == 2.0 {
		// Use bit shift instead of math.Pow (10x faster)
		delay = float64(s.BaseDelay) * float64(uint64(1)<<uint(attempt))
	} else {
		// General case
		delay = float64(s.BaseDelay) * math.Pow(s.Multiplier, float64(attempt))
	}

	// Cap at max delay
	if time.Duration(delay) > s.MaxDelay {
		delay = float64(s.MaxDelay)
	}

	// Add jitter (±JitterRatio)
	if s.JitterRatio > 0 {
		// Random value from -1.0 to 1.0
		jitterFactor := (rand.Float64()*2.0 - 1.0) * s.JitterRatio
		delay = delay * (1.0 + jitterFactor)
	}

	return time.Duration(delay)
}

// AllErrorsClassifier treats all errors as retryable (use with caution).
type AllErrorsClassifier struct{}

func (c *AllErrorsClassifier) IsRetryable(err error) bool {
	return err != nil
}

// NoErrorsClassifier treats all errors as non-retryable.
type NoErrorsClassifier struct{}

func (c *NoErrorsClassifier) IsRetryable(err error) bool {
	return false
}

// CustomErrorClassifier allows custom classification logic.
type CustomErrorClassifier struct {
	// Fn is the classification function.
	// Return true if the error should trigger a retry.
	Fn func(error) bool
}

func (c *CustomErrorClassifier) IsRetryable(err error) bool {
	if c.Fn == nil {
		return false
	}
	return c.Fn(err)
}

// IsRetryableError checks if an error matches any of the given sentinel errors.
//
// This is a helper for common patterns like:
//
//	var ErrTransient = errors.New("transient error")
//	if retry.IsRetryableError(err, ErrTransient) { ... }
func IsRetryableError(err error, sentinels ...error) bool {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
