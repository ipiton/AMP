package postgres

import (
	"sync/atomic"
	"time"
)

// PoolMetrics содержит метрики для мониторинга connection pool
type PoolMetrics struct {
	// Connection statistics
	ActiveConnections    atomic.Int32
	IdleConnections      atomic.Int32
	TotalConnections     atomic.Int64
	ConnectionsCreated   atomic.Int64
	ConnectionsDestroyed atomic.Int64

	// Performance metrics
	ConnectionWaitTime atomic.Int64 // nanoseconds
	QueryExecutionTime atomic.Int64 // nanoseconds
	TotalQueries       atomic.Int64

	// Error tracking
	ConnectionErrors atomic.Int64
	QueryErrors      atomic.Int64
	TimeoutErrors    atomic.Int64

	// Health status
	LastHealthCheck     atomic.Int64 // unix timestamp
	HealthCheckFailures atomic.Int64
	IsHealthy           atomic.Bool

	// Additional metrics
	SuccessfulConnections atomic.Int64
	FailedConnections     atomic.Int64
}

// PoolStats содержит snapshot метрик для отчета
type PoolStats struct {
	ActiveConnections     int32
	IdleConnections       int32
	TotalConnections      int64
	ConnectionsCreated    int64
	ConnectionsDestroyed  int64
	ConnectionWaitTime    time.Duration
	QueryExecutionTime    time.Duration
	TotalQueries          int64
	ConnectionErrors      int64
	QueryErrors           int64
	TimeoutErrors         int64
	LastHealthCheck       time.Time
	HealthCheckFailures   int64
	IsHealthy             bool
	SuccessfulConnections int64
	FailedConnections     int64
}

// NewPoolMetrics создает новую структуру метрик
func NewPoolMetrics() *PoolMetrics {
	metrics := &PoolMetrics{}
	metrics.LastHealthCheck.Store(time.Now().Unix())
	metrics.IsHealthy.Store(true)
	return metrics
}

// Snapshot возвращает snapshot текущих метрик
func (m *PoolMetrics) Snapshot() PoolStats {
	return PoolStats{
		ActiveConnections:     m.ActiveConnections.Load(),
		IdleConnections:       m.IdleConnections.Load(),
		TotalConnections:      m.TotalConnections.Load(),
		ConnectionsCreated:    m.ConnectionsCreated.Load(),
		ConnectionsDestroyed:  m.ConnectionsDestroyed.Load(),
		ConnectionWaitTime:    time.Duration(m.ConnectionWaitTime.Load()),
		QueryExecutionTime:    time.Duration(m.QueryExecutionTime.Load()),
		TotalQueries:          m.TotalQueries.Load(),
		ConnectionErrors:      m.ConnectionErrors.Load(),
		QueryErrors:           m.QueryErrors.Load(),
		TimeoutErrors:         m.TimeoutErrors.Load(),
		LastHealthCheck:       time.Unix(m.LastHealthCheck.Load(), 0),
		HealthCheckFailures:   m.HealthCheckFailures.Load(),
		IsHealthy:             m.IsHealthy.Load(),
		SuccessfulConnections: m.SuccessfulConnections.Load(),
		FailedConnections:     m.FailedConnections.Load(),
	}
}

// Reset сбрасывает метрики (полезно для тестирования)
func (m *PoolMetrics) Reset() {
	m.ActiveConnections.Store(0)
	m.IdleConnections.Store(0)
	m.TotalConnections.Store(0)
	m.ConnectionsCreated.Store(0)
	m.ConnectionsDestroyed.Store(0)
	m.ConnectionWaitTime.Store(0)
	m.QueryExecutionTime.Store(0)
	m.TotalQueries.Store(0)
	m.ConnectionErrors.Store(0)
	m.QueryErrors.Store(0)
	m.TimeoutErrors.Store(0)
	m.LastHealthCheck.Store(time.Now().Unix())
	m.HealthCheckFailures.Store(0)
	m.IsHealthy.Store(true)
	m.SuccessfulConnections.Store(0)
	m.FailedConnections.Store(0)
}

// RecordConnectionWait записывает время ожидания соединения
func (m *PoolMetrics) RecordConnectionWait(duration time.Duration) {
	m.ConnectionWaitTime.Add(duration.Nanoseconds())
}

// RecordQueryExecution записывает время выполнения запроса
func (m *PoolMetrics) RecordQueryExecution(duration time.Duration) {
	m.QueryExecutionTime.Add(duration.Nanoseconds())
	m.TotalQueries.Add(1)
}

// RecordConnectionError записывает ошибку соединения
func (m *PoolMetrics) RecordConnectionError() {
	m.ConnectionErrors.Add(1)
	m.FailedConnections.Add(1)
}

// RecordQueryError записывает ошибку запроса
func (m *PoolMetrics) RecordQueryError() {
	m.QueryErrors.Add(1)
}

// RecordTimeoutError записывает ошибку таймаута
func (m *PoolMetrics) RecordTimeoutError() {
	m.TimeoutErrors.Add(1)
}

// RecordSuccessfulConnection записывает успешное соединение
func (m *PoolMetrics) RecordSuccessfulConnection() {
	m.SuccessfulConnections.Add(1)
}

// UpdateConnectionStats обновляет статистику соединений
func (m *PoolMetrics) UpdateConnectionStats(active, idle int32, total int64) {
	m.ActiveConnections.Store(active)
	m.IdleConnections.Store(idle)
	m.TotalConnections.Store(total)
}

// RecordHealthCheck записывает результат health check
func (m *PoolMetrics) RecordHealthCheck(success bool) {
	m.LastHealthCheck.Store(time.Now().Unix())
	if !success {
		m.HealthCheckFailures.Add(1)
		m.IsHealthy.Store(false)
	} else {
		m.IsHealthy.Store(true)
	}
}

// RecordConnectionLifecycle записывает события жизненного цикла соединений
func (m *PoolMetrics) RecordConnectionLifecycle(created, destroyed int64) {
	m.ConnectionsCreated.Add(created)
	m.ConnectionsDestroyed.Add(destroyed)
}

// GetSuccessRate возвращает процент успешных операций
func (m *PoolMetrics) GetSuccessRate() float64 {
	total := m.TotalQueries.Load() + m.ConnectionErrors.Load() + m.QueryErrors.Load()
	if total == 0 {
		return 100.0
	}
	successful := m.TotalQueries.Load()
	return float64(successful) / float64(total) * 100.0
}

// GetAverageQueryTime возвращает среднее время выполнения запроса
func (m *PoolMetrics) GetAverageQueryTime() time.Duration {
	totalQueries := m.TotalQueries.Load()
	if totalQueries == 0 {
		return 0
	}
	return time.Duration(m.QueryExecutionTime.Load() / totalQueries)
}

// GetAverageConnectionWait возвращает среднее время ожидания соединения
func (m *PoolMetrics) GetAverageConnectionWait() time.Duration {
	totalConns := m.ConnectionsCreated.Load()
	if totalConns == 0 {
		return 0
	}
	return time.Duration(m.ConnectionWaitTime.Load() / totalConns)
}
