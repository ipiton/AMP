package publishing

import (
	"context"
	"testing"
)

// TestMetricsCollector_Interface verifies all collectors implement MetricsCollector interface
func TestMetricsCollector_Interface(t *testing.T) {
	var _ MetricsCollector = &HealthMetricsCollector{}
	var _ MetricsCollector = &RefreshMetricsCollector{}
	var _ MetricsCollector = &DiscoveryMetricsCollector{}
	var _ MetricsCollector = &QueueMetricsCollector{}
}

// TestPublishingMetricsCollector_Basic tests basic aggregator functionality
func TestPublishingMetricsCollector_Basic(t *testing.T) {
	collector := NewPublishingMetricsCollector()

	if collector == nil {
		t.Fatal("NewPublishingMetricsCollector() returned nil")
	}

	count := collector.CollectorCount()
	if count != 0 {
		t.Errorf("Expected 0 collectors, got %d", count)
	}

	// Test empty CollectAll
	snapshot := collector.CollectAll(context.Background())
	if snapshot == nil {
		t.Fatal("CollectAll() returned nil")
	}

	if len(snapshot.Metrics) != 0 {
		t.Errorf("Expected 0 metrics, got %d", len(snapshot.Metrics))
	}
}
