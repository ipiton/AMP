package publishing

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// BenchmarkHealthMonitor_Start tests Start performance.
func BenchmarkHealthMonitor_Start(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		monitor := createBenchHealthMonitor(b)
		b.StartTimer()

		_ = monitor.Start()

		b.StopTimer()
		_ = monitor.Stop(time.Second)
		b.StartTimer()
	}
}

// BenchmarkHealthMonitor_Stop tests Stop performance.
func BenchmarkHealthMonitor_Stop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		monitor := createBenchHealthMonitor(b)
		_ = monitor.Start()
		b.StartTimer()

		_ = monitor.Stop(time.Second)
	}
}

// BenchmarkHealthMonitor_GetHealth tests GetHealth performance.
func BenchmarkHealthMonitor_GetHealth(b *testing.B) {
	monitor, discovery := createBenchHealthMonitorWithDiscovery(b)

	// Add test targets
	targets := make([]*core.PublishingTarget, 20)
	for i := 0; i < 20; i++ {
		targets[i] = &core.PublishingTarget{
			Name:    "target" + string(rune('0'+i)),
			Type:    "webhook",
			URL:     "https://example.com",
			Enabled: true,
		}
	}
	discovery.SetTargets(targets)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = monitor.GetHealth(ctx)
	}
}

// BenchmarkHealthMonitor_GetHealthByName tests GetHealthByName performance.
func BenchmarkHealthMonitor_GetHealthByName(b *testing.B) {
	monitor, discovery := createBenchHealthMonitorWithDiscovery(b)

	target := &core.PublishingTarget{
		Name:    "bench-target",
		Type:    "webhook",
		URL:     "https://example.com",
		Enabled: true,
	}
	discovery.SetTargets([]*core.PublishingTarget{target})

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = monitor.GetHealthByName(ctx, "bench-target")
	}
}

// BenchmarkHealthMonitor_CheckNow tests CheckNow performance.
func BenchmarkHealthMonitor_CheckNow(b *testing.B) {
	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	monitor, discovery := createBenchHealthMonitorWithDiscovery(b)

	target := &core.PublishingTarget{
		Name:    "bench-target",
		Type:    "webhook",
		URL:     server.URL,
		Enabled: true,
	}
	discovery.SetTargets([]*core.PublishingTarget{target})

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = monitor.CheckNow(ctx, "bench-target")
	}
}

// BenchmarkHealthMonitor_GetStats tests GetStats performance.
func BenchmarkHealthMonitor_GetStats(b *testing.B) {
	monitor, discovery := createBenchHealthMonitorWithDiscovery(b)

	// Add test targets
	targets := make([]*core.PublishingTarget, 20)
	for i := 0; i < 20; i++ {
		targets[i] = &core.PublishingTarget{
			Name:    "target" + string(rune('0'+i)),
			Type:    "webhook",
			URL:     "https://example.com",
			Enabled: true,
		}
	}
	discovery.SetTargets(targets)

	// Populate cache with statuses
	ctx := context.Background()
	for _, target := range targets {
		monitor.statusCache.Set(&TargetHealthStatus{
			TargetName:     target.Name,
			TargetType:     target.Type,
			Status:         HealthStatusHealthy,
			LastCheck:      time.Now(),
			TotalChecks:    100,
			TotalSuccesses: 95,
			SuccessRate:    95.0,
		})
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = monitor.GetStats(ctx)
	}
}

// BenchmarkHealthStatusCache_Get tests cache Get performance.
func BenchmarkHealthStatusCache_Get(b *testing.B) {
	cache := newHealthStatusCache()

	// Populate cache
	for i := 0; i < 100; i++ {
		status := &TargetHealthStatus{
			TargetName: "target" + string(rune('0'+i)),
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("target50")
	}
}

// BenchmarkHealthStatusCache_Set tests cache Set performance.
func BenchmarkHealthStatusCache_Set(b *testing.B) {
	cache := newHealthStatusCache()

	status := &TargetHealthStatus{
		TargetName: "bench-target",
		Status:     HealthStatusHealthy,
		LastCheck:  time.Now(),
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cache.Set(status)
	}
}

// BenchmarkHealthStatusCache_GetAll tests cache GetAll performance.
func BenchmarkHealthStatusCache_GetAll(b *testing.B) {
	cache := newHealthStatusCache()

	// Populate cache with 100 targets
	for i := 0; i < 100; i++ {
		status := &TargetHealthStatus{
			TargetName: "target" + string(rune('0'+i)),
			Status:     HealthStatusHealthy,
			LastCheck:  time.Now(),
		}
		cache.Set(status)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = cache.GetAll()
	}
}

// BenchmarkProcessHealthCheckResult tests result processing performance.
func BenchmarkProcessHealthCheckResult(b *testing.B) {
	cache := newHealthStatusCache()
	metrics := getBenchMetrics(b)
	logger := slog.Default()
	config := DefaultHealthConfig()

	result := HealthCheckResult{
		TargetName: "bench-target",
		Success:    true,
		LatencyMs:  ptr(int64(100)),
		StatusCode: ptr(200),
		CheckedAt:  time.Now(),
		CheckType:  CheckTypePeriodic,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		processHealthCheckResult(cache, metrics, logger, config, result)
	}
}

// BenchmarkCalculateAggregateStats tests stats calculation performance.
func BenchmarkCalculateAggregateStats(b *testing.B) {
	// Create test statuses (100 targets)
	statuses := make([]TargetHealthStatus, 100)
	for i := 0; i < 100; i++ {
		statuses[i] = TargetHealthStatus{
			TargetName:     "target" + string(rune('0'+i)),
			Status:         HealthStatusHealthy,
			LastCheck:      time.Now(),
			TotalChecks:    100,
			TotalSuccesses: 95,
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = calculateAggregateStats(statuses)
	}
}

// BenchmarkClassifyNetworkError tests error classification performance.
func BenchmarkClassifyNetworkError(b *testing.B) {
	err := &timeoutError{}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = classifyNetworkError(err)
	}
}

// BenchmarkSanitizeErrorMessage tests error sanitization performance.
func BenchmarkSanitizeErrorMessage(b *testing.B) {
	msg := "HTTP request failed: Authorization: Bearer secret123\nother data"

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = sanitizeErrorMessage(msg)
	}
}

// Helper functions for benchmarks

var (
	benchMetrics     *HealthMetrics
	benchMetricsOnce sync.Once
)

func getBenchMetrics(b *testing.B) *HealthMetrics {
	b.Helper()

	benchMetricsOnce.Do(func() {
		var err error
		benchMetrics, err = NewHealthMetrics()
		if err != nil {
			b.Fatalf("Failed to create bench metrics: %v", err)
		}
	})

	return benchMetrics
}

func createBenchHealthMonitor(b *testing.B) *DefaultHealthMonitor {
	b.Helper()

	discovery := NewTestHealthDiscoveryManager()
	config := DefaultHealthConfig()
	config.CheckInterval = 1 * time.Minute
	config.WarmupDelay = 1 * time.Millisecond

	metrics := getBenchMetrics(b)

	monitor, err := NewHealthMonitor(discovery, config, slog.Default(), metrics)
	if err != nil {
		b.Fatalf("Failed to create health monitor: %v", err)
	}

	return monitor
}

func createBenchHealthMonitorWithDiscovery(b *testing.B) (*DefaultHealthMonitor, *TestHealthDiscoveryManager) {
	b.Helper()

	discovery := NewTestHealthDiscoveryManager()
	config := DefaultHealthConfig()
	config.CheckInterval = 1 * time.Minute
	config.WarmupDelay = 1 * time.Millisecond

	metrics := getBenchMetrics(b)

	monitor, err := NewHealthMonitor(discovery, config, slog.Default(), metrics)
	if err != nil {
		b.Fatalf("Failed to create health monitor: %v", err)
	}

	return monitor, discovery
}
