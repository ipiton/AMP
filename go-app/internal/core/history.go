package core

import (
	"context"
	"time"
)

// AlertHistoryRepository provides methods for querying alert history with pagination,
// filtering, sorting, and analytics capabilities.
type AlertHistoryRepository interface {
	// GetHistory retrieves paginated alert history with advanced filtering and sorting
	GetHistory(ctx context.Context, req *HistoryRequest) (*HistoryResponse, error)

	// GetAlertsByFingerprint retrieves all alerts with the same fingerprint (alert timeline)
	GetAlertsByFingerprint(ctx context.Context, fingerprint string, limit int) ([]*Alert, error)

	// GetRecentAlerts retrieves the most recent alerts across all fingerprints
	GetRecentAlerts(ctx context.Context, limit int) ([]*Alert, error)

	// GetAggregatedStats computes statistical aggregations over a time range
	GetAggregatedStats(ctx context.Context, timeRange *TimeRange) (*AggregatedStats, error)

	// GetTopAlerts returns the most frequently firing alerts
	GetTopAlerts(ctx context.Context, timeRange *TimeRange, limit int) ([]*TopAlert, error)

	// GetFlappingAlerts detects alerts that frequently transition between states
	GetFlappingAlerts(ctx context.Context, timeRange *TimeRange, threshold int) ([]*FlappingAlert, error)
}

// HistoryRequest represents a request for alert history with all parameters
type HistoryRequest struct {
	Filters    *AlertFilters `json:"filters" validate:"omitempty"`
	Pagination *Pagination   `json:"pagination" validate:"required"`
	Sorting    *Sorting      `json:"sorting,omitempty"`
}

// Validate validates the history request
func (r *HistoryRequest) Validate() error {
	if r.Pagination == nil {
		return ErrInvalidPagination
	}
	if err := r.Pagination.Validate(); err != nil {
		return err
	}
	if r.Filters != nil {
		if err := r.Filters.Validate(); err != nil {
			return err
		}
	}
	if r.Sorting != nil {
		if err := r.Sorting.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// HistoryResponse represents a paginated response with metadata
type HistoryResponse struct {
	Alerts     []*Alert `json:"alerts"`
	Total      int64    `json:"total"`
	Page       int      `json:"page"`
	PerPage    int      `json:"per_page"`
	TotalPages int      `json:"total_pages"`
	HasNext    bool     `json:"has_next"`
	HasPrev    bool     `json:"has_prev"`
}

// Pagination represents pagination parameters
type Pagination struct {
	Page    int `json:"page" validate:"min=1"`
	PerPage int `json:"per_page" validate:"min=1,max=1000"`
}

// Validate validates pagination parameters
func (p *Pagination) Validate() error {
	if p.Page < 1 {
		return ErrInvalidPage
	}
	if p.PerPage < 1 {
		return ErrInvalidPerPage
	}
	if p.PerPage > 1000 {
		return ErrPerPageTooLarge
	}
	return nil
}

// Offset calculates the SQL offset for pagination
func (p *Pagination) Offset() int {
	return (p.Page - 1) * p.PerPage
}

// Sorting represents sorting parameters
type Sorting struct {
	Field string    `json:"field" validate:"required,oneof=created_at starts_at ends_at status severity"`
	Order SortOrder `json:"order" validate:"required,oneof=asc desc"`
}

// Validate validates sorting parameters
func (s *Sorting) Validate() error {
	if s.Field == "" {
		return ErrInvalidSortField
	}
	validFields := map[string]bool{
		"created_at": true,
		"starts_at":  true,
		"ends_at":    true,
		"status":     true,
		"severity":   true,
		"updated_at": true,
	}
	if !validFields[s.Field] {
		return ErrInvalidSortField
	}
	if s.Order != SortOrderAsc && s.Order != SortOrderDesc {
		return ErrInvalidSortOrder
	}
	return nil
}

// ToSQL converts sorting to SQL ORDER BY clause
func (s *Sorting) ToSQL() string {
	if s == nil {
		return "starts_at DESC" // default sorting
	}
	return s.Field + " " + string(s.Order)
}

// SortOrder represents sorting direction
type SortOrder string

const (
	SortOrderAsc  SortOrder = "asc"
	SortOrderDesc SortOrder = "desc"
)

// AggregatedStats represents statistical aggregations
type AggregatedStats struct {
	TimeRange         *TimeRange         `json:"time_range"`
	TotalAlerts       int64              `json:"total_alerts"`
	FiringAlerts      int64              `json:"firing_alerts"`
	ResolvedAlerts    int64              `json:"resolved_alerts"`
	AlertsByStatus    map[string]int64   `json:"alerts_by_status"`
	AlertsBySeverity  map[string]int64   `json:"alerts_by_severity"`
	AlertsByNamespace map[string]int64   `json:"alerts_by_namespace"`
	UniqueFingerprints int64             `json:"unique_fingerprints"`
	AvgResolutionTime *time.Duration     `json:"avg_resolution_time,omitempty"`
	Trends            *TrendData         `json:"trends,omitempty"`
}

// TrendData represents time-series trend data
type TrendData struct {
	Hourly  []*DataPoint `json:"hourly,omitempty"`
	Daily   []*DataPoint `json:"daily,omitempty"`
	Weekly  []*DataPoint `json:"weekly,omitempty"`
}

// DataPoint represents a single data point in a time series
type DataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
	Value     float64   `json:"value,omitempty"`
}

// TopAlert represents an alert with frequency statistics
type TopAlert struct {
	Fingerprint   string    `json:"fingerprint"`
	AlertName     string    `json:"alert_name"`
	Namespace     *string   `json:"namespace,omitempty"`
	FireCount     int64     `json:"fire_count"`
	LastFiredAt   time.Time `json:"last_fired_at"`
	AvgDuration   *float64  `json:"avg_duration,omitempty"`
}

// FlappingAlert represents an alert that frequently changes state
type FlappingAlert struct {
	Fingerprint      string    `json:"fingerprint"`
	AlertName        string    `json:"alert_name"`
	Namespace        *string   `json:"namespace,omitempty"`
	TransitionCount  int64     `json:"transition_count"`
	FlappingScore    float64   `json:"flapping_score"`
	LastTransitionAt time.Time `json:"last_transition_at"`
}

// ============================================================================
// TN-064: Report Analytics Endpoint Types
// ============================================================================

// ReportRequest represents request parameters for GET /api/v2/report
type ReportRequest struct {
	TimeRange     *TimeRange `json:"time_range,omitempty"`
	Namespace     *string    `json:"namespace,omitempty"`
	Severity      *string    `json:"severity,omitempty"`
	TopLimit      int        `json:"top_limit" validate:"min=1,max=100"`
	MinFlapCount  int        `json:"min_flap_count" validate:"min=1,max=100"`
	IncludeRecent bool       `json:"include_recent"`
}

// ReportResponse represents the complete analytics report
type ReportResponse struct {
	Metadata       *ReportMetadata    `json:"metadata"`
	Summary        *AggregatedStats   `json:"summary"`
	TopAlerts      []*TopAlert        `json:"top_alerts"`
	FlappingAlerts []*FlappingAlert   `json:"flapping_alerts"`
	RecentAlerts   []*Alert           `json:"recent_alerts,omitempty"`
}

// ReportMetadata contains report generation metadata
type ReportMetadata struct {
	GeneratedAt      time.Time `json:"generated_at"`
	RequestID        string    `json:"request_id"`
	ProcessingTimeMs int64     `json:"processing_time_ms"`
	CacheHit         bool      `json:"cache_hit"`
	PartialFailure   bool      `json:"partial_failure"`
	Errors           []string  `json:"errors,omitempty"`
}
