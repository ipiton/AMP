// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Benchmarks
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/pkg/metrics"
)

// BenchmarkDashboardHealthHandler_GetHealth benchmarks the main GetHealth handler.
func BenchmarkDashboardHealthHandler_GetHealth(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 5},
		},
		&mockHealthMonitorForHealth{
			health: []publishing.TargetHealthStatus{},
		},
		nil, // logger
		metrics.DefaultRegistry(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.GetHealth(w, req)
	}
}

// BenchmarkDashboardHealthHandler_GetHealth_WithAllComponents benchmarks with all components.
func BenchmarkDashboardHealthHandler_GetHealth_WithAllComponents(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 10},
		},
		&mockHealthMonitorForHealth{
			health: []publishing.TargetHealthStatus{
				{TargetName: "target1", Status: "healthy"},
				{TargetName: "target2", Status: "healthy"},
			},
		},
		nil, // logger
		metrics.DefaultRegistry(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.GetHealth(w, req)
	}
}

// BenchmarkDashboardHealthHandler_GetHealth_MinimalConfig benchmarks with minimal configuration.
func BenchmarkDashboardHealthHandler_GetHealth_MinimalConfig(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		nil, // cache
		nil, // classification
		nil, // targetDiscovery
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.GetHealth(w, req)
	}
}

// BenchmarkDashboardHealthHandler_GetHealth_Concurrent benchmarks concurrent requests.
func BenchmarkDashboardHealthHandler_GetHealth_Concurrent(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil},
		&mockClassificationServiceForHealth{healthErr: nil},
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 5},
		},
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)
		for pb.Next() {
			w := httptest.NewRecorder()
			handler.GetHealth(w, req)
		}
	})
}

// BenchmarkDashboardHealthHandler_GetHealth_WithErrors benchmarks with error scenarios.
func BenchmarkDashboardHealthHandler_GetHealth_WithErrors(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		&mockCacheForHealth{healthErr: nil}, // Redis healthy
		&mockClassificationServiceForHealth{healthErr: nil}, // LLM healthy
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 0}, // No targets
		},
		nil, // healthMonitor
		nil, // logger
		metrics.DefaultRegistry(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.GetHealth(w, req)
	}
}

// BenchmarkDashboardHealthHandler_AggregateStatus benchmarks status aggregation logic.
func BenchmarkDashboardHealthHandler_AggregateStatus(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, nil, nil, nil, nil, nil, metrics.DefaultRegistry(),
	)

	services := map[string]ServiceHealth{
		"database":  {Status: "healthy"},
		"redis":     {Status: "healthy"},
		"llm":       {Status: "healthy"},
		"publishing": {Status: "healthy"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.aggregateStatus(services)
	}
}

// BenchmarkDashboardHealthHandler_AggregateStatus_Degraded benchmarks with degraded status.
func BenchmarkDashboardHealthHandler_AggregateStatus_Degraded(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, nil, nil, nil, nil, nil, metrics.DefaultRegistry(),
	)

	services := map[string]ServiceHealth{
		"database":  {Status: "healthy"},
		"redis":     {Status: "degraded", Error: "connection timeout"},
		"llm":       {Status: "healthy"},
		"publishing": {Status: "not_configured"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.aggregateStatus(services)
	}
}

// BenchmarkDashboardHealthHandler_AggregateStatus_Unhealthy benchmarks with unhealthy status.
func BenchmarkDashboardHealthHandler_AggregateStatus_Unhealthy(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, nil, nil, nil, nil, nil, metrics.DefaultRegistry(),
	)

	services := map[string]ServiceHealth{
		"database":  {Status: "unhealthy", Error: "connection failed"},
		"redis":     {Status: "degraded"},
		"llm":       {Status: "not_configured"},
		"publishing": {Status: "not_configured"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = handler.aggregateStatus(services)
	}
}

// BenchmarkDashboardHealthHandler_CheckRedisHealth benchmarks Redis health check.
func BenchmarkDashboardHealthHandler_CheckRedisHealth(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil,
		&mockCacheForHealth{healthErr: nil},
		nil, nil, nil, nil,
		metrics.DefaultRegistry(),
	)

	timeout := handler.config.RedisTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_ = handler.checkRedisHealth(ctx)
		cancel()
	}
}

// BenchmarkDashboardHealthHandler_CheckLLMHealth benchmarks LLM health check.
func BenchmarkDashboardHealthHandler_CheckLLMHealth(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, nil,
		&mockClassificationServiceForHealth{healthErr: nil},
		nil, nil, nil,
		metrics.DefaultRegistry(),
	)

	timeout := handler.config.LLMTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_ = handler.checkLLMHealth(ctx)
		cancel()
	}
}

// BenchmarkDashboardHealthHandler_CheckPublishingHealth benchmarks Publishing health check.
func BenchmarkDashboardHealthHandler_CheckPublishingHealth(b *testing.B) {
	handler := NewDashboardHealthHandler(
		nil, // dbPool
		nil, // cache
		nil, // classificationService
		&mockTargetDiscoveryForHealth{
			stats: publishing.DiscoveryStats{TotalTargets: 5},
		},
		&mockHealthMonitorForHealth{
			health: []publishing.TargetHealthStatus{
				{TargetName: "target1", Status: "healthy"},
			},
		},
		nil, // logger
		metrics.DefaultRegistry(),
	)

	timeout := handler.config.PublishingTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_ = handler.checkPublishingHealth(ctx)
		cancel()
	}
}
