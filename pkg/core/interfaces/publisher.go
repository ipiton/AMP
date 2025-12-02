package interfaces

import "context"

// ================================================================================
// Publishing Interfaces - OSS Core Extension Point
// ================================================================================
// These interfaces define alert publishing/notification contracts.
//
// OSS Implementations: Slack, PagerDuty, Email, Webhook (all included!)
// Custom Implementations: MS Teams, Jira, ServiceNow, etc.

// AlertPublisher publishes alerts to external systems
type AlertPublisher interface {
	// Name returns publisher name (e.g., "slack", "pagerduty", "custom")
	Name() string

	// Type returns publisher type (e.g., "slack", "webhook", "email")
	Type() string

	// Publish sends alert to target
	Publish(ctx context.Context, alert EnrichedAlert, target PublishingTarget) error

	// Health checks publisher health (e.g., API connectivity)
	Health(ctx context.Context) error

	// Shutdown gracefully shuts down publisher
	Shutdown(ctx context.Context) error
}

// PublishingTarget defines where to publish alerts
type PublishingTarget struct {
	// Core fields
	Name     string // unique target name
	Type     string // slack, pagerduty, email, webhook
	Endpoint string // URL or endpoint

	// Authentication
	Headers map[string]string // HTTP headers (API keys, tokens)

	// Configuration (type-specific)
	Config map[string]interface{}

	// Filtering
	Filters []PublishingFilter // only publish if filters pass

	// Metadata
	Enabled    bool
	CreatedAt  int64
	UpdatedAt  int64
	CreatedBy  string
}

// PublishingFilter defines publishing conditions
type PublishingFilter struct {
	Type  string // label, severity, classification, time
	Field string // which field to check
	Op    string // eq, ne, regex, gt, lt
	Value string // value to compare
}

// ================================================================================
// Publisher Registry
// ================================================================================

// PublisherRegistry manages multiple publishers
type PublisherRegistry interface {
	// Register adds a publisher
	Register(name string, publisher AlertPublisher) error

	// Get retrieves a publisher by name
	Get(name string) (AlertPublisher, bool)

	// GetByType retrieves publishers by type
	GetByType(pubType string) []AlertPublisher

	// List returns all registered publishers
	List() []string

	// Unregister removes a publisher
	Unregister(name string) error
}

// ================================================================================
// Formatter Interface (converts alerts to target format)
// ================================================================================

// AlertFormatter formats alerts for specific targets
type AlertFormatter interface {
	// Format converts enriched alert to target format
	Format(ctx context.Context, alert EnrichedAlert, targetType string) (interface{}, error)

	// SupportedFormats returns list of supported formats
	SupportedFormats() []string
}

// ================================================================================
// OSS Publishers (Included in Core)
// ================================================================================
// These are the built-in publishers available in OSS edition.
// All are production-ready and well-documented.

// SlackPublisher configuration
type SlackConfig struct {
	WebhookURL string
	Channel    string
	Username   string
	IconEmoji  string

	// Threading support (group related alerts)
	EnableThreading bool
	ThreadTTL       int // seconds to keep thread ID cached

	// Formatting
	UseBlockKit  bool // modern Block Kit vs legacy attachments
	MentionUsers []string // @user mentions for critical alerts
}

// PagerDutyPublisher configuration
type PagerDutyConfig struct {
	IntegrationKey string // Events API v2 key
	Severity       string // critical, error, warning, info

	// Incident management
	DedupKey        string // deduplication key
	Links           []string // links to runbooks, dashboards
	CustomDetails   map[string]interface{} // additional context

	// Change Events
	EnableChangeEvents bool // track deployments
}

// EmailPublisher configuration
type EmailConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string

	// Message
	From    string
	To      []string
	CC      []string
	Subject string

	// Formatting
	UseHTML   bool // HTML vs plain text
	Template  string // template name
}

// WebhookPublisher configuration (generic HTTP POST)
type WebhookConfig struct {
	URL     string
	Method  string // POST, PUT, PATCH
	Headers map[string]string

	// Authentication
	AuthType string // none, basic, bearer, apikey
	AuthToken string

	// Retry
	MaxRetries  int
	RetryDelay  int // seconds
	Timeout     int // seconds

	// TLS
	SkipTLSVerify bool
	CABundle      []byte
}

// ================================================================================
// Custom Publisher Example
// ================================================================================
// This shows how easy it is to add custom publishers.

/*
// Example: MS Teams Publisher
type MSTeamsPublisher struct {
    webhookURL string
}

func (p *MSTeamsPublisher) Name() string { return "ms-teams" }
func (p *MSTeamsPublisher) Type() string { return "teams" }

func (p *MSTeamsPublisher) Publish(ctx context.Context, alert EnrichedAlert, target PublishingTarget) error {
    // Format alert as Adaptive Card
    card := formatAsAdaptiveCard(alert)

    // POST to Teams webhook
    return httpPost(p.webhookURL, card)
}

func (p *MSTeamsPublisher) Health(ctx context.Context) error {
    // Check webhook reachable
    return httpGet(p.webhookURL)
}

// Register it:
registry.Register("ms-teams", &MSTeamsPublisher{...})
*/

// ================================================================================
// Advanced Publishing Features
// ================================================================================

// ParallelPublisher publishes to multiple targets in parallel
type ParallelPublisher interface {
	// PublishToAll publishes to all configured targets
	PublishToAll(ctx context.Context, alert EnrichedAlert) error

	// PublishToTargets publishes to specific targets
	PublishToTargets(ctx context.Context, alert EnrichedAlert, targets []PublishingTarget) error

	// GetStats returns publishing statistics
	GetStats(ctx context.Context) (*PublishingStats, error)
}

// PublishingStats provides publishing metrics
type PublishingStats struct {
	TotalPublished int64
	TotalFailed    int64
	TargetStats    map[string]*TargetStats
}

// TargetStats per-target statistics
type TargetStats struct {
	Name      string
	Type      string
	Published int64
	Failed    int64
	AvgLatency float64 // milliseconds
	LastPublished int64 // Unix timestamp
	LastError     string
}

// PublishingQueue manages async publishing with retry
type PublishingQueue interface {
	// Enqueue adds alert to publishing queue
	Enqueue(ctx context.Context, alert EnrichedAlert, target PublishingTarget) error

	// GetQueueSize returns current queue depth
	GetQueueSize(ctx context.Context) (int64, error)

	// Start starts queue workers
	Start(ctx context.Context) error

	// Stop gracefully stops queue workers
	Stop(ctx context.Context) error
}
