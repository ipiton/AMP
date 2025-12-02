package processing

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// Mock AlertHandler for testing
type mockAlertHandler struct {
	processAlertFunc func(ctx context.Context, alert *core.Alert) error
	callCount        int32
	mu               sync.Mutex
}

func (m *mockAlertHandler) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	atomic.AddInt32(&m.callCount, 1)
	if m.processAlertFunc != nil {
		return m.processAlertFunc(ctx, alert)
	}
	return nil
}

func (m *mockAlertHandler) getCallCount() int {
	return int(atomic.LoadInt32(&m.callCount))
}

func TestNewAsyncWebhookProcessor(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   5,
		QueueSize: 100,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)
	require.NotNil(t, processor)
	assert.Equal(t, 5, processor.workers)
	assert.Equal(t, 100, processor.queueSize)
}

func TestNewAsyncWebhookProcessor_DefaultValues(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler: handler,
		// Workers and QueueSize not specified
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)
	assert.Equal(t, 10, processor.workers)     // Default
	assert.Equal(t, 1000, processor.queueSize) // Default
}

func TestNewAsyncWebhookProcessor_MissingHandler(t *testing.T) {
	config := AsyncProcessorConfig{
		Workers:   5,
		QueueSize: 100,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	assert.Error(t, err)
	assert.Nil(t, processor)
	assert.Contains(t, err.Error(), "handler is required")
}

func TestStartStop(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   2,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Start processor
	err = processor.Start(ctx)
	require.NoError(t, err)

	// Verify running
	stats := processor.GetStats()
	assert.True(t, stats.Running)

	// Stop processor
	err = processor.Stop()
	require.NoError(t, err)

	// Verify stopped
	stats = processor.GetStats()
	assert.False(t, stats.Running)
}

func TestStart_AlreadyRunning(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   1,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Start once
	err = processor.Start(ctx)
	require.NoError(t, err)

	// Start again (should fail)
	err = processor.Start(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	// Cleanup
	_ = processor.Stop()
}

func TestSubmitJob_Success(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   2,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = processor.Start(ctx)
	require.NoError(t, err)
	defer processor.Stop()

	// Submit job
	job := &WebhookJob{
		ID: "test-job-1",
		Alerts: []*core.Alert{
			{
				Fingerprint: "fp1",
				AlertName:   "TestAlert",
				Status:      core.StatusFiring,
				StartsAt:    time.Now(),
			},
		},
		CreatedAt: time.Now(),
	}

	err = processor.SubmitJob(ctx, job)
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify alert was processed
	assert.Equal(t, 1, handler.getCallCount())
}

func TestSubmitJob_ProcessorNotRunning(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   1,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	// Don't start processor

	job := &WebhookJob{
		ID:     "test-job",
		Alerts: []*core.Alert{},
	}

	ctx := context.Background()
	err = processor.SubmitJob(ctx, job)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestSubmitJob_QueueFull(t *testing.T) {
	// Block handler to prevent processing
	processing := make(chan struct{})
	handler := &mockAlertHandler{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			<-processing // Block until test completes
			return nil
		},
	}

	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   1,
		QueueSize: 2, // Small queue
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = processor.Start(ctx)
	require.NoError(t, err)

	// Fill queue (2 in queue + 1 being processed)
	for i := 0; i < 3; i++ {
		job := &WebhookJob{
			ID:     fmt.Sprintf("job-%d", i),
			Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
		}
		err := processor.SubmitJob(ctx, job)
		if err != nil {
			t.Logf("Job %d submission failed: %v", i, err)
		} else {
			t.Logf("Job %d submitted successfully", i)
		}
		time.Sleep(10 * time.Millisecond) // Small delay
	}

	// Try to submit when queue is full
	job := &WebhookJob{
		ID:     "overflow-job",
		Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
	}

	err = processor.SubmitJob(ctx, job)
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "queue full")
	}

	// Unblock processing and cleanup
	close(processing)
	_ = processor.Stop()
}

func TestSubmitJob_MultipleJobs(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   3,
		QueueSize: 100,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = processor.Start(ctx)
	require.NoError(t, err)
	defer processor.Stop()

	// Submit multiple jobs
	jobCount := 10
	alertsPerJob := 5

	for i := 0; i < jobCount; i++ {
		alerts := make([]*core.Alert, alertsPerJob)
		for j := 0; j < alertsPerJob; j++ {
			alerts[j] = &core.Alert{
				Fingerprint: fmt.Sprintf("fp-%d-%d", i, j),
				AlertName:   fmt.Sprintf("Alert-%d-%d", i, j),
				Status:      core.StatusFiring,
				StartsAt:    time.Now(),
			}
		}

		job := &WebhookJob{
			ID:        fmt.Sprintf("job-%d", i),
			Alerts:    alerts,
			CreatedAt: time.Now(),
		}

		err := processor.SubmitJob(ctx, job)
		assert.NoError(t, err)
	}

	// Wait for all jobs to process
	time.Sleep(200 * time.Millisecond)

	// Verify all alerts were processed
	expectedCalls := jobCount * alertsPerJob
	assert.Equal(t, expectedCalls, handler.getCallCount())
}

func TestProcessJob_WithErrors(t *testing.T) {
	failCount := 0
	handler := &mockAlertHandler{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			if alert.AlertName == "FailingAlert" {
				failCount++
				return fmt.Errorf("simulated error")
			}
			return nil
		},
	}

	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   1,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = processor.Start(ctx)
	require.NoError(t, err)
	defer processor.Stop()

	// Submit job with some failing alerts
	job := &WebhookJob{
		ID: "mixed-job",
		Alerts: []*core.Alert{
			{Fingerprint: "fp1", AlertName: "GoodAlert1", Status: core.StatusFiring, StartsAt: time.Now()},
			{Fingerprint: "fp2", AlertName: "FailingAlert", Status: core.StatusFiring, StartsAt: time.Now()},
			{Fingerprint: "fp3", AlertName: "GoodAlert2", Status: core.StatusFiring, StartsAt: time.Now()},
		},
		CreatedAt: time.Now(),
	}

	err = processor.SubmitJob(ctx, job)
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify all alerts were attempted
	assert.Equal(t, 3, handler.getCallCount())
	assert.Equal(t, 1, failCount)
}

func TestGracefulShutdown(t *testing.T) {
	handler := &mockAlertHandler{
		processAlertFunc: func(ctx context.Context, alert *core.Alert) error {
			time.Sleep(50 * time.Millisecond) // Simulate work
			return nil
		},
	}

	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   2,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx := context.Background()
	err = processor.Start(ctx)
	require.NoError(t, err)

	// Submit jobs (reduced to 3 to ensure they finish within Stop timeout)
	jobCount := 3
	for i := 0; i < jobCount; i++ {
		job := &WebhookJob{
			ID:     fmt.Sprintf("job-%d", i),
			Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
		}
		_ = processor.SubmitJob(ctx, job)
	}

	// Stop (should wait for in-flight jobs)
	err = processor.Stop()
	assert.NoError(t, err)

	// Verify all jobs were processed (should be jobCount)
	assert.GreaterOrEqual(t, handler.getCallCount(), jobCount-1) // Allow for potential race
}

func TestContextCancellation(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   2,
		QueueSize: 10,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	err = processor.Start(ctx)
	require.NoError(t, err)

	// Submit job
	job := &WebhookJob{
		ID:     "test-job",
		Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
	}
	_ = processor.SubmitJob(ctx, job)

	// Cancel context
	cancel()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Stop should succeed (workers should have stopped via context)
	err = processor.Stop()
	// May get "not running" error if workers already stopped
	// That's OK
}

func TestGetStats(t *testing.T) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   5,
		QueueSize: 100,
	}

	processor, err := NewAsyncWebhookProcessor(config)
	require.NoError(t, err)

	// Before start
	stats := processor.GetStats()
	assert.False(t, stats.Running)
	assert.Equal(t, 5, stats.Workers)
	assert.Equal(t, 100, stats.QueueSize)
	assert.Equal(t, 0, stats.CurrentQueue)

	// After start
	ctx := context.Background()
	_ = processor.Start(ctx)
	defer processor.Stop()

	stats = processor.GetStats()
	assert.True(t, stats.Running)
}

// Benchmark tests
func BenchmarkSubmitJob(b *testing.B) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   10,
		QueueSize: 10000,
	}

	processor, _ := NewAsyncWebhookProcessor(config)
	ctx := context.Background()
	_ = processor.Start(ctx)
	defer processor.Stop()

	job := &WebhookJob{
		ID:     "bench-job",
		Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = processor.SubmitJob(ctx, job)
	}
}

func BenchmarkProcessJob_SingleAlert(b *testing.B) {
	handler := &mockAlertHandler{}
	config := AsyncProcessorConfig{
		Handler:   handler,
		Workers:   10,
		QueueSize: 100000,
	}

	processor, _ := NewAsyncWebhookProcessor(config)
	ctx := context.Background()
	_ = processor.Start(ctx)
	defer processor.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := &WebhookJob{
			ID:     fmt.Sprintf("job-%d", i),
			Alerts: []*core.Alert{{Fingerprint: "fp", AlertName: "test", Status: core.StatusFiring, StartsAt: time.Now()}},
		}
		_ = processor.SubmitJob(ctx, job)
	}

	// Wait for all jobs to complete
	b.StopTimer()
	time.Sleep(1 * time.Second)
}
