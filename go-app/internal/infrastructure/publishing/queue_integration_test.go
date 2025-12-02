package publishing

import (
	"fmt"
	"testing"
	"time"
)

// TestPublishingQueueConfig_Defaults tests default configuration
func TestPublishingQueueConfig_Defaults(t *testing.T) {
	config := DefaultPublishingQueueConfig()

	if config.WorkerCount <= 0 {
		t.Errorf("Expected positive WorkerCount, got %d", config.WorkerCount)
	}
	if config.HighPriorityQueueSize <= 0 {
		t.Errorf("Expected positive HighPriorityQueueSize, got %d", config.HighPriorityQueueSize)
	}
	if config.MaxRetries <= 0 {
		t.Errorf("Expected positive MaxRetries, got %d", config.MaxRetries)
	}
	if config.RetryInterval <= 0 {
		t.Errorf("Expected positive RetryInterval, got %v", config.RetryInterval)
	}
}

// TestPublishingQueueConfig_CustomValues tests custom configuration
func TestPublishingQueueConfig_CustomValues(t *testing.T) {
	config := PublishingQueueConfig{
		WorkerCount:             5,
		HighPriorityQueueSize:   100,
		MediumPriorityQueueSize: 200,
		LowPriorityQueueSize:    300,
		MaxRetries:              3,
		RetryInterval:           100 * time.Millisecond,
		CircuitTimeout:          30 * time.Second,
	}

	if config.WorkerCount != 5 {
		t.Errorf("Expected WorkerCount 5, got %d", config.WorkerCount)
	}
	if config.HighPriorityQueueSize != 100 {
		t.Errorf("Expected HighPriorityQueueSize 100, got %d", config.HighPriorityQueueSize)
	}
	if config.MaxRetries != 3 {
		t.Errorf("Expected MaxRetries 3, got %d", config.MaxRetries)
	}
}

// TestPublishingJob_StateTransitions tests job state transitions
func TestPublishingJob_StateTransitions(t *testing.T) {
	job := createSampleDLQJob()

	// Initial state
	if job.State != JobStateFailed {
		t.Errorf("Expected initial state Failed, got %v", job.State)
	}

	// Transition to Queued
	job.State = JobStateQueued
	if job.State != JobStateQueued {
		t.Errorf("Expected state Queued, got %v", job.State)
	}

	// Transition to Processing
	job.State = JobStateProcessing
	now := time.Now()
	job.StartedAt = &now

	if job.State != JobStateProcessing {
		t.Errorf("Expected state Processing, got %v", job.State)
	}
	if job.StartedAt == nil {
		t.Error("Expected non-nil StartedAt")
	}

	// Transition to Succeeded
	job.State = JobStateSucceeded
	completedNow := time.Now()
	job.CompletedAt = &completedNow

	if job.State != JobStateSucceeded {
		t.Errorf("Expected state Succeeded, got %v", job.State)
	}
	if job.CompletedAt == nil {
		t.Error("Expected non-nil CompletedAt")
	}
}

// TestPublishingJob_RetryTracking tests retry count tracking
func TestPublishingJob_RetryTracking(t *testing.T) {
	job := createSampleDLQJob()

	// Initial retry count
	if job.RetryCount != 3 {
		t.Errorf("Expected initial RetryCount 3, got %d", job.RetryCount)
	}

	// Increment retry count
	job.RetryCount++
	if job.RetryCount != 4 {
		t.Errorf("Expected RetryCount 4 after increment, got %d", job.RetryCount)
	}

	// Set retry state
	job.State = JobStateRetrying
	if job.State != JobStateRetrying {
		t.Errorf("Expected state Retrying, got %v", job.State)
	}
}

// TestPublishingJob_ErrorTracking tests error information tracking
func TestPublishingJob_ErrorTracking(t *testing.T) {
	job := createSampleDLQJob()

	// Initial error
	if job.LastError == nil {
		t.Fatal("Expected non-nil LastError")
	}
	if job.ErrorType != QueueErrorTypeTransient {
		t.Errorf("Expected error type Transient, got %v", job.ErrorType)
	}

	// Update error
	job.LastError = fmt.Errorf("permanent error: invalid credentials")
	job.ErrorType = QueueErrorTypePermanent

	if job.ErrorType != QueueErrorTypePermanent {
		t.Errorf("Expected error type Permanent after update, got %v", job.ErrorType)
	}
}

// TestPublishingJob_Timestamps tests timestamp tracking
func TestPublishingJob_Timestamps(t *testing.T) {
	job := createSampleDLQJob()

	// SubmittedAt is set
	if job.SubmittedAt.IsZero() {
		t.Error("Expected non-zero SubmittedAt")
	}

	// StartedAt is set
	if job.StartedAt == nil || job.StartedAt.IsZero() {
		t.Error("Expected non-zero StartedAt")
	}

	// CompletedAt initially nil (job failed)
	if job.CompletedAt != nil && !job.CompletedAt.IsZero() {
		t.Logf("CompletedAt is set: %v", job.CompletedAt)
	}

	// Set CompletedAt
	now := time.Now()
	job.CompletedAt = &now

	// Calculate duration
	duration := job.CompletedAt.Sub(*job.StartedAt)
	if duration < 0 {
		t.Errorf("Expected positive duration, got %v", duration)
	}
}

// TestPublishingJob_PriorityAssignment tests priority field
func TestPublishingJob_PriorityAssignment(t *testing.T) {
	job := createSampleDLQJob()

	// Default priority is High
	if job.Priority != PriorityHigh {
		t.Errorf("Expected priority High, got %v", job.Priority)
	}

	// Change priority
	job.Priority = PriorityLow
	if job.Priority != PriorityLow {
		t.Errorf("Expected priority Low after change, got %v", job.Priority)
	}
}

// TestJobState_String tests JobState string conversion
func TestJobState_String(t *testing.T) {
	states := []struct {
		state    JobState
		expected string
	}{
		{JobStateQueued, "queued"},
		{JobStateProcessing, "processing"},
		{JobStateRetrying, "retrying"},
		{JobStateSucceeded, "succeeded"},
		{JobStateFailed, "failed"},
		{JobStateDLQ, "dlq"},
	}

	for _, tt := range states {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestPriority_String tests Priority string conversion
func TestPriority_String(t *testing.T) {
	priorities := []struct {
		priority Priority
		expected string
	}{
		{PriorityHigh, "high"},
		{PriorityMedium, "medium"},
		{PriorityLow, "low"},
	}

	for _, tt := range priorities {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.priority.String()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestQueueErrorType_String tests QueueErrorType string conversion
func TestQueueErrorType_String(t *testing.T) {
	errorTypes := []struct {
		errorType QueueErrorType
		expected  string
	}{
		{QueueErrorTypeTransient, "transient"},
		{QueueErrorTypePermanent, "permanent"},
		{QueueErrorTypeUnknown, "unknown"},
	}

	for _, tt := range errorTypes {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.errorType.String()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// BenchmarkPublishingJob_StateTransition benchmarks state transitions
func BenchmarkPublishingJob_StateTransition(b *testing.B) {
	job := createSampleDLQJob()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job.State = JobStateQueued
		job.State = JobStateProcessing
		job.State = JobStateSucceeded
	}
}

// BenchmarkPublishingJob_RetryIncrement benchmarks retry count increment
func BenchmarkPublishingJob_RetryIncrement(b *testing.B) {
	job := createSampleDLQJob()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job.RetryCount++
	}
}
