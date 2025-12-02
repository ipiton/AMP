package publishing

import (
	"context"
	"testing"
	"time"
)

// ============================================================================
// PublishingMetricsCollector Benchmarks
// ============================================================================

// BenchmarkCollectAll benchmarks full metrics collection from all collectors.
func BenchmarkCollectAll(b *testing.B) {
	collector := NewPublishingMetricsCollector()

	// Register mock collectors
	collector.RegisterCollector(&MockBenchCollector{name: "health", delay: 10 * time.Microsecond})
	collector.RegisterCollector(&MockBenchCollector{name: "refresh", delay: 10 * time.Microsecond})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = collector.CollectAll(ctx)
	}
}

// BenchmarkCollectAll_Concurrent benchmarks concurrent collection requests.
func BenchmarkCollectAll_Concurrent(b *testing.B) {
	collector := NewPublishingMetricsCollector()

	// Register mock collectors
	collector.RegisterCollector(&MockBenchCollector{name: "health", delay: 10 * time.Microsecond})
	collector.RegisterCollector(&MockBenchCollector{name: "refresh", delay: 10 * time.Microsecond})

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = collector.CollectAll(ctx)
		}
	})
}

// BenchmarkCollectorCount benchmarks collector count retrieval.
func BenchmarkCollectorCount(b *testing.B) {
	collector := NewPublishingMetricsCollector()
	collector.RegisterCollector(&MockBenchCollector{name: "test", delay: 0})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = collector.CollectorCount()
	}
}

// BenchmarkGetCollectorNames benchmarks collector names retrieval.
func BenchmarkGetCollectorNames(b *testing.B) {
	collector := NewPublishingMetricsCollector()
	collector.RegisterCollector(&MockBenchCollector{name: "health", delay: 0})
	collector.RegisterCollector(&MockBenchCollector{name: "refresh", delay: 0})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = collector.GetCollectorNames()
	}
}

// ============================================================================
// Individual Collector Benchmarks
// ============================================================================

// BenchmarkHealthMetricsCollector benchmarks health metrics collection.
func BenchmarkHealthMetricsCollector(b *testing.B) {
	// Create mock health monitor
	mockMonitor := &MockHealthMonitor{
		healthStatuses: []TargetHealthStatus{
			{TargetName: "rootly-prod", Status: HealthStatusHealthy, SuccessRate: 99.5},
			{TargetName: "slack-prod", Status: HealthStatusHealthy, SuccessRate: 98.0},
		},
	}

	collector := NewHealthMetricsCollector(mockMonitor)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = collector.Collect(ctx)
	}
}

// BenchmarkRefreshMetricsCollector benchmarks refresh metrics collection.
func BenchmarkRefreshMetricsCollector(b *testing.B) {
	// Create mock refresh manager
	mockManager := &MockRefreshManager{
		status: RefreshStatus{
			LastRefresh:       time.Now(),
			NextRefresh:       time.Now().Add(5 * time.Minute),
			RefreshDuration:   100 * time.Millisecond,
			TargetsDiscovered: 10,
			State:             "idle",
		},
	}

	collector := NewRefreshMetricsCollector(mockManager)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = collector.Collect(ctx)
	}
}


// ============================================================================
// Mock Implementations for Benchmarks
// ============================================================================

// MockBenchCollector is a fast mock collector for benchmarks.
type MockBenchCollector struct {
	name  string
	delay time.Duration
}

func (m *MockBenchCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	return map[string]float64{
		"test_metric": 42.0,
	}, nil
}

func (m *MockBenchCollector) Name() string {
	return m.name
}

func (m *MockBenchCollector) IsAvailable() bool {
	return true
}

// MockHealthMonitor for benchmarks.
type MockHealthMonitor struct {
	healthStatuses []TargetHealthStatus
}

func (m *MockHealthMonitor) Start() error { return nil }
func (m *MockHealthMonitor) Stop(timeout time.Duration) error { return nil }
func (m *MockHealthMonitor) GetHealth(ctx context.Context) ([]TargetHealthStatus, error) {
	return m.healthStatuses, nil
}
func (m *MockHealthMonitor) GetHealthByName(ctx context.Context, targetName string) (*TargetHealthStatus, error) {
	return nil, nil
}
func (m *MockHealthMonitor) CheckNow(ctx context.Context, targetName string) (*TargetHealthStatus, error) {
	return nil, nil
}
func (m *MockHealthMonitor) GetStats(ctx context.Context) (*HealthStats, error) {
	return &HealthStats{}, nil
}

// MockRefreshManager for benchmarks.
type MockRefreshManager struct {
	status RefreshStatus
}

func (m *MockRefreshManager) Start() error { return nil }
func (m *MockRefreshManager) Stop(timeout time.Duration) error { return nil }
func (m *MockRefreshManager) RefreshNow() error { return nil }
func (m *MockRefreshManager) GetStatus() RefreshStatus { return m.status }
