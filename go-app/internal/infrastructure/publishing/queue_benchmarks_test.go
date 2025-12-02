package publishing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Benchmark helper to create alert for queue benchmarking
func createQueueBenchmarkAlert(severity string) *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "bench-fp",
			Labels:      map[string]string{"severity": severity},
			Annotations: map[string]string{"summary": "Benchmark alert"},
			Status:      core.StatusFiring,
		},
	}
}

// BenchmarkDeterminePriority benchmarks priority determination
func BenchmarkDeterminePriority(b *testing.B) {
	alert := createQueueBenchmarkAlert("critical")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = determinePriority(alert)
	}
}

// BenchmarkDeterminePriority_CriticalAlert benchmarks critical alert priority
func BenchmarkDeterminePriority_CriticalAlert(b *testing.B) {
	alert := createQueueBenchmarkAlert("critical")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := determinePriority(alert)
		if priority != PriorityHigh {
			b.Fatal("Expected PriorityHigh")
		}
	}
}

// BenchmarkDeterminePriority_LowAlert benchmarks low priority alert
func BenchmarkDeterminePriority_LowAlert(b *testing.B) {
	alert := createQueueBenchmarkAlert("info")
	alert.Alert.Status = core.StatusResolved

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := determinePriority(alert)
		if priority != PriorityLow {
			b.Fatal("Expected PriorityLow")
		}
	}
}

// BenchmarkCalculateBackoff_Sequential benchmarks backoff calculation
func BenchmarkCalculateBackoff_Sequential(b *testing.B) {
	config := DefaultQueueRetryConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		attempt := i % 10 // Cycle through attempts 0-9
		_ = CalculateBackoff(attempt, config)
	}
}

// BenchmarkCalculateBackoff_MaxBackoff benchmarks max backoff limit
func BenchmarkCalculateBackoff_MaxBackoff(b *testing.B) {
	config := DefaultQueueRetryConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CalculateBackoff(100, config) // High attempt number (capped at MaxBackoff)
	}
}

// BenchmarkShouldRetry_TransientError benchmarks retry decision for transient errors
func BenchmarkShouldRetry_TransientError(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ShouldRetry(QueueErrorTypeTransient, 1, 3)
	}
}

// BenchmarkShouldRetry_PermanentError benchmarks retry decision for permanent errors
func BenchmarkShouldRetry_PermanentError(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ShouldRetry(QueueErrorTypePermanent, 0, 3)
	}
}

// BenchmarkClassifyPublishingError_HTTPError benchmarks HTTP error classification
func BenchmarkClassifyPublishingError_HTTPError(b *testing.B) {
	err := &mockHTTPError{statusCode: 503, message: "Service Unavailable"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyPublishingError(err)
	}
}

// BenchmarkClassifyPublishingError_NetworkError benchmarks network error classification
func BenchmarkClassifyPublishingError_NetworkError(b *testing.B) {
	err := fmt.Errorf("network timeout")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyPublishingError(err)
	}
}

// BenchmarkLRUJobTrackingStore_AddParallel benchmarks parallel Add operations
func BenchmarkLRUJobTrackingStore_AddParallel(b *testing.B) {
	store := NewLRUJobTrackingStore(100000)
	job := createSampleDLQJob()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			job.ID = fmt.Sprintf("job-%d", i)
			store.Add(job)
			i++
		}
	})
}

// BenchmarkLRUJobTrackingStore_GetParallel benchmarks parallel Get operations
func BenchmarkLRUJobTrackingStore_GetParallel(b *testing.B) {
	store := NewLRUJobTrackingStore(100000)

	// Populate store
	for i := 0; i < 1000; i++ {
		job := createSampleDLQJob()
		job.ID = fmt.Sprintf("job-%d", i)
		store.Add(job)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			jobID := fmt.Sprintf("job-%d", i%1000)
			_ = store.Get(jobID)
			i++
		}
	})
}

// BenchmarkCircuitBreaker_CanAttempt benchmarks circuit breaker check
func BenchmarkCircuitBreaker_CanAttempt(b *testing.B) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.CanAttempt()
	}
}

// BenchmarkCircuitBreaker_RecordSuccess benchmarks success recording
func BenchmarkCircuitBreaker_RecordSuccess(b *testing.B) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordSuccess()
	}
}

// BenchmarkCircuitBreaker_RecordFailure benchmarks failure recording
func BenchmarkCircuitBreaker_RecordFailure(b *testing.B) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordFailure()
	}
}

// BenchmarkPublishingQueueConfig_Creation benchmarks config creation
func BenchmarkPublishingQueueConfig_Creation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DefaultPublishingQueueConfig()
	}
}

// BenchmarkQueueRetryConfig_Creation benchmarks retry config creation
func BenchmarkQueueRetryConfig_Creation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DefaultQueueRetryConfig()
	}
}

// BenchmarkPriority_ToString benchmarks Priority.String() conversion
func BenchmarkPriority_ToString(b *testing.B) {
	priority := PriorityHigh

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = priority.String()
	}
}

// BenchmarkJobState_ToString benchmarks JobState.String() conversion
func BenchmarkJobState_ToString(b *testing.B) {
	state := JobStateProcessing

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.String()
	}
}

// BenchmarkQueueErrorType_ToString benchmarks QueueErrorType.String() conversion
func BenchmarkQueueErrorType_ToString(b *testing.B) {
	errorType := QueueErrorTypeTransient

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = errorType.String()
	}
}

// BenchmarkDLQEntry_Creation benchmarks DLQ entry creation
func BenchmarkDLQEntry_Creation(b *testing.B) {
	job := createSampleDLQJob()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &DLQEntry{
			Fingerprint:   job.EnrichedAlert.Alert.Fingerprint,
			TargetName:    job.Target.Name,
			TargetType:    job.Target.Type,
			ErrorMessage:  job.LastError.Error(),
			ErrorType:     job.ErrorType.String(),
			RetryCount:    job.RetryCount,
			Priority:      job.Priority.String(),
			FailedAt:      time.Now(),
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Replayed:      false,
		}
	}
}

// BenchmarkJobSnapshot_Creation benchmarks job snapshot creation
func BenchmarkJobSnapshot_Creation(b *testing.B) {
	job := createSampleDLQJob()
	now := time.Now().Unix()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &JobSnapshot{
			ID:          job.ID,
			Fingerprint: job.EnrichedAlert.Alert.Fingerprint,
			TargetName:  job.Target.Name,
			Priority:    job.Priority.String(),
			State:       job.State.String(),
			SubmittedAt: now,
			RetryCount:  job.RetryCount,
		}
	}
}

// BenchmarkContextWithTimeout benchmarks context creation with timeout
func BenchmarkContextWithTimeout(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		cancel()
		_ = ctx2
	}
}
