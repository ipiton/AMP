// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Health Check Handler
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/database/postgres"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics"
)

// DashboardHealthHandler handles dashboard health check endpoint.
// Provides comprehensive health status for all critical system components.
type DashboardHealthHandler struct {
	// Required dependencies
	dbPool *postgres.PostgresPool
	cache  cache.Cache // optional

	// Optional dependencies
	classificationService services.ClassificationService // optional
	targetDiscovery       publishing.TargetDiscoveryManager // optional
	healthMonitor         publishing.HealthMonitor // optional

	// Observability
	logger  *slog.Logger
	metrics *metrics.MetricsRegistry
	healthMetrics *DashboardHealthMetrics

	// Configuration
	config *HealthCheckConfig
}

// NewDashboardHealthHandler creates a new dashboard health handler.
func NewDashboardHealthHandler(
	dbPool *postgres.PostgresPool,
	cache cache.Cache,
	classificationService services.ClassificationService,
	targetDiscovery publishing.TargetDiscoveryManager,
	healthMonitor publishing.HealthMonitor,
	logger *slog.Logger,
	metricsRegistry *metrics.MetricsRegistry,
) *DashboardHealthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if metricsRegistry == nil {
		metricsRegistry = metrics.NewMetricsRegistry()
	}

	return &DashboardHealthHandler{
		dbPool:                dbPool,
		cache:                 cache,
		classificationService: classificationService,
		targetDiscovery:       targetDiscovery,
		healthMonitor:         healthMonitor,
		logger:                logger,
		metrics:               metricsRegistry,
		healthMetrics:         GetDashboardHealthMetrics(),
		config:                DefaultHealthCheckConfig(),
	}
}

// GetHealth handles GET /api/dashboard/health requests.
// Performs parallel health checks for all system components and returns aggregated status.
func (h *DashboardHealthHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.config.OverallTimeout)
	defer cancel()

	h.logger.Info("Dashboard health check requested",
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"method", r.Method,
		"path", r.URL.Path,
	)

	// Create response structure
	response := DashboardHealthResponse{
		Timestamp: time.Now(),
		Services:  make(map[string]ServiceHealth),
	}

	// Channel for collecting results
	results := make(chan healthCheckResult, 5)

	// Launch parallel health checks
	var wg sync.WaitGroup

	// Database check (critical)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dbCtx, cancel := context.WithTimeout(ctx, h.config.DatabaseTimeout)
		defer cancel()
		h.logger.Debug("Starting database health check",
			"timeout", h.config.DatabaseTimeout,
		)
		health := h.checkDatabaseHealth(dbCtx)
		results <- healthCheckResult{component: "database", health: health}
	}()

	// Redis check
	wg.Add(1)
	go func() {
		defer wg.Done()
		redisCtx, cancel := context.WithTimeout(ctx, h.config.RedisTimeout)
		defer cancel()
		h.logger.Debug("Starting Redis health check",
			"timeout", h.config.RedisTimeout,
		)
		health := h.checkRedisHealth(redisCtx)
		results <- healthCheckResult{component: "redis", health: health}
	}()

	// LLM check (optional)
	if h.classificationService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			llmCtx, cancel := context.WithTimeout(ctx, h.config.LLMTimeout)
			defer cancel()
			h.logger.Debug("Starting LLM service health check",
				"timeout", h.config.LLMTimeout,
			)
			health := h.checkLLMHealth(llmCtx)
			results <- healthCheckResult{component: "llm_service", health: health}
		}()
	} else {
		h.logger.Debug("Skipping LLM service health check (not configured)")
	}

	// Publishing check (optional)
	if h.targetDiscovery != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pubCtx, cancel := context.WithTimeout(ctx, h.config.PublishingTimeout)
			defer cancel()
			h.logger.Debug("Starting publishing system health check",
				"timeout", h.config.PublishingTimeout,
			)
			health := h.checkPublishingHealth(pubCtx)
			results <- healthCheckResult{component: "publishing", health: health}
		}()
	} else {
		h.logger.Debug("Skipping publishing system health check (not configured)")
	}

	// Wait for all checks to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for result := range results {
		response.Services[result.component] = result.health
	}

	// Aggregate overall status
	var statusCode int
	response.Status, statusCode = h.aggregateStatus(response.Services)

	// Collect system metrics (optional, non-blocking)
	if h.config.EnableSystemMetrics {
		response.Metrics = h.collectSystemMetrics(ctx)
	}

	// Record metrics
	h.recordMetrics(response)

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode health response",
			"error", err,
			"status", response.Status,
			"status_code", statusCode,
		)
		// Try to send error response if encoding failed
		http.Error(w, "Internal server error: failed to encode response", http.StatusInternalServerError)
		return
	}

	// Log completion with detailed information
	if response.Status == "unhealthy" {
		h.logger.Error("Dashboard health check completed - unhealthy",
			"status", response.Status,
			"status_code", statusCode,
			"services_checked", len(response.Services),
			"timestamp", response.Timestamp,
		)
	} else if response.Status == "degraded" {
		h.logger.Warn("Dashboard health check completed - degraded",
			"status", response.Status,
			"status_code", statusCode,
			"services_checked", len(response.Services),
			"timestamp", response.Timestamp,
		)
	} else {
		h.logger.Info("Dashboard health check completed - healthy",
			"status", response.Status,
			"status_code", statusCode,
			"services_checked", len(response.Services),
			"timestamp", response.Timestamp,
		)
	}

	// Log component statuses for debugging
	for component, serviceHealth := range response.Services {
		if serviceHealth.Status != "healthy" && serviceHealth.Status != "available" && serviceHealth.Status != "not_configured" {
			h.logger.Debug("Component health status",
				"component", component,
				"status", serviceHealth.Status,
				"latency_ms", serviceHealth.LatencyMS,
				"error", serviceHealth.Error,
			)
		}
	}
}

// checkDatabaseHealth performs health check for PostgreSQL database.
func (h *DashboardHealthHandler) checkDatabaseHealth(ctx context.Context) ServiceHealth {
	start := time.Now()

	health := ServiceHealth{
		Status:  "unhealthy",
		Details: make(map[string]interface{}),
	}

	if h.dbPool == nil {
		health.Status = "not_configured"
		// Don't record metrics for not_configured components
		return health
	}

	// Check connection
	err := h.dbPool.Health(ctx)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		health.LatencyMS = &latency

		// Classify error type
		errMsg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "health check timeout after " + h.config.DatabaseTimeout.String()
			h.logger.Warn("Database health check timeout",
				"timeout", h.config.DatabaseTimeout,
				"latency_ms", latency,
			)
		} else if ctx.Err() == context.Canceled {
			errMsg = "health check cancelled"
			h.logger.Warn("Database health check cancelled",
				"latency_ms", latency,
			)
		} else {
			h.logger.Error("Database health check failed",
				"error", err,
				"latency_ms", latency,
			)
		}

		health.Error = errMsg

		// Record metrics for failed check
		if h.healthMetrics != nil {
			h.healthMetrics.RecordCheck("database", health.Status, time.Since(start))
		}

		return health
	}

	// Get pool statistics
	stats := h.dbPool.Stats()
	health.Details["connection_pool"] = fmt.Sprintf("%d/%d",
		stats.ActiveConnections, stats.TotalConnections)
	health.Details["type"] = "postgresql"

	latency := time.Since(start).Milliseconds()
	health.LatencyMS = &latency
	health.Status = "healthy"

	h.logger.Debug("Database health check passed",
		"latency_ms", latency,
		"connection_pool", health.Details["connection_pool"],
	)

	// Record metrics
	if h.healthMetrics != nil {
		h.healthMetrics.RecordCheck("database", health.Status, time.Since(start))
	}

	return health
}

// checkRedisHealth performs health check for Redis cache.
func (h *DashboardHealthHandler) checkRedisHealth(ctx context.Context) ServiceHealth {
	start := time.Now()

	health := ServiceHealth{
		Status:  "not_configured",
		Details: make(map[string]interface{}),
	}

	if h.cache == nil {
		return health
	}

	// Check connection
	err := h.cache.HealthCheck(ctx)
	if err != nil {
		health.Status = "unhealthy"
		latency := time.Since(start).Milliseconds()
		health.LatencyMS = &latency

		// Classify error type
		errMsg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "health check timeout after " + h.config.RedisTimeout.String()
			h.logger.Warn("Redis health check timeout",
				"timeout", h.config.RedisTimeout,
				"latency_ms", latency,
			)
		} else if ctx.Err() == context.Canceled {
			errMsg = "health check cancelled"
			h.logger.Warn("Redis health check cancelled",
				"latency_ms", latency,
			)
		} else {
			h.logger.Warn("Redis health check failed",
				"error", err,
				"latency_ms", latency,
			)
		}

		health.Error = errMsg

		// Record metrics for failed check
		if h.healthMetrics != nil {
			h.healthMetrics.RecordCheck("redis", health.Status, time.Since(start))
		}

		return health
	}

	// Note: Redis stats collection is optional and may not be available
	// For now, we skip detailed stats collection to avoid interface complexity

	latency := time.Since(start).Milliseconds()
	health.LatencyMS = &latency
	health.Status = "healthy"

	h.logger.Debug("Redis health check passed",
		"latency_ms", latency,
	)

	// Record metrics
	if h.healthMetrics != nil {
		h.healthMetrics.RecordCheck("redis", health.Status, time.Since(start))
	}

	return health
}

// checkLLMHealth performs health check for LLM classification service.
func (h *DashboardHealthHandler) checkLLMHealth(ctx context.Context) ServiceHealth {
	start := time.Now()

	health := ServiceHealth{
		Status:  "not_configured",
		Details: make(map[string]interface{}),
	}

	if h.classificationService == nil {
		return health
	}

	// Check health
	err := h.classificationService.Health(ctx)
	if err != nil {
		health.Status = "unavailable"
		latency := time.Since(start).Milliseconds()
		health.LatencyMS = &latency

		// Classify error type
		errMsg := err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "health check timeout after " + h.config.LLMTimeout.String()
			h.logger.Warn("LLM service health check timeout",
				"timeout", h.config.LLMTimeout,
				"latency_ms", latency,
			)
		} else if ctx.Err() == context.Canceled {
			errMsg = "health check cancelled"
			h.logger.Warn("LLM service health check cancelled",
				"latency_ms", latency,
			)
		} else {
			h.logger.Warn("LLM service health check failed",
				"error", err,
				"latency_ms", latency,
			)
		}

		health.Error = errMsg

		// Record metrics for failed check
		if h.healthMetrics != nil {
			h.healthMetrics.RecordCheck("llm_service", health.Status, time.Since(start))
		}

		return health
	}

	latency := time.Since(start).Milliseconds()
	health.LatencyMS = &latency
	health.Status = "available"

	h.logger.Debug("LLM service health check passed",
		"latency_ms", latency,
	)

	// Record metrics
	if h.healthMetrics != nil {
		h.healthMetrics.RecordCheck("llm_service", health.Status, time.Since(start))
	}

	return health
}

// checkPublishingHealth performs health check for publishing system.
func (h *DashboardHealthHandler) checkPublishingHealth(ctx context.Context) ServiceHealth {
	start := time.Now()

	health := ServiceHealth{
		Status:  "not_configured",
		Details: make(map[string]interface{}),
	}

	if h.targetDiscovery == nil {
		return health
	}

	// Get stats
	stats := h.targetDiscovery.GetStats()
	health.Details["targets_count"] = stats.TotalTargets

	// Check unhealthy targets
	if h.healthMonitor != nil {
		allHealth, err := h.healthMonitor.GetHealth(ctx)
		if err != nil {
			health.Status = "degraded"
			latency := time.Since(start).Milliseconds()
			health.LatencyMS = &latency

			// Classify error type
			errMsg := err.Error()
			if ctx.Err() == context.DeadlineExceeded {
				errMsg = "health check timeout after " + h.config.PublishingTimeout.String()
				h.logger.Warn("Publishing system health check timeout",
					"timeout", h.config.PublishingTimeout,
					"latency_ms", latency,
				)
			} else if ctx.Err() == context.Canceled {
				errMsg = "health check cancelled"
				h.logger.Warn("Publishing system health check cancelled",
					"latency_ms", latency,
				)
			} else {
				h.logger.Warn("Failed to get publishing health status",
					"error", err,
					"latency_ms", latency,
				)
			}

			health.Error = errMsg

			// Record metrics for failed check
			if h.healthMetrics != nil {
				h.healthMetrics.RecordCheck("publishing", health.Status, time.Since(start))
			}

			return health
		}

		unhealthyCount := 0
		for _, targetHealth := range allHealth {
			if targetHealth.Status == publishing.HealthStatusUnhealthy {
				unhealthyCount++
			}
		}
		health.Details["unhealthy_targets"] = unhealthyCount

		if unhealthyCount > 0 {
			health.Status = "degraded"
		} else {
			health.Status = "healthy"
		}
	} else {
		health.Status = "healthy"
	}

	latency := time.Since(start).Milliseconds()
	health.LatencyMS = &latency

	h.logger.Debug("Publishing system health check completed",
		"status", health.Status,
		"latency_ms", latency,
		"targets_count", health.Details["targets_count"],
	)

	// Record metrics
	if h.healthMetrics != nil {
		h.healthMetrics.RecordCheck("publishing", health.Status, time.Since(start))
	}

	return health
}

// collectSystemMetrics collects system-level metrics (CPU, memory, request rate, error rate).
// This is optional and non-blocking.
func (h *DashboardHealthHandler) collectSystemMetrics(ctx context.Context) *SystemMetrics {
	// TODO: Implement system metrics collection if needed
	// For now, return nil (metrics collection is optional)
	return nil
}

// aggregateStatus aggregates component statuses into overall system status.
// Returns (status, httpStatusCode).
func (h *DashboardHealthHandler) aggregateStatus(services map[string]ServiceHealth) (string, int) {
	// Check database first (critical)
	dbHealth, dbExists := services["database"]
	if !dbExists || dbHealth.Status == "not_configured" {
		// Database is required
		return "unhealthy", http.StatusServiceUnavailable
	}
	if dbHealth.Status == "unhealthy" {
		return "unhealthy", http.StatusServiceUnavailable
	}

	// Check other services
	hasDegraded := false
	hasUnhealthy := false

	for component, health := range services {
		if component == "database" {
			continue // Already checked
		}

		switch health.Status {
		case "unhealthy":
			// Redis unhealthy might be critical depending on configuration
			if component == "redis" {
				hasDegraded = true // Degrade, but don't fail
			} else {
				hasUnhealthy = true
			}
		case "degraded", "unavailable":
			hasDegraded = true
		}
	}

	// Determine overall status
	if hasUnhealthy {
		return "unhealthy", http.StatusServiceUnavailable
	}
	if hasDegraded {
		return "degraded", http.StatusOK
	}

	return "healthy", http.StatusOK
}

// recordMetrics records Prometheus metrics for health checks.
func (h *DashboardHealthHandler) recordMetrics(response DashboardHealthResponse) {
	if h.healthMetrics == nil {
		return
	}

	// Record overall status
	h.healthMetrics.RecordOverallStatus(response.Status)

	// Record component statuses (already recorded in individual check methods)
	// This is a fallback for any components that weren't recorded individually
	for component, health := range response.Services {
		if health.LatencyMS != nil {
			duration := time.Duration(*health.LatencyMS) * time.Millisecond
			h.healthMetrics.RecordCheck(component, health.Status, duration)
		}
	}

	h.logger.Debug("Health check metrics recorded",
		"overall_status", response.Status,
		"services_checked", len(response.Services),
	)
}
