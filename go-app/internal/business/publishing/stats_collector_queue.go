package publishing

import (
	"context"
	"fmt"

	"github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// QueueMetricsCollector collects metrics from TN-056 Publishing Queue.
//
// This collector uses the PublishingQueue's GetStats() method to efficiently
// retrieve current queue statistics without scraping Prometheus metrics.
//
// Metrics collected (from QueueStats struct):
//   - queue_size_total (current total queue depth)
//   - queue_size_high_priority (high priority queue depth)
//   - queue_size_medium_priority (medium priority queue depth)
//   - queue_size_low_priority (low priority queue depth)
//   - queue_capacity (maximum queue capacity)
//   - queue_utilization (TotalSize / Capacity ratio, 0-1)
//   - worker_count (total workers)
//   - active_jobs (jobs currently being processed)
//   - jobs_submitted_total (cumulative submission count)
//   - jobs_completed_total (cumulative completion count)
//   - jobs_failed_total (cumulative failure count)
//
// Note: Some advanced metrics (retry stats, circuit breaker states, DLQ size)
// are only available via Prometheus scraping. This collector focuses on
// operational stats via direct GetStats() access for <10µs performance.
//
// Performance: <10µs (direct struct access via GetStats)
//
// Thread-Safe: Yes (PublishingQueue.GetStats is thread-safe)
//
// Example:
//
//	collector := NewQueueMetricsCollector(publishingQueue)
//	metrics, err := collector.Collect(ctx)
//	if err == nil {
//	    fmt.Printf("Queue size: %v\n", metrics["queue_size_total"])
//	}
type QueueMetricsCollector struct {
	queue *publishing.PublishingQueue // PublishingQueue instance (from TN-056)
}

// NewQueueMetricsCollector creates QueueMetricsCollector.
//
// Parameters:
//   - queue: PublishingQueue instance (from TN-056)
//
// Returns:
//   - *QueueMetricsCollector: Collector instance
//
// Example:
//
//	collector := NewQueueMetricsCollector(publishingQueue)
func NewQueueMetricsCollector(queue *publishing.PublishingQueue) *QueueMetricsCollector {
	return &QueueMetricsCollector{queue: queue}
}

// Collect reads queue metrics from PublishingQueue.
//
// This method uses PublishingQueue.GetStats() to get current queue state,
// then converts to map[string]float64 format for aggregation.
//
// Algorithm:
//  1. Check if queue is available (nil check)
//  2. Call queue.GetStats() (returns QueueStats)
//  3. Calculate derived metrics (utilization = size / capacity)
//  4. Convert to metric map
//
// Performance: <10µs (GetStats returns cached stats)
//
// Example:
//
//	metrics, err := collector.Collect(ctx)
//	// metrics = {
//	//   "queue_size_total": 42.0,
//	//   "queue_size_high_priority": 15.0,
//	//   "queue_size_medium_priority": 20.0,
//	//   "queue_size_low_priority": 7.0,
//	//   "queue_capacity": 1000.0,
//	//   "queue_utilization": 0.042,  // 42/1000
//	//   "worker_count": 10.0,
//	//   "active_jobs": 5.0,
//	//   "jobs_submitted_total": 12345.0,
//	//   "jobs_completed_total": 12200.0,
//	//   "jobs_failed_total": 100.0,
//	// }
func (c *QueueMetricsCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if c.queue == nil {
		return nil, fmt.Errorf("publishing queue not initialized")
	}

	// Get queue statistics (direct cache access)
	stats := c.queue.GetStats()

	// Initialize metric map (pre-allocate for ~12 metrics)
	metrics := make(map[string]float64, 12)

	// Queue size metrics
	metrics["queue_size_total"] = float64(stats.TotalSize)
	metrics["queue_size_high_priority"] = float64(stats.HighPriority)
	metrics["queue_size_medium_priority"] = float64(stats.MedPriority)
	metrics["queue_size_low_priority"] = float64(stats.LowPriority)

	// Capacity and utilization
	metrics["queue_capacity"] = float64(stats.Capacity)
	if stats.Capacity > 0 {
		utilization := float64(stats.TotalSize) / float64(stats.Capacity)
		metrics["queue_utilization"] = utilization
	} else {
		metrics["queue_utilization"] = 0.0
	}

	// Worker pool metrics
	metrics["worker_count"] = float64(stats.WorkerCount)
	metrics["active_jobs"] = float64(stats.ActiveJobs)

	// Worker utilization (active jobs / worker count)
	if stats.WorkerCount > 0 {
		workerUtilization := float64(stats.ActiveJobs) / float64(stats.WorkerCount)
		metrics["worker_utilization"] = workerUtilization
	} else {
		metrics["worker_utilization"] = 0.0
	}

	// Job processing metrics (cumulative counters)
	metrics["jobs_submitted_total"] = float64(stats.TotalSubmitted)
	metrics["jobs_completed_total"] = float64(stats.TotalCompleted)
	metrics["jobs_failed_total"] = float64(stats.TotalFailed)

	// Derived success rate (completed / submitted)
	if stats.TotalSubmitted > 0 {
		successRate := float64(stats.TotalCompleted) / float64(stats.TotalSubmitted)
		metrics["job_success_rate"] = successRate
	} else {
		metrics["job_success_rate"] = 1.0 // No jobs = 100% success (neutral)
	}

	// Note: Advanced metrics (retry stats, circuit breaker, DLQ) are tracked
	// in Prometheus and would require Gatherer scraping. We focus on operational
	// stats here for optimal performance (<10µs).

	return metrics, nil
}

// Name returns "queue" (for debugging).
func (c *QueueMetricsCollector) Name() string {
	return "queue"
}

// IsAvailable returns true if queue initialized.
func (c *QueueMetricsCollector) IsAvailable() bool {
	return c.queue != nil
}
