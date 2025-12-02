package publishing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// TestMiddlewareChain_Order tests middleware execution order
func TestMiddlewareChain_Order(t *testing.T) {
	var executionOrder []string

	// Create middleware that records execution
	middleware1 := func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			executionOrder = append(executionOrder, "middleware1-before")
			result, err := next(alert)
			executionOrder = append(executionOrder, "middleware1-after")
			return result, err
		}
	}

	middleware2 := func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			executionOrder = append(executionOrder, "middleware2-before")
			result, err := next(alert)
			executionOrder = append(executionOrder, "middleware2-after")
			return result, err
		}
	}

	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		executionOrder = append(executionOrder, "base")
		return map[string]any{"formatted": true}, nil
	}

	// Create chain: middleware1 → middleware2 → base
	chain := NewMiddlewareChain(baseFormatter, middleware1, middleware2)

	// Execute
	_, err := chain.Format(createTestEnrichedAlert())
	require.NoError(t, err)

	// Verify execution order
	expected := []string{
		"middleware1-before",
		"middleware2-before",
		"base",
		"middleware2-after",
		"middleware1-after",
	}
	assert.Equal(t, expected, executionOrder, "Middleware should execute in correct order")
}

// TestValidationMiddleware_Success tests successful validation
func TestValidationMiddleware_Success(t *testing.T) {
	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"formatted": true}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, ValidationMiddleware())

	// Valid alert
	alert := createTestEnrichedAlert()
	result, err := chain.Format(alert)

	assert.NoError(t, err, "Should pass validation")
	assert.Equal(t, true, result["formatted"])
}

// TestValidationMiddleware_Failures tests validation errors
func TestValidationMiddleware_Failures(t *testing.T) {
	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"formatted": true}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, ValidationMiddleware())

	testCases := []struct {
		name          string
		alert         *core.EnrichedAlert
		expectedError string
	}{
		{
			name:          "nil alert",
			alert:         nil,
			expectedError: "enriched alert is nil",
		},
		{
			name: "nil inner alert",
			alert: &core.EnrichedAlert{
				Alert: nil,
			},
			expectedError: "alert is nil",
		},
		{
			name: "empty alert name",
			alert: &core.EnrichedAlert{
				Alert: &core.Alert{
					AlertName:   "",
					Status:      core.StatusFiring,
					Labels:      map[string]string{},
					Annotations: map[string]string{},
				},
			},
			expectedError: "alert name is empty",
		},
		{
			name: "invalid status",
			alert: &core.EnrichedAlert{
				Alert: &core.Alert{
					AlertName:   "TestAlert",
					Status:      core.AlertStatus("invalid"),
					Labels:      map[string]string{},
					Annotations: map[string]string{},
				},
			},
			expectedError: "invalid status",
		},
		{
			name: "nil labels",
			alert: &core.EnrichedAlert{
				Alert: &core.Alert{
					AlertName:   "TestAlert",
					Status:      core.StatusFiring,
					Labels:      nil,
					Annotations: map[string]string{},
				},
			},
			expectedError: "labels map is nil",
		},
		{
			name: "nil annotations",
			alert: &core.EnrichedAlert{
				Alert: &core.Alert{
					AlertName:   "TestAlert",
					Status:      core.StatusFiring,
					Labels:      map[string]string{},
					Annotations: nil,
				},
			},
			expectedError: "annotations map is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := chain.Format(tc.alert)

			assert.Error(t, err, "Should return validation error")
			assert.Nil(t, result, "Result should be nil on error")
			assert.Contains(t, err.Error(), tc.expectedError, "Error message should match")

			// Verify error type
			_, ok := err.(*ValidationError)
			assert.True(t, ok, "Should return ValidationError type")
		})
	}
}

// TestMetricsMiddleware_Recording tests metrics recording
// TODO: Update for new MetricsMiddleware API (currently disabled)
// func TestMetricsMiddleware_Recording(t *testing.T) { ... }

// TestRateLimitMiddleware tests rate limiting
func TestRateLimitMiddleware(t *testing.T) {
	// Mock rate limiter (allows first 3 requests, then blocks)
	limiter := &mockRateLimiter{allowCount: 3}

	rateLimitMiddleware := NewRateLimitMiddleware(limiter)

	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"formatted": true}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, rateLimitMiddleware)

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		result, err := chain.Format(createTestEnrichedAlert())
		assert.NoError(t, err, "Request %d should succeed", i+1)
		assert.NotNil(t, result)
	}

	// 4th request should be rate limited
	result, err := chain.Format(createTestEnrichedAlert())
	assert.Error(t, err, "4th request should be rate limited")
	assert.Nil(t, result)
	assert.IsType(t, &RateLimitError{}, err, "Should return RateLimitError")
	assert.Contains(t, err.Error(), "rate limit", "Error message should mention rate limit")
}

// TestTimeoutMiddleware tests timeout behavior
func TestTimeoutMiddleware(t *testing.T) {
	t.Run("completes within timeout", func(t *testing.T) {
		timeoutMiddleware := TimeoutMiddleware(100 * time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			time.Sleep(10 * time.Millisecond) // Fast operation
			return map[string]any{"formatted": true}, nil
		}

		chain := NewMiddlewareChain(baseFormatter, timeoutMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.NoError(t, err, "Should complete within timeout")
		assert.NotNil(t, result)
	})

	t.Run("exceeds timeout", func(t *testing.T) {
		timeoutMiddleware := TimeoutMiddleware(50 * time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			time.Sleep(200 * time.Millisecond) // Slow operation
			return map[string]any{"formatted": true}, nil
		}

		chain := NewMiddlewareChain(baseFormatter, timeoutMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.Error(t, err, "Should timeout")
		assert.Nil(t, result)
		assert.IsType(t, &TimeoutError{}, err, "Should return TimeoutError")
		assert.Contains(t, err.Error(), "timeout", "Error message should mention timeout")
	})
}

// TestRetryMiddleware tests retry behavior
func TestRetryMiddleware(t *testing.T) {
	t.Run("succeeds on first attempt", func(t *testing.T) {
		retryMiddleware := RetryMiddleware(3, 10*time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			return map[string]any{"formatted": true}, nil
		}

		chain := NewMiddlewareChain(baseFormatter, retryMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("succeeds on retry", func(t *testing.T) {
		attemptCount := 0
		retryMiddleware := RetryMiddleware(3, 10*time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			attemptCount++
			if attemptCount < 3 {
				return nil, errors.New("transient error")
			}
			return map[string]any{"formatted": true}, nil
		}

		chain := NewMiddlewareChain(baseFormatter, retryMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.NoError(t, err, "Should succeed after retries")
		assert.NotNil(t, result)
		assert.Equal(t, 3, attemptCount, "Should attempt 3 times")
	})

	t.Run("fails after max retries", func(t *testing.T) {
		attemptCount := 0
		retryMiddleware := RetryMiddleware(2, 10*time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			attemptCount++
			return nil, errors.New("persistent error")
		}

		chain := NewMiddlewareChain(baseFormatter, retryMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.Error(t, err, "Should fail after max retries")
		assert.Nil(t, result)
		assert.Equal(t, 3, attemptCount, "Should attempt maxRetries+1 times (initial + 2 retries)")
		assert.Contains(t, err.Error(), "failed after 2 retries")
	})

	t.Run("does not retry validation errors", func(t *testing.T) {
		attemptCount := 0
		retryMiddleware := RetryMiddleware(3, 10*time.Millisecond)

		baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
			attemptCount++
			return nil, &ValidationError{Field: "test", Message: "validation failed"}
		}

		chain := NewMiddlewareChain(baseFormatter, retryMiddleware)
		result, err := chain.Format(createTestEnrichedAlert())

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, 1, attemptCount, "Should not retry validation errors")
		assert.IsType(t, &ValidationError{}, err)
	})
}

// Mock rate limiter for testing
type mockRateLimiter struct {
	allowCount int
	callCount  int
}

func (m *mockRateLimiter) Allow() bool {
	m.callCount++
	return m.callCount <= m.allowCount
}

func (m *mockRateLimiter) Wait(ctx context.Context) error {
	return nil
}
