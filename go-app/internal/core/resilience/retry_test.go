package resilience

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestWithRetry_Success(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
	}

	called := 0
	err := WithRetry(context.Background(), policy, func() error {
		called++
		return nil // Success on first try
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	if called != 1 {
		t.Errorf("Expected 1 call, got %d", called)
	}
}

func TestWithRetry_SuccessAfterRetries(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
		Logger:     slog.Default(),
	}

	called := 0
	failUntil := 2

	err := WithRetry(context.Background(), policy, func() error {
		called++
		if called < failUntil {
			return errors.New("transient error")
		}
		return nil // Success on attempt 2
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	if called != failUntil {
		t.Errorf("Expected %d calls, got %d", failUntil, called)
	}
}

func TestWithRetry_AllRetriesFailed(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
	}

	called := 0
	expectedError := errors.New("permanent error")

	err := WithRetry(context.Background(), policy, func() error {
		called++
		return expectedError
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Should be called: initial + 3 retries = 4 times
	expectedCalls := policy.MaxRetries + 1
	if called != expectedCalls {
		t.Errorf("Expected %d calls, got %d", expectedCalls, called)
	}

	if !errors.Is(err, expectedError) {
		t.Errorf("Expected error to wrap %v, got %v", expectedError, err)
	}
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 10,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   1 * time.Second,
		Multiplier: 2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	called := 0
	done := make(chan error, 1)

	go func() {
		err := WithRetry(ctx, policy, func() error {
			called++
			if called == 2 {
				// Cancel context during retry
				cancel()
			}
			return errors.New("error")
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
		if called < 2 {
			t.Errorf("Expected at least 2 calls before cancellation, got %d", called)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out")
	}
}

func TestWithRetry_NonRetryableError(t *testing.T) {
	checker := &neverRetryChecker{}

	policy := &RetryPolicy{
		MaxRetries:   3,
		BaseDelay:    10 * time.Millisecond,
		ErrorChecker: checker,
	}

	called := 0
	expectedError := errors.New("non-retryable error")

	err := WithRetry(context.Background(), policy, func() error {
		called++
		return expectedError
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	// Should be called only once (no retries for non-retryable errors)
	if called != 1 {
		t.Errorf("Expected 1 call (no retries), got %d", called)
	}
}

// Helper: neverRetryChecker always returns false
type neverRetryChecker struct{}

func (c *neverRetryChecker) IsRetryable(err error) bool {
	return false
}

func TestWithRetryFunc_Success(t *testing.T) {
	policy := DefaultRetryPolicy()
	policy.BaseDelay = 10 * time.Millisecond

	expectedResult := "success"
	result, err := WithRetryFunc(context.Background(), policy, func() (string, error) {
		return expectedResult, nil
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	if result != expectedResult {
		t.Errorf("Expected result %q, got %q", expectedResult, result)
	}
}

func TestWithRetryFunc_SuccessAfterRetries(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   100 * time.Millisecond,
		Multiplier: 2.0,
	}

	called := 0
	failUntil := 3
	expectedResult := 42

	result, err := WithRetryFunc(context.Background(), policy, func() (int, error) {
		called++
		if called < failUntil {
			return 0, errors.New("transient error")
		}
		return expectedResult, nil
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	if result != expectedResult {
		t.Errorf("Expected result %d, got %d", expectedResult, result)
	}

	if called != failUntil {
		t.Errorf("Expected %d calls, got %d", failUntil, called)
	}
}

func TestCalculateNextDelay_ExponentialBackoff(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 5,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Jitter:     false, // Disable jitter for predictable testing
	}

	tests := []struct {
		currentDelay time.Duration
		expected     time.Duration
	}{
		{100 * time.Millisecond, 200 * time.Millisecond}, // 100 * 2.0
		{200 * time.Millisecond, 400 * time.Millisecond}, // 200 * 2.0
		{400 * time.Millisecond, 800 * time.Millisecond}, // 400 * 2.0
		{3 * time.Second, 5 * time.Second},               // Capped at MaxDelay
	}

	for _, tt := range tests {
		actual := calculateNextDelay(tt.currentDelay, policy)
		if actual != tt.expected {
			t.Errorf("calculateNextDelay(%v) = %v, expected %v", tt.currentDelay, actual, tt.expected)
		}
	}
}

func TestCalculateNextDelay_WithJitter(t *testing.T) {
	policy := &RetryPolicy{
		MaxRetries: 5,
		BaseDelay:  100 * time.Millisecond,
		MaxDelay:   5 * time.Second,
		Multiplier: 2.0,
		Jitter:     true,
	}

	currentDelay := 100 * time.Millisecond
	expectedBase := 200 * time.Millisecond
	maxJitter := time.Duration(float64(expectedBase) * 0.1) // 10% jitter

	// Run multiple times to ensure jitter is applied
	for i := 0; i < 10; i++ {
		actual := calculateNextDelay(currentDelay, policy)

		// Should be between base and base + 10%
		if actual < expectedBase || actual > expectedBase+maxJitter {
			t.Errorf("Iteration %d: delay %v outside expected range [%v, %v]",
				i, actual, expectedBase, expectedBase+maxJitter)
		}
	}
}

func TestDefaultRetryPolicy(t *testing.T) {
	policy := DefaultRetryPolicy()

	if policy.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries=3, got %d", policy.MaxRetries)
	}
	if policy.BaseDelay != 100*time.Millisecond {
		t.Errorf("Expected BaseDelay=100ms, got %v", policy.BaseDelay)
	}
	if policy.MaxDelay != 5*time.Second {
		t.Errorf("Expected MaxDelay=5s, got %v", policy.MaxDelay)
	}
	if policy.Multiplier != 2.0 {
		t.Errorf("Expected Multiplier=2.0, got %f", policy.Multiplier)
	}
	if !policy.Jitter {
		t.Error("Expected Jitter=true")
	}
}

func TestWithRetry_NilPolicy(t *testing.T) {
	called := 0
	err := WithRetry(context.Background(), nil, func() error {
		called++
		return nil
	})

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should use default policy
	if called != 1 {
		t.Errorf("Expected 1 call, got %d", called)
	}
}

func TestWaitWithContext_Success(t *testing.T) {
	ctx := context.Background()
	delay := 50 * time.Millisecond

	start := time.Now()
	completed := waitWithContext(ctx, delay)
	elapsed := time.Since(start)

	if !completed {
		t.Error("Expected wait to complete successfully")
	}

	if elapsed < delay {
		t.Errorf("Expected wait to take at least %v, took %v", delay, elapsed)
	}
}

func TestWaitWithContext_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	delay := 1 * time.Second

	// Cancel context immediately
	cancel()

	start := time.Now()
	completed := waitWithContext(ctx, delay)
	elapsed := time.Since(start)

	if completed {
		t.Error("Expected wait to be cancelled")
	}

	if elapsed > 100*time.Millisecond {
		t.Errorf("Expected fast cancellation, took %v", elapsed)
	}
}

func TestShouldRetry_WithChecker(t *testing.T) {
	checker := &alwaysRetryChecker{}
	policy := &RetryPolicy{
		ErrorChecker: checker,
	}

	if !shouldRetry(errors.New("any error"), policy.ErrorChecker) {
		t.Error("Expected error to be retryable")
	}
}

func TestShouldRetry_NilError(t *testing.T) {
	if shouldRetry(nil, nil) {
		t.Error("Expected nil error to not be retryable")
	}
}

// Helper test error checker
type alwaysRetryChecker struct{}

func (c *alwaysRetryChecker) IsRetryable(err error) bool {
	return err != nil
}
