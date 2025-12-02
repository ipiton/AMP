package publishing

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

// TestEdgeCase_EmptyCollectors tests behavior with no collectors registered
func TestEdgeCase_EmptyCollectors(t *testing.T) {
	collector := NewPublishingMetricsCollector()

	ctx := context.Background()
	snapshot := collector.CollectAll(ctx)

	// Should return empty snapshot (not nil)
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if len(snapshot.AvailableCollectors) != 0 {
		t.Errorf("Expected 0 collectors, got %d", len(snapshot.AvailableCollectors))
	}

	if len(snapshot.Metrics) != 0 {
		t.Errorf("Expected 0 metrics, got %d", len(snapshot.Metrics))
	}

	if snapshot.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
}

// TestEdgeCase_NilContext tests handling of nil context
func TestEdgeCase_NilContext(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil context, got none")
		}
	}()

	collector := NewPublishingMetricsCollector()
	mockCollector := &NamedCollector{
		name: "TestCollector",
		metrics: map[string]float64{"test": 1.0},
	}
	collector.RegisterCollector(mockCollector)

	// This should panic (context.Background() is used internally)
	collector.CollectAll(nil)
}

// TestEdgeCase_UnavailableCollectors tests collectors with IsAvailable() = false
func TestEdgeCase_UnavailableCollectors(t *testing.T) {
	unavailableCollector := &UnavailableCollector{}

	collector := NewPublishingMetricsCollector()
	collector.RegisterCollector(unavailableCollector)

	ctx := context.Background()
	snapshot := collector.CollectAll(ctx)

	// Should skip unavailable collector
	if len(snapshot.Metrics) != 0 {
		t.Errorf("Expected 0 metrics from unavailable collector, got %d", len(snapshot.Metrics))
	}

	// Collector should not be in available list
	if len(snapshot.AvailableCollectors) != 0 {
		t.Errorf("Expected 0 available collectors (unavailable), got %d", len(snapshot.AvailableCollectors))
	}
}

// TestEdgeCase_DuplicateMetricKeys tests handling of duplicate metric keys
func TestEdgeCase_DuplicateMetricKeys(t *testing.T) {
	collector1 := &NamedCollector{
		name: "Collector1",
		metrics: map[string]float64{
			"duplicate_metric": 100.0,
			"unique1":          1.0,
		},
	}

	collector2 := &NamedCollector{
		name: "Collector2",
		metrics: map[string]float64{
			"duplicate_metric": 200.0, // Same key, different value
			"unique2":          2.0,
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(collector1)
	aggregator.RegisterCollector(collector2)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Should have 3 metrics (duplicate key overwrites)
	if len(snapshot.Metrics) != 3 {
		t.Errorf("Expected 3 metrics, got %d", len(snapshot.Metrics))
	}

	// Last writer wins (collector2)
	if snapshot.Metrics["duplicate_metric"] != 200.0 {
		t.Errorf("Expected duplicate_metric=200.0, got %v", snapshot.Metrics["duplicate_metric"])
	}
}

// TestEdgeCase_VeryLargeMetricValues tests handling of extreme values
func TestEdgeCase_VeryLargeMetricValues(t *testing.T) {
	collector := &NamedCollector{
		name: "ExtremeValues",
		metrics: map[string]float64{
			"very_large":    1e308,          // Near float64 max
			"very_small":    1e-308,         // Near float64 min
			"zero":          0.0,
			"negative_zero": -0.0,
			"infinity":      math.Inf(1),    // Positive infinity
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(collector)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Should handle all values (including infinity)
	if len(snapshot.Metrics) != 5 {
		t.Errorf("Expected 5 metrics, got %d", len(snapshot.Metrics))
	}
}

// TestEdgeCase_SpecialCharactersInMetricNames tests metric names with special chars
func TestEdgeCase_SpecialCharactersInMetricNames(t *testing.T) {
	collector := &NamedCollector{
		name: "SpecialChars",
		metrics: map[string]float64{
			"metric_with_spaces":           1.0,
			"metric-with-dashes":           2.0,
			"metric.with.dots":             3.0,
			"metric{with=\"labels\"}":      4.0,
			"metric/with/slashes":          5.0,
			"metric:with:colons":           6.0,
			"метрика_с_unicode":            7.0, // Cyrillic
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(collector)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Should handle all special characters
	if len(snapshot.Metrics) != 7 {
		t.Errorf("Expected 7 metrics, got %d", len(snapshot.Metrics))
	}

	// Verify specific metric
	if val, ok := snapshot.Metrics["metric{with=\"labels\"}"]; !ok || val != 4.0 {
		t.Errorf("Expected metric with labels to exist with value 4.0, got %v", val)
	}
}

// TestEdgeCase_TimeSeriesStorage_RingBufferWrap tests ring buffer wraparound
func TestEdgeCase_TimeSeriesStorage_RingBufferWrap(t *testing.T) {
	storage := NewTimeSeriesStorage(1 * time.Hour)

	// Record more snapshots than expected capacity
	totalSnapshots := 100
	for i := 0; i < totalSnapshots; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"iteration": float64(i),
			},
			CollectionDuration:  time.Millisecond,
			AvailableCollectors: []string{"TestCollector"},
		}
		storage.Record(snapshot)
	}

	// Get all snapshots
	snapshots := storage.GetAll()
	size := storage.Size()

	// Ring buffer should have retained SOME snapshots (not necessarily all 100)
	if size == 0 {
		t.Error("Ring buffer should retain snapshots")
	}

	if size > totalSnapshots {
		t.Errorf("Size %d exceeds total recorded %d", size, totalSnapshots)
	}

	// Verify snapshots are in order
	if len(snapshots) >= 2 {
		first := snapshots[0].Metrics["iteration"]
		second := snapshots[1].Metrics["iteration"]
		if second <= first {
			t.Errorf("Snapshots not in order: first=%v, second=%v", first, second)
		}
	}

	t.Logf("Ring buffer retained %d of %d snapshots", size, totalSnapshots)
}

// Note: TrendDetector edge cases are covered in trends_detector_test.go

// TestEdgeCase_CollectorName_VeryLong tests handling of very long collector names
func TestEdgeCase_CollectorName_VeryLong(t *testing.T) {
	longName := strings.Repeat("VeryLongCollectorName", 100) // 2000+ chars
	collector := &NamedCollector{
		name: longName,
		metrics: map[string]float64{
			"test": 1.0,
		},
	}

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(collector)

	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)

	// Should handle long names without issues
	if len(snapshot.Metrics) != 1 {
		t.Errorf("Expected 1 metric, got %d", len(snapshot.Metrics))
	}
}

// TestEdgeCase_CollectorRegistration tests collector registration
func TestEdgeCase_CollectorRegistration(t *testing.T) {
	aggregator := NewPublishingMetricsCollector()

	// Initially empty
	ctx := context.Background()
	snapshot := aggregator.CollectAll(ctx)
	if len(snapshot.AvailableCollectors) != 0 {
		t.Errorf("Expected 0 collectors initially, got %d", len(snapshot.AvailableCollectors))
	}

	// Register collector
	collector := &NamedCollector{
		name:    "TestCollector",
		metrics: map[string]float64{"test": 1.0},
	}
	aggregator.RegisterCollector(collector)

	// Should have 1 collector now
	snapshot = aggregator.CollectAll(ctx)
	if len(snapshot.AvailableCollectors) != 1 {
		t.Errorf("Expected 1 collector after registration, got %d", len(snapshot.AvailableCollectors))
	}

	if snapshot.AvailableCollectors[0] != "TestCollector" {
		t.Errorf("Expected collector name 'TestCollector', got %s", snapshot.AvailableCollectors[0])
	}
}

// Helper: UnavailableCollector always returns IsAvailable() = false
type UnavailableCollector struct{}

func (c *UnavailableCollector) Collect(ctx context.Context) (map[string]float64, error) {
	return map[string]float64{"unavailable_metric": 1.0}, nil
}

func (c *UnavailableCollector) Name() string {
	return "UnavailableCollector"
}

func (c *UnavailableCollector) IsAvailable() bool {
	return false
}

// Helper: NamedCollector with custom name
type NamedCollector struct {
	name    string
	metrics map[string]float64
}

func (c *NamedCollector) Collect(ctx context.Context) (map[string]float64, error) {
	return c.metrics, nil
}

func (c *NamedCollector) Name() string {
	return c.name
}

func (c *NamedCollector) IsAvailable() bool {
	return true
}
