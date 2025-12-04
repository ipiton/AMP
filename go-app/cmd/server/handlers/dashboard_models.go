// Package handlers provides HTTP handlers for the dashboard.
// TN-77: Modern Dashboard Page (150% Quality Target)
package handlers

import "time"

// ModernDashboardData is the main data structure for modern dashboard page (TN-77).
type ModernDashboardData struct {
	// Stats Overview
	FiringAlerts    int `json:"firing_alerts"`
	ResolvedAlerts  int `json:"resolved_today"`
	ActiveSilences  int `json:"active_silences"`
	InhibitedAlerts int `json:"inhibited_alerts"`

	// Recent Data
	RecentAlerts         []AlertSummary   `json:"recent_alerts"`
	ActiveSilencesList   []SilenceSummary `json:"active_silences_list"`
	AlertTimeline        *TimelineData    `json:"alert_timeline,omitempty"`
	Health               *HealthStatus    `json:"health,omitempty"`
}

// AlertSummary is a compact alert representation for dashboard.
type AlertSummary struct {
	Fingerprint      string            `json:"fingerprint"`
	AlertName        string            `json:"alertname"`
	Status           string            `json:"status"`       // firing, resolved
	Severity         string            `json:"severity"`     // critical, warning, info
	Summary          string            `json:"summary"`
	Description      string            `json:"description"`
	Labels           map[string]string `json:"labels"`
	StartsAt         time.Time         `json:"starts_at"`
	EndsAt           *time.Time        `json:"ends_at,omitempty"`
	AIClassification *AIClassification `json:"ai_classification,omitempty"`
}

// AIClassification contains LLM-generated metadata.
type AIClassification struct {
	Severity    string   `json:"severity"`     // critical, warning, info, noise
	Confidence  float64  `json:"confidence"`   // 0.0-1.0
	Reasoning   string   `json:"reasoning"`
	ActionItems []string `json:"action_items,omitempty"`
}

// SilenceSummary is a compact silence representation.
type SilenceSummary struct {
	ID        string            `json:"id"`
	Creator   string            `json:"creator"`
	Comment   string            `json:"comment"`
	Matchers  []Matcher         `json:"matchers"`
	StartsAt  time.Time         `json:"starts_at"`
	EndsAt    time.Time         `json:"ends_at"`
	Status    string            `json:"status"` // active, pending, expired
	ExpiresIn string            `json:"expires_in"`
}

// Matcher represents a silence matcher.
type Matcher struct {
	Name     string `json:"name"`
	Operator string `json:"operator"` // =, !=, =~, !~
	Value    string `json:"value"`
}

// TimelineData for alert timeline chart.
type TimelineData struct {
	Labels []string          `json:"labels"` // ["00:00", "01:00", ...]
	Series []TimelineSeries  `json:"series"`
}

// TimelineSeries represents a data series in the timeline chart.
type TimelineSeries struct {
	Name   string  `json:"name"`   // "Critical", "Warning", "Info"
	Color  string  `json:"color"`  // "#f44336"
	Values []int   `json:"values"` // [5, 12, 8, ...]
}

// HealthStatus contains system health metrics.
type HealthStatus struct {
	Overall    string        `json:"overall"` // healthy, degraded, unhealthy
	Components []HealthCheck `json:"components"`
}

// HealthCheck represents a single component health check.
type HealthCheck struct {
	Name    string  `json:"name"`       // "PostgreSQL", "Redis", "LLM"
	Status  string  `json:"status"`     // healthy, degraded, unhealthy
	Latency float64 `json:"latency_ms"` // milliseconds
	Message string  `json:"message,omitempty"`
}
