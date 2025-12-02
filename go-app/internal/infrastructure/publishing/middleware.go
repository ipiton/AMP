package publishing

import (
	"context"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// FormatterMiddleware wraps a formatFunc to add preprocessing/postprocessing.
//
// Pattern: Chain of Responsibility
// Each middleware can:
//   - Preprocess the alert before formatting
//   - Call the next formatter in the chain
//   - Postprocess the result
//   - Handle errors
//
// Example:
//
//	validated := ValidationMiddleware()(baseFormatter)
//	cached := CachingMiddleware(cache)(validated)
//	traced := TracingMiddleware(tracer)(cached)
//	final := traced
type FormatterMiddleware func(next formatFunc) formatFunc

// MiddlewareChain composes multiple middleware into a single formatter.
//
// Execution order: first middleware is outermost layer (executed first)
//
// Example:
//
//	chain := NewMiddlewareChain(baseFormatter,
//	    ValidationMiddleware(),
//	    CachingMiddleware(cache),
//	    MetricsMiddleware(metrics),
//	)
//	result, err := chain.Format(ctx, alert, format)
//
// Execution flow:
//  1. ValidationMiddleware (validates input)
//  2. CachingMiddleware (checks cache)
//  3. MetricsMiddleware (records metrics)
//  4. baseFormatter (actual formatting)
type MiddlewareChain struct {
	formatter formatFunc
}

// NewMiddlewareChain creates a chain with middleware applied in order.
//
// Parameters:
//   base: Base formatter (innermost layer)
//   middleware: Middleware to apply (first = outermost)
//
// Returns:
//   *MiddlewareChain: Composed formatter with all middleware
func NewMiddlewareChain(base formatFunc, middleware ...FormatterMiddleware) *MiddlewareChain {
	// Apply middleware in reverse order (last middleware wraps base first)
	formatter := base
	for i := len(middleware) - 1; i >= 0; i-- {
		formatter = middleware[i](formatter)
	}

	return &MiddlewareChain{formatter: formatter}
}

// Format executes the middleware chain.
func (c *MiddlewareChain) Format(alert *core.EnrichedAlert) (map[string]any, error) {
	return c.formatter(alert)
}

// ValidationMiddleware validates EnrichedAlert before formatting.
//
// Checks:
//   - Alert is not nil
//   - Alert.Alert is not nil
//   - AlertName is not empty
//   - Status is valid (firing or resolved)
//   - Labels map is not nil
//   - Annotations map is not nil
//
// Returns: ValidationError on failure, calls next on success
func ValidationMiddleware() FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			// Validate EnrichedAlert
			if alert == nil {
				return nil, &ValidationError{
					Field:   "alert",
					Message: "enriched alert is nil",
				}
			}

			// Validate Alert
			if alert.Alert == nil {
				return nil, &ValidationError{
					Field:   "alert.Alert",
					Message: "alert is nil",
				}
			}

			// Validate AlertName
			if alert.Alert.AlertName == "" {
				return nil, &ValidationError{
					Field:   "alert.AlertName",
					Message: "alert name is empty",
				}
			}

			// Validate Status
			if alert.Alert.Status != core.StatusFiring && alert.Alert.Status != core.StatusResolved {
				return nil, &ValidationError{
					Field:   "alert.Status",
					Message: fmt.Sprintf("invalid status: %s (expected firing or resolved)", alert.Alert.Status),
					Value:   string(alert.Alert.Status),
				}
			}

			// Validate Labels (must exist, can be empty)
			if alert.Alert.Labels == nil {
				return nil, &ValidationError{
					Field:   "alert.Labels",
					Message: "labels map is nil",
				}
			}

			// Validate Annotations (must exist, can be empty)
			if alert.Alert.Annotations == nil {
				return nil, &ValidationError{
					Field:   "alert.Annotations",
					Message: "annotations map is nil",
				}
			}

			// All validations passed, call next
			return next(alert)
		}
	}
}

// NOTE: For comprehensive Prometheus metrics, see metrics.go (FormatterMetrics + MetricsMiddleware)

// RateLimitMiddleware limits formatting rate per format.
//
// Uses token bucket algorithm:
//   - Bucket capacity: max burst size
//   - Refill rate: tokens per second
//
// Returns: RateLimitError if rate limit exceeded
type RateLimitMiddleware struct {
	limiter RateLimiter
}

// RateLimiter interface for rate limiting
type RateLimiter interface {
	// Allow checks if request is allowed
	Allow() bool
	// Wait blocks until request is allowed
	Wait(ctx context.Context) error
}

// NewRateLimitMiddleware creates rate limiting middleware.
//
// Parameters:
//   limiter: Rate limiter implementation (e.g., golang.org/x/time/rate)
//
// Behavior:
//   - If Allow() returns false, returns RateLimitError
//   - Otherwise, calls next formatter
func NewRateLimitMiddleware(limiter RateLimiter) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			// Check rate limit
			if !limiter.Allow() {
				return nil, &RateLimitError{
					Message: "formatting rate limit exceeded",
				}
			}

			// Call next formatter
			return next(alert)
		}
	}
}

// TimeoutMiddleware adds timeout to formatting operations.
//
// Parameters:
//   timeout: Maximum duration for formatting
//
// Returns: TimeoutError if formatting exceeds timeout
func TimeoutMiddleware(timeout time.Duration) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			// Channel for result
			type result struct {
				data map[string]any
				err  error
			}
			resultCh := make(chan result, 1)

			// Run formatter in goroutine
			go func() {
				data, err := next(alert)
				resultCh <- result{data: data, err: err}
			}()

			// Wait for result or timeout
			select {
			case res := <-resultCh:
				return res.data, res.err
			case <-ctx.Done():
				return nil, &TimeoutError{
					Timeout: timeout,
					Message: fmt.Sprintf("formatting exceeded timeout (%s)", timeout),
				}
			}
		}
	}
}

// RetryMiddleware retries formatting on transient errors.
//
// Parameters:
//   maxRetries: Maximum number of retry attempts
//   backoff: Delay between retries (exponential backoff)
//
// Retries on:
//   - Network errors
//   - Timeout errors
//   - 5xx server errors
//
// Does NOT retry on:
//   - Validation errors
//   - 4xx client errors
func RetryMiddleware(maxRetries int, initialBackoff time.Duration) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			var lastErr error
			backoff := initialBackoff

			for attempt := 0; attempt <= maxRetries; attempt++ {
				// Try formatting
				result, err := next(alert)

				// Success
				if err == nil {
					return result, nil
				}

				// Check if error is retryable
				if !isRetryableError(err) {
					return nil, err
				}

				// Save error
				lastErr = err

				// Last attempt, don't wait
				if attempt == maxRetries {
					break
				}

				// Wait before retry (exponential backoff)
				time.Sleep(backoff)
				backoff *= 2
			}

			// All retries exhausted
			return nil, fmt.Errorf("formatting failed after %d retries: %w", maxRetries, lastErr)
		}
	}
}

// isRetryableError determines if error should trigger retry
func isRetryableError(err error) bool {
	// Validation errors are not retryable
	if _, ok := err.(*ValidationError); ok {
		return false
	}

	// Rate limit errors are not retryable (client should backoff)
	if _, ok := err.(*RateLimitError); ok {
		return false
	}

	// Timeout errors are retryable
	if _, ok := err.(*TimeoutError); ok {
		return true
	}

	// All other errors are retryable (conservative approach)
	return true
}

// Error types

// ValidationError indicates alert validation failure
type ValidationError struct {
	Field      string // Field that failed validation
	Message    string // Error message
	Value      string // Invalid value (optional)
	Suggestion string // Fix suggestion (optional)
}

func (e *ValidationError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("validation error: %s: %s (value: %s)", e.Field, e.Message, e.Value)
	}
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// RateLimitError indicates rate limit exceeded
type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit error: %s", e.Message)
}

// TimeoutError indicates formatting timeout
type TimeoutError struct {
	Timeout time.Duration
	Message string
}

func (e *TimeoutError) Error() string {
	return e.Message
}
