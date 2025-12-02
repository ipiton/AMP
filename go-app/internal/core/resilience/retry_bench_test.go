package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

// BenchmarkWithRetry_NoRetries benchmarks the overhead when operation succeeds immediately
func BenchmarkWithRetry_NoRetries(b *testing.B) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = WithRetry(ctx, policy, func() error {
			return nil // Immediate success
		})
	}
}

// BenchmarkWithRetry_OneRetry benchmarks the overhead with one retry
func BenchmarkWithRetry_OneRetry(b *testing.B) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  1 * time.Microsecond, // Very small delay for benchmarking
		MaxDelay:   10 * time.Microsecond,
		Multiplier: 2.0,
		Jitter:     false, // Disable jitter for consistent benchmarking
	}

	ctx := context.Background()
	attempt := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		attempt = 0
		_ = WithRetry(ctx, policy, func() error {
			attempt++
			if attempt == 1 {
				return errors.New("transient error")
			}
			return nil
		})
	}
}

// BenchmarkWithRetryFunc_NoRetries benchmarks WithRetryFunc with immediate success
func BenchmarkWithRetryFunc_NoRetries(b *testing.B) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = WithRetryFunc(ctx, policy, func() (int, error) {
			return 42, nil
		})
	}
}

// BenchmarkCalculateNextDelay benchmarks the delay calculation
func BenchmarkCalculateNextDelay(b *testing.B) {
	policy := &RetryPolicy{
		MaxRetries: 5,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Jitter:     true,
	}

	currentDelay := 100 * time.Millisecond

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = calculateNextDelay(currentDelay, policy)
	}
}

// BenchmarkCalculateNextDelay_NoJitter benchmarks delay calculation without jitter
func BenchmarkCalculateNextDelay_NoJitter(b *testing.B) {
	policy := &RetryPolicy{
		MaxRetries: 5,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Jitter:     false,
	}

	currentDelay := 100 * time.Millisecond

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = calculateNextDelay(currentDelay, policy)
	}
}

// BenchmarkDefaultErrorChecker benchmarks the default error checker
func BenchmarkDefaultErrorChecker(b *testing.B) {
	checker := &DefaultErrorChecker{}
	err := errors.New("test error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = checker.IsRetryable(err)
	}
}

// BenchmarkHTTPErrorChecker benchmarks the HTTP error checker
func BenchmarkHTTPErrorChecker(b *testing.B) {
	checker := NewHTTPErrorChecker()
	err := errors.New("HTTP 500: Internal Server Error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = checker.IsRetryable(err)
	}
}

// BenchmarkChainedErrorChecker benchmarks chained error checking
func BenchmarkChainedErrorChecker(b *testing.B) {
	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{
			&DefaultErrorChecker{},
			NewHTTPErrorChecker(),
		},
	}
	err := errors.New("test error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = checker.IsRetryable(err)
	}
}

// BenchmarkShouldRetry benchmarks the shouldRetry function
func BenchmarkShouldRetry(b *testing.B) {
	checker := &DefaultErrorChecker{}
	err := errors.New("test error")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = shouldRetry(err, checker)
	}
}

// BenchmarkWaitWithContext_Immediate benchmarks immediate context cancellation
func BenchmarkWaitWithContext_Immediate(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	delay := 1 * time.Second

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = waitWithContext(ctx, delay)
	}
}
