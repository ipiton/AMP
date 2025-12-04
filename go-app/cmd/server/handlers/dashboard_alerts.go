// Package handlers provides HTTP handlers for the Alert History Service.
// TN-84: GET /api/dashboard/alerts/recent - Dashboard Alerts Handler (150% Quality Target)
package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/ui"
)

// DashboardAlertsHandler handles dashboard-specific alert endpoints.
// Optimized for dashboard usage: compact format, fast response, optional classification.
type DashboardAlertsHandler struct {
	historyRepo          core.AlertHistoryRepository
	classificationEnricher ui.ClassificationEnricher // optional
	cache                cache.Cache                    // optional, for response caching
	logger               *slog.Logger
}

// NewDashboardAlertsHandler creates a new dashboard alerts handler.
func NewDashboardAlertsHandler(
	historyRepo core.AlertHistoryRepository,
	classificationEnricher ui.ClassificationEnricher, // optional, can be nil
	cache cache.Cache,                                  // optional, can be nil
	logger *slog.Logger,
) *DashboardAlertsHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &DashboardAlertsHandler{
		historyRepo:          historyRepo,
		classificationEnricher: classificationEnricher,
		cache:                cache,
		logger:               logger,
	}
}

// DashboardAlertResponse represents the response format for dashboard alerts endpoint.
type DashboardAlertResponse struct {
	Alerts    []DashboardAlert  `json:"alerts"`
	Count     int               `json:"count"`
	Limit     int               `json:"limit"`
	Filters   *ResponseFilters  `json:"filters,omitempty"`
	Timestamp string            `json:"timestamp"`
}

// DashboardAlert represents a compact alert format for dashboard display.
type DashboardAlert struct {
	Fingerprint string            `json:"fingerprint"`
	AlertName   string            `json:"alert_name"`
	Status      string            `json:"status"`
	Severity    string            `json:"severity"`
	Summary     string            `json:"summary,omitempty"`
	StartsAt    time.Time         `json:"starts_at"`
	Labels      map[string]string `json:"labels,omitempty"` // only important labels

	// Optional (if include_classification=true)
	Classification *ClassificationSummary `json:"classification,omitempty"`
}

// ClassificationSummary represents classification data in compact format.
type ClassificationSummary struct {
	Severity   string  `json:"severity"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// ResponseFilters represents applied filters in response.
type ResponseFilters struct {
	Status  string `json:"status,omitempty"`
	Severity string `json:"severity,omitempty"`
}

// QueryParams represents parsed query parameters.
type QueryParams struct {
	Limit                int
	Status               string
	Severity             string
	IncludeClassification bool
}

// GetRecentAlerts handles GET /api/dashboard/alerts/recent
// Returns recent alerts in compact format optimized for dashboard display.
func (h *DashboardAlertsHandler) GetRecentAlerts(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Only accept GET requests
	if r.Method != http.MethodGet {
		h.logger.Warn("Invalid HTTP method", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	params, err := h.parseQueryParams(r)
	if err != nil {
		h.logger.Warn("Invalid query parameters", "error", err)
		http.Error(w, fmt.Sprintf("Invalid parameters: %v", err), http.StatusBadRequest)
		return
	}

	// Check cache (if enabled)
	var response *DashboardAlertResponse
	if h.cache != nil {
		cacheKey := h.buildCacheKey(params)
		var cachedResp DashboardAlertResponse
		if err := h.cache.Get(r.Context(), cacheKey, &cachedResp); err == nil {
			h.logger.Debug("Cache hit for dashboard alerts", "key", cacheKey)
			h.sendJSON(w, http.StatusOK, &cachedResp)
			return
		}
	}

	// Get recent alerts from repository
	ctx := r.Context()
	alerts, err := h.historyRepo.GetRecentAlerts(ctx, params.Limit)
	if err != nil {
		h.logger.Error("Failed to get recent alerts", "error", err)
		http.Error(w, "Failed to retrieve recent alerts", http.StatusInternalServerError)
		return
	}

	// Apply filters (in-memory filtering)
	filteredAlerts := h.applyFilters(alerts, params)

	// Enrich with classification if requested
	var enrichedAlerts []*ui.EnrichedAlert
	if params.IncludeClassification && h.classificationEnricher != nil {
		enriched, err := h.classificationEnricher.EnrichAlerts(ctx, filteredAlerts)
		if err != nil {
			h.logger.Warn("Failed to enrich alerts with classification, continuing without classification",
				"error", err,
				"alerts_count", len(filteredAlerts))
			// Graceful degradation: convert alerts to enriched format without classification
			enrichedAlerts = convertToEnrichedAlertsForDashboard(filteredAlerts)
		} else {
			enrichedAlerts = enriched
		}
	} else {
		// No classification requested, convert alerts to enriched format without classification
		enrichedAlerts = convertToEnrichedAlertsForDashboard(filteredAlerts)
	}

	// Format response
	response = h.formatResponse(enrichedAlerts, params)

	// Cache response (if enabled)
	if h.cache != nil {
		cacheKey := h.buildCacheKey(params)
		if err := h.cache.Set(r.Context(), cacheKey, response, 5*time.Second); err != nil {
			h.logger.Warn("Failed to cache response", "error", err)
		}
	}

	// Send response
	h.sendJSON(w, http.StatusOK, response)

	duration := time.Since(startTime)
	h.logger.Info("Dashboard alerts request completed",
		"count", len(response.Alerts),
		"limit", params.Limit,
		"duration_ms", duration.Milliseconds(),
		"classification", params.IncludeClassification,
	)
}

// parseQueryParams parses and validates query parameters.
func (h *DashboardAlertsHandler) parseQueryParams(r *http.Request) (*QueryParams, error) {
	query := r.URL.Query()
	params := &QueryParams{
		Limit:                10, // default
		IncludeClassification: false,
	}

	// Parse limit
	if limitStr := query.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			return nil, fmt.Errorf("invalid limit: must be between 1 and 50")
		}
		if limit > 50 {
			return nil, fmt.Errorf("invalid limit: must be between 1 and 50")
		}
		params.Limit = limit
	}

	// Parse status filter
	if statusStr := query.Get("status"); statusStr != "" {
		status := strings.ToLower(statusStr)
		if status != "firing" && status != "resolved" {
			return nil, fmt.Errorf("invalid status: must be 'firing' or 'resolved'")
		}
		params.Status = status
	}

	// Parse severity filter
	if severityStr := query.Get("severity"); severityStr != "" {
		severity := strings.ToLower(severityStr)
		validSeverities := map[string]bool{
			"critical": true,
			"warning":  true,
			"info":     true,
			"noise":    true,
		}
		if !validSeverities[severity] {
			return nil, fmt.Errorf("invalid severity: must be 'critical', 'warning', 'info', or 'noise'")
		}
		params.Severity = severity
	}

	// Parse include_classification
	if includeStr := query.Get("include_classification"); includeStr != "" {
		include, err := strconv.ParseBool(includeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid include_classification: must be 'true' or 'false'")
		}
		params.IncludeClassification = include
	}

	return params, nil
}

// applyFilters applies status and severity filters to alerts.
func (h *DashboardAlertsHandler) applyFilters(alerts []*core.Alert, params *QueryParams) []*core.Alert {
	if params.Status == "" && params.Severity == "" {
		return alerts // No filters, return all
	}

	filtered := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		// Apply status filter
		if params.Status != "" && string(alert.Status) != params.Status {
			continue
		}

		// Apply severity filter
		if params.Severity != "" {
			alertSeverityPtr := alert.Severity()
			if alertSeverityPtr == nil {
				continue
			}
			alertSeverity := strings.ToLower(*alertSeverityPtr)
			if alertSeverity != params.Severity {
				continue
			}
		}

		filtered = append(filtered, alert)
	}

	return filtered
}

// formatResponse formats enriched alerts into dashboard response format.
func (h *DashboardAlertsHandler) formatResponse(enrichedAlerts []*ui.EnrichedAlert, params *QueryParams) *DashboardAlertResponse {
	alerts := make([]DashboardAlert, 0, len(enrichedAlerts))

	for _, enriched := range enrichedAlerts {
		if enriched == nil || enriched.Alert == nil {
			continue
		}

		alert := enriched.Alert

		// Extract summary from annotations
		summary := alert.AlertName
		if desc, ok := alert.Annotations["description"]; ok && desc != "" {
			summary = desc
		} else if message, ok := alert.Annotations["message"]; ok && message != "" {
			summary = message
		}

		// Extract important labels (namespace, instance, job)
		importantLabels := make(map[string]string)
		importantKeys := []string{"namespace", "instance", "job", "cluster", "environment"}
		for _, key := range importantKeys {
			if value, ok := alert.Labels[key]; ok {
				importantLabels[key] = value
			}
		}

		severity := "info" // default
		if sevPtr := alert.Severity(); sevPtr != nil {
			severity = *sevPtr
		}

		dashboardAlert := DashboardAlert{
			Fingerprint: alert.Fingerprint,
			AlertName:   alert.AlertName,
			Status:      string(alert.Status),
			Severity:    severity,
			Summary:     summary,
			StartsAt:    alert.StartsAt,
			Labels:      importantLabels,
		}

		// Add classification if available
		if params.IncludeClassification && enriched.HasClassification && enriched.Classification != nil {
			dashboardAlert.Classification = &ClassificationSummary{
				Severity:   string(enriched.Classification.Severity),
				Confidence: enriched.Classification.Confidence,
				Source:     enriched.ClassificationSource,
			}
		}

		alerts = append(alerts, dashboardAlert)
	}

	// Build filters for response
	var filters *ResponseFilters
	if params.Status != "" || params.Severity != "" {
		filters = &ResponseFilters{
			Status:   params.Status,
			Severity: params.Severity,
		}
	}

	return &DashboardAlertResponse{
		Alerts:    alerts,
		Count:     len(alerts),
		Limit:     params.Limit,
		Filters:   filters,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// buildCacheKey builds a cache key from query parameters.
func (h *DashboardAlertsHandler) buildCacheKey(params *QueryParams) string {
	key := fmt.Sprintf("dashboard:alerts:recent:%d", params.Limit)
	if params.Status != "" {
		key += fmt.Sprintf(":status:%s", params.Status)
	}
	if params.Severity != "" {
		key += fmt.Sprintf(":severity:%s", params.Severity)
	}
	if params.IncludeClassification {
		key += ":classification"
	}
	return key
}

// sendJSON sends a JSON response.
func (h *DashboardAlertsHandler) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("Failed to encode JSON response", "error", err)
	}
}

// convertToEnrichedAlertsForDashboard converts core.Alert slice to EnrichedAlert slice without classification.
// This is a wrapper around the shared convertToEnrichedAlerts function to avoid redeclaration.
func convertToEnrichedAlertsForDashboard(alerts []*core.Alert) []*ui.EnrichedAlert {
	// Use the shared function from alert_list_ui.go
	// Since it's in the same package, we can call it directly
	// But to avoid circular dependency, we'll inline it here
	if len(alerts) == 0 {
		return []*ui.EnrichedAlert{}
	}
	enriched := make([]*ui.EnrichedAlert, len(alerts))
	for i, alert := range alerts {
		enriched[i] = &ui.EnrichedAlert{
			Alert:             alert,
			HasClassification: false,
		}
	}
	return enriched
}
