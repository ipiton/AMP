// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Data Models
//
// This package provides comprehensive health check endpoint for dashboard
// that performs parallel health checks for all critical system components
// (Database, Redis, LLM Service, Publishing System) with graceful degradation.
package handlers

import "time"

// DashboardHealthResponse represents the health check response for dashboard.
//
// The response includes overall system status, individual component health statuses,
// and optional system-level metrics (CPU, memory, request rate, error rate).
//
// Example:
//
//	{
//	  "status": "healthy",
//	  "timestamp": "2025-11-21T19:30:00Z",
//	  "services": {
//	    "database": { "status": "healthy", "latency_ms": 5 },
//	    "redis": { "status": "healthy", "latency_ms": 2 }
//	  }
//	}
type DashboardHealthResponse struct {
	// Status is the overall system health status.
	// Possible values: "healthy", "degraded", "unhealthy"
	Status string `json:"status"` // healthy/degraded/unhealthy

	// Timestamp is the time when the health check was performed (ISO 8601 format).
	Timestamp time.Time `json:"timestamp"`

	// Services is a map of component names to their health statuses.
	// Keys: "database", "redis", "llm_service", "publishing"
	Services map[string]ServiceHealth `json:"services"`

	// Metrics contains optional system-level metrics (CPU, memory, request rate, error rate).
	// Only included if EnableSystemMetrics is true in HealthCheckConfig.
	Metrics *SystemMetrics `json:"metrics,omitempty"`
}

// ServiceHealth represents the health status of a single service component.
//
// Each component can have one of the following statuses:
//   - "healthy": Component is operational
//   - "degraded": Component has issues but is partially functional
//   - "unhealthy": Component is not operational
//   - "not_configured": Component is not configured (optional component)
//   - "available": Component is available (used for LLM service)
//   - "unavailable": Component is not available (used for LLM service)
//
// Example:
//
//	{
//	  "status": "healthy",
//	  "latency_ms": 5,
//	  "details": { "connection_pool": "10/20", "type": "postgresql" }
//	}
type ServiceHealth struct {
	// Status is the component health status.
	// Possible values: "healthy", "degraded", "unhealthy", "not_configured", "available", "unavailable"
	Status string `json:"status"` // healthy/unhealthy/degraded/not_configured/available/unavailable

	// LatencyMS is the health check latency in milliseconds (optional).
	// Only included if health check completed successfully or failed after timeout.
	LatencyMS *int64 `json:"latency_ms,omitempty"`

	// Details contains component-specific details (optional).
	// For database: connection_pool, type
	// For publishing: targets_count, healthy_targets, unhealthy_targets
	Details map[string]interface{} `json:"details,omitempty"`

	// Error contains error message if component is unhealthy or degraded (optional).
	Error string `json:"error,omitempty"`
}

// SystemMetrics represents system-level resource usage metrics.
//
// All values are normalized to 0.0-1.0 range (except RequestRate which is requests per second).
// Only included if EnableSystemMetrics is true in HealthCheckConfig.
type SystemMetrics struct {
	// CPUUsage is the CPU usage as a fraction (0.0-1.0).
	// 0.0 = 0%, 1.0 = 100%
	CPUUsage float64 `json:"cpu_usage,omitempty"` // 0.0-1.0

	// MemoryUsage is the memory usage as a fraction (0.0-1.0).
	// 0.0 = 0%, 1.0 = 100%
	MemoryUsage float64 `json:"memory_usage,omitempty"` // 0.0-1.0

	// RequestRate is the HTTP request rate per second.
	RequestRate float64 `json:"request_rate,omitempty"` // requests per second

	// ErrorRate is the HTTP error rate as a fraction (0.0-1.0).
	// 0.0 = 0%, 1.0 = 100%
	ErrorRate float64 `json:"error_rate,omitempty"` // 0.0-1.0
}

// HealthCheckConfig contains configuration for health checks.
//
// All timeout values control how long each component health check can take.
// If a component doesn't respond within its timeout, it's marked as degraded/unhealthy.
//
// Example:
//
//	config := DefaultHealthCheckConfig()
//	config.DatabaseTimeout = 10 * time.Second // Increase database timeout
//	config.EnableSystemMetrics = true          // Enable system metrics collection
type HealthCheckConfig struct {
	// DatabaseTimeout is the timeout for database health check (default: 5s).
	DatabaseTimeout time.Duration

	// RedisTimeout is the timeout for Redis cache health check (default: 2s).
	RedisTimeout time.Duration

	// LLMTimeout is the timeout for LLM service health check (default: 3s).
	LLMTimeout time.Duration

	// PublishingTimeout is the timeout for publishing system health check (default: 5s).
	PublishingTimeout time.Duration

	// OverallTimeout is the overall timeout for all health checks combined (default: 10s).
	// This is the maximum time the entire health check endpoint can take.
	OverallTimeout time.Duration

	// EnableSystemMetrics enables collection of system-level metrics (CPU, memory, etc.).
	// Default: false (disabled for performance)
	EnableSystemMetrics bool
}

// DefaultHealthCheckConfig returns default health check configuration.
//
// Default timeouts:
//   - Database: 5 seconds
//   - Redis: 2 seconds
//   - LLM Service: 3 seconds
//   - Publishing: 5 seconds
//   - Overall: 10 seconds
//
// System metrics are disabled by default for performance.
func DefaultHealthCheckConfig() *HealthCheckConfig {
	return &HealthCheckConfig{
		DatabaseTimeout:    5 * time.Second,
		RedisTimeout:      2 * time.Second,
		LLMTimeout:        3 * time.Second,
		PublishingTimeout: 5 * time.Second,
		OverallTimeout:    10 * time.Second,
		EnableSystemMetrics: false,
	}
}

// healthCheckResult is used internally for collecting parallel health check results.
type healthCheckResult struct {
	component string
	health    ServiceHealth
}
