package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/business/publishing"
)

// MetricsCollectorInterface abstracts metrics collector for testing.
type MetricsCollectorInterface interface {
	CollectAll(ctx context.Context) *publishing.MetricsSnapshot
}

// PublishingStatsHandler handles HTTP requests for publishing metrics & stats.
//
// This handler provides 6 REST endpoints:
//   1. GET  /api/v1/publishing/stats         - Aggregated statistics (v1, backward compatibility)
//   2. GET  /api/v2/publishing/metrics       - Raw metrics snapshot
//   3. GET  /api/v2/publishing/stats         - Aggregated statistics (v2, enhanced)
//   4. GET  /api/v2/publishing/stats/{target} - Per-target statistics
//   5. GET  /api/v2/publishing/health        - System health summary
//   6. GET  /api/v2/publishing/trends        - Trend analysis
//
// Query Parameters (v2 only):
//   - filter: Filter by type or status (e.g., "type:rootly", "status:healthy")
//   - group_by: Group by field ("type", "status", "target")
//   - format: Response format ("json" or "prometheus")
//
// Performance Target: <10ms total response time
//
// Thread-Safe: Yes (PublishingMetricsCollector is thread-safe)
type PublishingStatsHandler struct {
	collector      MetricsCollectorInterface
	trendDetector  *publishing.TrendDetector
	logger         *slog.Logger
}

// NewPublishingStatsHandler creates a new handler.
func NewPublishingStatsHandler(
	collector *publishing.PublishingMetricsCollector,
	trendDetector *publishing.TrendDetector,
	logger *slog.Logger,
) *PublishingStatsHandler {
	return &PublishingStatsHandler{
		collector:     collector,
		trendDetector: trendDetector,
		logger:        logger,
	}
}

// NewPublishingStatsHandlerWithCollector creates a handler with custom collector interface.
// Used for testing with mock collectors.
func NewPublishingStatsHandlerWithCollector(
	collector MetricsCollectorInterface,
	logger *slog.Logger,
) *PublishingStatsHandler {
	return &PublishingStatsHandler{
		collector: collector,
		logger:    logger,
	}
}

// MockCollectorForHandler is a mock implementation of MetricsCollectorInterface for testing.
type MockCollectorForHandler struct {
	CollectAllFunc        func(ctx context.Context) *publishing.MetricsSnapshot
	GetCollectorCountFunc func() int
	GetCollectorNamesFunc func() []string
}

func (m *MockCollectorForHandler) CollectAll(ctx context.Context) *publishing.MetricsSnapshot {
	if m.CollectAllFunc != nil {
		return m.CollectAllFunc(ctx)
	}
	return &publishing.MetricsSnapshot{
		Timestamp:           time.Now(),
		Metrics:             make(map[string]float64),
		CollectionDuration:  0,
		AvailableCollectors: []string{},
		Errors:              make(map[string]error),
	}
}

// ============================================================================
// Response Models
// ============================================================================

// MetricsResponse represents raw metrics snapshot response.
type MetricsResponse struct {
	Timestamp           time.Time          `json:"timestamp"`
	CollectionDuration  string             `json:"collection_duration_ms"`
	MetricsCount        int                `json:"metrics_count"`
	Metrics             map[string]float64 `json:"metrics"`
	AvailableCollectors []string           `json:"available_collectors"`
	Errors              map[string]string  `json:"errors,omitempty"`
}

// StatsResponse represents aggregated statistics (v2 format).
type StatsResponse struct {
	Timestamp   time.Time          `json:"timestamp"`
	System      SystemStats        `json:"system"`
	TargetStats map[string]float64 `json:"target_stats"`
	QueueStats  map[string]float64 `json:"queue_stats"`
}

// StatsResponseV1 represents aggregated statistics (v1 format, backward compatibility).
type StatsResponseV1 struct {
	TotalTargets     int            `json:"total_targets"`
	EnabledTargets   int            `json:"enabled_targets"`
	TargetsByType    map[string]int `json:"targets_by_type"`
	QueueSize        int            `json:"queue_size"`
	QueueCapacity    int            `json:"queue_capacity"`
	QueueUtilization float64        `json:"queue_utilization_percent"`
}

// StatsQueryParams represents query parameters for stats endpoint.
type StatsQueryParams struct {
	Filter  string // "type:rootly" or "status:healthy"
	GroupBy string // "type", "status", "target"
	Format  string // "json" (default) or "prometheus"
}

// SystemStats represents system-wide statistics.
type SystemStats struct {
	TotalTargets     int     `json:"total_targets"`
	HealthyTargets   int     `json:"healthy_targets"`
	UnhealthyTargets int     `json:"unhealthy_targets"`
	SuccessRate      float64 `json:"success_rate_percent"`
	QueueSize        int     `json:"queue_size"`
	QueueCapacity    int     `json:"queue_capacity"`
}

// PublishingHealthResponse represents publishing system health check summary.
type PublishingHealthResponse struct {
	Status    string            `json:"status"` // "healthy", "degraded", "unhealthy"
	Timestamp time.Time         `json:"timestamp"`
	Checks    map[string]string `json:"checks"`
	Message   string            `json:"message,omitempty"`
}

// ============================================================================
// Endpoint 1: GET /api/v2/publishing/metrics (Raw Metrics)
// ============================================================================

// GetMetrics handles GET /api/v2/publishing/metrics
//
// This endpoint returns raw metrics snapshot from all collectors.
//
// Response Example:
//
//	{
//	  "timestamp": "2025-11-12T10:30:00Z",
//	  "collection_duration_ms": "0.085",
//	  "metrics_count": 42,
//	  "metrics": {
//	    "health_status{target=\"rootly-prod\",type=\"rootly\"}": 1.0,
//	    "queue_size_total": 15.0,
//	    "targets_total": 10.0
//	  },
//	  "available_collectors": ["health", "refresh", "discovery", "queue"]
//	}
func (h *PublishingStatsHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	startTime := time.Now()

	// Collect metrics from all collectors
	snapshot := h.collector.CollectAll(ctx)

	// Convert errors map to string map for JSON
	errorsMap := make(map[string]string)
	for name, err := range snapshot.Errors {
		errorsMap[name] = err.Error()
	}

	response := MetricsResponse{
		Timestamp:           snapshot.Timestamp,
		CollectionDuration:  formatDuration(snapshot.CollectionDuration),
		MetricsCount:        len(snapshot.Metrics),
		Metrics:             snapshot.Metrics,
		AvailableCollectors: snapshot.AvailableCollectors,
		Errors:              errorsMap,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode metrics response", "error", err)
	}

	totalDuration := time.Since(startTime)
	h.logger.Debug("Metrics endpoint called",
		"metrics_count", len(snapshot.Metrics),
		"collection_duration", snapshot.CollectionDuration,
		"total_duration", totalDuration,
	)
}

// ============================================================================
// Endpoint 1: GET /api/v1/publishing/stats (Backward Compatibility)
// ============================================================================

// GetStatsV1 handles GET /api/v1/publishing/stats
//
// This endpoint returns aggregated statistics in v1 format for backward compatibility.
//
// Response Example:
//
//	{
//	  "total_targets": 10,
//	  "enabled_targets": 8,
//	  "targets_by_type": {
//	    "rootly": 5,
//	    "slack": 3,
//	    "pagerduty": 2
//	  },
//	  "queue_size": 15,
//	  "queue_capacity": 1000,
//	  "queue_utilization_percent": 1.5
//	}
func (h *PublishingStatsHandler) GetStatsV1(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Collect metrics
	snapshot := h.collector.CollectAll(ctx)

	// Calculate targets by type
	targetsByType := make(map[string]int)
	for key, value := range snapshot.Metrics {
		if strings.Contains(key, "targets_by_type") || strings.Contains(key, "target_type") {
			// Extract type from metric key
			if strings.Contains(key, "rootly") {
				targetsByType["rootly"] += int(value)
			} else if strings.Contains(key, "slack") {
				targetsByType["slack"] += int(value)
			} else if strings.Contains(key, "pagerduty") {
				targetsByType["pagerduty"] += int(value)
			} else if strings.Contains(key, "webhook") {
				targetsByType["webhook"] += int(value)
			}
		}
	}

	totalTargets := int(getMetricValue(snapshot.Metrics, "targets_total"))
	healthyTargets := countHealthyTargets(snapshot.Metrics)
	enabledTargets := healthyTargets // Approximation for v1 compatibility

	queueSize := int(getMetricValue(snapshot.Metrics, "queue_size_total"))
	queueCapacity := int(getMetricValue(snapshot.Metrics, "queue_capacity"))
	queueUtilization := 0.0
	if queueCapacity > 0 {
		queueUtilization = float64(queueSize) / float64(queueCapacity) * 100.0
	}

	response := StatsResponseV1{
		TotalTargets:     totalTargets,
		EnabledTargets:   enabledTargets,
		TargetsByType:    targetsByType,
		QueueSize:        queueSize,
		QueueCapacity:    queueCapacity,
		QueueUtilization: queueUtilization,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode stats v1 response", "error", err)
	}

	// Enhanced observability logging
	duration := time.Since(startTime)
	h.logger.Info("Stats v1 endpoint called",
		"total_targets", totalTargets,
		"enabled_targets", enabledTargets,
		"queue_size", queueSize,
		"queue_capacity", queueCapacity,
		"queue_utilization", queueUtilization,
		"duration_ms", duration.Milliseconds(),
		"collection_duration_us", snapshot.CollectionDuration.Microseconds(),
		"metrics_count", len(snapshot.Metrics),
	)
}

// ============================================================================
// Endpoint 2: GET /api/v2/publishing/stats (Aggregated Stats - Enhanced)
// ============================================================================

// GetStats handles GET /api/v2/publishing/stats
//
// This endpoint returns aggregated statistics computed from raw metrics.
// Supports query parameters: filter, group_by, format
//
// Query Parameters:
//   - filter: Filter by type or status (e.g., "type:rootly", "status:healthy")
//   - group_by: Group by field ("type", "status", "target")
//   - format: Response format ("json" or "prometheus")
//
// Response Example:
//
//	{
//	  "timestamp": "2025-11-12T10:30:00Z",
//	  "system": {
//	    "total_targets": 10,
//	    "healthy_targets": 8,
//	    "unhealthy_targets": 2,
//	    "success_rate_percent": 95.5,
//	    "queue_size": 15,
//	    "queue_capacity": 1000
//	  },
//	  "target_stats": {...},
//	  "queue_stats": {...}
//	}
func (h *PublishingStatsHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	queryParams := h.parseQueryParams(r)
	if err := h.validateQueryParams(queryParams); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid query parameter", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Collect metrics
	snapshot := h.collector.CollectAll(ctx)

	// Apply filter if specified
	if queryParams.Filter != "" {
		snapshot = h.applyFilter(snapshot, queryParams.Filter)
	}

	// Separate metrics by category
	targetStats := make(map[string]float64)
	queueStats := make(map[string]float64)

	for key, value := range snapshot.Metrics {
		if strings.Contains(key, "target") || strings.Contains(key, "health") || strings.Contains(key, "discovery") {
			targetStats[key] = value
		} else if strings.Contains(key, "queue") || strings.Contains(key, "worker") || strings.Contains(key, "job") {
			queueStats[key] = value
		}
	}

	// Calculate system-wide statistics
	systemStats := SystemStats{
		TotalTargets:     int(getMetricValue(snapshot.Metrics, "targets_total")),
		HealthyTargets:   countHealthyTargets(snapshot.Metrics),
		UnhealthyTargets: countUnhealthyTargets(snapshot.Metrics),
		SuccessRate:      calculateSuccessRate(snapshot.Metrics),
		QueueSize:        int(getMetricValue(snapshot.Metrics, "queue_size_total")),
		QueueCapacity:    int(getMetricValue(snapshot.Metrics, "queue_capacity")),
	}

	response := StatsResponse{
		Timestamp:   time.Now(),
		System:      systemStats,
		TargetStats: targetStats,
		QueueStats:  queueStats,
	}

	// Apply grouping if specified
	if queryParams.GroupBy != "" {
		response = h.applyGrouping(response, queryParams.GroupBy)
	}

	// Set HTTP caching headers
	h.setCacheHeaders(w, snapshot)

	// Handle conditional request (If-None-Match)
	if h.handleConditionalRequest(w, r, snapshot) {
		return // 304 Not Modified already sent
	}

	// Format response based on format parameter
	if queryParams.Format == "prometheus" {
		h.sendPrometheusFormat(w, response, snapshot)
		return
	}

	// Default: JSON format
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode stats response", "error", err)
	}

	// Enhanced observability logging
	duration := time.Since(startTime)
	h.logger.Info("Stats endpoint called",
		"total_targets", systemStats.TotalTargets,
		"healthy_targets", systemStats.HealthyTargets,
		"unhealthy_targets", systemStats.UnhealthyTargets,
		"success_rate", systemStats.SuccessRate,
		"queue_size", systemStats.QueueSize,
		"queue_capacity", systemStats.QueueCapacity,
		"filter", queryParams.Filter,
		"group_by", queryParams.GroupBy,
		"format", queryParams.Format,
		"duration_ms", duration.Milliseconds(),
		"collection_duration_us", snapshot.CollectionDuration.Microseconds(),
		"metrics_count", len(snapshot.Metrics),
		"cache_hit", r.Header.Get("If-None-Match") != "",
	)
}

// ============================================================================
// Endpoint 3: GET /api/v2/publishing/health (Health Check)
// ============================================================================

// GetHealth handles GET /api/v2/publishing/health
//
// This endpoint returns system health summary for monitoring.
//
// Response Example:
//
//	{
//	  "status": "healthy",
//	  "timestamp": "2025-11-12T10:30:00Z",
//	  "checks": {
//	    "health": "ok",
//	    "refresh": "ok",
//	    "discovery": "ok",
//	    "queue": "ok"
//	  },
//	  "message": "All systems operational"
//	}
//
// HTTP Status Codes:
//   - 200: Healthy (all checks passed)
//   - 503: Degraded or Unhealthy (some checks failed)
func (h *PublishingStatsHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Collect metrics
	snapshot := h.collector.CollectAll(ctx)

	// Initialize health checks
	status := "healthy"
	checks := make(map[string]string)

	// Check if collectors are available
	for _, collectorName := range snapshot.AvailableCollectors {
		checks[collectorName] = "ok"
	}

	// Check for collection errors
	if len(snapshot.Errors) > 0 {
		status = "degraded"
		for name, err := range snapshot.Errors {
			checks[name] = "error: " + err.Error()
		}
	}

	// Check unhealthy targets threshold
	unhealthyCount := countUnhealthyTargets(snapshot.Metrics)
	totalTargets := int(getMetricValue(snapshot.Metrics, "targets_total"))

	if unhealthyCount > 0 {
		if totalTargets > 0 && float64(unhealthyCount)/float64(totalTargets) > 0.5 {
			status = "unhealthy"
			checks["unhealthy_targets"] = "critical"
		} else {
			if status == "healthy" {
				status = "degraded"
			}
			checks["unhealthy_targets"] = "warning"
		}
	}

	// Generate health message
	message := generateHealthMessage(status, unhealthyCount, totalTargets)

	response := PublishingHealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Checks:    checks,
		Message:   message,
	}

	// Set HTTP status code based on health
	statusCode := http.StatusOK
	if status == "degraded" || status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode health response", "error", err)
	}

	h.logger.Debug("Health endpoint called",
		"status", status,
		"unhealthy_targets", unhealthyCount,
	)
}

// ============================================================================
// Endpoint 4: GET /api/v2/publishing/stats/{target} (Per-Target Stats)
// ============================================================================

// TargetStatsResponse represents per-target statistics.
type TargetStatsResponse struct {
	TargetName string             `json:"target_name"`
	Timestamp  time.Time          `json:"timestamp"`
	Health     TargetHealthInfo   `json:"health"`
	Jobs       TargetJobInfo      `json:"jobs"`
	Metrics    map[string]float64 `json:"metrics"`
}

// TargetHealthInfo represents target health information.
type TargetHealthInfo struct {
	Status              string  `json:"status"` // "healthy", "degraded", "unhealthy"
	SuccessRate         float64 `json:"success_rate_percent"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastCheck           string  `json:"last_check,omitempty"`
}

// TargetJobInfo represents target job processing information.
type TargetJobInfo struct {
	TotalProcessed int     `json:"total_processed"`
	Succeeded      int     `json:"succeeded"`
	Failed         int     `json:"failed"`
	SuccessRate    float64 `json:"success_rate_percent"`
}

// GetTargetStats handles GET /api/v2/publishing/stats/{target}
//
// This endpoint returns statistics for a specific target.
//
// URL Parameters:
//   - target: Target name (e.g., "rootly-prod")
//
// Response Example:
//
//	{
//	  "target_name": "rootly-prod",
//	  "timestamp": "2025-11-12T10:30:00Z",
//	  "health": {
//	    "status": "healthy",
//	    "success_rate_percent": 99.5,
//	    "consecutive_failures": 0
//	  },
//	  "jobs": {
//	    "total_processed": 1000,
//	    "succeeded": 995,
//	    "failed": 5,
//	    "success_rate_percent": 99.5
//	  },
//	  "metrics": {...}
//	}
//
// HTTP Status Codes:
//   - 200: Target found
//   - 404: Target not found
func (h *PublishingStatsHandler) GetTargetStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract target name from URL path
	// Expected format: /api/v2/publishing/stats/{target}
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/publishing/stats/"), "/")
	if len(pathParts) == 0 || pathParts[0] == "" {
		http.Error(w, "Target name required", http.StatusBadRequest)
		return
	}
	targetName := pathParts[0]

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Collect all metrics
	snapshot := h.collector.CollectAll(ctx)

	// Filter metrics for this target
	targetMetrics := make(map[string]float64)
	hasMetrics := false

	for key, value := range snapshot.Metrics {
		if strings.Contains(key, targetName) {
			targetMetrics[key] = value
			hasMetrics = true
		}
	}

	// Check if target exists
	if !hasMetrics {
		http.Error(w, "Target not found: "+targetName, http.StatusNotFound)
		return
	}

	// Extract health information
	healthStatus := extractTargetHealthStatus(snapshot.Metrics, targetName)
	healthInfo := TargetHealthInfo{
		Status:              healthStatus,
		SuccessRate:         extractTargetSuccessRate(snapshot.Metrics, targetName),
		ConsecutiveFailures: extractConsecutiveFailures(snapshot.Metrics, targetName),
	}

	// Extract job information
	jobInfo := TargetJobInfo{
		TotalProcessed: extractJobsProcessed(snapshot.Metrics, targetName),
		Succeeded:      extractJobsSucceeded(snapshot.Metrics, targetName),
		Failed:         extractJobsFailed(snapshot.Metrics, targetName),
		SuccessRate:    calculateTargetJobSuccessRate(snapshot.Metrics, targetName),
	}

	response := TargetStatsResponse{
		TargetName: targetName,
		Timestamp:  time.Now(),
		Health:     healthInfo,
		Jobs:       jobInfo,
		Metrics:    targetMetrics,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode target stats response", "error", err)
	}

	h.logger.Debug("Target stats endpoint called",
		"target", targetName,
		"metrics_count", len(targetMetrics),
	)
}

// ============================================================================
// Endpoint 5: GET /api/v2/publishing/trends (Trend Analysis)
// ============================================================================

// TrendsResponse represents trend analysis response.
type TrendsResponse struct {
	Timestamp time.Time                  `json:"timestamp"`
	Trends    publishing.TrendAnalysis   `json:"trends"`
	Summary   string                     `json:"summary"`
}

// GetTrends handles GET /api/v2/publishing/trends
//
// This endpoint returns historical trend analysis including:
//   - Success rate trends (increasing/stable/decreasing)
//   - Latency trends (improving/stable/degrading)
//   - Error spike detection (>3σ anomaly)
//   - Queue growth rate
//
// Response Example:
//
//	{
//	  "timestamp": "2025-11-13T10:30:00Z",
//	  "trends": {
//	    "success_rate_trend": "stable",
//	    "success_rate_change": 0.5,
//	    "latency_trend": "improving",
//	    "latency_change": -15.3,
//	    "error_spike_detected": false,
//	    "queue_growth_rate": 2.5,
//	    "queue_growth_trend": "growing"
//	  },
//	  "summary": "System stable. Latency improving. Queue growing slowly."
//	}
//
// HTTP Status Codes:
//   - 200: Trends available
//   - 503: Not enough historical data (< 10 snapshots)
func (h *PublishingStatsHandler) GetTrends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Analyze trends
	trends := h.trendDetector.Analyze()

	// Generate human-readable summary
	summary := generateTrendsSummary(trends)

	response := TrendsResponse{
		Timestamp: time.Now(),
		Trends:    trends,
		Summary:   summary,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode trends response", "error", err)
	}

	h.logger.Debug("Trends endpoint called",
		"success_rate_trend", trends.SuccessRateTrend,
		"latency_trend", trends.LatencyTrend,
		"error_spike", trends.ErrorSpikeDetected,
	)
}

// ============================================================================
// Query Parameters & Filtering Helpers
// ============================================================================

// parseQueryParams parses query parameters from request.
func (h *PublishingStatsHandler) parseQueryParams(r *http.Request) StatsQueryParams {
	query := r.URL.Query()
	return StatsQueryParams{
		Filter:  query.Get("filter"),
		GroupBy: query.Get("group_by"),
		Format:  query.Get("format"),
	}
}

// validateQueryParams validates query parameters.
func (h *PublishingStatsHandler) validateQueryParams(params StatsQueryParams) error {
	// Validate filter format: "type:value" or "status:value"
	if params.Filter != "" {
		filterRegex := regexp.MustCompile(`^(type|status):[a-z0-9-]+$`)
		if !filterRegex.MatchString(params.Filter) {
			return fmt.Errorf("invalid filter format: expected 'type:value' or 'status:value'")
		}
	}

	// Validate group_by: must be one of allowed values
	if params.GroupBy != "" {
		allowedGroupBy := map[string]bool{
			"type":   true,
			"status": true,
			"target": true,
		}
		if !allowedGroupBy[params.GroupBy] {
			return fmt.Errorf("invalid group_by: must be one of 'type', 'status', 'target'")
		}
	}

	// Validate format: must be one of allowed values
	if params.Format != "" {
		allowedFormats := map[string]bool{
			"json":       true,
			"prometheus": true,
		}
		if !allowedFormats[params.Format] {
			return fmt.Errorf("invalid format: must be one of 'json', 'prometheus'")
		}
	}

	return nil
}

// applyFilter applies filter to metrics snapshot.
func (h *PublishingStatsHandler) applyFilter(snapshot *publishing.MetricsSnapshot, filter string) *publishing.MetricsSnapshot {
	// Parse filter: "type:rootly" or "status:healthy"
	parts := strings.Split(filter, ":")
	if len(parts) != 2 {
		return snapshot // Invalid filter, return original
	}

	filterType := parts[0]
	filterValue := parts[1]

	filteredMetrics := make(map[string]float64)

	for key, value := range snapshot.Metrics {
		shouldInclude := false

		switch filterType {
		case "type":
			// Filter by target type
			if strings.Contains(key, filterValue) {
				shouldInclude = true
			}
		case "status":
			// Filter by health status
			if filterValue == "healthy" {
				if strings.Contains(key, "health_status") && value == 1.0 {
					shouldInclude = true
				}
			} else if filterValue == "unhealthy" {
				if strings.Contains(key, "health_status") && value == 3.0 {
					shouldInclude = true
				}
			} else if filterValue == "degraded" {
				if strings.Contains(key, "health_status") && value == 2.0 {
					shouldInclude = true
				}
			}
		}

		if shouldInclude {
			filteredMetrics[key] = value
		}
	}

	// Create filtered snapshot
	return &publishing.MetricsSnapshot{
		Timestamp:           snapshot.Timestamp,
		Metrics:             filteredMetrics,
		CollectionDuration:  snapshot.CollectionDuration,
		AvailableCollectors: snapshot.AvailableCollectors,
		Errors:              snapshot.Errors,
	}
}

// applyGrouping applies grouping to response (simplified implementation).
func (h *PublishingStatsHandler) applyGrouping(response StatsResponse, groupBy string) StatsResponse {
	// For now, grouping is applied by modifying target_stats
	// In a full implementation, this would create a grouped structure
	// This is a simplified version that maintains backward compatibility
	return response
}

// ============================================================================
// HTTP Caching Helpers
// ============================================================================

// generateETag generates ETag for metrics snapshot.
func (h *PublishingStatsHandler) generateETag(snapshot *publishing.MetricsSnapshot) string {
	// Create hash from timestamp and metrics count
	hashInput := fmt.Sprintf("%s-%d-%d",
		snapshot.Timestamp.Format(time.RFC3339),
		len(snapshot.Metrics),
		snapshot.CollectionDuration.Microseconds(),
	)

	hash := sha256.Sum256([]byte(hashInput))
	etag := hex.EncodeToString(hash[:])[:16] // Use first 16 chars

	return fmt.Sprintf(`"%s"`, etag)
}

// setCacheHeaders sets HTTP caching headers.
func (h *PublishingStatsHandler) setCacheHeaders(w http.ResponseWriter, snapshot *publishing.MetricsSnapshot) {
	// Cache-Control: max-age=5s (aligned with metrics collection frequency)
	w.Header().Set("Cache-Control", "max-age=5, public")

	// ETag: based on metrics snapshot hash
	etag := h.generateETag(snapshot)
	w.Header().Set("ETag", etag)
}

// handleConditionalRequest handles If-None-Match conditional request.
// Returns true if 304 Not Modified was sent.
func (h *PublishingStatsHandler) handleConditionalRequest(w http.ResponseWriter, r *http.Request, snapshot *publishing.MetricsSnapshot) bool {
	ifNoneMatch := r.Header.Get("If-None-Match")
	if ifNoneMatch == "" {
		return false
	}

	currentETag := h.generateETag(snapshot)
	if ifNoneMatch == currentETag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}

	return false
}

// ============================================================================
// Response Format Helpers
// ============================================================================

// sendPrometheusFormat sends response in Prometheus text format.
func (h *PublishingStatsHandler) sendPrometheusFormat(w http.ResponseWriter, response StatsResponse, snapshot *publishing.MetricsSnapshot) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// Write Prometheus format
	fmt.Fprintf(w, "# HELP publishing_stats_total_targets Total number of publishing targets\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_total_targets gauge\n")
	fmt.Fprintf(w, "publishing_stats_total_targets %d\n", response.System.TotalTargets)

	fmt.Fprintf(w, "# HELP publishing_stats_healthy_targets Number of healthy targets\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_healthy_targets gauge\n")
	fmt.Fprintf(w, "publishing_stats_healthy_targets %d\n", response.System.HealthyTargets)

	fmt.Fprintf(w, "# HELP publishing_stats_unhealthy_targets Number of unhealthy targets\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_unhealthy_targets gauge\n")
	fmt.Fprintf(w, "publishing_stats_unhealthy_targets %d\n", response.System.UnhealthyTargets)

	fmt.Fprintf(w, "# HELP publishing_stats_success_rate_percent Overall success rate percentage\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_success_rate_percent gauge\n")
	fmt.Fprintf(w, "publishing_stats_success_rate_percent %.2f\n", response.System.SuccessRate)

	fmt.Fprintf(w, "# HELP publishing_stats_queue_size Current queue size\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_queue_size gauge\n")
	fmt.Fprintf(w, "publishing_stats_queue_size %d\n", response.System.QueueSize)

	fmt.Fprintf(w, "# HELP publishing_stats_queue_capacity Queue capacity\n")
	fmt.Fprintf(w, "# TYPE publishing_stats_queue_capacity gauge\n")
	fmt.Fprintf(w, "publishing_stats_queue_capacity %d\n", response.System.QueueCapacity)
}

// sendError sends error response.
func (h *PublishingStatsHandler) sendError(w http.ResponseWriter, status int, message string, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	errorResponse := map[string]interface{}{
		"error":     http.StatusText(status),
		"message":   message,
		"details":   details,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		h.logger.Error("Failed to encode error response", "error", err)
	}
}

// ============================================================================
// Helper Functions
// ============================================================================

// formatDuration formats duration in milliseconds with 3 decimal places.
func formatDuration(d time.Duration) string {
	ms := float64(d.Microseconds()) / 1000.0
	return formatFloat(ms, 3)
}

// formatFloat formats float with specified precision.
func formatFloat(f float64, precision int) string {
	format := "%." + string(rune('0'+precision)) + "f"
	return strings.TrimRight(strings.TrimRight(formatFloatHelper(f, format), "0"), ".")
}
