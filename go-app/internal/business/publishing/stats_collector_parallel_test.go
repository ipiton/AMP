package publishing

import (
	"testing"
	"time"

	infraPublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// Test: NewParallelPublishStatsCollector
func TestNewParallelPublishStatsCollector(t *testing.T) {
	collector := NewParallelPublishStatsCollector(1000)

	if collector == nil {
		t.Fatal("Expected collector, got nil")
	}
	if collector.maxSamples != 1000 {
		t.Errorf("Expected maxSamples=1000, got %d", collector.maxSamples)
	}
	if len(collector.durationSamples) != 0 {
		t.Errorf("Expected empty durationSamples, got %d", len(collector.durationSamples))
	}
}

// Test: RecordPublish and GetStats
func TestRecordPublishAndGetStats(t *testing.T) {
	collector := NewParallelPublishStatsCollector(100)

	// Create test result (all succeeded)
	result1 := &infraPublishing.ParallelPublishResult{
		TotalTargets: 3,
		SuccessCount: 3,
		FailureCount: 0,
		SkippedCount: 0,
		Duration:     100 * time.Millisecond,
	}
	collector.RecordPublish(result1)

	// Create test result (partial success)
	result2 := &infraPublishing.ParallelPublishResult{
		TotalTargets:     5,
		SuccessCount:     3,
		FailureCount:     2,
		SkippedCount:     0,
		IsPartialSuccess: true,
		Duration:         200 * time.Millisecond,
	}
	collector.RecordPublish(result2)

	// Create test result (all failed)
	result3 := &infraPublishing.ParallelPublishResult{
		TotalTargets: 2,
		SuccessCount: 0,
		FailureCount: 2,
		SkippedCount: 0,
		Duration:     50 * time.Millisecond,
	}
	collector.RecordPublish(result3)

	// Get stats
	stats := collector.GetStats()

	// Verify counts
	if stats.TotalOperations != 3 {
		t.Errorf("Expected 3 operations, got %d", stats.TotalOperations)
	}
	if stats.SuccessfulOperations != 2 { // result1 + result2 (partial counts as success)
		t.Errorf("Expected 2 successful operations, got %d", stats.SuccessfulOperations)
	}
	if stats.FailedOperations != 1 { // result3
		t.Errorf("Expected 1 failed operation, got %d", stats.FailedOperations)
	}
	if stats.PartialSuccessOps != 1 { // result2
		t.Errorf("Expected 1 partial success, got %d", stats.PartialSuccessOps)
	}

	// Verify targets
	if stats.TotalTargetsAttempted != 10 { // 3 + 5 + 2
		t.Errorf("Expected 10 total targets, got %d", stats.TotalTargetsAttempted)
	}
	if stats.TotalTargetsSucceeded != 6 { // 3 + 3
		t.Errorf("Expected 6 successful targets, got %d", stats.TotalTargetsSucceeded)
	}
	if stats.TotalTargetsFailed != 4 { // 2 + 2
		t.Errorf("Expected 4 failed targets, got %d", stats.TotalTargetsFailed)
	}

	// Verify averages
	expectedAvgTargets := 10.0 / 3.0
	if stats.AvgTargetsPerOp != expectedAvgTargets {
		t.Errorf("Expected avg targets %.2f, got %.2f", expectedAvgTargets, stats.AvgTargetsPerOp)
	}

	// Verify success rates (with tolerance for floating point precision)
	expectedOpSuccessRate := (2.0 / 3.0) * 100.0
	diff := stats.OperationSuccessRate - expectedOpSuccessRate
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.01 {
		t.Errorf("Expected op success rate %.2f, got %.2f", expectedOpSuccessRate, stats.OperationSuccessRate)
	}

	expectedTargetSuccessRate := (6.0 / 10.0) * 100.0
	if stats.TargetSuccessRate != expectedTargetSuccessRate {
		t.Errorf("Expected target success rate %.2f, got %.2f", expectedTargetSuccessRate, stats.TargetSuccessRate)
	}

	// Verify duration stats
	if stats.MinDurationMs <= 0 {
		t.Error("Expected MinDurationMs > 0")
	}
	if stats.MaxDurationMs <= 0 {
		t.Error("Expected MaxDurationMs > 0")
	}
	if stats.AvgDurationMs <= 0 {
		t.Error("Expected AvgDurationMs > 0")
	}

	// Verify timestamps
	if stats.FirstOperationAt == nil {
		t.Error("Expected FirstOperationAt to be set")
	}
	if stats.LastOperationAt == nil {
		t.Error("Expected LastOperationAt to be set")
	}
}

// Test: Reset
func TestReset(t *testing.T) {
	collector := NewParallelPublishStatsCollector(100)

	// Record some data
	result := &infraPublishing.ParallelPublishResult{
		TotalTargets: 3,
		SuccessCount: 3,
		FailureCount: 0,
		SkippedCount: 0,
		Duration:     100 * time.Millisecond,
	}
	collector.RecordPublish(result)

	// Verify data exists
	stats := collector.GetStats()
	if stats.TotalOperations == 0 {
		t.Fatal("Expected operations > 0 before reset")
	}

	// Reset
	collector.Reset()

	// Verify cleared
	stats = collector.GetStats()
	if stats.TotalOperations != 0 {
		t.Errorf("Expected 0 operations after reset, got %d", stats.TotalOperations)
	}
	if stats.SuccessfulOperations != 0 {
		t.Errorf("Expected 0 successful operations after reset, got %d", stats.SuccessfulOperations)
	}
	if len(collector.durationSamples) != 0 {
		t.Errorf("Expected empty duration samples after reset, got %d", len(collector.durationSamples))
	}
	if stats.FirstOperationAt != nil {
		t.Error("Expected nil FirstOperationAt after reset")
	}
}

// Test: Nil result handling
func TestRecordPublish_NilResult(t *testing.T) {
	collector := NewParallelPublishStatsCollector(100)

	// Record nil result (should not panic)
	collector.RecordPublish(nil)

	// Verify no data recorded
	stats := collector.GetStats()
	if stats.TotalOperations != 0 {
		t.Errorf("Expected 0 operations, got %d", stats.TotalOperations)
	}
}

// Test: Duration samples circular buffer
func TestDurationSamplesCircularBuffer(t *testing.T) {
	maxSamples := 5
	collector := NewParallelPublishStatsCollector(maxSamples)

	// Record 10 results (more than maxSamples)
	for i := 0; i < 10; i++ {
		result := &infraPublishing.ParallelPublishResult{
			TotalTargets: 1,
			SuccessCount: 1,
			FailureCount: 0,
			SkippedCount: 0,
			Duration:     time.Duration(i+1) * time.Millisecond,
		}
		collector.RecordPublish(result)
	}

	// Verify samples count capped at maxSamples
	if len(collector.durationSamples) != maxSamples {
		t.Errorf("Expected %d samples, got %d", maxSamples, len(collector.durationSamples))
	}

	// Verify stats still accurate
	stats := collector.GetStats()
	if stats.TotalOperations != 10 {
		t.Errorf("Expected 10 operations, got %d", stats.TotalOperations)
	}
}
