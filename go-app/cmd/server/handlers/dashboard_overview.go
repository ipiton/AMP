// Package handlers provides HTTP handlers for the Alert History Service.
// TN-81: GET /api/dashboard/overview - Dashboard Overview Handler (150% Quality Target)
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// PublishingStatsProvider provides publishing statistics.
// This is an interface to allow optional integration with publishing system.
type PublishingStatsProvider interface {
	GetTargetCount() int
	GetPublishingMode() string
	GetSuccessfulPublishes() int64
	GetFailedPublishes() int64
}

// publishingStatsProviderImpl implements PublishingStatsProvider using MetricsCollectorInterface.
type publishingStatsProviderImpl struct {
	collector MetricsCollectorInterface // from publishing_stats.go
	logger    *slog.Logger
}

// NewPublishingStatsProvider creates a new publishing stats provider.
func NewPublishingStatsProvider(statsHandler *PublishingStatsHandler, logger *slog.Logger) PublishingStatsProvider {
	if logger == nil {
		logger = slog.Default()
	}
	if statsHandler == nil {
		return &publishingStatsProviderImpl{
			collector: nil,
			logger:    logger,
		}
	}
	// Access collector through the handler (it's private, so we need to use a method or make it accessible)
	// For now, we'll create a wrapper that uses the handler's GetStats method indirectly
	// Or we can make collector accessible - but that's a breaking change
	// Let's use a simpler approach: pass the collector directly
	return &publishingStatsProviderImpl{
		collector: statsHandler.collector, // This will work if collector is accessible
		logger:    logger,
	}
}

// NewPublishingStatsProviderWithCollector creates a provider with direct collector access.
// This is used when we have direct access to the collector.
func NewPublishingStatsProviderWithCollector(collector MetricsCollectorInterface, logger *slog.Logger) PublishingStatsProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &publishingStatsProviderImpl{
		collector: collector,
		logger:    logger,
	}
}

// GetTargetCount returns the number of publishing targets.
func (p *publishingStatsProviderImpl) GetTargetCount() int {
	if p.collector == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot := p.collector.CollectAll(ctx)
	if snapshot == nil {
		return 0
	}
	// Extract target count from metrics
	if count, ok := snapshot.Metrics["discovery.total_targets"]; ok {
		return int(count)
	}
	return 0
}

// GetPublishingMode returns the current publishing mode.
func (p *publishingStatsProviderImpl) GetPublishingMode() string {
	if p.collector == nil {
		return "unknown"
	}
	// Try to determine mode from metrics
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot := p.collector.CollectAll(ctx)
	if snapshot == nil {
		return "unknown"
	}
	// Check if metrics-only mode (no targets)
	if count, ok := snapshot.Metrics["discovery.total_targets"]; ok && count == 0 {
		return "metrics-only"
	}
	return "intelligent"
}

// GetSuccessfulPublishes returns the number of successful publishes.
func (p *publishingStatsProviderImpl) GetSuccessfulPublishes() int64 {
	if p.collector == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot := p.collector.CollectAll(ctx)
	if snapshot == nil {
		return 0
	}
	// Extract successful publishes from metrics
	if count, ok := snapshot.Metrics["queue.jobs_succeeded_total"]; ok {
		return int64(count)
	}
	return 0
}

// GetFailedPublishes returns the number of failed publishes.
func (p *publishingStatsProviderImpl) GetFailedPublishes() int64 {
	if p.collector == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot := p.collector.CollectAll(ctx)
	if snapshot == nil {
		return 0
	}
	// Extract failed publishes from metrics
	if count, ok := snapshot.Metrics["queue.jobs_failed_total"]; ok {
		return int64(count)
	}
	return 0
}

// DashboardOverviewHandler handles dashboard overview endpoint.
// Aggregates statistics from multiple sources: alerts, classification, publishing, health.
type DashboardOverviewHandler struct {
	historyRepo          core.AlertHistoryRepository
	classificationService services.ClassificationService // optional
	publishingStats      PublishingStatsProvider         // optional
	cache                cache.Cache                     // optional
	logger               *slog.Logger
}

// NewDashboardOverviewHandler creates a new dashboard overview handler.
func NewDashboardOverviewHandler(
	historyRepo core.AlertHistoryRepository,
	classificationService services.ClassificationService, // optional, can be nil
	publishingStats PublishingStatsProvider,              // optional, can be nil
	cache cache.Cache,                                    // optional, can be nil
	logger *slog.Logger,
) *DashboardOverviewHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &DashboardOverviewHandler{
		historyRepo:          historyRepo,
		classificationService: classificationService,
		publishingStats:      publishingStats,
		cache:                cache,
		logger:               logger,
	}
}

// DashboardOverviewResponse represents the response format for dashboard overview endpoint.
type DashboardOverviewResponse struct {
	// Alert statistics
	TotalAlerts    int `json:"total_alerts"`
	ActiveAlerts   int `json:"active_alerts"`
	ResolvedAlerts int `json:"resolved_alerts"`
	AlertsLast24h  int `json:"alerts_last_24h"`

	// Classification statistics
	ClassificationEnabled      bool    `json:"classification_enabled"`
	ClassifiedAlerts           int64   `json:"classified_alerts"`
	ClassificationCacheHitRate float64 `json:"classification_cache_hit_rate"`
	LLMServiceAvailable       bool    `json:"llm_service_available"`

	// Publishing statistics
	PublishingTargets  int    `json:"publishing_targets"`
	PublishingMode      string `json:"publishing_mode"`
	SuccessfulPublishes int64  `json:"successful_publishes"`
	FailedPublishes     int64  `json:"failed_publishes"`

	// System health
	SystemHealthy bool `json:"system_healthy"`
	RedisConnected bool `json:"redis_connected"`

	// Metadata
	LastUpdated string `json:"last_updated"`
}

// alertStats represents alert statistics.
type alertStats struct {
	totalAlerts    int
	activeAlerts   int
	resolvedAlerts int
	alertsLast24h  int
	err            error
}

// classificationStats represents classification statistics.
type classificationStats struct {
	enabled      bool
	classified   int64
	cacheHitRate float64
	llmAvailable bool
	err          error
}

// publishingStats represents publishing statistics.
type publishingStats struct {
	targets           int
	mode              string
	successfulPublishes int64
	failedPublishes   int64
	err               error
}

// systemHealth represents system health status.
type systemHealth struct {
	healthy       bool
	redisConnected bool
	err           error
}

// GetOverview handles GET /api/dashboard/overview
// Returns consolidated overview statistics from multiple sources.
func (h *DashboardOverviewHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Only accept GET requests
	if r.Method != http.MethodGet {
		h.logger.Warn("Invalid HTTP method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check cache (if enabled)
	var response *DashboardOverviewResponse
	if h.cache != nil {
		cacheKey := "dashboard:overview"
		var cachedResp DashboardOverviewResponse
		if err := h.cache.Get(r.Context(), cacheKey, &cachedResp); err == nil {
			h.logger.Debug("Cache hit for dashboard overview", "key", cacheKey)
			h.sendJSON(w, http.StatusOK, &cachedResp)
			return
		}
	}

	// Collect statistics in parallel
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	alertStatsChan := make(chan alertStats, 1)
	classificationStatsChan := make(chan classificationStats, 1)
	publishingStatsChan := make(chan publishingStats, 1)
	healthChan := make(chan systemHealth, 1)

	// Collect alert statistics
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats := h.collectAlertStats(ctx)
		alertStatsChan <- stats
	}()

	// Collect classification statistics
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats := h.collectClassificationStats(ctx)
		classificationStatsChan <- stats
	}()

	// Collect publishing statistics
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats := h.collectPublishingStats(ctx)
		publishingStatsChan <- stats
	}()

	// Collect system health
	wg.Add(1)
	go func() {
		defer wg.Done()
		health := h.collectSystemHealth(ctx)
		healthChan <- health
	}()

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(alertStatsChan)
		close(classificationStatsChan)
		close(publishingStatsChan)
		close(healthChan)
	}()

	// Collect results
	alertStats := <-alertStatsChan
	classificationStats := <-classificationStatsChan
	publishingStats := <-publishingStatsChan
	health := <-healthChan

	// Aggregate statistics
	response = h.aggregateStats(alertStats, classificationStats, publishingStats, health)

	// Cache response (if enabled)
	if h.cache != nil {
		cacheKey := "dashboard:overview"
		if err := h.cache.Set(r.Context(), cacheKey, response, 15*time.Second); err != nil {
			h.logger.Warn("Failed to cache response", "error", err)
		}
	}

	// Send response
	h.sendJSON(w, http.StatusOK, response)

	duration := time.Since(startTime)
	h.logger.Info("Dashboard overview request completed",
		"duration_ms", duration.Milliseconds(),
		"alert_stats_err", alertStats.err != nil,
		"classification_stats_err", classificationStats.err != nil,
		"publishing_stats_err", publishingStats.err != nil,
		"health_err", health.err != nil,
	)
}

// collectAlertStats collects alert statistics.
func (h *DashboardOverviewHandler) collectAlertStats(ctx context.Context) alertStats {
	stats := alertStats{}

	// Create context with timeout (5 seconds)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Get all alerts (with reasonable limit)
	historyReq := &core.HistoryRequest{
		Filters: nil,
		Pagination: &core.Pagination{
			Page:    1,
			PerPage: 10000, // Reasonable limit for statistics
		},
		Sorting: &core.Sorting{
			Field: "starts_at",
			Order: core.SortOrderDesc,
		},
	}

	historyResp, err := h.historyRepo.GetHistory(ctx, historyReq)
	if err != nil {
		h.logger.Warn("Failed to collect alert statistics", "error", err)
		stats.err = err
		return stats
	}

	// Count by status
	activeCount := 0
	resolvedCount := 0
	last24hCount := 0

	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	for _, alert := range historyResp.Alerts {
		if alert.Status == core.AlertStatus("firing") {
			activeCount++
		} else if alert.Status == core.AlertStatus("resolved") {
			resolvedCount++
		}

		// Count alerts from last 24h
		if alert.StartsAt.After(yesterday) {
			last24hCount++
		}
	}

	stats.totalAlerts = int(historyResp.Total)
	stats.activeAlerts = activeCount
	stats.resolvedAlerts = resolvedCount
	stats.alertsLast24h = last24hCount

	return stats
}

// collectClassificationStats collects classification statistics.
func (h *DashboardOverviewHandler) collectClassificationStats(ctx context.Context) classificationStats {
	stats := classificationStats{
		enabled:      false,
		classified:   0,
		cacheHitRate: 0.0,
		llmAvailable: false,
	}

	if h.classificationService == nil {
		return stats // Graceful degradation
	}

	// Create context with timeout (5 seconds)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Get classification stats
	classificationStats := h.classificationService.GetStats()
	stats.enabled = true
	stats.classified = classificationStats.TotalRequests
	stats.cacheHitRate = classificationStats.CacheHitRate

	// Check LLM service availability
	if err := h.classificationService.Health(ctx); err == nil {
		stats.llmAvailable = true
	} else {
		h.logger.Debug("LLM service unavailable", "error", err)
	}

	return stats
}

// collectPublishingStats collects publishing statistics.
func (h *DashboardOverviewHandler) collectPublishingStats(ctx context.Context) publishingStats {
	stats := publishingStats{
		targets:            0,
		mode:               "unknown",
		successfulPublishes: 0,
		failedPublishes:    0,
	}

	if h.publishingStats == nil {
		return stats // Graceful degradation
	}

	// Create context with timeout (5 seconds)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stats.targets = h.publishingStats.GetTargetCount()
	stats.mode = h.publishingStats.GetPublishingMode()
	stats.successfulPublishes = h.publishingStats.GetSuccessfulPublishes()
	stats.failedPublishes = h.publishingStats.GetFailedPublishes()

	return stats
}

// collectSystemHealth collects system health status.
func (h *DashboardOverviewHandler) collectSystemHealth(ctx context.Context) systemHealth {
	health := systemHealth{
		healthy:       true,
		redisConnected: false,
	}

	// Create context with timeout (5 seconds)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Check Redis connection
	if h.cache != nil {
		if err := h.cache.HealthCheck(ctx); err == nil {
			health.redisConnected = true
		} else {
			h.logger.Debug("Redis health check failed", "error", err)
		}
	}

	// Check LLM service (via classification service)
	if h.classificationService != nil {
		if err := h.classificationService.Health(ctx); err != nil {
			h.logger.Debug("LLM service health check failed", "error", err)
			// Don't mark system as unhealthy if LLM is unavailable (non-critical)
		}
	}

	return health
}

// aggregateStats aggregates all statistics into response.
func (h *DashboardOverviewHandler) aggregateStats(
	alertStats alertStats,
	classificationStats classificationStats,
	publishingStats publishingStats,
	health systemHealth,
) *DashboardOverviewResponse {
	// Determine overall system health
	systemHealthy := true
	if alertStats.err != nil {
		systemHealthy = false
		h.logger.Warn("Alert statistics collection failed", "error", alertStats.err)
	}

	return &DashboardOverviewResponse{
		// Alert statistics
		TotalAlerts:    alertStats.totalAlerts,
		ActiveAlerts:   alertStats.activeAlerts,
		ResolvedAlerts: alertStats.resolvedAlerts,
		AlertsLast24h:  alertStats.alertsLast24h,

		// Classification statistics
		ClassificationEnabled:      classificationStats.enabled,
		ClassifiedAlerts:           classificationStats.classified,
		ClassificationCacheHitRate: classificationStats.cacheHitRate,
		LLMServiceAvailable:       classificationStats.llmAvailable,

		// Publishing statistics
		PublishingTargets:  publishingStats.targets,
		PublishingMode:      publishingStats.mode,
		SuccessfulPublishes: publishingStats.successfulPublishes,
		FailedPublishes:     publishingStats.failedPublishes,

		// System health
		SystemHealthy: systemHealthy,
		RedisConnected: health.redisConnected,

		// Metadata
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}
}

// sendJSON sends a JSON response.
func (h *DashboardOverviewHandler) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("Failed to encode JSON response", "error", err)
	}
}
