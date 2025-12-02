package publishing

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestIntegration_EndToEnd_MetricsCollection tests the complete flow:
// Collectors -> Aggregator -> MetricsSnapshot
func TestIntegration_EndToEnd_MetricsCollection(t *testing.T) {
	// 1. Create mock collectors
	healthCollector := &IntegrationMockCollector{
		name: "HealthCollector",
		metrics: map[string]float64{
			"health_status{target=\"test-target\"}":       1.0,
			"health_success_rate{target=\"test-target\"}":  95.5,
		},
	}

	queueCollector := &IntegrationMockCollector{
		name: "QueueCollector",
		metrics: map[string]float64{
			"queue_size":            50,
			"queue_capacity":        1000,
			"queue_jobs_submitted":  10000,
			"queue_jobs_completed":  9500,
			"queue_jobs_failed":     500,
		},
	}

	// 2. Create aggregator and register collectors
	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(healthCollector)
	aggregator.RegisterCollector(queueCollector)

	// 3. Collect metrics
	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// 4. Validate snapshot
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if len(snapshot.AvailableCollectors) != 2 {
		t.Errorf("Expected 2 collectors, got %d", len(snapshot.AvailableCollectors))
	}

	if len(snapshot.Metrics) != 7 {
		t.Errorf("Expected 7 metrics, got %d", len(snapshot.Metrics))
	}

	// Verify health metrics
	if snapshot.Metrics["health_status{target=\"test-target\"}"] != 1.0 {
		t.Error("Missing or incorrect health_status metric")
	}

	if snapshot.Metrics["health_success_rate{target=\"test-target\"}"] != 95.5 {
		t.Error("Missing or incorrect health_success_rate metric")
	}

	// Verify queue metrics
	if snapshot.Metrics["queue_size"] != 50 {
		t.Error("Missing or incorrect queue_size metric")
	}

	if snapshot.Metrics["queue_capacity"] != 1000 {
		t.Error("Missing or incorrect queue_capacity metric")
	}
}

// TestIntegration_ConcurrentCollections tests concurrent metric collection
// under load (simulates production environment)
func TestIntegration_ConcurrentCollections(t *testing.T) {
	// Create slow collector to simulate real-world delays
	slowCollector := &SlowCollector{
		delay: 50 * time.Millisecond,
		metrics: map[string]float64{
			"slow_metric": 42.0,
		},
	}

	fastCollector := &IntegrationMockCollector{
		name: "FastCollector",
		metrics: map[string]float64{
			"fast_metric": 99.9,
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(slowCollector)
	aggregator.RegisterCollector(fastCollector)

	// Run 10 concurrent collections
	const concurrency = 10
	var wg sync.WaitGroup
	results := make(chan *MetricsSnapshot, concurrency)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			snapshot := aggregator.CollectAll(ctx)
			results <- snapshot
		}()
	}

	wg.Wait()
	close(results)
	elapsed := time.Since(start)

	// Validate all collections succeeded
	count := 0
	for snapshot := range results {
		count++
		if len(snapshot.Metrics) != 2 {
			t.Errorf("Expected 2 metrics, got %d", len(snapshot.Metrics))
		}
		if len(snapshot.AvailableCollectors) != 2 {
			t.Errorf("Expected 2 collectors, got %d", len(snapshot.AvailableCollectors))
		}
	}

	if count != concurrency {
		t.Errorf("Expected %d results, got %d", concurrency, count)
	}

	// Each collection should take ~50ms (slow collector delay)
	// With concurrency=10, total should be ~50-100ms (not 500ms)
	maxExpected := 150 * time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("Concurrent collections too slow: %v (expected <%v)", elapsed, maxExpected)
	}

	t.Logf("Concurrent collections completed in %v (10 collections)", elapsed)
}

// TestIntegration_CollectorFailureHandling tests graceful handling
// of collector failures (fail-safe design)
func TestIntegration_CollectorFailureHandling(t *testing.T) {
	// Create failing collector
	failingCollector := &FailingCollector{
		err: context.DeadlineExceeded,
	}

	// Create working collector
	workingCollector := &IntegrationMockCollector{
		name: "WorkingCollector",
		metrics: map[string]float64{
			"working_metric": 100.0,
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(failingCollector)
	aggregator.RegisterCollector(workingCollector)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Should return partial results (working collector only)
	if len(snapshot.Metrics) != 1 {
		t.Errorf("Expected 1 metric from working collector, got %d", len(snapshot.Metrics))
	}

	if snapshot.Metrics["working_metric"] != 100.0 {
		t.Errorf("Expected working_metric=100.0, got %v", snapshot.Metrics["working_metric"])
	}

	// Both collectors are "available" (IsAvailable()=true), but one failed
	if len(snapshot.AvailableCollectors) != 2 {
		t.Errorf("Expected 2 available collectors (1 working, 1 failing), got %d", len(snapshot.AvailableCollectors))
	}

	// Should have 1 error recorded
	if len(snapshot.Errors) != 1 {
		t.Errorf("Expected 1 error from failing collector, got %d", len(snapshot.Errors))
	}
}

// TestIntegration_ContextCancellation tests context cancellation handling
func TestIntegration_ContextCancellation(t *testing.T) {
	// Create slow collector
	slowCollector := &SlowCollector{
		delay: 5 * time.Second, // Very slow
		metrics: map[string]float64{
			"slow_metric": 1.0,
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(slowCollector)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	snapshot := aggregator.CollectAll(ctx)
	elapsed := time.Since(start)

	// Should return quickly (not wait 5 seconds)
	if elapsed > 200*time.Millisecond {
		t.Errorf("Collection did not respect context timeout: %v", elapsed)
	}

	// Should return partial/empty results
	if len(snapshot.Metrics) > 0 {
		t.Logf("Got partial metrics: %v", snapshot.Metrics)
	}

	t.Logf("Context cancellation handled in %v", elapsed)
}

// TestIntegration_MetricsOverTime tests time-series storage
func TestIntegration_MetricsOverTime(t *testing.T) {
	// Create storage
	storage := NewTimeSeriesStorage(1 * time.Hour)

	// Record snapshots over time with increasing queue size
	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"queue_size":           float64(100 + i*10), // Growing: 100 -> 190
				"queue_jobs_submitted": float64(1000 + i*50),
				"queue_jobs_completed": float64(950 + i*45),
			},
			CollectionDuration:  time.Millisecond,
			AvailableCollectors: []string{"QueueCollector"},
		}
		storage.Record(snapshot)
	}

	// Verify storage
	if storage.Size() != 10 {
		t.Errorf("Expected 10 snapshots, got %d", storage.Size())
	}

	// Get recent data (last 5 minutes)
	startTime := baseTime.Add(5 * time.Minute)
	endTime := baseTime.Add(10 * time.Minute)
	recent := storage.GetRange(startTime, endTime)
	if len(recent) != 5 {
		t.Errorf("Expected 5 recent snapshots, got %d", len(recent))
	}

	// Verify metrics are increasing
	first := recent[0].Metrics["queue_size"]
	last := recent[len(recent)-1].Metrics["queue_size"]
	if last <= first {
		t.Errorf("Expected queue_size to grow, first=%v last=%v", first, last)
	}

	t.Logf("Queue size grew from %.0f to %.0f over 5 minutes", first, last)
}

// TestIntegration_MultiCollectorAggregation tests aggregation from 4 different collectors
func TestIntegration_MultiCollectorAggregation(t *testing.T) {
	// Setup all 4 collector types
	healthCollector := &IntegrationMockCollector{
		name: "HealthCollector",
		metrics: map[string]float64{
			"health_status{target=\"rootly-prod\"}":       1.0,
			"health_success_rate{target=\"rootly-prod\"}":  99.5,
		},
	}

	refreshCollector := &IntegrationMockCollector{
		name: "RefreshCollector",
		metrics: map[string]float64{
			"refresh_last_timestamp":       float64(time.Now().Unix()),
			"refresh_targets_discovered":   10,
		},
	}

	discoveryCollector := &IntegrationMockCollector{
		name: "DiscoveryCollector",
		metrics: map[string]float64{
			"discovery_total_targets": 10,
			"discovery_latency_ms":    50,
		},
	}

	queueCollector := &IntegrationMockCollector{
		name: "QueueCollector",
		metrics: map[string]float64{
			"queue_size":     15,
			"queue_capacity": 1000,
		},
	}

	// Aggregate all collectors
	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(healthCollector)
	aggregator.RegisterCollector(refreshCollector)
	aggregator.RegisterCollector(discoveryCollector)
	aggregator.RegisterCollector(queueCollector)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Validate aggregation
	if len(snapshot.AvailableCollectors) != 4 {
		t.Errorf("Expected 4 collectors, got %d", len(snapshot.AvailableCollectors))
	}

	expectedMetrics := 2 + 2 + 2 + 2 // 8 total metrics
	if len(snapshot.Metrics) != expectedMetrics {
		t.Errorf("Expected %d metrics, got %d", expectedMetrics, len(snapshot.Metrics))
	}

	// Verify metrics from each collector
	if _, ok := snapshot.Metrics["health_status{target=\"rootly-prod\"}"]; !ok {
		t.Error("Missing health metric")
	}
	if _, ok := snapshot.Metrics["refresh_last_timestamp"]; !ok {
		t.Error("Missing refresh metric")
	}
	if _, ok := snapshot.Metrics["discovery_total_targets"]; !ok {
		t.Error("Missing discovery metric")
	}
	if _, ok := snapshot.Metrics["queue_size"]; !ok {
		t.Error("Missing queue metric")
	}

	t.Logf("Successfully aggregated %d metrics from %d collectors", len(snapshot.Metrics), len(snapshot.AvailableCollectors))
}

// Helper: SlowCollector simulates slow metric collection
type SlowCollector struct {
	delay   time.Duration
	metrics map[string]float64
}

func (c *SlowCollector) Collect(ctx context.Context) (map[string]float64, error) {
	select {
	case <-time.After(c.delay):
		return c.metrics, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *SlowCollector) Name() string {
	return "SlowCollector"
}

func (c *SlowCollector) IsAvailable() bool {
	return true
}

// Helper: FailingCollector simulates collector failures
type FailingCollector struct {
	err error
}

func (c *FailingCollector) Collect(ctx context.Context) (map[string]float64, error) {
	return nil, c.err
}

func (c *FailingCollector) Name() string {
	return "FailingCollector"
}

func (c *FailingCollector) IsAvailable() bool {
	return true
}

// Helper: IntegrationMockCollector is a simple mock for integration tests
type IntegrationMockCollector struct {
	name    string
	metrics map[string]float64
}

func (c *IntegrationMockCollector) Collect(ctx context.Context) (map[string]float64, error) {
	return c.metrics, nil
}

func (c *IntegrationMockCollector) Name() string {
	return c.name
}

func (c *IntegrationMockCollector) IsAvailable() bool {
	return true
}
