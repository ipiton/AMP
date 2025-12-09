package v2

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const databaseSubsystem = "database"

// DatabaseMetrics provides metrics for database operations.
//
// This struct consolidates database metrics from:
//   - pkg/metrics/metrics.go (DatabaseMetrics)
//
// Migration:
//
//	Old: databaseMetrics.QueryDuration.WithLabelValues("select").Observe(0.05)
//	New: registry.Database.RecordQuery("select", true, 0.05*time.Second)
type DatabaseMetrics struct {
	// ========================================================================
	// Query Metrics
	// ========================================================================

	// queryTotal counts total queries by query type and status.
	// Labels: query_type (select/insert/update/delete/transaction), status (success/error)
	queryTotal *prometheus.CounterVec

	// queryDurationSeconds measures query duration by query type.
	// Labels: query_type
	queryDurationSeconds *prometheus.HistogramVec

	// queryErrorsTotal counts query errors by query type and error type.
	// Labels: query_type, error_type (connection/timeout/deadlock/constraint/syntax/unknown)
	queryErrorsTotal *prometheus.CounterVec

	// ========================================================================
	// Connection Pool Metrics
	// ========================================================================

	// connectionsActive tracks active connections.
	connectionsActive prometheus.Gauge

	// connectionsIdle tracks idle connections.
	connectionsIdle prometheus.Gauge

	// connectionsTotal counts total connections by status.
	// Labels: status (opened/closed/failed)
	connectionsTotal *prometheus.CounterVec

	// connectionWaitSeconds measures time waiting for a connection from the pool.
	connectionWaitSeconds prometheus.Histogram

	// poolSize tracks the current connection pool size.
	poolSize prometheus.Gauge

	// ========================================================================
	// Transaction Metrics
	// ========================================================================

	// transactionsTotal counts transactions by status.
	// Labels: status (committed/rolledback)
	transactionsTotal *prometheus.CounterVec

	// transactionDurationSeconds measures transaction duration.
	transactionDurationSeconds prometheus.Histogram
}

// NewDatabaseMetrics creates and registers all database metrics.
func NewDatabaseMetrics(registerer prometheus.Registerer) *DatabaseMetrics {
	m := &DatabaseMetrics{}

	// Query Metrics
	m.queryTotal = newCounterVec(registerer, databaseSubsystem,
		"queries_total",
		"Total database queries by query type and status",
		[]string{"query_type", "status"})

	m.queryDurationSeconds = newHistogramVec(registerer, databaseSubsystem,
		"query_duration_seconds",
		"Database query duration in seconds",
		DatabaseBuckets,
		[]string{"query_type"})

	m.queryErrorsTotal = newCounterVec(registerer, databaseSubsystem,
		"query_errors_total",
		"Database query errors by query type and error type",
		[]string{"query_type", "error_type"})

	// Connection Pool Metrics
	m.connectionsActive = newGauge(registerer, databaseSubsystem,
		"connections_active",
		"Number of active database connections")

	m.connectionsIdle = newGauge(registerer, databaseSubsystem,
		"connections_idle",
		"Number of idle database connections")

	m.connectionsTotal = newCounterVec(registerer, databaseSubsystem,
		"connections_total",
		"Total database connections by status",
		[]string{"status"})

	m.connectionWaitSeconds = newHistogram(registerer, databaseSubsystem,
		"connection_wait_seconds",
		"Time spent waiting for a database connection",
		[]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5})

	m.poolSize = newGauge(registerer, databaseSubsystem,
		"pool_size",
		"Current database connection pool size")

	// Transaction Metrics
	m.transactionsTotal = newCounterVec(registerer, databaseSubsystem,
		"transactions_total",
		"Total database transactions by status",
		[]string{"status"})

	m.transactionDurationSeconds = newHistogram(registerer, databaseSubsystem,
		"transaction_duration_seconds",
		"Database transaction duration in seconds",
		DatabaseBuckets)

	return m
}

// ============================================================================
// Query Methods
// ============================================================================

// RecordQuery records a database query.
func (m *DatabaseMetrics) RecordQuery(queryType string, success bool, duration time.Duration) {
	status := "error"
	if success {
		status = "success"
	}
	m.queryTotal.WithLabelValues(queryType, status).Inc()
	m.queryDurationSeconds.WithLabelValues(queryType).Observe(duration.Seconds())
}

// RecordQueryError records a query error with specific error type.
func (m *DatabaseMetrics) RecordQueryError(queryType, errorType string) {
	m.queryErrorsTotal.WithLabelValues(queryType, errorType).Inc()
}

// ============================================================================
// Connection Pool Methods
// ============================================================================

// SetConnectionCounts sets the active and idle connection counts.
func (m *DatabaseMetrics) SetConnectionCounts(active, idle int) {
	m.connectionsActive.Set(float64(active))
	m.connectionsIdle.Set(float64(idle))
}

// RecordConnectionOpened records a connection being opened.
func (m *DatabaseMetrics) RecordConnectionOpened() {
	m.connectionsTotal.WithLabelValues("opened").Inc()
}

// RecordConnectionClosed records a connection being closed.
func (m *DatabaseMetrics) RecordConnectionClosed() {
	m.connectionsTotal.WithLabelValues("closed").Inc()
}

// RecordConnectionFailed records a failed connection attempt.
func (m *DatabaseMetrics) RecordConnectionFailed() {
	m.connectionsTotal.WithLabelValues("failed").Inc()
}

// RecordConnectionWait records time spent waiting for a connection.
func (m *DatabaseMetrics) RecordConnectionWait(duration time.Duration) {
	m.connectionWaitSeconds.Observe(duration.Seconds())
}

// SetPoolSize sets the connection pool size.
func (m *DatabaseMetrics) SetPoolSize(size int) {
	m.poolSize.Set(float64(size))
}

// ============================================================================
// Transaction Methods
// ============================================================================

// RecordTransactionCommit records a committed transaction.
func (m *DatabaseMetrics) RecordTransactionCommit(duration time.Duration) {
	m.transactionsTotal.WithLabelValues("committed").Inc()
	m.transactionDurationSeconds.Observe(duration.Seconds())
}

// RecordTransactionRollback records a rolled back transaction.
func (m *DatabaseMetrics) RecordTransactionRollback(duration time.Duration) {
	m.transactionsTotal.WithLabelValues("rolledback").Inc()
	m.transactionDurationSeconds.Observe(duration.Seconds())
}
