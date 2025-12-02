package publishing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricsSnapshot represents a point-in-time snapshot of all metrics.
//
// This struct holds raw metric values collected from all subsystems at a specific
// timestamp. It's used as input for statistics aggregation.
//
// Performance:
//   - Memory: ~5KB per snapshot (50 metrics * 100 bytes avg)
//   - Serialization: <1ms JSON encoding
//
// Thread-Safe: Yes (read-only after creation)
//
// Example:
//
//	snapshot := collector.CollectAll()
//	fmt.Printf("Collected %d metrics at %v\n", len(snapshot.Metrics), snapshot.Timestamp)
type MetricsSnapshot struct {
	// Timestamp when metrics were collected
	Timestamp time.Time

	// Metrics is a map of metric_name -> value
	// Example: {"health_checks_total{target=\"rootly-prod\",status=\"success\"}": 1234.0}
	Metrics map[string]float64

	// CollectionDuration is how long collection took
	CollectionDuration time.Duration

	// AvailableCollectors tracks which collectors were available (non-nil)
	AvailableCollectors []string

	// Errors tracks collection errors by collector name
	Errors map[string]error
}

// MetricsCollector defines interface for collecting metrics from a subsystem.
//
// Each subsystem (Health, Refresh, Discovery, Queue, Publishers) implements
// this interface to provide uniform access to Prometheus metrics.
//
// Design Pattern: Strategy Pattern (interchangeable collectors)
//
// Performance Target: <10µs per collector
//
// Thread-Safe: Yes (implementations must be thread-safe)
//
// Example:
//
//	collector := NewHealthMetricsCollector(healthMetrics)
//	snapshot, err := collector.Collect(ctx)
//	if err == nil {
//	    fmt.Printf("Collected %d metrics from %s\n", len(snapshot), collector.Name())
//	}
type MetricsCollector interface {
	// Collect returns current metrics snapshot.
	//
	// Returns:
	//   - map[string]float64: Metric name → value pairs
	//   - error: If collection failed (should be rare)
	//
	// Performance: <10µs target
	//
	// Thread-Safe: Yes
	Collect(ctx context.Context) (map[string]float64, error)

	// Name returns collector name (for debugging).
	//
	// Examples: "health", "refresh", "discovery", "queue", "rootly"
	Name() string

	// IsAvailable returns true if subsystem metrics initialized.
	//
	// This allows graceful handling of optional subsystems (e.g., queue may not be deployed yet).
	IsAvailable() bool
}

// PublishingMetricsCollector aggregates all subsystem collectors.
//
// This struct manages multiple MetricsCollector instances and provides
// a single entry point for collecting all publishing system metrics.
//
// Performance:
//   - CollectAll(): <100µs (parallel collection with WaitGroup)
//   - Memory: ~100 bytes overhead (pointers to collectors)
//
// Thread-Safe: Yes (concurrent CollectAll() calls supported)
//
// Example:
//
//	collector := NewPublishingMetricsCollector()
//	collector.RegisterCollector(NewHealthMetricsCollector(healthMetrics))
//	collector.RegisterCollector(NewRefreshMetricsCollector(refreshMetrics))
//	snapshot := collector.CollectAll(ctx) // <100µs
type PublishingMetricsCollector struct {
	// collectors holds all registered MetricsCollector instances
	collectors []MetricsCollector

	// mu protects collectors slice during registration
	mu sync.RWMutex

	// collectionDuration tracks collection time (self-monitoring)
	collectionDuration prometheus.Histogram
}

// NewPublishingMetricsCollector creates a new aggregator.
//
// Returns:
//   - *PublishingMetricsCollector: Empty collector (no collectors registered yet)
//
// Example:
//
//	collector := NewPublishingMetricsCollector()
func NewPublishingMetricsCollector() *PublishingMetricsCollector {
	return &PublishingMetricsCollector{
		collectors: make([]MetricsCollector, 0, 10), // Pre-allocate for 10 collectors
		collectionDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing_stats",
				Name:      "collection_duration_seconds",
				Help:      "Publishing metrics collection duration",
				Buckets:   []float64{0.00001, 0.00005, 0.0001, 0.0005, 0.001, 0.005, 0.01}, // 10µs to 10ms
			},
		),
	}
}

// RegisterCollector adds a collector to the aggregator.
//
// Parameters:
//   - collector: MetricsCollector instance to register
//
// Thread-Safe: Yes (uses mutex)
//
// Example:
//
//	collector.RegisterCollector(NewHealthMetricsCollector(healthMetrics))
func (c *PublishingMetricsCollector) RegisterCollector(collector MetricsCollector) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.collectors = append(c.collectors, collector)
}

// CollectAll collects metrics from all registered collectors.
//
// This method:
//   1. Creates context with 5s timeout (safety net)
//   2. Concurrently collects from all collectors (sync.WaitGroup)
//   3. Handles nil collectors gracefully (skip if not available)
//   4. Aggregates results into single MetricsSnapshot
//   5. Records collection duration
//
// Performance:
//   - Target: <100µs (with 10 collectors at 10µs each)
//   - Actual: max(collector_durations) due to parallelization
//
// Thread-Safe: Yes (multiple goroutines can call concurrently)
//
// Example:
//
//	snapshot := collector.CollectAll(ctx)
//	fmt.Printf("Collected %d metrics in %v\n", len(snapshot.Metrics), snapshot.CollectionDuration)
func (c *PublishingMetricsCollector) CollectAll(ctx context.Context) *MetricsSnapshot {
	startTime := time.Now()

	// Create timeout context (safety net)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Prepare result
	snapshot := &MetricsSnapshot{
		Timestamp:           startTime,
		Metrics:             make(map[string]float64, 100), // Pre-allocate for ~100 metrics
		AvailableCollectors: make([]string, 0, 10),
		Errors:              make(map[string]error),
	}

	// Concurrent collection
	c.mu.RLock()
	collectors := c.collectors // Snapshot collectors list
	c.mu.RUnlock()

	var (
		mu sync.Mutex     // Protects ALL snapshot fields (thread-safe)
		wg sync.WaitGroup // Waits for all collectors
	)

	for _, collector := range collectors {
		if !collector.IsAvailable() {
			continue // Skip uninitialized subsystems
		}

		wg.Add(1)
		go func(coll MetricsCollector) {
			defer wg.Done()

			// Collect metrics from this collector
			metrics, err := coll.Collect(ctx)

			// CRITICAL: Lock BEFORE any writes to snapshot
			mu.Lock()
			defer mu.Unlock()

			// Register collector as available (inside lock!)
			snapshot.AvailableCollectors = append(snapshot.AvailableCollectors, coll.Name())

			if err != nil {
				snapshot.Errors[coll.Name()] = err
				return
			}

			// Merge metrics into snapshot (inside lock)
			for name, value := range metrics {
				snapshot.Metrics[name] = value
			}
		}(collector)
	}

	// Wait for all collectors to finish (with timeout safety)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All collectors finished
	case <-ctx.Done():
		// Timeout reached (should be rare)
		// CRITICAL: Protect timeout error write with mutex
		mu.Lock()
		snapshot.Errors["timeout"] = fmt.Errorf("collection timeout after 5s")
		mu.Unlock()
	}

	// Record duration
	snapshot.CollectionDuration = time.Since(startTime)
	c.collectionDuration.Observe(snapshot.CollectionDuration.Seconds())

	return snapshot
}

// GetCollectorNames returns names of all registered collectors.
//
// Returns:
//   - []string: Slice of collector names (e.g., ["health", "refresh", "discovery"])
//
// Thread-Safe: Yes
//
// Example:
//
//	names := collector.GetCollectorNames()
//	fmt.Printf("Registered collectors: %v\n", names)
func (c *PublishingMetricsCollector) GetCollectorNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0, len(c.collectors))
	for _, coll := range c.collectors {
		names = append(names, coll.Name())
	}
	return names
}

// CollectorCount returns number of registered collectors.
//
// Returns:
//   - int: Number of collectors
//
// Thread-Safe: Yes
func (c *PublishingMetricsCollector) CollectorCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.collectors)
}
