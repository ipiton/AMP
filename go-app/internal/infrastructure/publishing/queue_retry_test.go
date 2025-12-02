package publishing

import (
	"testing"
	"time"
)

// TestCalculateBackoff_FirstAttempt tests backoff calculation for first attempt
func TestCalculateBackoff_FirstAttempt(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: false, // Disable jitter for deterministic test
		JitterMax:     0,
	}

	backoff := CalculateBackoff(0, config)

	// 2^0 * 100ms = 100ms
	expected := 100 * time.Millisecond
	if backoff != expected {
		t.Errorf("Expected %v for first attempt, got %v", expected, backoff)
	}
}

// TestCalculateBackoff_SecondAttempt tests backoff calculation for second attempt
func TestCalculateBackoff_SecondAttempt(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: false,
		JitterMax:     0,
	}

	backoff := CalculateBackoff(1, config)

	// 2^1 * 100ms = 200ms
	expected := 200 * time.Millisecond
	if backoff != expected {
		t.Errorf("Expected %v for second attempt, got %v", expected, backoff)
	}
}

// TestCalculateBackoff_ExponentialGrowth tests exponential backoff growth
func TestCalculateBackoff_ExponentialGrowth(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: false,
		JitterMax:     0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},   // 2^0 * 100ms = 100ms
		{1, 200 * time.Millisecond},   // 2^1 * 100ms = 200ms
		{2, 400 * time.Millisecond},   // 2^2 * 100ms = 400ms
		{3, 800 * time.Millisecond},   // 2^3 * 100ms = 800ms
		{4, 1600 * time.Millisecond},  // 2^4 * 100ms = 1600ms
		{5, 3200 * time.Millisecond},  // 2^5 * 100ms = 3200ms
		{6, 6400 * time.Millisecond},  // 2^6 * 100ms = 6400ms
		{7, 12800 * time.Millisecond}, // 2^7 * 100ms = 12800ms
	}

	for _, tt := range tests {
		t.Run(time.Duration(tt.attempt).String(), func(t *testing.T) {
			backoff := CalculateBackoff(tt.attempt, config)

			if backoff != tt.expected {
				t.Errorf("Attempt %d: expected %v, got %v", tt.attempt, tt.expected, backoff)
			}
		})
	}
}

// TestCalculateBackoff_MaxBackoffLimit tests max backoff enforcement
func TestCalculateBackoff_MaxBackoffLimit(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    5 * time.Second, // Low limit for testing
		JitterEnabled: false,
		JitterMax:     0,
	}

	// Attempt 10: 2^10 * 100ms = 102400ms (>5s)
	backoff := CalculateBackoff(10, config)

	if backoff != config.MaxBackoff {
		t.Errorf("Expected max backoff %v, got %v", config.MaxBackoff, backoff)
	}
}

// TestCalculateBackoff_WithJitter tests jitter addition
func TestCalculateBackoff_WithJitter(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: true,
		JitterMax:     1 * time.Second,
	}

	backoff := CalculateBackoff(0, config)

	// 2^0 * 100ms = 100ms, jitter: 0-1000ms
	minExpected := 100 * time.Millisecond
	maxExpected := 100*time.Millisecond + 1*time.Second

	if backoff < minExpected || backoff > maxExpected {
		t.Errorf("Backoff with jitter %v out of range [%v, %v]", backoff, minExpected, maxExpected)
	}
}

// TestCalculateBackoff_NoJitter tests jitter disabled
func TestCalculateBackoff_NoJitter(t *testing.T) {
	config := QueueRetryConfig{
		BaseInterval:  100 * time.Millisecond,
		MaxBackoff:    30 * time.Second,
		JitterEnabled: false,
		JitterMax:     0,
	}

	// Run 10 times to verify deterministic behavior
	backoffs := make(map[time.Duration]int)
	for i := 0; i < 10; i++ {
		backoff := CalculateBackoff(0, config)
		backoffs[backoff]++
	}

	// Should have exactly 1 unique value (deterministic)
	if len(backoffs) != 1 {
		t.Errorf("Expected deterministic backoff (1 unique value), got %d unique values", len(backoffs))
	}

	// Value should be 100ms
	if _, ok := backoffs[100*time.Millisecond]; !ok {
		t.Errorf("Expected 100ms backoff, got: %v", backoffs)
	}
}

// TestShouldRetry_PermanentError tests no retry for permanent errors
func TestShouldRetry_PermanentError(t *testing.T) {
	shouldRetry := ShouldRetry(QueueErrorTypePermanent, 0, 3)

	if shouldRetry {
		t.Errorf("Expected no retry for permanent error, got shouldRetry=true")
	}
}

// TestShouldRetry_TransientError tests retry for transient errors
func TestShouldRetry_TransientError(t *testing.T) {
	shouldRetry := ShouldRetry(QueueErrorTypeTransient, 0, 3)

	if !shouldRetry {
		t.Errorf("Expected retry for transient error, got shouldRetry=false")
	}
}

// TestShouldRetry_MaxRetriesReached tests no retry when max reached
func TestShouldRetry_MaxRetriesReached(t *testing.T) {
	tests := []struct {
		currentAttempt int
		maxRetries     int
		shouldRetry    bool
	}{
		{0, 3, true},  // Attempt 0, max 3 → retry
		{1, 3, true},  // Attempt 1, max 3 → retry
		{2, 3, true},  // Attempt 2, max 3 → retry
		{3, 3, false}, // Attempt 3, max 3 → no retry (max reached)
		{4, 3, false}, // Attempt 4, max 3 → no retry (exceeded)
	}

	for _, tt := range tests {
		t.Run(time.Duration(tt.currentAttempt).String(), func(t *testing.T) {
			shouldRetry := ShouldRetry(QueueErrorTypeTransient, tt.currentAttempt, tt.maxRetries)

			if shouldRetry != tt.shouldRetry {
				t.Errorf("Attempt %d (max %d): expected shouldRetry=%v, got %v",
					tt.currentAttempt, tt.maxRetries, tt.shouldRetry, shouldRetry)
			}
		})
	}
}

// TestShouldRetry_UnknownError tests retry for unknown errors
func TestShouldRetry_UnknownError(t *testing.T) {
	shouldRetry := ShouldRetry(QueueErrorTypeUnknown, 0, 3)

	// Unknown errors should be retried (safe default)
	if !shouldRetry {
		t.Errorf("Expected retry for unknown error, got shouldRetry=false")
	}
}

// TestDefaultQueueRetryConfig tests default configuration values
func TestDefaultQueueRetryConfig(t *testing.T) {
	config := DefaultQueueRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", config.MaxRetries)
	}
	if config.BaseInterval != 100*time.Millisecond {
		t.Errorf("Expected BaseInterval=100ms, got %v", config.BaseInterval)
	}
	if config.MaxBackoff != 30*time.Second {
		t.Errorf("Expected MaxBackoff=30s, got %v", config.MaxBackoff)
	}
	if !config.JitterEnabled {
		t.Errorf("Expected JitterEnabled=true, got false")
	}
	if config.JitterMax != 1*time.Second {
		t.Errorf("Expected JitterMax=1s, got %v", config.JitterMax)
	}
}

// BenchmarkCalculateBackoff benchmarks backoff calculation
func BenchmarkCalculateBackoff(b *testing.B) {
	config := DefaultQueueRetryConfig()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateBackoff(i%10, config)
	}
}

// BenchmarkCalculateBackoff_NoJitter benchmarks backoff without jitter
func BenchmarkCalculateBackoff_NoJitter(b *testing.B) {
	config := DefaultQueueRetryConfig()
	config.JitterEnabled = false
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateBackoff(i%10, config)
	}
}

// BenchmarkShouldRetry benchmarks retry decision
func BenchmarkShouldRetry(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ShouldRetry(QueueErrorTypeTransient, i%5, 3)
	}
}
