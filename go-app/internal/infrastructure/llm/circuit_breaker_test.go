package llm

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCircuitBreaker_NewCircuitBreaker tests circuit breaker creation.
func TestCircuitBreaker_NewCircuitBreaker(t *testing.T) {
	tests := []struct {
		name        string
		config      CircuitBreakerConfig
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid_config",
			config:  DefaultCircuitBreakerConfig(),
			wantErr: false,
		},
		{
			name: "zero_max_failures",
			config: CircuitBreakerConfig{
				MaxFailures:      0,
				ResetTimeout:     30 * time.Second,
				FailureThreshold: 0.5,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
				Enabled:          true,
			},
			wantErr:     true,
			errContains: "max_failures must be positive",
		},
		{
			name: "zero_reset_timeout",
			config: CircuitBreakerConfig{
				MaxFailures:      5,
				ResetTimeout:     0,
				FailureThreshold: 0.5,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
				Enabled:          true,
			},
			wantErr:     true,
			errContains: "reset_timeout must be positive",
		},
		{
			name: "invalid_failure_threshold_negative",
			config: CircuitBreakerConfig{
				MaxFailures:      5,
				ResetTimeout:     30 * time.Second,
				FailureThreshold: -0.1,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
				Enabled:          true,
			},
			wantErr:     true,
			errContains: "failure_threshold must be between 0 and 1",
		},
		{
			name: "invalid_failure_threshold_over_one",
			config: CircuitBreakerConfig{
				MaxFailures:      5,
				ResetTimeout:     30 * time.Second,
				FailureThreshold: 1.1,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
				Enabled:          true,
			},
			wantErr:     true,
			errContains: "failure_threshold must be between 0 and 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, err := NewCircuitBreaker(tt.config, nil, nil)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, cb)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, cb)
				assert.Equal(t, StateClosed, cb.GetState())
			}
		})
	}
}

// TestCircuitBreaker_StateTransitions tests state machine transitions.
func TestCircuitBreaker_StateTransitions(t *testing.T) {
	tests := []struct {
		name          string
		maxFailures   int
		operations    []bool // true = success, false = failure
		expectedState CircuitBreakerState
	}{
		{
			name:          "should_stay_closed_on_success",
			maxFailures:   3,
			operations:    []bool{true, true, true},
			expectedState: StateClosed,
		},
		{
			name:          "should_open_after_threshold_failures",
			maxFailures:   3,
			operations:    []bool{false, false, false},
			expectedState: StateOpen,
		},
		{
			name:          "should_stay_closed_if_below_threshold",
			maxFailures:   5,
			operations:    []bool{false, true, true, false, true}, // 2 failures out of 5 = 40% < 50%
			expectedState: StateClosed,
		},
		{
			name:          "should_open_on_consecutive_failures",
			maxFailures:   3,
			operations:    []bool{true, true, false, false, false},
			expectedState: StateOpen,
		},
		{
			name:          "should_reset_consecutive_failures_on_success",
			maxFailures:   5,                                      // Higher threshold
			operations:    []bool{false, true, false, true, true}, // 2 failures out of 5 = 40% < 50%, consecutive reset by success
			expectedState: StateClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultCircuitBreakerConfig()
			config.MaxFailures = tt.maxFailures
			config.SlowCallDuration = 100 * time.Millisecond

			cb, err := NewCircuitBreaker(config, nil, nil)
			require.NoError(t, err)

			// Execute operations
			for i, success := range tt.operations {
				ctx := context.Background()
				err := cb.Call(ctx, func(ctx context.Context) error {
					if success {
						return nil
					}
					return errors.New("test failure")
				})

				t.Logf("Operation %d: success=%v, error=%v, state=%s",
					i+1, success, err, cb.GetState())
			}

			// Verify final state
			assert.Equal(t, tt.expectedState, cb.GetState(),
				"Expected state %s but got %s", tt.expectedState, cb.GetState())
		})
	}
}

// TestCircuitBreaker_HalfOpenTransition tests transition from OPEN to HALF_OPEN.
func TestCircuitBreaker_HalfOpenTransition(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.ResetTimeout = 100 * time.Millisecond
	config.SlowCallDuration = 50 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	// Open the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	assert.Equal(t, StateOpen, cb.GetState())

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Next call should transition to HALF_OPEN
	err = cb.Call(ctx, func(ctx context.Context) error {
		return nil // Success
	})

	assert.NoError(t, err)
	assert.Equal(t, StateClosed, cb.GetState(), "Should transition to CLOSED after successful test")
}

// TestCircuitBreaker_HalfOpenToOpen tests transition from HALF_OPEN back to OPEN on failure.
func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.ResetTimeout = 100 * time.Millisecond
	config.SlowCallDuration = 50 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	// Open the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	assert.Equal(t, StateOpen, cb.GetState())

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Next call fails - should go back to OPEN
	err = cb.Call(ctx, func(ctx context.Context) error {
		return errors.New("test failure in half-open")
	})

	assert.Error(t, err)
	assert.Equal(t, StateOpen, cb.GetState(), "Should transition back to OPEN after failed test")
}

// TestCircuitBreaker_FailFast tests that OPEN circuit fails fast without calling operation.
func TestCircuitBreaker_FailFast(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.ResetTimeout = 1 * time.Hour // Long timeout so it stays open
	config.SlowCallDuration = 50 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	// Open the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	assert.Equal(t, StateOpen, cb.GetState())

	// Verify fail-fast behavior
	operationCalled := false
	err = cb.Call(ctx, func(ctx context.Context) error {
		operationCalled = true
		return nil
	})

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrCircuitBreakerOpen))
	assert.False(t, operationCalled, "Operation should not be called when circuit is open")
}

// TestCircuitBreaker_SlowCalls tests that slow calls are treated as failures.
func TestCircuitBreaker_SlowCalls(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.SlowCallDuration = 50 * time.Millisecond
	config.ResetTimeout = 100 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	// Make slow calls (successful but exceed threshold)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			time.Sleep(100 * time.Millisecond) // Slow
			return nil
		})
	}

	// Circuit should be open due to slow calls
	assert.Equal(t, StateOpen, cb.GetState(), "Circuit should open after slow calls")
}

// TestCircuitBreaker_ConcurrentAccess tests thread safety with concurrent calls.
func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 10
	config.SlowCallDuration = 10 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	const numGoroutines = 100
	const callsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx := context.Background()
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < callsPerGoroutine; j++ {
				_ = cb.Call(ctx, func(ctx context.Context) error {
					time.Sleep(1 * time.Millisecond)
					if (id+j)%3 == 0 {
						return errors.New("intermittent failure")
					}
					return nil
				})
			}
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred and state is consistent
	stats := cb.GetStats()
	t.Logf("Final stats: State=%s, Failures=%d, Successes=%d",
		stats.State, stats.FailureCount, stats.SuccessCount)

	assert.True(t, stats.FailureCount+stats.SuccessCount > 0, "Should have recorded calls")
}

// TestCircuitBreaker_SlidingWindow tests that old results are cleaned up.
func TestCircuitBreaker_SlidingWindow(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 10
	config.TimeWindow = 200 * time.Millisecond
	config.FailureThreshold = 0.5 // 50% failure rate
	config.SlowCallDuration = 10 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Make some failures
	for i := 0; i < 6; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	stats1 := cb.GetStats()
	t.Logf("After failures: TotalCalls=%d, State=%s", stats1.TotalCalls, stats1.State)

	// Wait for time window to expire
	time.Sleep(250 * time.Millisecond)

	// Make a success call to trigger cleanup
	_ = cb.Call(ctx, func(ctx context.Context) error {
		return nil
	})

	stats2 := cb.GetStats()
	t.Logf("After window expiry: TotalCalls=%d, State=%s", stats2.TotalCalls, stats2.State)

	// Old results should be cleaned up
	assert.Less(t, stats2.TotalCalls, stats1.TotalCalls+1,
		"Old results should be cleaned from sliding window")
}

// TestCircuitBreaker_GetStats tests statistics retrieval.
func TestCircuitBreaker_GetStats(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 3
	config.SlowCallDuration = 50 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Make some calls
	_ = cb.Call(ctx, func(ctx context.Context) error { return nil })
	_ = cb.Call(ctx, func(ctx context.Context) error { return errors.New("failure") })
	_ = cb.Call(ctx, func(ctx context.Context) error { return nil })

	stats := cb.GetStats()

	assert.Equal(t, StateClosed, stats.State)
	assert.Equal(t, 1, stats.FailureCount)
	assert.Equal(t, 2, stats.SuccessCount)
	assert.Equal(t, 0, stats.ConsecutiveFailures)
	assert.Equal(t, 1, stats.ConsecutiveSuccesses)
	assert.False(t, stats.LastSuccess.IsZero())
	assert.False(t, stats.LastFailure.IsZero())
}

// TestCircuitBreaker_Reset tests manual reset functionality.
func TestCircuitBreaker_Reset(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 2
	config.ResetTimeout = 1 * time.Hour
	config.SlowCallDuration = 50 * time.Millisecond

	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	// Open the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	assert.Equal(t, StateOpen, cb.GetState())

	// Reset
	cb.Reset()

	assert.Equal(t, StateClosed, cb.GetState())
	stats := cb.GetStats()
	assert.Equal(t, 0, stats.FailureCount)
	assert.Equal(t, 0, stats.ConsecutiveFailures)
	assert.Equal(t, 0, stats.TotalCalls)
}

// TestCircuitBreaker_ContextCancellation tests that context cancellation is respected.
func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	cb, err := NewCircuitBreaker(config, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = cb.Call(ctx, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			return nil
		}
	})

	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "Should return context.Canceled error")
}

// TestCircuitBreakerState_String tests state string representation.
func TestCircuitBreakerState_String(t *testing.T) {
	tests := []struct {
		state    CircuitBreakerState
		expected string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

// TestDefaultCircuitBreakerConfig tests default configuration.
func TestDefaultCircuitBreakerConfig(t *testing.T) {
	config := DefaultCircuitBreakerConfig()

	assert.Equal(t, 5, config.MaxFailures)
	assert.Equal(t, 30*time.Second, config.ResetTimeout)
	assert.Equal(t, 0.5, config.FailureThreshold)
	assert.Equal(t, 60*time.Second, config.TimeWindow)
	assert.Equal(t, 3*time.Second, config.SlowCallDuration)
	assert.Equal(t, 1, config.HalfOpenMaxCalls)
	assert.True(t, config.Enabled)
}

// TestCircuitBreakerConfig_Validate tests configuration validation.
func TestCircuitBreakerConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      CircuitBreakerConfig
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid_config",
			config:  DefaultCircuitBreakerConfig(),
			wantErr: false,
		},
		{
			name: "negative_max_failures",
			config: CircuitBreakerConfig{
				MaxFailures:      -1,
				ResetTimeout:     30 * time.Second,
				FailureThreshold: 0.5,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
			},
			wantErr:     true,
			errContains: "max_failures must be positive",
		},
		{
			name: "negative_reset_timeout",
			config: CircuitBreakerConfig{
				MaxFailures:      5,
				ResetTimeout:     -1 * time.Second,
				FailureThreshold: 0.5,
				TimeWindow:       60 * time.Second,
				SlowCallDuration: 3 * time.Second,
				HalfOpenMaxCalls: 1,
			},
			wantErr:     true,
			errContains: "reset_timeout must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCircuitBreaker_WithMetrics tests metrics recording.
func TestCircuitBreaker_WithMetrics(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	config.MaxFailures = 5 // Higher threshold to avoid opening during test
	config.SlowCallDuration = 50 * time.Millisecond

	logger := slog.Default()
	metrics := NewCircuitBreakerMetrics()

	cb, err := NewCircuitBreaker(config, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Success
	err = cb.Call(ctx, func(ctx context.Context) error {
		return nil
	})
	assert.NoError(t, err)

	// Another success to maintain <50% failure rate
	err = cb.Call(ctx, func(ctx context.Context) error {
		return nil
	})
	assert.NoError(t, err)

	// Failure
	err = cb.Call(ctx, func(ctx context.Context) error {
		return errors.New("test failure")
	})
	assert.Error(t, err)

	// Slow call (treated as failure but operation doesn't return error)
	err = cb.Call(ctx, func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	// Operation succeeds but CB counts it as failure due to slowness
	assert.NoError(t, err) // The operation itself didn't error

	// Verify metrics are updated (no panics)
	stats := cb.GetStats()
	assert.True(t, stats.SuccessCount > 0)
	assert.True(t, stats.FailureCount > 0)

	// Circuit should still be closed (2 failures + 2 successes = 50% threshold)
	assert.Equal(t, StateClosed, cb.GetState())
}
