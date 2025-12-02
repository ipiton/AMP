package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/ipiton/AMP/pkg/metrics"
)

// mockPostgresPool is a mock implementation of PostgresPool for testing.
type mockPostgresPool struct {
	stats *PoolStats
}

func (m *mockPostgresPool) Stats() PoolStats {
	return *m.stats
}

func (m *mockPostgresPool) Pool() interface{} {
	return nil
}

func (m *mockPostgresPool) Close() error {
	return nil
}

func (m *mockPostgresPool) Ping(ctx context.Context) error {
	return nil
}

func TestNewPrometheusExporter(t *testing.T) {
	mockPool := &mockPostgresPool{
		stats: &PoolStats{
			ActiveConnections:   5,
			IdleConnections:     10,
			ConnectionsCreated:  100,
			ConnectionWaitTime:  50 * time.Millisecond,
			TotalQueries:        1000,
			QueryExecutionTime:  500 * time.Millisecond,
			ConnectionErrors:    2,
			QueryErrors:         5,
			TimeoutErrors:       1,
		},
	}

	registry := metrics.NewMetricsRegistry("test_prom_exporter")
	dbMetrics := registry.Infra().DB

	exporter := NewPrometheusExporter(mockPool, dbMetrics)

	if exporter == nil {
		t.Fatal("NewPrometheusExporter returned nil")
	}

	if exporter.pool != mockPool {
		t.Error("Pool not set correctly")
	}

	if exporter.dbMetrics != dbMetrics {
		t.Error("DBMetrics not set correctly")
	}
}

func TestPrometheusExporter_StartStop(t *testing.T) {
	mockPool := &mockPostgresPool{
		stats: &PoolStats{
			ActiveConnections:   5,
			IdleConnections:     10,
			ConnectionsCreated:  100,
			ConnectionWaitTime:  50 * time.Millisecond,
			TotalQueries:        1000,
			QueryExecutionTime:  500 * time.Millisecond,
			ConnectionErrors:    2,
			QueryErrors:         5,
			TimeoutErrors:       1,
		},
	}

	registry := metrics.NewMetricsRegistry("test_prom_start_stop")
	dbMetrics := registry.Infra().DB

	exporter := NewPrometheusExporter(mockPool, dbMetrics)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start the exporter with very short interval
	exporter.Start(ctx, 20*time.Millisecond)

	// Wait for at least one export cycle
	time.Sleep(50 * time.Millisecond)

	// Stop the exporter
	exporter.Stop()

	// Wait a bit to ensure graceful shutdown
	time.Sleep(10 * time.Millisecond)

	// Should not panic
}

func TestPrometheusExporter_ExportMetrics(t *testing.T) {
	mockPool := &mockPostgresPool{
		stats: &PoolStats{
			ActiveConnections:   7,
			IdleConnections:     3,
			ConnectionsCreated:  50,
			ConnectionWaitTime:  100 * time.Millisecond,
			TotalQueries:        500,
			QueryExecutionTime:  250 * time.Millisecond,
			ConnectionErrors:    1,
			QueryErrors:         2,
			TimeoutErrors:       0,
		},
	}

	registry := metrics.NewMetricsRegistry("test_prom_export")
	dbMetrics := registry.Infra().DB

	exporter := NewPrometheusExporter(mockPool, dbMetrics)

	// Call exportMetrics directly (private method test via reflection-like approach)
	// In production, this would be tested via integration tests with real Prometheus scraping
	exporter.exportMetrics()

	// Verify that metrics are exported (no panic)
	// In a real integration test, you would scrape the /metrics endpoint
	// and verify the values. Here we just ensure no panic occurs.

	// Test with nil pool (should log warning and not panic)
	exporter.pool = nil
	exporter.exportMetrics()

	// Test with nil dbMetrics (should log warning and not panic)
	exporter.pool = mockPool
	exporter.dbMetrics = nil
	exporter.exportMetrics()
}

func TestPrometheusExporter_ConcurrentAccess(t *testing.T) {
	mockPool := &mockPostgresPool{
		stats: &PoolStats{
			ActiveConnections:   5,
			IdleConnections:     10,
			ConnectionsCreated:  100,
			ConnectionWaitTime:  50 * time.Millisecond,
			TotalQueries:        1000,
			QueryExecutionTime:  500 * time.Millisecond,
			ConnectionErrors:    2,
			QueryErrors:         5,
			TimeoutErrors:       1,
		},
	}

	registry := metrics.NewMetricsRegistry("test_prom_concurrent")
	dbMetrics := registry.Infra().DB

	exporter := NewPrometheusExporter(mockPool, dbMetrics)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start multiple exporters (simulating concurrent access)
	for i := 0; i < 5; i++ {
		go exporter.Start(ctx, 10*time.Millisecond)
	}

	// Wait for concurrent exports
	time.Sleep(100 * time.Millisecond)

	// Stop the exporter
	exporter.Stop()

	// Should not panic or race
}

func BenchmarkPrometheusExporter_ExportMetrics(b *testing.B) {
	mockPool := &mockPostgresPool{
		stats: &PoolStats{
			ActiveConnections:   5,
			IdleConnections:     10,
			ConnectionsCreated:  100,
			ConnectionWaitTime:  50 * time.Millisecond,
			TotalQueries:        1000,
			QueryExecutionTime:  500 * time.Millisecond,
			ConnectionErrors:    2,
			QueryErrors:         5,
			TimeoutErrors:       1,
		},
	}

	registry := metrics.NewMetricsRegistry("bench_prom_export")
	dbMetrics := registry.Infra().DB

	exporter := NewPrometheusExporter(mockPool, dbMetrics)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		exporter.exportMetrics()
	}
}
