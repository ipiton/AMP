package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// BenchmarkCircuitBreaker_ClosedState_Overhead measures overhead in normal (closed) operation.
// Target: <0.5ms overhead (150% goal, baseline was <1ms)
func BenchmarkCircuitBreaker_ClosedState_Overhead(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.SlowCallDuration = 10 * time.Second // High threshold to avoid slow call detection

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	operation := func(ctx context.Context) error {
		// Simulate fast operation (10µs)
		time.Sleep(10 * time.Microsecond)
		return nil
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cb.Call(ctx, operation)
	}
}

// BenchmarkCircuitBreaker_OpenState_FailFast measures fail-fast performance when open.
// Target: <10µs per blocked request (ultra-fast fail-fast)
func BenchmarkCircuitBreaker_OpenState_FailFast(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.ResetTimeout = 1 * time.Hour // Keep it open
	config.SlowCallDuration = 1 * time.Second

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Open the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	// Verify it's open
	if cb.GetState() != StateOpen {
		b.Fatal("Circuit breaker should be open")
	}

	operation := func(ctx context.Context) error {
		return nil
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cb.Call(ctx, operation)
	}
}

// BenchmarkCircuitBreaker_StateTransition measures state transition overhead.
func BenchmarkCircuitBreaker_StateTransition(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 5
	config.ResetTimeout = 100 * time.Millisecond
	config.SlowCallDuration = 1 * time.Second

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Make some failures to open circuit
		for j := 0; j < 5; j++ {
			_ = cb.Call(ctx, func(ctx context.Context) error {
				return errors.New("failure")
			})
		}

		// Reset to closed
		cb.Reset()
	}
}

// BenchmarkCircuitBreaker_ConcurrentCalls measures performance under concurrent load.
// Target: No significant degradation with 100+ concurrent goroutines
func BenchmarkCircuitBreaker_ConcurrentCalls(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 1000 // High threshold
	config.SlowCallDuration = 1 * time.Second

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	operation := func(ctx context.Context) error {
		time.Sleep(10 * time.Microsecond)
		return nil
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cb.Call(ctx, operation)
		}
	})
}

// BenchmarkCircuitBreaker_GetStats measures statistics retrieval overhead.
func BenchmarkCircuitBreaker_GetStats(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Add some data
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			if i%3 == 0 {
				return errors.New("failure")
			}
			return nil
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cb.GetStats()
	}
}

// BenchmarkCircuitBreaker_WithMetrics measures overhead when metrics are enabled.
func BenchmarkCircuitBreaker_WithMetrics(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.SlowCallDuration = 10 * time.Second

	metrics := NewCircuitBreakerMetrics()
	cb, err := NewCircuitBreaker(config, nil, metrics)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	operation := func(ctx context.Context) error {
		time.Sleep(10 * time.Microsecond)
		return nil
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cb.Call(ctx, operation)
	}
}

// BenchmarkCircuitBreaker_SlidingWindowCleanup measures sliding window cleanup performance.
func BenchmarkCircuitBreaker_SlidingWindowCleanup(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.TimeWindow = 100 * time.Millisecond
	config.SlowCallDuration = 10 * time.Second

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Fill sliding window
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return nil
		})
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	// Measure cleanup performance
	for i := 0; i < b.N; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return nil
		})
	}
}

// BenchmarkCircuitBreaker_CompareNoOpVsCircuitBreaker compares overhead vs no circuit breaker.
func BenchmarkCircuitBreaker_CompareNoOpVsCircuitBreaker(b *testing.B) {
	config := DefaultCircuitBreakerConfig()
	config.SlowCallDuration = 10 * time.Second

	cb, err := NewCircuitBreaker(config, nil, nil)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	operation := func(ctx context.Context) error {
		time.Sleep(10 * time.Microsecond)
		return nil
	}

	b.Run("WithCircuitBreaker", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = cb.Call(ctx, operation)
		}
	})

	b.Run("WithoutCircuitBreaker", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = operation(ctx)
		}
	})
}
