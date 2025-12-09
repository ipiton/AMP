package httperror

import (
	"math"
	"math/rand"
	"time"
)

// RetryStrategy defines how to handle retry logic for failed requests.
type RetryStrategy struct {
	// MaxRetries is the maximum number of retry attempts.
	// 0 means no retries, -1 means infinite retries.
	MaxRetries int

	// InitialDelay is the initial delay before the first retry.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries.
	MaxDelay time.Duration

	// Multiplier is the factor by which the delay increases after each retry.
	// Set to 1.0 for constant delay, 2.0 for exponential backoff.
	Multiplier float64

	// Jitter adds randomness to delays to prevent thundering herd.
	// 0.0 means no jitter, 0.1 means ±10% jitter.
	Jitter float64

	// RetryableStatusCodes defines which HTTP status codes should be retried.
	// If empty, uses default (429, 500, 502, 503, 504).
	RetryableStatusCodes []int
}

// DefaultRetryStrategy returns a sensible default retry strategy.
//
// Default values:
//   - MaxRetries: 3
//   - InitialDelay: 1 second
//   - MaxDelay: 30 seconds
//   - Multiplier: 2.0 (exponential backoff)
//   - Jitter: 0.1 (±10%)
func DefaultRetryStrategy() *RetryStrategy {
	return &RetryStrategy{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		RetryableStatusCodes: []int{
			429, // Too Many Requests
			500, // Internal Server Error
			502, // Bad Gateway
			503, // Service Unavailable
			504, // Gateway Timeout
		},
	}
}

// AggressiveRetryStrategy returns a strategy with more retries for critical operations.
func AggressiveRetryStrategy() *RetryStrategy {
	return &RetryStrategy{
		MaxRetries:   5,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.2,
		RetryableStatusCodes: []int{
			429, 500, 502, 503, 504,
		},
	}
}

// ConservativeRetryStrategy returns a strategy with fewer, slower retries.
func ConservativeRetryStrategy() *RetryStrategy {
	return &RetryStrategy{
		MaxRetries:   2,
		InitialDelay: 2 * time.Second,
		MaxDelay:     10 * time.Second,
		Multiplier:   1.5,
		Jitter:       0.1,
		RetryableStatusCodes: []int{
			429, 503, 504,
		},
	}
}

// NoRetryStrategy returns a strategy that never retries.
func NoRetryStrategy() *RetryStrategy {
	return &RetryStrategy{
		MaxRetries: 0,
	}
}

// ShouldRetry determines if an error should be retried based on the strategy.
//
// Returns:
//   - shouldRetry: whether to retry the request
//   - delay: how long to wait before retrying (0 if not retrying)
func (s *RetryStrategy) ShouldRetry(err error, attempt int) (shouldRetry bool, delay time.Duration) {
	if s == nil || err == nil {
		return false, 0
	}

	// Check max retries (0 means no retries, -1 means infinite)
	if s.MaxRetries >= 0 && attempt >= s.MaxRetries {
		return false, 0
	}

	// Check if error is retryable
	if !s.IsRetryableError(err) {
		return false, 0
	}

	// Calculate delay with exponential backoff
	delay = s.CalculateDelay(attempt, err)

	return true, delay
}

// IsRetryableError checks if an error should be retried according to the strategy.
func (s *RetryStrategy) IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check HTTPAPIError
	var httpErr *HTTPAPIError
	if AsHTTPError(err, &httpErr) {
		// Check custom retryable status codes
		if len(s.RetryableStatusCodes) > 0 {
			for _, code := range s.RetryableStatusCodes {
				if httpErr.StatusCode == code {
					return true
				}
			}
			return false
		}

		// Use default retryable check
		return httpErr.IsRetryable()
	}

	// Check network errors
	return IsRetryableNetworkError(err)
}

// CalculateDelay calculates the delay before the next retry attempt.
//
// Uses exponential backoff with optional jitter:
//
//	delay = min(initialDelay * (multiplier ^ attempt), maxDelay) * (1 ± jitter)
//
// If the error has a RetryAfter value, it is used instead (but capped at maxDelay).
func (s *RetryStrategy) CalculateDelay(attempt int, err error) time.Duration {
	if s == nil {
		return 0
	}

	// Check for Retry-After header in error
	if retryAfter := GetRetryAfter(err); retryAfter > 0 {
		delay := time.Duration(retryAfter) * time.Second
		if s.MaxDelay > 0 && delay > s.MaxDelay {
			delay = s.MaxDelay
		}
		return s.applyJitter(delay)
	}

	// Calculate exponential backoff
	delay := float64(s.InitialDelay)
	if s.Multiplier > 0 {
		delay *= math.Pow(s.Multiplier, float64(attempt))
	}

	// Cap at max delay
	if s.MaxDelay > 0 && time.Duration(delay) > s.MaxDelay {
		delay = float64(s.MaxDelay)
	}

	return s.applyJitter(time.Duration(delay))
}

// applyJitter adds random jitter to a delay.
func (s *RetryStrategy) applyJitter(delay time.Duration) time.Duration {
	if s.Jitter <= 0 {
		return delay
	}

	// Calculate jitter range: delay * jitter
	jitterRange := float64(delay) * s.Jitter

	// Random value in [-jitterRange, +jitterRange]
	jitterValue := (rand.Float64()*2 - 1) * jitterRange

	// Apply jitter
	result := float64(delay) + jitterValue

	// Ensure non-negative
	if result < 0 {
		result = 0
	}

	return time.Duration(result)
}

// RetryContext holds state for retry operations.
type RetryContext struct {
	Strategy    *RetryStrategy
	Attempt     int
	LastError   error
	TotalTime   time.Duration
	StartTime   time.Time
	MaxDuration time.Duration // 0 means no limit
}

// NewRetryContext creates a new retry context with the given strategy.
func NewRetryContext(strategy *RetryStrategy) *RetryContext {
	if strategy == nil {
		strategy = DefaultRetryStrategy()
	}
	return &RetryContext{
		Strategy:  strategy,
		Attempt:   0,
		StartTime: time.Now(),
	}
}

// WithMaxDuration sets the maximum total duration for all retry attempts.
func (c *RetryContext) WithMaxDuration(d time.Duration) *RetryContext {
	c.MaxDuration = d
	return c
}

// Next prepares for the next retry attempt.
// Returns false if no more retries should be attempted.
func (c *RetryContext) Next(err error) bool {
	c.LastError = err

	// Check max duration
	if c.MaxDuration > 0 {
		c.TotalTime = time.Since(c.StartTime)
		if c.TotalTime >= c.MaxDuration {
			return false
		}
	}

	shouldRetry, _ := c.Strategy.ShouldRetry(err, c.Attempt)
	if !shouldRetry {
		return false
	}

	c.Attempt++
	return true
}

// Delay returns the delay to wait before the current retry attempt.
func (c *RetryContext) Delay() time.Duration {
	return c.Strategy.CalculateDelay(c.Attempt-1, c.LastError)
}

// Wait blocks for the calculated delay duration.
func (c *RetryContext) Wait() {
	delay := c.Delay()
	if delay > 0 {
		time.Sleep(delay)
	}
}

// AsHTTPError is a helper that wraps errors.As for HTTPAPIError.
func AsHTTPError(err error, target **HTTPAPIError) bool {
	if err == nil || target == nil {
		return false
	}
	return asHTTPErrorImpl(err, target)
}

// asHTTPErrorImpl does the actual errors.As call
func asHTTPErrorImpl(err error, target **HTTPAPIError) bool {
	type httpAPIError interface {
		error
		IsRetryable() bool
		IsRateLimit() bool
	}

	// First try direct assertion
	if e, ok := err.(*HTTPAPIError); ok {
		*target = e
		return true
	}

	// Then try errors.As
	var httpErr *HTTPAPIError
	if ok := unwrapAs(err, &httpErr); ok {
		*target = httpErr
		return true
	}

	return false
}

// unwrapAs is a helper for errors.As
func unwrapAs(err error, target interface{}) bool {
	if err == nil {
		return false
	}

	// Type switch for direct match
	if e, ok := err.(*HTTPAPIError); ok {
		if t, ok := target.(**HTTPAPIError); ok {
			*t = e
			return true
		}
	}

	// Check unwrapped error
	if unwrapper, ok := err.(interface{ Unwrap() error }); ok {
		return unwrapAs(unwrapper.Unwrap(), target)
	}

	return false
}
