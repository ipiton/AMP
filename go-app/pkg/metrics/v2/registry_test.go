package v2

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewRegistry(t *testing.T) {
	// Use a custom registry to avoid conflicts with global state
	reg := prometheus.NewRegistry()
	registry := NewRegistry(WithPrometheusRegisterer(reg))

	if registry == nil {
		t.Fatal("NewRegistry returned nil")
	}

	if registry.Publishing == nil {
		t.Error("Publishing metrics not initialized")
	}
	if registry.HTTP == nil {
		t.Error("HTTP metrics not initialized")
	}
	if registry.Database == nil {
		t.Error("Database metrics not initialized")
	}
	if registry.Cache == nil {
		t.Error("Cache metrics not initialized")
	}
}

func TestRegistry_Registerer(t *testing.T) {
	reg := prometheus.NewRegistry()
	registry := NewRegistry(WithPrometheusRegisterer(reg))

	if registry.Registerer() != reg {
		t.Error("Registerer() did not return the expected registerer")
	}
}

func TestPublishingMetrics_RecordMessage(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	metrics.RecordMessage(ProviderSlack, "success")
	metrics.RecordMessage(ProviderSlack, "error")
	metrics.RecordMessage(ProviderPagerDuty, "success")

	count := testutil.CollectAndCount(metrics.messagesTotal)
	if count != 3 {
		t.Errorf("expected 3 metric series, got %d", count)
	}
}

func TestPublishingMetrics_RecordAPIRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	metrics.RecordAPIRequest(ProviderSlack, "/webhook", "POST", 200, 100*time.Millisecond)
	metrics.RecordAPIRequest(ProviderRootly, "/incidents", "POST", 201, 250*time.Millisecond)

	// Verify counter
	counterCount := testutil.CollectAndCount(metrics.apiRequestsTotal)
	if counterCount != 2 {
		t.Errorf("expected 2 counter series, got %d", counterCount)
	}

	// Verify histogram
	histCount := testutil.CollectAndCount(metrics.apiDurationSeconds)
	if histCount != 2 {
		t.Errorf("expected 2 histogram series, got %d", histCount)
	}
}

func TestPublishingMetrics_RecordRateLimitHit(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	metrics.RecordRateLimitHit(ProviderSlack)
	metrics.RecordRateLimitHit(ProviderSlack)
	metrics.RecordRateLimitHit(ProviderPagerDuty)

	count := testutil.CollectAndCount(metrics.rateLimitHitsTotal)
	if count != 2 {
		t.Errorf("expected 2 metric series, got %d", count)
	}
}

func TestPublishingMetrics_Queue(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	// Test queue size update
	metrics.UpdateQueueSize("high", 10, 100)
	metrics.UpdateQueueSize("low", 5, 50)

	// Test job recording
	metrics.RecordJobSuccess("rootly-prod", "high", 150*time.Millisecond)
	metrics.RecordJobFailure("slack-prod")
	metrics.RecordJobDLQ("webhook-prod")

	// Verify gauges
	if count := testutil.CollectAndCount(metrics.queueSize); count != 2 {
		t.Errorf("expected 2 queueSize series, got %d", count)
	}

	// Verify jobs counter
	if count := testutil.CollectAndCount(metrics.jobsProcessedTotal); count != 3 {
		t.Errorf("expected 3 jobsProcessed series, got %d", count)
	}
}

func TestPublishingMetrics_CircuitBreaker(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	metrics.SetCircuitBreakerState("rootly-prod", CircuitBreakerClosed)
	metrics.SetCircuitBreakerState("slack-prod", CircuitBreakerOpen)
	metrics.RecordCircuitBreakerTrip("slack-prod")

	if count := testutil.CollectAndCount(metrics.circuitBreakerState); count != 2 {
		t.Errorf("expected 2 circuitBreakerState series, got %d", count)
	}

	if count := testutil.CollectAndCount(metrics.circuitBreakerTripsTotal); count != 1 {
		t.Errorf("expected 1 circuitBreakerTrips series, got %d", count)
	}
}

func TestPublishingMetrics_HealthCheck(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	metrics.RecordHealthCheck("rootly-prod", true, 50*time.Millisecond)
	metrics.RecordHealthCheck("rootly-prod", false, 5*time.Second)
	metrics.SetTargetHealthStatus("rootly-prod", "rootly", HealthStatusHealthy)

	// Should have 2 series for health checks (success and failure)
	if count := testutil.CollectAndCount(metrics.healthChecksTotal); count != 2 {
		t.Errorf("expected 2 healthChecks series, got %d", count)
	}
}

func TestHTTPMetrics_RecordRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(reg)

	metrics.RecordRequest("GET", "/api/alerts", 200, 50*time.Millisecond)
	metrics.RecordRequest("POST", "/webhook", 201, 100*time.Millisecond)
	metrics.RecordRequest("GET", "/api/alerts", 500, 200*time.Millisecond)

	if count := testutil.CollectAndCount(metrics.requestsTotal); count != 3 {
		t.Errorf("expected 3 request series, got %d", count)
	}

	// Duration histogram should have 2 series (unique method+path combinations)
	if count := testutil.CollectAndCount(metrics.requestDurationSeconds); count != 2 {
		t.Errorf("expected 2 duration series, got %d", count)
	}
}

func TestHTTPMetrics_RequestsInFlight(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(reg)

	metrics.IncRequestsInFlight()
	metrics.IncRequestsInFlight()
	metrics.DecRequestsInFlight()

	// Value should be 1 (2 inc - 1 dec)
	value := testutil.ToFloat64(metrics.requestsInFlight)
	if value != 1 {
		t.Errorf("expected requestsInFlight to be 1, got %f", value)
	}
}

func TestHTTPMetrics_TemplateRender(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(reg)

	metrics.RecordTemplateRender("dashboard.html", true, 10*time.Millisecond)
	metrics.RecordTemplateRender("alerts.html", false, 5*time.Millisecond)
	metrics.RecordTemplateCacheHit()

	if count := testutil.CollectAndCount(metrics.templateRenderTotal); count != 2 {
		t.Errorf("expected 2 template render series, got %d", count)
	}
}

func TestDatabaseMetrics_RecordQuery(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewDatabaseMetrics(reg)

	metrics.RecordQuery("select", true, 5*time.Millisecond)
	metrics.RecordQuery("insert", true, 10*time.Millisecond)
	metrics.RecordQuery("select", false, 100*time.Millisecond)

	if count := testutil.CollectAndCount(metrics.queryTotal); count != 3 {
		t.Errorf("expected 3 query series, got %d", count)
	}
}

func TestDatabaseMetrics_ConnectionPool(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewDatabaseMetrics(reg)

	metrics.SetConnectionCounts(5, 10)
	metrics.RecordConnectionOpened()
	metrics.RecordConnectionClosed()
	metrics.RecordConnectionFailed()

	// Verify active connections
	if value := testutil.ToFloat64(metrics.connectionsActive); value != 5 {
		t.Errorf("expected connectionsActive to be 5, got %f", value)
	}

	// Verify idle connections
	if value := testutil.ToFloat64(metrics.connectionsIdle); value != 10 {
		t.Errorf("expected connectionsIdle to be 10, got %f", value)
	}

	// Verify connection total counters
	if count := testutil.CollectAndCount(metrics.connectionsTotal); count != 3 {
		t.Errorf("expected 3 connections series, got %d", count)
	}
}

func TestDatabaseMetrics_Transactions(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewDatabaseMetrics(reg)

	metrics.RecordTransactionCommit(50 * time.Millisecond)
	metrics.RecordTransactionRollback(100 * time.Millisecond)

	if count := testutil.CollectAndCount(metrics.transactionsTotal); count != 2 {
		t.Errorf("expected 2 transaction series, got %d", count)
	}
}

func TestCacheMetrics_HitsMisses(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewCacheMetrics(reg)

	metrics.RecordHit(CacheTypeRedis, "session")
	metrics.RecordMiss(CacheTypeRedis, "session")
	metrics.RecordHit(CacheTypeMemory, "classification")

	if count := testutil.CollectAndCount(metrics.hitsTotal); count != 2 {
		t.Errorf("expected 2 hits series, got %d", count)
	}

	if count := testutil.CollectAndCount(metrics.missesTotal); count != 1 {
		t.Errorf("expected 1 misses series, got %d", count)
	}
}

func TestCacheMetrics_L1L2Convenience(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewCacheMetrics(reg)

	metrics.RecordL1CacheHit()
	metrics.RecordL2CacheHit()
	metrics.RecordCacheMiss()

	// Should have 2 hit series (L1 and L2 classification)
	if count := testutil.CollectAndCount(metrics.hitsTotal); count != 2 {
		t.Errorf("expected 2 hits series, got %d", count)
	}

	// Should have 2 miss series (L1 and L2 classification)
	if count := testutil.CollectAndCount(metrics.missesTotal); count != 2 {
		t.Errorf("expected 2 misses series, got %d", count)
	}
}

func TestCacheMetrics_Redis(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewCacheMetrics(reg)

	metrics.SetRedisConnectionsActive(5)
	metrics.RecordRedisCommand("GET", true, 1*time.Millisecond)
	metrics.RecordRedisCommand("SET", false, 5*time.Millisecond)

	// Verify active connections
	if value := testutil.ToFloat64(metrics.redisConnectionsActive); value != 5 {
		t.Errorf("expected redisConnectionsActive to be 5, got %f", value)
	}

	// Verify command counters
	if count := testutil.CollectAndCount(metrics.redisCommandsTotal); count != 2 {
		t.Errorf("expected 2 redis command series, got %d", count)
	}
}

func TestCacheMetrics_SetSize(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewCacheMetrics(reg)

	metrics.SetSize(CacheTypeMemory, 100)
	metrics.SetSize(CacheTypeRedis, 1000)

	if count := testutil.CollectAndCount(metrics.sizeGauge); count != 2 {
		t.Errorf("expected 2 size series, got %d", count)
	}
}

// TestNamespaceConsistency verifies all metrics use the correct namespace
func TestNamespaceConsistency(t *testing.T) {
	if Namespace != "alert_history" {
		t.Errorf("expected namespace to be 'alert_history', got '%s'", Namespace)
	}
}

// BenchmarkPublishingMetrics_RecordMessage benchmarks message recording
func BenchmarkPublishingMetrics_RecordMessage(b *testing.B) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.RecordMessage(ProviderSlack, "success")
	}
}

// BenchmarkPublishingMetrics_RecordAPIRequest benchmarks API request recording
func BenchmarkPublishingMetrics_RecordAPIRequest(b *testing.B) {
	reg := prometheus.NewRegistry()
	metrics := NewPublishingMetrics(reg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.RecordAPIRequest(ProviderSlack, "/webhook", "POST", 200, 100*time.Millisecond)
	}
}

// BenchmarkHTTPMetrics_RecordRequest benchmarks HTTP request recording
func BenchmarkHTTPMetrics_RecordRequest(b *testing.B) {
	reg := prometheus.NewRegistry()
	metrics := NewHTTPMetrics(reg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.RecordRequest("GET", "/api/alerts", 200, 50*time.Millisecond)
	}
}

// BenchmarkCacheMetrics_RecordHit benchmarks cache hit recording
func BenchmarkCacheMetrics_RecordHit(b *testing.B) {
	reg := prometheus.NewRegistry()
	metrics := NewCacheMetrics(reg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.RecordHit(CacheTypeRedis, "session")
	}
}
