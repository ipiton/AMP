package publishing

import (
	"context"
	"testing"
	"time"

	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// TestMetricsCollector_Interface verifies all collectors implement MetricsCollector interface
func TestMetricsCollector_Interface(t *testing.T) {
	var _ MetricsCollector = &HealthMetricsCollector{}
	var _ MetricsCollector = &RefreshMetricsCollector{}
	var _ MetricsCollector = &DiscoveryMetricsCollector{}
	var _ MetricsCollector = &QueueMetricsCollector{}
	var _ MetricsCollector = &ModeMetricsCollector{}
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

func TestModeMetricsCollector_Collect(t *testing.T) {
	t.Parallel()

	collector := NewModeMetricsCollector(modeCollectorStub{
		currentMode: infrapublishing.ModeMetricsOnly,
		metrics: infrapublishing.ModeMetrics{
			CurrentMode:         infrapublishing.ModeMetricsOnly,
			CurrentModeDuration: 42 * time.Second,
			TransitionCount:     3,
		},
	})

	metrics, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if got := metrics["publishing_mode_current"]; got != 1.0 {
		t.Fatalf("publishing_mode_current = %v, want 1", got)
	}
	if got := metrics["publishing_mode_transition_count"]; got != 3 {
		t.Fatalf("publishing_mode_transition_count = %v, want 3", got)
	}
	if got := metrics["publishing_mode_duration_seconds"]; got != 42 {
		t.Fatalf("publishing_mode_duration_seconds = %v, want 42", got)
	}
}

type modeCollectorStub struct {
	currentMode infrapublishing.Mode
	metrics     infrapublishing.ModeMetrics
}

func (m modeCollectorStub) GetCurrentMode() infrapublishing.Mode {
	return m.currentMode
}

func (m modeCollectorStub) IsMetricsOnly() bool {
	return m.currentMode == infrapublishing.ModeMetricsOnly
}

func (m modeCollectorStub) CheckModeTransition() (infrapublishing.Mode, bool, error) {
	return m.currentMode, false, nil
}

func (m modeCollectorStub) OnTargetsChanged() error {
	return nil
}

func (m modeCollectorStub) Subscribe(callback infrapublishing.ModeChangeCallback) infrapublishing.UnsubscribeFunc {
	return func() {}
}

func (m modeCollectorStub) GetModeMetrics() infrapublishing.ModeMetrics {
	return m.metrics
}

func (m modeCollectorStub) Start(ctx context.Context) error {
	return nil
}

func (m modeCollectorStub) Stop() error {
	return nil
}
