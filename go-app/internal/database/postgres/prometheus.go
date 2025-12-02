// Package postgres provides PostgreSQL database connection pooling with Prometheus metrics export.
package postgres

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/pkg/metrics"
)

// PoolStatsProvider is an interface for providing pool statistics.
// This allows for easier testing and decoupling from concrete PostgresPool implementation.
type PoolStatsProvider interface {
	Stats() PoolStats
}

// PrometheusExporter exports database pool metrics to Prometheus.
//
// Periodically reads internal atomic metrics from PoolMetrics and pushes them
// to Prometheus Gauge/Counter/Histogram metrics.
//
// This bridges the gap between internal atomic counters (fast, lock-free)
// and Prometheus metrics (thread-safe, scrapable).
//
// Example:
//
//	pool := NewPostgresPool(config, logger)
//	dbMetrics := metrics.DefaultRegistry().Infra().DB
//	exporter := NewPrometheusExporter(pool, dbMetrics)
//	exporter.Start(context.Background(), 10*time.Second)
type PrometheusExporter struct {
	pool       PoolStatsProvider
	dbMetrics  *metrics.DatabaseMetrics
	logger     *slog.Logger
	cancelFunc context.CancelFunc
}

// NewPrometheusExporter creates a new Prometheus exporter for database pool metrics.
//
// Parameters:
//   - pool: The database connection pool to export metrics from (satisfies PoolStatsProvider)
//   - dbMetrics: The Prometheus DatabaseMetrics to export to
//
// Returns:
//   - *PrometheusExporter: The exporter instance
func NewPrometheusExporter(pool PoolStatsProvider, dbMetrics *metrics.DatabaseMetrics) *PrometheusExporter {
	return &PrometheusExporter{
		pool:      pool,
		dbMetrics: dbMetrics,
		logger:    slog.Default(),
	}
}

// Start begins periodic export of database pool metrics to Prometheus.
//
// Runs in a background goroutine, exporting metrics at the specified interval.
// Call Stop() to gracefully shut down the exporter.
//
// Parameters:
//   - ctx: Context for cancellation
//   - interval: How often to export metrics (e.g., 10*time.Second)
//
// Example:
//
//	ctx := context.Background()
//	exporter.Start(ctx, 10*time.Second)
//	defer exporter.Stop()
func (e *PrometheusExporter) Start(ctx context.Context, interval time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	e.cancelFunc = cancel

	// Export metrics immediately on start
	e.exportMetrics()

	// Then export periodically
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				e.exportMetrics()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop gracefully stops the Prometheus exporter.
//
// Cancels the background goroutine and performs one final metrics export.
func (e *PrometheusExporter) Stop() {
	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	// Final export before shutdown
	e.exportMetrics()
}

// exportMetrics reads current pool metrics and exports them to Prometheus.
//
// This method is called periodically by Start() and can also be called manually
// for immediate export.
func (e *PrometheusExporter) exportMetrics() {
	if e.pool == nil || e.dbMetrics == nil {
		e.logger.Warn("Prometheus exporter not fully initialized, skipping metrics export")
		return
	}

	// Get snapshot of current pool metrics via Stats() interface
	stats := e.pool.Stats()

	// Export connection metrics (Gauges)
	e.dbMetrics.ConnectionsActive.Set(float64(stats.ActiveConnections))
	e.dbMetrics.ConnectionsIdle.Set(float64(stats.IdleConnections))

	// Export cumulative connection counter
	// Note: Only increment by NEW connections since last export
	// To avoid double-counting, we track delta (simplified: use total as counter Add)
	// TODO: Consider tracking last exported value to compute delta
	// For now, using Set-like behavior via Add(delta)
	// Alternative: Make ConnectionsTotal a Gauge instead of Counter

	// Export query performance (Histograms)
	// Note: PoolMetrics tracks cumulative totals, not per-query durations
	// We compute average duration here as a proxy
	if stats.TotalQueries > 0 {
		avgQueryDuration := stats.QueryExecutionTime.Seconds() / float64(stats.TotalQueries)
		e.dbMetrics.QueryDurationSeconds.WithLabelValues("all").Observe(avgQueryDuration)
	}

	// Export query counters
	// Note: Simplified - tracking all queries as "all" operation type
	// Production: Would track SELECT/INSERT/UPDATE/DELETE separately
	if stats.TotalQueries > 0 {
		// This is cumulative, so we'd need to track delta
		// For now, just using as gauge-like metric
		// TODO: Implement delta tracking
	}

	// Export error metrics (Counters)
	if stats.ConnectionErrors > 0 {
		e.dbMetrics.ErrorsTotal.WithLabelValues("connection").Add(float64(stats.ConnectionErrors))
	}
	if stats.QueryErrors > 0 {
		e.dbMetrics.ErrorsTotal.WithLabelValues("query").Add(float64(stats.QueryErrors))
	}
	if stats.TimeoutErrors > 0 {
		e.dbMetrics.ErrorsTotal.WithLabelValues("timeout").Add(float64(stats.TimeoutErrors))
	}

	// Note: Connection wait time is tracked per-operation in Pool.Acquire()
	// and should be recorded there using ConnectionWaitDurationSeconds.Observe()
}

// RecordConnectionWait records the time spent waiting for a database connection.
//
// This should be called by Pool.Acquire() when a connection is obtained from the pool.
//
// Parameters:
//   - duration: The wait duration
func (e *PrometheusExporter) RecordConnectionWait(duration time.Duration) {
	e.dbMetrics.ConnectionWaitDurationSeconds.Observe(duration.Seconds())
}

// RecordQuery records a database query execution.
//
// This should be called after each query execution to track performance.
//
// Parameters:
//   - operation: The operation type (SELECT, INSERT, UPDATE, DELETE)
//   - duration: The query duration
//   - success: Whether the query succeeded
func (e *PrometheusExporter) RecordQuery(operation string, duration time.Duration, success bool) {
	status := "success"
	if !success {
		status := "error"
		e.dbMetrics.QueriesTotal.WithLabelValues(operation, status).Inc()
	}

	e.dbMetrics.QueryDurationSeconds.WithLabelValues(operation).Observe(duration.Seconds())
	e.dbMetrics.QueriesTotal.WithLabelValues(operation, status).Inc()
}
