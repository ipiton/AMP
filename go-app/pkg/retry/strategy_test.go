package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestDo_Success tests successful operation on first attempt
func TestDo_Success(t *testing.T) {
	strategy := NoRetry()
	callCount := 0

	result, err := Do(context.Background(), strategy, func() (string, error) {
		callCount++
		return "success", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, callCount, "should only call once on success")
}

// TestDo_RetryableError tests retry on transient error
func TestDo_RetryableError(t *testing.T) {
	strategy := Default().WithMaxAttempts(3)
	callCount := 0
	retryableErr := errors.New("temporary network error")

	// Mock classifier that treats our error as retryable
	strategy.ErrorClassifier = &CustomErrorClassifier{
		Fn: func(err error) bool {
			return errors.Is(err, retryableErr)
		},
	}

	result, err := Do(context.Background(), strategy, func() (string, error) {
		callCount++
		if callCount < 3 {
			return "", retryableErr
		}
		return "success", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, callCount, "should retry until success")
}

// TestDo_NonRetryableError tests immediate return on permanent error
func TestDo_NonRetryableError(t *testing.T) {
	strategy := Default()
	callCount := 0
	permanentErr := errors.New("validation error")

	strategy.ErrorClassifier = &NoErrorsClassifier{}

	result, err := Do(context.Background(), strategy, func() (string, error) {
		callCount++
		return "", permanentErr
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-retryable")
	assert.Equal(t, "", result)
	assert.Equal(t, 1, callCount, "should not retry permanent errors")
}

// TestDo_MaxRetriesExceeded tests failure after max retries
func TestDo_MaxRetriesExceeded(t *testing.T) {
	strategy := Default().WithMaxAttempts(3)
	strategy.BaseDelay = 1 * time.Millisecond // Fast test
	callCount := 0
	retryableErr := errors.New("always fails")

	strategy.ErrorClassifier = &AllErrorsClassifier{}

	result, err := Do(context.Background(), strategy, func() (string, error) {
		callCount++
		return "", retryableErr
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries")
	assert.Equal(t, "", result)
	assert.Equal(t, 3, callCount, "should attempt max times")
}

// TestDo_ContextCancellation tests cancellation during retry
func TestDo_ContextCancellation(t *testing.T) {
	strategy := Default().WithMaxAttempts(5)
	strategy.BaseDelay = 100 * time.Millisecond
	callCount := 0

	ctx, cancel := context.WithCancel(context.Background())
	strategy.ErrorClassifier = &AllErrorsClassifier{}

	// Cancel after first attempt
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := Do(ctx, strategy, func() (string, error) {
		callCount++
		return "", errors.New("retry me")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cancel")
	assert.Equal(t, "", result)
	assert.Less(t, callCount, 5, "should stop on cancellation")
}

// TestDoSimple tests simple operation without return value
func TestDoSimple(t *testing.T) {
	strategy := Default().WithMaxAttempts(2)
	strategy.BaseDelay = 1 * time.Millisecond
	callCount := 0

	strategy.ErrorClassifier = &AllErrorsClassifier{}

	err := DoSimple(context.Background(), strategy, func() error {
		callCount++
		if callCount < 2 {
			return errors.New("retry")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 2, callCount)
}

// TestCalculateDelay tests exponential backoff calculation
func TestCalculateDelay(t *testing.T) {
	tests := []struct {
		name     string
		strategy Strategy
		attempt  int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{
			name: "first retry (2^0)",
			strategy: Strategy{
				BaseDelay:   100 * time.Millisecond,
				MaxDelay:    30 * time.Second,
				Multiplier:  2.0,
				JitterRatio: 0, // No jitter for predictable test
			},
			attempt:  0,
			minDelay: 100 * time.Millisecond,
			maxDelay: 100 * time.Millisecond,
		},
		{
			name: "second retry (2^1)",
			strategy: Strategy{
				BaseDelay:   100 * time.Millisecond,
				MaxDelay:    30 * time.Second,
				Multiplier:  2.0,
				JitterRatio: 0,
			},
			attempt:  1,
			minDelay: 200 * time.Millisecond,
			maxDelay: 200 * time.Millisecond,
		},
		{
			name: "third retry (2^2)",
			strategy: Strategy{
				BaseDelay:   100 * time.Millisecond,
				MaxDelay:    30 * time.Second,
				Multiplier:  2.0,
				JitterRatio: 0,
			},
			attempt:  2,
			minDelay: 400 * time.Millisecond,
			maxDelay: 400 * time.Millisecond,
		},
		{
			name: "capped at max delay",
			strategy: Strategy{
				BaseDelay:   100 * time.Millisecond,
				MaxDelay:    500 * time.Millisecond,
				Multiplier:  2.0,
				JitterRatio: 0,
			},
			attempt:  10, // 100 * 2^10 = 102400ms > 500ms
			minDelay: 500 * time.Millisecond,
			maxDelay: 500 * time.Millisecond,
		},
		{
			name: "with jitter (±15%)",
			strategy: Strategy{
				BaseDelay:   1000 * time.Millisecond,
				MaxDelay:    30 * time.Second,
				Multiplier:  2.0,
				JitterRatio: 0.15,
			},
			attempt:  0,
			minDelay: 850 * time.Millisecond,  // 1000 * (1 - 0.15)
			maxDelay: 1150 * time.Millisecond, // 1000 * (1 + 0.15)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := tt.strategy.calculateDelay(tt.attempt)
			assert.GreaterOrEqual(t, delay, tt.minDelay, "delay too small")
			assert.LessOrEqual(t, delay, tt.maxDelay, "delay too large")
		})
	}
}

// TestHTTPErrorClassifier tests HTTP error classification
func TestHTTPErrorClassifier(t *testing.T) {
	classifier := &HTTPErrorClassifier{}

	tests := []struct {
		name       string
		err        error
		retryable  bool
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "generic error",
			err:       errors.New("some error"),
			retryable: false,
		},
		// HTTPAPIError tests are in pkg/httperror package
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifier.IsRetryable(tt.err)
			assert.Equal(t, tt.retryable, result)
		})
	}
}

// TestStrategyDefaults tests default strategy configurations
func TestStrategyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		strategy Strategy
		expected Strategy
	}{
		{
			name:     "Default()",
			strategy: Default(),
			expected: Strategy{
				MaxAttempts:     3,
				BaseDelay:       100 * time.Millisecond,
				MaxDelay:        30 * time.Second,
				Multiplier:      2.0,
				JitterRatio:     0.15,
				ErrorClassifier: &HTTPErrorClassifier{},
			},
		},
		{
			name:     "Aggressive()",
			strategy: Aggressive(),
			expected: Strategy{
				MaxAttempts:     5,
				BaseDelay:       50 * time.Millisecond,
				MaxDelay:        10 * time.Second,
				Multiplier:      1.5,
				JitterRatio:     0.2,
				ErrorClassifier: &HTTPErrorClassifier{},
			},
		},
		{
			name:     "Conservative()",
			strategy: Conservative(),
			expected: Strategy{
				MaxAttempts:     2,
				BaseDelay:       500 * time.Millisecond,
				MaxDelay:        60 * time.Second,
				Multiplier:      3.0,
				JitterRatio:     0.1,
				ErrorClassifier: &HTTPErrorClassifier{},
			},
		},
		{
			name:     "NoRetry()",
			strategy: NoRetry(),
			expected: Strategy{
				MaxAttempts:     1,
				ErrorClassifier: &HTTPErrorClassifier{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected.MaxAttempts, tt.strategy.MaxAttempts)
			assert.Equal(t, tt.expected.BaseDelay, tt.strategy.BaseDelay)
			assert.Equal(t, tt.expected.MaxDelay, tt.strategy.MaxDelay)
			assert.Equal(t, tt.expected.Multiplier, tt.strategy.Multiplier)
			assert.Equal(t, tt.expected.JitterRatio, tt.strategy.JitterRatio)
		})
	}
}

// TestStrategyBuilders tests fluent builder methods
func TestStrategyBuilders(t *testing.T) {
	base := Default()

	t.Run("WithMaxAttempts", func(t *testing.T) {
		modified := base.WithMaxAttempts(5)
		assert.Equal(t, 5, modified.MaxAttempts)
		assert.Equal(t, 3, base.MaxAttempts, "original should not change")
	})

	t.Run("WithErrorClassifier", func(t *testing.T) {
		custom := &AllErrorsClassifier{}
		modified := base.WithErrorClassifier(custom)
		assert.Equal(t, custom, modified.ErrorClassifier)
	})
}

// TestIsRetryableError tests sentinel error checking helper
func TestIsRetryableError(t *testing.T) {
	var (
		ErrTransient  = errors.New("transient")
		ErrPermanent  = errors.New("permanent")
		ErrOther      = errors.New("other")
	)

	tests := []struct {
		name      string
		err       error
		sentinels []error
		expected  bool
	}{
		{
			name:      "matches first sentinel",
			err:       ErrTransient,
			sentinels: []error{ErrTransient, ErrPermanent},
			expected:  true,
		},
		{
			name:      "matches second sentinel",
			err:       ErrPermanent,
			sentinels: []error{ErrTransient, ErrPermanent},
			expected:  true,
		},
		{
			name:      "no match",
			err:       ErrOther,
			sentinels: []error{ErrTransient, ErrPermanent},
			expected:  false,
		},
		{
			name:      "wrapped error",
			err:       errors.Join(ErrTransient, ErrOther),
			sentinels: []error{ErrTransient},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err, tt.sentinels...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// BenchmarkDo benchmarks retry execution
func BenchmarkDo(b *testing.B) {
	strategy := Default()
	strategy.BaseDelay = 1 * time.Nanosecond // Minimal delay for benchmark

	b.Run("success_first_attempt", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = Do(context.Background(), strategy, func() (int, error) {
				return 42, nil
			})
		}
	})

	b.Run("success_third_attempt", func(b *testing.B) {
		strategy.ErrorClassifier = &AllErrorsClassifier{}
		for i := 0; i < b.N; i++ {
			attempt := 0
			_, _ = Do(context.Background(), strategy, func() (int, error) {
				attempt++
				if attempt < 3 {
					return 0, errors.New("retry")
				}
				return 42, nil
			})
		}
	})
}

// BenchmarkCalculateDelay benchmarks delay calculation
func BenchmarkCalculateDelay(b *testing.B) {
	strategy := Default()

	b.Run("multiplier_2.0_optimized", func(b *testing.B) {
		strategy.Multiplier = 2.0
		for i := 0; i < b.N; i++ {
			_ = strategy.calculateDelay(5)
		}
	})

	b.Run("multiplier_1.5_general", func(b *testing.B) {
		strategy.Multiplier = 1.5
		for i := 0; i < b.N; i++ {
			_ = strategy.calculateDelay(5)
		}
	})

	b.Run("with_jitter", func(b *testing.B) {
		strategy.Multiplier = 2.0
		strategy.JitterRatio = 0.15
		for i := 0; i < b.N; i++ {
			_ = strategy.calculateDelay(5)
		}
	})
}

// Example_basic demonstrates basic retry usage
func Example_basic() {
	strategy := Default()

	result, err := Do(context.Background(), strategy, func() (string, error) {
		// Your operation here
		return "success", nil
	})

	if err != nil {
		// Handle error
		return
	}

	_ = result // Use result
}

// Example_withConfiguration demonstrates custom configuration
func Example_withConfiguration() {
	strategy := Default().
		WithMaxAttempts(5).
		WithErrorClassifier(&CustomErrorClassifier{
			Fn: func(err error) bool {
				// Custom retry logic
				return true
			},
		})

	err := DoSimple(context.Background(), strategy, func() error {
		// Your operation here
		return nil
	})

	_ = err
}
