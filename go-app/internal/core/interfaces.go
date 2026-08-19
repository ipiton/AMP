package core

import (
	"context"
	"time"
)

// AlertSeverity represents alert severity levels
type AlertSeverity string

const (
	SeverityCritical AlertSeverity = "critical"
	SeverityWarning  AlertSeverity = "warning"
	SeverityInfo     AlertSeverity = "info"
	SeverityNoise    AlertSeverity = "noise"
)

// AlertStatus represents alert status values
type AlertStatus string

const (
	StatusFiring   AlertStatus = "firing"
	StatusResolved AlertStatus = "resolved"
)

// PublishingFormat represents publishing format options
type PublishingFormat string

const (
	FormatAlertmanager PublishingFormat = "alertmanager"
	FormatRootly       PublishingFormat = "rootly"
	FormatPagerDuty    PublishingFormat = "pagerduty"
	FormatSlack        PublishingFormat = "slack"
	FormatWebhook      PublishingFormat = "webhook"
	FormatTelegram     PublishingFormat = "telegram"
)

// Alert represents alert data model
type Alert struct {
	Fingerprint  string            `json:"fingerprint" validate:"required"`
	AlertName    string            `json:"alert_name" validate:"required"`
	Status       AlertStatus       `json:"status" validate:"required,oneof=firing resolved"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"starts_at" validate:"required"`
	EndsAt       *time.Time        `json:"ends_at,omitempty"`
	GeneratorURL *string           `json:"generator_url,omitempty" validate:"omitempty,url"`
	Timestamp    *time.Time        `json:"timestamp,omitempty"`
}

// DeliveryKey identifies this alert for notification-delivery bookkeeping:
// "<fingerprint>:<status>" (task fu4, alertmanager-parity wave 4 — per-alert
// outcome tracking for non-batch publishers).
//
// It is the ATOM shared by two otherwise unrelated pieces of state, and they
// MUST agree or the per-alert delivered-set stops matching the group's dedup
// signature:
//
//   - grouping.alertSetSignature — the sorted, "|"-joined list of exactly
//     these keys, stored in the notification log per (group, target) and
//     compared to decide whether a group's alert set changed since the last
//     send.
//   - the per-(group, target) DELIVERED SET — which individual alerts a
//     non-batch target (Slack/Telegram/PagerDuty/Email, one wire message per
//     alert) already accepted, so a partial failure re-sends only the alerts
//     that did not land instead of the whole set.
//
// Status is part of the key on purpose: an alert flipping firing<->resolved
// is a NEW notification, never a delivered duplicate — same reasoning that
// makes the group signature status-sensitive. Fingerprint alone would
// suppress the resolved notification for an alert whose firing notification
// already landed.
//
// Lives here, in core, rather than in either consumer, because
// infrastructure/grouping and infrastructure/publishing deliberately do not
// import each other (see grouping.GroupNotificationPublisher) and would
// otherwise each carry their own copy of the format.
func (a *Alert) DeliveryKey() string {
	if a == nil {
		return ""
	}
	return a.Fingerprint + ":" + string(a.Status)
}

// Namespace returns alert namespace from labels
func (a *Alert) Namespace() *string {
	if ns, ok := a.Labels["namespace"]; ok {
		return &ns
	}
	return nil
}

// Severity returns alert severity from labels
func (a *Alert) Severity() *string {
	if sev, ok := a.Labels["severity"]; ok {
		return &sev
	}
	return nil
}

// ClassificationResult represents LLM classification result
type ClassificationResult struct {
	Severity        AlertSeverity  `json:"severity" validate:"required,oneof=critical warning info noise"`
	Confidence      float64        `json:"confidence" validate:"gte=0,lte=1"`
	Reasoning       string         `json:"reasoning" validate:"required"`
	Recommendations []string       `json:"recommendations"`
	ProcessingTime  float64        `json:"processing_time" validate:"gte=0"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// PublishingTarget represents publishing target configuration
type PublishingTarget struct {
	Name         string            `json:"name" validate:"required"`
	Type         string            `json:"type" validate:"required"`
	URL          string            `json:"url" validate:"required,url"`
	Enabled      bool              `json:"enabled"`
	FilterConfig map[string]any    `json:"filter_config"`
	Headers      map[string]string `json:"headers"`
	Format       PublishingFormat  `json:"format" validate:"required,oneof=alertmanager rootly pagerduty slack webhook telegram"`

	// Receivers holds the Alertmanager receiver names this target is scoped
	// to, sourced from the `amp.receiver` label on the target's K8s Secret
	// (comma-separated list, e.g. "slack-critical,pagerduty-oncall").
	//
	// Empty/nil means the target has NO receiver scoping: it belongs to
	// ALL receivers (backward compatibility with targets predating
	// receiver-based routing). See PublishingCoordinator.PublishToTargets.
	Receivers []string `json:"receivers,omitempty"`

	// HTTPConfig is upstream Alertmanager's per-integration `http_config`
	// (FU-HTTP-CONFIG, wave 7 track C): proxy, TLS, basic auth, bearer
	// authorization and redirect policy for THIS target's HTTP client.
	//
	// nil (the overwhelmingly common case) means the publisher keeps its own
	// built-in client and nothing about delivery changes. When set, the
	// publisher factory builds a dedicated *http.Client from it and includes
	// HTTPClientConfig.Fingerprint() in every client cache key, so two targets
	// sharing a URL and a credential but differing in http_config can never
	// share a client.
	//
	// BOTH target sources populate it: BuildConfigTargets maps
	// `receivers[].<kind>_configs[].http_config` (with the `global.http_config`
	// fallback already resolved at parse time), and a K8s Secret target can
	// carry an `http_config` object in its JSON blob — same field name, same
	// shape, no extra parsing code.
	HTTPConfig *HTTPClientConfig `json:"http_config,omitempty"`
}

// EnrichedAlert represents alert enriched with classification data
type EnrichedAlert struct {
	Alert               *Alert                `json:"alert"`
	Classification      *ClassificationResult `json:"classification,omitempty"`
	EnrichmentMetadata  map[string]any        `json:"enrichment_metadata,omitempty"`
	ProcessingTimestamp *time.Time            `json:"processing_timestamp,omitempty"`
}

// Database interfaces following SOLID principles

// TimeRange represents time range filter
type TimeRange struct {
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

// AlertFilters represents filters for alert queries
type AlertFilters struct {
	Status    *AlertStatus      `json:"status,omitempty"`
	Severity  *string           `json:"severity,omitempty"`
	Namespace *string           `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	TimeRange *TimeRange        `json:"time_range,omitempty"`
	Limit     int               `json:"limit" validate:"gte=0,lte=1000"`
	Offset    int               `json:"offset" validate:"gte=0"`
}

// Validate validates AlertFilters parameters
func (f *AlertFilters) Validate() error {
	// Validate Limit
	if f.Limit < 0 {
		return ErrInvalidFilterLimit
	}
	if f.Limit > 1000 {
		return ErrFilterLimitTooLarge
	}

	// Validate Offset
	if f.Offset < 0 {
		return ErrInvalidFilterOffset
	}

	// Validate Status
	if f.Status != nil {
		if *f.Status != StatusFiring && *f.Status != StatusResolved {
			return ErrInvalidFilterStatus
		}
	}

	// Validate Severity
	if f.Severity != nil {
		validSeverities := map[string]bool{
			"critical": true,
			"warning":  true,
			"info":     true,
			"noise":    true,
		}
		if !validSeverities[*f.Severity] {
			return ErrInvalidFilterSeverity
		}
	}

	// Validate TimeRange
	if f.TimeRange != nil {
		if f.TimeRange.From != nil && f.TimeRange.To != nil {
			if f.TimeRange.From.After(*f.TimeRange.To) {
				return ErrInvalidTimeRange
			}
		}
	}

	// Validate Labels (max 20 labels)
	if len(f.Labels) > 20 {
		return ErrTooManyLabels
	}

	// Validate label keys and values (max 255 chars each)
	for key, value := range f.Labels {
		if len(key) == 0 {
			return ErrEmptyLabelKey
		}
		if len(key) > 255 {
			return ErrLabelKeyTooLong
		}
		if len(value) > 255 {
			return ErrLabelValueTooLong
		}
	}

	return nil
}

// AlertList represents paginated list of alerts
type AlertList struct {
	Alerts []*Alert `json:"alerts"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

// AlertStats represents alert statistics
type AlertStats struct {
	TotalAlerts       int            `json:"total_alerts"`
	AlertsByStatus    map[string]int `json:"alerts_by_status"`
	AlertsBySeverity  map[string]int `json:"alerts_by_severity"`
	AlertsByNamespace map[string]int `json:"alerts_by_namespace"`
	OldestAlert       *time.Time     `json:"oldest_alert,omitempty"`
	NewestAlert       *time.Time     `json:"newest_alert,omitempty"`
}

// AlertStorage interface for alert storage operations
type AlertStorage interface {
	// Basic CRUD operations
	SaveAlert(ctx context.Context, alert *Alert) error
	GetAlertByFingerprint(ctx context.Context, fingerprint string) (*Alert, error)
	ListAlerts(ctx context.Context, filters *AlertFilters) (*AlertList, error)
	UpdateAlert(ctx context.Context, alert *Alert) error
	DeleteAlert(ctx context.Context, fingerprint string) error

	// Additional operations
	GetAlertStats(ctx context.Context) (*AlertStats, error)
	CleanupOldAlerts(ctx context.Context, retentionDays int) (int, error)
}

// ClassificationStorage interface for classification storage operations
type ClassificationStorage interface {
	SaveClassification(ctx context.Context, fingerprint string, result *ClassificationResult) error
	GetClassification(ctx context.Context, fingerprint string) (*ClassificationResult, error)
}

// PublishingLogStorage interface for publishing log storage
type PublishingLogStorage interface {
	LogPublishingAttempt(ctx context.Context, fingerprint, targetName string, success bool, errorMessage *string, processingTime *float64) error
	GetPublishingHistory(ctx context.Context, fingerprint string) ([]*PublishingLog, error)
}

// PublishingLog represents publishing attempt log
type PublishingLog struct {
	ID             string    `json:"id"`
	Fingerprint    string    `json:"fingerprint"`
	TargetName     string    `json:"target_name"`
	Success        bool      `json:"success"`
	ErrorMessage   *string   `json:"error_message,omitempty"`
	ProcessingTime *float64  `json:"processing_time,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Combined Database interface for full functionality
type Database interface {
	// Core database operations
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Health(ctx context.Context) error

	// Alert operations
	AlertStorage

	// Classification operations
	ClassificationStorage

	// Publishing operations
	PublishingLogStorage

	// Migration operations
	MigrateUp(ctx context.Context) error
	MigrateDown(ctx context.Context, steps int) error

	// Utility operations
	GetStats(ctx context.Context) (map[string]interface{}, error)
}

// Cache interfaces

// Cache interface for generic caching
type Cache interface {
	Get(ctx context.Context, key string) (any, error)
	Set(ctx context.Context, key string, value any, ttl *time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// DistributedLock interface for distributed locking
type DistributedLock interface {
	AcquireLock(ctx context.Context, key string, timeout time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, key string) error
}

// LLM Service interfaces

// LLMClient interface for LLM communication
type LLMClient interface {
	ClassifyAlert(ctx context.Context, alert *Alert, context map[string]any) (*ClassificationResult, error)
	GenerateRecommendations(ctx context.Context, alert *Alert, classification *ClassificationResult) ([]string, error)
}

// AlertClassifier interface for alert classification service
type AlertClassifier interface {
	Classify(ctx context.Context, alert *Alert) (*ClassificationResult, error)
}

// Publishing interfaces

// AlertFormatter interface for alert formatting
type AlertFormatter interface {
	FormatAlert(ctx context.Context, enrichedAlert *EnrichedAlert, targetFormat PublishingFormat) (map[string]any, error)
}

// AlertPublisher interface for alert publishing
type AlertPublisher interface {
	PublishAlert(ctx context.Context, enrichedAlert *EnrichedAlert, target *PublishingTarget) error
}

// FilterEngine interface for alert filtering
type FilterEngine interface {
	ShouldPublish(ctx context.Context, enrichedAlert *EnrichedAlert, target *PublishingTarget) (bool, error)
}

// Configuration Management interfaces

// ConfigurationManager interface for configuration management
type ConfigurationManager interface {
	GetConfig(ctx context.Context, key string, defaultValue any) (any, error)
	GetAllConfigs(ctx context.Context) (map[string]any, error)
	ReloadConfig(ctx context.Context) error
}

// SecretsManager interface for secrets management
type SecretsManager interface {
	GetSecret(ctx context.Context, key string) (string, error)
	ListSecrets(ctx context.Context, labelSelector string) (map[string]map[string]string, error)
}

// TargetDiscovery interface for dynamic target discovery
type TargetDiscovery interface {
	DiscoverTargets(ctx context.Context) ([]*PublishingTarget, error)
	RefreshTargets(ctx context.Context) error
}

// Health Check interface

// HealthChecker interface for health checking
type HealthChecker interface {
	CheckHealth(ctx context.Context) (map[string]any, error)
	CheckReadiness(ctx context.Context) (map[string]any, error)
}

// Metrics interface

// MetricsCollector interface for metrics collection
type MetricsCollector interface {
	IncrementCounter(ctx context.Context, name string, labels map[string]string)
	SetGauge(ctx context.Context, name string, value float64, labels map[string]string)
	ObserveHistogram(ctx context.Context, name string, value float64, labels map[string]string)
}

// Event Processing interface

// EventProcessor interface for event processing strategies
type EventProcessor interface {
	ProcessEvent(ctx context.Context, eventData map[string]any) error
	CanHandle(eventType string) bool
}

// Repository Pattern interface

// Repository interface for generic repository operations
type Repository[T any] interface {
	Create(ctx context.Context, entity *T) error
	GetByID(ctx context.Context, id string) (*T, error)
	Update(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filters map[string]any, limit, offset int) ([]*T, error)
}
