package publishing

import (
	"sync"
	"time"

	infraPublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// ParallelPublishStats represents statistics for parallel publishing operations.
//
// This structure tracks:
//   - Total parallel publish operations
//   - Success/failure/partial success counts
//   - Average targets per operation
//   - Success rate statistics
//   - Duration statistics
type ParallelPublishStats struct {
	// Counts
	TotalOperations       int64   `json:"total_operations"`        // Total parallel publish operations
	SuccessfulOperations  int64   `json:"successful_operations"`   // Operations with at least one success
	FailedOperations      int64   `json:"failed_operations"`       // Operations with all failures
	PartialSuccessOps     int64   `json:"partial_success_ops"`     // Operations with partial success

	// Targets
	TotalTargetsAttempted int64   `json:"total_targets_attempted"` // Total targets attempted across all operations
	TotalTargetsSucceeded int64   `json:"total_targets_succeeded"` // Total targets succeeded
	TotalTargetsFailed    int64   `json:"total_targets_failed"`    // Total targets failed
	TotalTargetsSkipped   int64   `json:"total_targets_skipped"`   // Total targets skipped
	AvgTargetsPerOp       float64 `json:"avg_targets_per_op"`      // Average targets per operation

	// Success Rates
	OperationSuccessRate  float64 `json:"operation_success_rate"`  // % of operations with at least one success
	TargetSuccessRate     float64 `json:"target_success_rate"`     // % of targets that succeeded

	// Duration Statistics
	AvgDurationMs         float64 `json:"avg_duration_ms"`         // Average duration per operation (ms)
	MinDurationMs         float64 `json:"min_duration_ms"`         // Minimum duration (ms)
	MaxDurationMs         float64 `json:"max_duration_ms"`         // Maximum duration (ms)
	P50DurationMs         float64 `json:"p50_duration_ms"`         // 50th percentile (median)
	P95DurationMs         float64 `json:"p95_duration_ms"`         // 95th percentile
	P99DurationMs         float64 `json:"p99_duration_ms"`         // 99th percentile

	// Time Range
	FirstOperationAt      *time.Time `json:"first_operation_at,omitempty"`  // First operation timestamp
	LastOperationAt       *time.Time `json:"last_operation_at,omitempty"`   // Last operation timestamp
}

// ParallelPublishStatsCollector collects statistics for parallel publishing operations.
//
// This collector is thread-safe and can be called concurrently from multiple goroutines.
// It maintains:
//   - Aggregate counters (thread-safe with mutex)
//   - Duration samples (for percentile calculation)
//   - Time series data (optional, if TimeSeriesStorage is provided)
type ParallelPublishStatsCollector struct {
	mu               sync.RWMutex
	totalOps         int64
	successfulOps    int64
	failedOps        int64
	partialOps       int64
	totalTargets     int64
	successTargets   int64
	failedTargets    int64
	skippedTargets   int64
	durationSamples  []float64 // Store duration samples for percentile calculation
	maxSamples       int       // Maximum samples to keep (for memory bounds)
	firstOpAt        *time.Time
	lastOpAt         *time.Time
}

// NewParallelPublishStatsCollector creates a new parallel publish stats collector.
//
// Parameters:
//   - maxSamples: Maximum duration samples to keep (default: 10000)
//
// Returns:
//   - *ParallelPublishStatsCollector: New collector instance
func NewParallelPublishStatsCollector(maxSamples int) *ParallelPublishStatsCollector {
	if maxSamples <= 0 {
		maxSamples = 10000 // Default: 10k samples
	}

	return &ParallelPublishStatsCollector{
		maxSamples:      maxSamples,
		durationSamples: make([]float64, 0, maxSamples),
	}
}

// RecordPublish records a parallel publish operation result.
//
// This method:
//   - Increments counters based on result
//   - Records duration sample
//   - Updates timestamps
//   - Thread-safe (uses mutex)
//
// Parameters:
//   - result: Parallel publish result to record
func (c *ParallelPublishStatsCollector) RecordPublish(result *infraPublishing.ParallelPublishResult) {
	if result == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update operation counts
	c.totalOps++
	if result.AllSucceeded() {
		c.successfulOps++
	} else if result.AllFailed() {
		c.failedOps++
	} else if result.IsPartialSuccess {
		c.partialOps++
		c.successfulOps++ // Partial success counts as success (at least one succeeded)
	}

	// Update target counts
	c.totalTargets += int64(result.TotalTargets)
	c.successTargets += int64(result.SuccessCount)
	c.failedTargets += int64(result.FailureCount)
	c.skippedTargets += int64(result.SkippedCount)

	// Update duration samples
	durationMs := float64(result.Duration.Microseconds()) / 1000.0
	if len(c.durationSamples) < c.maxSamples {
		c.durationSamples = append(c.durationSamples, durationMs)
	} else {
		// Circular buffer: Overwrite oldest sample
		idx := int(c.totalOps-1) % c.maxSamples
		c.durationSamples[idx] = durationMs
	}

	// Update timestamps
	now := time.Now()
	if c.firstOpAt == nil {
		c.firstOpAt = &now
	}
	c.lastOpAt = &now
}

// GetStats returns current parallel publish statistics.
//
// This method:
//   - Calculates aggregate statistics
//   - Computes percentiles from duration samples
//   - Thread-safe (uses read lock)
//
// Returns:
//   - ParallelPublishStats: Current statistics
func (c *ParallelPublishStatsCollector) GetStats() ParallelPublishStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := ParallelPublishStats{
		TotalOperations:       c.totalOps,
		SuccessfulOperations:  c.successfulOps,
		FailedOperations:      c.failedOps,
		PartialSuccessOps:     c.partialOps,
		TotalTargetsAttempted: c.totalTargets,
		TotalTargetsSucceeded: c.successTargets,
		TotalTargetsFailed:    c.failedTargets,
		TotalTargetsSkipped:   c.skippedTargets,
		FirstOperationAt:      c.firstOpAt,
		LastOperationAt:       c.lastOpAt,
	}

	// Calculate average targets per operation
	if c.totalOps > 0 {
		stats.AvgTargetsPerOp = float64(c.totalTargets) / float64(c.totalOps)
	}

	// Calculate success rates
	if c.totalOps > 0 {
		stats.OperationSuccessRate = (float64(c.successfulOps) / float64(c.totalOps)) * 100.0
	}
	if c.totalTargets > 0 {
		stats.TargetSuccessRate = (float64(c.successTargets) / float64(c.totalTargets)) * 100.0
	}

	// Calculate duration statistics
	if len(c.durationSamples) > 0 {
		stats.AvgDurationMs = c.calculateAvg()
		stats.MinDurationMs = c.calculateMin()
		stats.MaxDurationMs = c.calculateMax()
		stats.P50DurationMs = c.calculatePercentile(0.50)
		stats.P95DurationMs = c.calculatePercentile(0.95)
		stats.P99DurationMs = c.calculatePercentile(0.99)
	}

	return stats
}

// Reset resets all statistics.
//
// This method clears:
//   - All counters
//   - Duration samples
//   - Timestamps
//   - Thread-safe (uses write lock)
func (c *ParallelPublishStatsCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalOps = 0
	c.successfulOps = 0
	c.failedOps = 0
	c.partialOps = 0
	c.totalTargets = 0
	c.successTargets = 0
	c.failedTargets = 0
	c.skippedTargets = 0
	c.durationSamples = make([]float64, 0, c.maxSamples)
	c.firstOpAt = nil
	c.lastOpAt = nil
}

// calculateAvg calculates the average of duration samples.
// Assumes lock is already held.
func (c *ParallelPublishStatsCollector) calculateAvg() float64 {
	if len(c.durationSamples) == 0 {
		return 0
	}

	sum := 0.0
	for _, d := range c.durationSamples {
		sum += d
	}
	return sum / float64(len(c.durationSamples))
}

// calculateMin calculates the minimum of duration samples.
// Assumes lock is already held.
func (c *ParallelPublishStatsCollector) calculateMin() float64 {
	if len(c.durationSamples) == 0 {
		return 0
	}

	min := c.durationSamples[0]
	for _, d := range c.durationSamples[1:] {
		if d < min {
			min = d
		}
	}
	return min
}

// calculateMax calculates the maximum of duration samples.
// Assumes lock is already held.
func (c *ParallelPublishStatsCollector) calculateMax() float64 {
	if len(c.durationSamples) == 0 {
		return 0
	}

	max := c.durationSamples[0]
	for _, d := range c.durationSamples[1:] {
		if d > max {
			max = d
		}
	}
	return max
}

// calculatePercentile calculates the percentile of duration samples.
// Assumes lock is already held.
//
// Note: This is a simple implementation using linear interpolation.
// For production use, consider using a more sophisticated algorithm
// or a library like go-percentile.
func (c *ParallelPublishStatsCollector) calculatePercentile(p float64) float64 {
	if len(c.durationSamples) == 0 {
		return 0
	}

	// Create a sorted copy (don't modify original)
	sorted := make([]float64, len(c.durationSamples))
	copy(sorted, c.durationSamples)

	// Simple bubble sort (good enough for small samples)
	// For production with large samples, use sort.Float64s()
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Calculate index
	idx := p * float64(len(sorted)-1)
	lower := int(idx)
	upper := lower + 1

	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}

	// Linear interpolation
	weight := idx - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
