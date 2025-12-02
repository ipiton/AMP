package interfaces

import (
	"context"
	"time"
)

// ================================================================================
// Storage Interfaces - OSS Core
// ================================================================================
// These interfaces define storage contracts for alert persistence.
// OSS implementations: PostgreSQL, SQLite, Memory
// Custom implementations: TimescaleDB, ClickHouse, etc.

// Alert represents a core alert (defined in domain package to avoid circular deps)
// This is a forward declaration - actual type in pkg/core/domain
type Alert interface{}

// StorageBackend defines the main storage interface
type StorageBackend interface {
	// Core operations
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Health(ctx context.Context) error

	// Alert operations
	StoreAlert(ctx context.Context, alert Alert) error
	GetAlert(ctx context.Context, fingerprint string) (Alert, error)
	QueryAlerts(ctx context.Context, filters QueryFilters) ([]Alert, error)
	DeleteAlert(ctx context.Context, fingerprint string) error

	// Batch operations
	StoreBatch(ctx context.Context, alerts []Alert) error

	// Statistics
	GetStats(ctx context.Context) (*StorageStats, error)
}

// QueryFilters defines filtering options for alert queries
type QueryFilters struct {
	// Time range
	StartTime *time.Time
	EndTime   *time.Time

	// Label filters
	Labels map[string]string // Exact match
	LabelsRegex map[string]string // Regex match

	// Status filters
	Status []string // firing, resolved

	// Pagination
	Limit  int
	Offset int

	// Sorting
	SortBy    string // field name
	SortOrder string // asc, desc
}

// StorageStats provides storage statistics
type StorageStats struct {
	TotalAlerts     int64
	FiringAlerts    int64
	ResolvedAlerts  int64
	StorageSizeBytes int64
	OldestAlert     *time.Time
	NewestAlert     *time.Time
}

// HistoryStorage defines extended history storage (optional)
type HistoryStorage interface {
	StorageBackend

	// Extended queries
	GetAlertHistory(ctx context.Context, fingerprint string, days int) ([]Alert, error)
	GetTopAlerts(ctx context.Context, limit int, days int) ([]AlertStats, error)
	GetFlappingAlerts(ctx context.Context, threshold int, days int) ([]Alert, error)
	GetTrends(ctx context.Context, days int) (*TrendData, error)
}

// AlertStats represents alert statistics
type AlertStats struct {
	Fingerprint string
	AlertName   string
	Count       int64
	LastSeen    time.Time
}

// TrendData represents trend analysis
type TrendData struct {
	Period       string // day, week, month
	TotalAlerts  []int64
	FiringAlerts []int64
	Labels       []string // time labels
}

// ================================================================================
// Classification Storage
// ================================================================================

// ClassificationResult represents alert classification (defined in domain)
type ClassificationResult interface{}

// ClassificationStorage stores classification results
type ClassificationStorage interface {
	StoreClassification(ctx context.Context, fingerprint string, result ClassificationResult) error
	GetClassification(ctx context.Context, fingerprint string) (ClassificationResult, error)
	DeleteClassification(ctx context.Context, fingerprint string) error
}

// ================================================================================
// Silence Storage
// ================================================================================

// Silence represents a silence rule (defined in domain)
type Silence interface{}

// SilenceStorage stores silence rules
type SilenceStorage interface {
	CreateSilence(ctx context.Context, silence Silence) (string, error) // returns ID
	GetSilence(ctx context.Context, id string) (Silence, error)
	UpdateSilence(ctx context.Context, silence Silence) error
	DeleteSilence(ctx context.Context, id string) error
	ListSilences(ctx context.Context, filters SilenceFilters) ([]Silence, error)
	GetActiveSilences(ctx context.Context) ([]Silence, error)
}

// SilenceFilters defines filtering options for silence queries
type SilenceFilters struct {
	Status   []string // active, pending, expired
	Creator  string
	StartTime *time.Time
	EndTime   *time.Time
	Limit    int
	Offset   int
}

// ================================================================================
// Template Storage
// ================================================================================

// Template represents a notification template (defined in domain)
type Template interface{}

// TemplateStorage stores notification templates
type TemplateStorage interface {
	CreateTemplate(ctx context.Context, template Template) (string, error) // returns ID
	GetTemplate(ctx context.Context, id string) (Template, error)
	UpdateTemplate(ctx context.Context, template Template) error
	DeleteTemplate(ctx context.Context, id string) error
	ListTemplates(ctx context.Context, filters TemplateFilters) ([]Template, error)
}

// TemplateFilters defines filtering options for template queries
type TemplateFilters struct {
	Type   string // slack, pagerduty, email, webhook
	Active *bool
	Limit  int
	Offset int
}
