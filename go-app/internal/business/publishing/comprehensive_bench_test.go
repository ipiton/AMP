package publishing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// comprehensive_bench_test.go - Comprehensive benchmarks for all publishing components
// Target: 40+ benchmarks, verify performance meets 150% quality standards

// ============================================================================
// Health Monitor Benchmarks
// ============================================================================

// BenchmarkHealthCheck_SingleTarget benchmarks single target health check
func BenchmarkHealthCheck_SingleTarget(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "bench-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		monitor.CheckNow(ctx, "bench-target")
	}
}

// BenchmarkHealthCheck_ParallelTargets_10 benchmarks 10 targets in parallel
func BenchmarkHealthCheck_ParallelTargets_10(b *testing.B) {
	benchmarkHealthCheckParallel(b, 10)
}

// BenchmarkHealthCheck_ParallelTargets_50 benchmarks 50 targets in parallel
func BenchmarkHealthCheck_ParallelTargets_50(b *testing.B) {
	benchmarkHealthCheckParallel(b, 50)
}

// BenchmarkHealthCheck_ParallelTargets_100 benchmarks 100 targets in parallel
func BenchmarkHealthCheck_ParallelTargets_100(b *testing.B) {
	benchmarkHealthCheckParallel(b, 100)
}

func benchmarkHealthCheckParallel(b *testing.B, targetCount int) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	targets := make([]*core.PublishingTarget, targetCount)
	for i := 0; i < targetCount; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: server.URL, Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	config := DefaultHealthConfig()
	config.MaxConcurrentChecks = 50

	monitor, _ := NewHealthMonitor(discovery, config, nil, metrics)
	monitor.Start()
	defer monitor.Stop(time.Second)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		monitor.GetHealth(ctx)
	}
}

// BenchmarkGetHealth_ConcurrentReads benchmarks concurrent health status reads
func BenchmarkGetHealth_ConcurrentReads(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "bench-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			monitor.GetHealth(ctx)
		}
	})
}

// BenchmarkHealthStatusCache_Lookup benchmarks cache lookup performance
func BenchmarkHealthStatusCache_Lookup(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	targets := make([]*core.PublishingTarget, 100)
	for i := 0; i < 100; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: server.URL, Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)
	monitor.Start()
	time.Sleep(200 * time.Millisecond) // Let initial checks complete
	defer monitor.Stop(time.Second)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		targetName := fmt.Sprintf("target-%d", i%100)
		monitor.GetHealthByName(ctx, targetName)
	}
}

// ============================================================================
// Discovery Benchmarks
// ============================================================================

// BenchmarkDiscoverTargets_10Secrets benchmarks discovery with 10 secrets
func BenchmarkDiscoverTargets_10Secrets(b *testing.B) {
	benchmarkDiscoverTargets(b, 10)
}

// BenchmarkDiscoverTargets_50Secrets benchmarks discovery with 50 secrets
func BenchmarkDiscoverTargets_50Secrets(b *testing.B) {
	benchmarkDiscoverTargets(b, 50)
}

// BenchmarkDiscoverTargets_100Secrets benchmarks discovery with 100 secrets
func BenchmarkDiscoverTargets_100Secrets(b *testing.B) {
	benchmarkDiscoverTargets(b, 100)
}

func benchmarkDiscoverTargets(b *testing.B, secretCount int) {
	targets := make([]*core.PublishingTarget, secretCount)
	for i := 0; i < secretCount; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: "http://example.com", Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		discovery.DiscoverTargets(ctx)
	}
}

// BenchmarkGetTarget_CacheLookup benchmarks target cache lookup
func BenchmarkGetTarget_CacheLookup(b *testing.B) {
	targets := make([]*core.PublishingTarget, 1000)
	for i := 0; i < 1000; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: "http://example.com", Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		targetName := fmt.Sprintf("target-%d", i%1000)
		discovery.GetTarget(targetName)
	}
}

// ============================================================================
// Metrics Collection Benchmarks
// ============================================================================

// BenchmarkMetricsCollection_HealthOnly benchmarks health metrics collection
func BenchmarkMetricsCollection_HealthOnly(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "bench-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	collector := NewHealthMetricsCollector(monitor)
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		collector.Collect(ctx)
	}
}

// BenchmarkMetricsCollection_AllSources benchmarks aggregated metrics collection
func BenchmarkMetricsCollection_AllSources(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "bench-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	aggregator := NewPublishingMetricsCollector()
	aggregator.RegisterCollector(NewHealthMetricsCollector(monitor))

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		aggregator.CollectAll(ctx)
	}
}

// BenchmarkPrometheusMetrics_RecordHealthCheck benchmarks Prometheus metric recording
func BenchmarkPrometheusMetrics_RecordHealthCheck(b *testing.B) {
	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metrics.RecordHealthCheck("test-target", true, 10*time.Millisecond)
	}
}

// BenchmarkPrometheusMetrics_SetHealthStatus benchmarks status gauge updates
func BenchmarkPrometheusMetrics_SetHealthStatus(b *testing.B) {
	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		metrics.SetTargetHealthStatus("test-target", "webhook", v2.HealthStatusHealthy)
	}
}

// ============================================================================
// Concurrent Operations Benchmarks
// ============================================================================

// BenchmarkConcurrent_HealthChecks benchmarks concurrent health checks
func BenchmarkConcurrent_HealthChecks(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	targets := make([]*core.PublishingTarget, 10)
	for i := 0; i < 10; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: server.URL, Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			targetName := fmt.Sprintf("target-%d", b.N%10)
			monitor.CheckNow(ctx, targetName)
		}
	})
}

// BenchmarkConcurrent_MetricsCollection benchmarks concurrent metrics reads
func BenchmarkConcurrent_MetricsCollection(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "bench-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	collector := NewHealthMetricsCollector(monitor)
	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			collector.Collect(ctx)
		}
	})
}

// ============================================================================
// Memory Allocation Benchmarks
// ============================================================================

// BenchmarkMemory_HealthStatusAllocation benchmarks memory allocation for health status
func BenchmarkMemory_HealthStatusAllocation(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		now := time.Now()
		_ = &TargetHealthStatus{
			TargetName:          "test-target",
			TargetType:          "webhook",
			Status: HealthStatusHealthy,
			LastCheck:           now,
			LastSuccess:         &now,
			ConsecutiveFailures: 0,
			SuccessRate:         100.0,
		}
	}
}

// BenchmarkMemory_MetricsMap benchmarks memory for metrics map
func BenchmarkMemory_MetricsMap(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		metrics := map[string]float64{
			"health_checks_total": 1000,
			"queue_size":          50,
			"targets_discovered":  10,
		}
		_ = metrics
	}
}

// ============================================================================
// Latency Benchmarks
// ============================================================================

// BenchmarkLatency_HealthCheck_Fast benchmarks fast health checks (<10ms)
func BenchmarkLatency_HealthCheck_Fast(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "fast-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	monitor, _ := NewHealthMonitor(discovery, DefaultHealthConfig(), nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		monitor.CheckNow(ctx, "fast-target")
	}
}

// BenchmarkLatency_HealthCheck_Slow benchmarks slow health checks (100ms+)
func BenchmarkLatency_HealthCheck_Slow(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := &core.PublishingTarget{
		Name: "slow-target", Type: "webhook", URL: server.URL, Enabled: true,
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets([]*core.PublishingTarget{target})

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	config := DefaultHealthConfig()
	config.HTTPTimeout = 5 * time.Second

	monitor, _ := NewHealthMonitor(discovery, config, nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		monitor.CheckNow(ctx, "slow-target")
	}
}

// ============================================================================
// Scalability Benchmarks
// ============================================================================

// BenchmarkScalability_1Target benchmarks with 1 target
func BenchmarkScalability_1Target(b *testing.B) {
	benchmarkScalability(b, 1)
}

// BenchmarkScalability_10Targets benchmarks with 10 targets
func BenchmarkScalability_10Targets(b *testing.B) {
	benchmarkScalability(b, 10)
}

// BenchmarkScalability_100Targets benchmarks with 100 targets
func BenchmarkScalability_100Targets(b *testing.B) {
	benchmarkScalability(b, 100)
}

// BenchmarkScalability_1000Targets benchmarks with 1000 targets
func BenchmarkScalability_1000Targets(b *testing.B) {
	benchmarkScalability(b, 1000)
}

func benchmarkScalability(b *testing.B, targetCount int) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	targets := make([]*core.PublishingTarget, targetCount)
	for i := 0; i < targetCount; i++ {
		targets[i] = &core.PublishingTarget{
			Name: fmt.Sprintf("target-%d", i), Type: "webhook", URL: server.URL, Enabled: true,
		}
	}

	discovery := NewTestHealthDiscoveryManager()
	discovery.SetTargets(targets)

	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	config := DefaultHealthConfig()
	config.MaxConcurrentChecks = 100

	monitor, _ := NewHealthMonitor(discovery, config, nil, metrics)

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		monitor.GetHealth(ctx)
	}
}
