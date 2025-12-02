package publishing

import (
	"math"
	"math/rand"
	"time"
)

// QueueRetryConfig defines retry behavior configuration for publishing queue
type QueueRetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts (default: 3)
	BaseInterval  time.Duration // Base retry interval (default: 100ms)
	MaxBackoff    time.Duration // Maximum backoff duration (default: 30s)
	JitterEnabled bool          // Enable jitter (default: true)
	JitterMax     time.Duration // Maximum jitter (default: 1s)
}

// DefaultQueueRetryConfig returns default retry configuration
func DefaultQueueRetryConfig() QueueRetryConfig {
	return QueueRetryConfig{
		MaxRetries:    3,
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: true,
		JitterMax:     1 * time.Second,
	}
}

// CalculateBackoff calculates exponential backoff with optional jitter.
//
// Formula: min(baseInterval * 2^attempt, maxBackoff) + jitter
//
// Parameters:
//   - attempt: Current retry attempt number (0-based)
//   - config: Retry configuration
//
// Returns:
//   - time.Duration: Backoff duration
//
// Example:
//
//	backoff := CalculateBackoff(0, DefaultQueueRetryConfig()) // 100ms + jitter
//	backoff = CalculateBackoff(3, DefaultQueueRetryConfig())  // 800ms + jitter
func CalculateBackoff(attempt int, config QueueRetryConfig) time.Duration {
	// Calculate exponential backoff: baseInterval * 2^attempt
	backoff := time.Duration(math.Pow(2, float64(attempt))) * config.BaseInterval

	// Apply max backoff limit
	if backoff > config.MaxBackoff {
		backoff = config.MaxBackoff
	}

	// Add jitter if enabled
	if config.JitterEnabled {
		jitter := time.Duration(rand.Intn(int(config.JitterMax.Milliseconds()))) * time.Millisecond
		backoff += jitter
	}

	return backoff
}

// ShouldRetry determines if a job should be retried based on error type and attempt count.
//
// Retry Rules:
//   - Permanent errors: NO retry (immediate DLQ)
//   - Max retries reached: NO retry (send to DLQ)
//   - Transient/Unknown errors: YES retry (up to maxRetries)
//
// Parameters:
//   - errorType: The classified error type
//   - currentAttempt: Current attempt number (0-based)
//   - maxRetries: Maximum retry attempts allowed
//
// Returns:
//   - bool: true if should retry, false otherwise
//
// Example:
//
//	shouldRetry := ShouldRetry(QueueErrorTypeTransient, 0, 3) // true
//	shouldRetry = ShouldRetry(QueueErrorTypePermanent, 0, 3) // false
//	shouldRetry = ShouldRetry(QueueErrorTypeTransient, 3, 3) // false (max reached)
func ShouldRetry(errorType QueueErrorType, currentAttempt int, maxRetries int) bool {
	// Never retry permanent errors
	if errorType == QueueErrorTypePermanent {
		return false
	}

	// Check if max retries reached
	if currentAttempt >= maxRetries {
		return false
	}

	// Retry for transient and unknown errors
	return true
}

// RetryResult represents the outcome of a retry operation
type RetryResult struct {
	Success       bool          // Whether the operation succeeded
	TotalAttempts int           // Total number of attempts made
	TotalDuration time.Duration // Total duration including backoffs
	LastError     error         // Last error encountered (nil if success)
	ErrorType     QueueErrorType // Classified error type
}
