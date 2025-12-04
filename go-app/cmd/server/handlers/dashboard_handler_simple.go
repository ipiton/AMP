// Package handlers provides HTTP handlers for the dashboard.
// TN-77: Modern Dashboard Page - Simple Handler with Mock Data
package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/ui"
)

// SimpleDashboardHandler handles dashboard page requests with mock data.
// This is a simplified version for quick demonstration. Full version with
// PostgreSQL/Redis/SilenceManager integration will be in dashboard_handler.go.
type SimpleDashboardHandler struct {
	templateEngine *ui.TemplateEngine
	logger         *slog.Logger
}

// NewSimpleDashboardHandler creates a new simple dashboard handler.
func NewSimpleDashboardHandler(
	templateEngine *ui.TemplateEngine,
	logger *slog.Logger,
) *SimpleDashboardHandler {
	return &SimpleDashboardHandler{
		templateEngine: templateEngine,
		logger:         logger.With("component", "simple_dashboard_handler"),
	}
}

// ServeHTTP handles GET /dashboard requests.
func (h *SimpleDashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Prepare mock dashboard data
	data := h.getMockDashboardData()

	// Prepare template data
	pageData := ui.PageData{
		Title: "Dashboard - Alertmanager++",
		Breadcrumbs: []ui.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Dashboard", URL: "/dashboard"},
		},
		Data: data,
	}

	// Render template
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templateEngine.Render(w, "pages/dashboard", pageData); err != nil {
		h.logger.Error("Failed to render dashboard template", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}

	duration := time.Since(startTime)
	h.logger.Info("Dashboard rendered",
		"duration_ms", duration.Milliseconds(),
	)
}

// getMockDashboardData returns mock dashboard data for demonstration.
func (h *SimpleDashboardHandler) getMockDashboardData() *ModernDashboardData {
	now := time.Now()

	return &ModernDashboardData{
		// Stats
		FiringAlerts:    42,
		ResolvedAlerts:  128,
		ActiveSilences:  5,
		InhibitedAlerts: 8,

		// Recent Alerts
		RecentAlerts: []AlertSummary{
			{
				Fingerprint: "abc123",
				AlertName:   "HighMemoryUsage",
				Status:      "firing",
				Severity:    "critical",
				Summary:     "Pod production-api-7d9f8 at 95% memory usage - immediate action required",
				Description: "Memory usage has exceeded 90% threshold for the past 5 minutes",
				Labels: map[string]string{
					"namespace": "production",
					"pod":       "api-7d9f8",
					"severity":  "critical",
				},
				StartsAt: now.Add(-30 * time.Minute),
				AIClassification: &AIClassification{
					Severity:   "critical",
					Confidence: 0.92,
					Reasoning:  "Memory usage exceeds 90% threshold, pod restart imminent",
					ActionItems: []string{
						"Scale horizontally to reduce load",
						"Investigate potential memory leak",
					},
				},
			},
			{
				Fingerprint: "def456",
				AlertName:   "HighCPULoad",
				Status:      "firing",
				Severity:    "warning",
				Summary:     "CPU load average is high on node-worker-3",
				Description: "5-minute load average is 4.5 on a 4-core machine",
				Labels: map[string]string{
					"node":     "worker-3",
					"severity": "warning",
				},
				StartsAt: now.Add(-15 * time.Minute),
				AIClassification: &AIClassification{
					Severity:   "warning",
					Confidence: 0.85,
					Reasoning:  "CPU load is elevated but not critical yet",
					ActionItems: []string{
						"Monitor for sustained high load",
					},
				},
			},
			{
				Fingerprint: "ghi789",
				AlertName:   "DiskSpaceWarning",
				Status:      "firing",
				Severity:    "warning",
				Summary:     "Disk space at 75% on /data partition",
				Labels: map[string]string{
					"host":      "db-master",
					"partition": "/data",
				},
				StartsAt: now.Add(-2 * time.Hour),
			},
		},

		// Active Silences
		ActiveSilencesList: []SilenceSummary{
			{
				ID:      "silence-123",
				Creator: "ops-team",
				Comment: "Maintenance window: Database migration in progress",
				Matchers: []Matcher{
					{Name: "alertname", Operator: "=", Value: "HighMemoryUsage"},
					{Name: "namespace", Operator: "=", Value: "production"},
				},
				StartsAt:  now.Add(-30 * time.Minute),
				EndsAt:    now.Add(90 * time.Minute),
				Status:    "active",
				ExpiresIn: "1h 30m",
			},
			{
				ID:      "silence-456",
				Creator: "platform-team",
				Comment: "Planned upgrade: Kubernetes control plane",
				Matchers: []Matcher{
					{Name: "severity", Operator: "=", Value: "warning"},
				},
				StartsAt:  now.Add(-10 * time.Minute),
				EndsAt:    now.Add(50 * time.Minute),
				Status:    "active",
				ExpiresIn: "50m",
			},
		},

		// Health Status
		Health: &HealthStatus{
			Overall: "healthy",
			Components: []HealthCheck{
				{
					Name:    "PostgreSQL",
					Status:  "healthy",
					Latency: 2.3,
				},
				{
					Name:    "Redis",
					Status:  "healthy",
					Latency: 0.8,
				},
				{
					Name:    "LLM Service",
					Status:  "degraded",
					Latency: 450,
					Message: "High latency detected",
				},
				{
					Name:    "Publishing Queue",
					Status:  "healthy",
					Latency: 5.1,
					Message: "12 jobs in queue",
				},
			},
		},

		// Timeline Data (optional - will render server-side SVG)
		AlertTimeline: nil, // Using static SVG in partial template for now
	}
}
