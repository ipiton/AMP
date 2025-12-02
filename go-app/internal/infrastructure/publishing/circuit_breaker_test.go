package publishing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewCircuitBreaker(t *testing.T) {
	config := CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          5 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	assert.NotNil(t, cb)
	assert.Equal(t, StateClosed, cb.State())
}

func TestCircuitBreaker_Closed(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          5 * time.Second,
	})

	// Initially closed
	assert.True(t, cb.CanAttempt())
	assert.Equal(t, StateClosed, cb.State())

	// Record success
	cb.RecordSuccess()
	assert.Equal(t, 0, cb.GetFailureCount())
}

func TestCircuitBreaker_OpenAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          100 * time.Millisecond,
	})

	// Record 3 failures
	cb.RecordFailure()
	assert.Equal(t, StateClosed, cb.State())
	cb.RecordFailure()
	assert.Equal(t, StateClosed, cb.State())
	cb.RecordFailure()

	// Should open after 3 failures
	assert.Equal(t, StateOpen, cb.State())
	assert.False(t, cb.CanAttempt())
}

func TestCircuitBreaker_HalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout:          50 * time.Millisecond,
	})

	// Open circuit
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.State())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Should allow attempt (half-open)
	assert.True(t, cb.CanAttempt())
}

func TestCircuitBreaker_RecoverAfterHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout:          50 * time.Millisecond,
	})

	// Open circuit
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.State())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Record successes in half-open
	cb.RecordSuccess() // Transition to half-open
	cb.RecordSuccess() // Still half-open
	cb.RecordSuccess() // Should close now (threshold = 2)

	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 0, cb.GetFailureCount())
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout:          5 * time.Second,
	})

	// Open circuit
	cb.RecordFailure()
	cb.RecordFailure()
	assert.Equal(t, StateOpen, cb.State())

	// Reset
	cb.Reset()

	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 0, cb.GetFailureCount())
	assert.True(t, cb.CanAttempt())
}
