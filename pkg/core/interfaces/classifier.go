package interfaces

import "context"

// ================================================================================
// Classification Interfaces - OSS Core Extension Point
// ================================================================================
// These interfaces define alert classification contracts.
//
// OSS Implementation: Rule-based classifier (free, always available)
// Custom Implementations: LLM-based (OpenAI, Anthropic), ML models, external APIs
//
// Key Design: Core knows NOTHING about classification implementation.
// Implementations are injected via dependency injection.

// AlertClassifier classifies alerts and assigns metadata
type AlertClassifier interface {
	// Name returns the classifier name (e.g., "rule-based", "openai", "anthropic")
	Name() string

	// Classify analyzes an alert and returns classification result
	Classify(ctx context.Context, alert Alert) (*ClassificationResult, error)

	// ClassifyBatch processes multiple alerts efficiently
	ClassifyBatch(ctx context.Context, alerts []Alert) ([]*ClassificationResult, error)

	// Health checks classifier health (e.g., API connectivity)
	Health(ctx context.Context) error
}

// ClassificationResult contains classification output
// This is returned by ALL classifiers (rule-based, LLM, ML, etc.)
type ClassificationResult struct {
	// Core fields (always populated)
	Severity   string  // critical, warning, info
	Confidence float64 // 0.0-1.0

	// Optional fields (may be nil/empty)
	Category    string   // network, database, application, infrastructure
	Priority    string   // p0, p1, p2, p3, p4
	Tags        []string // additional tags
	Reasoning   string   // why this classification (useful for LLM)
	Suggestions []string // recommended actions

	// Metadata
	ClassifierName string // which classifier produced this
	ClassifiedAt   int64  // Unix timestamp
	ModelVersion   string // model/rule version used

	// Cost tracking (for paid classifiers like LLM)
	TokensUsed    int     // for LLM APIs
	CostUSD       float64 // estimated cost
}

// ClassifierConfig holds classifier configuration
type ClassifierConfig struct {
	Name    string
	Type    string // "rule-based", "llm", "ml", "custom"
	Enabled bool

	// Generic config (implementation-specific)
	Config map[string]interface{}

	// Fallback classifier (if this fails)
	Fallback AlertClassifier
}

// ================================================================================
// Rule-Based Classifier (OSS Default)
// ================================================================================
// The OSS edition includes a rule-based classifier that's always available.
// It uses label-based rules to classify alerts without external dependencies.

// ClassificationRule defines a single classification rule
type ClassificationRule struct {
	Name        string
	Description string

	// Matching conditions
	LabelMatchers map[string]string // label -> regex pattern

	// Classification output
	Severity string
	Category string
	Priority string
	Tags     []string

	// Rule metadata
	Enabled bool
	Weight  int // for conflict resolution (higher wins)
}

// RuleBasedClassifier configuration
type RuleBasedClassifierConfig struct {
	Rules []ClassificationRule

	// Defaults if no rules match
	DefaultSeverity string
	DefaultCategory string
	DefaultPriority string

	// Behavior
	AllowMultipleMatches bool // if false, use highest weight rule
}

// ================================================================================
// Enrichment Interface (Optional)
// ================================================================================
// Enrichers add metadata to alerts without changing core classification.
// This is separate from classification to allow composition.

// AlertEnricher adds metadata to alerts
type AlertEnricher interface {
	// Name returns enricher name
	Name() string

	// Enrich adds metadata to alert
	Enrich(ctx context.Context, alert Alert) (*EnrichedAlert, error)

	// Health checks enricher health
	Health(ctx context.Context) error
}

// EnrichedAlert is an alert with additional metadata
type EnrichedAlert struct {
	// Original alert (embedded)
	Alert Alert

	// Classification (from AlertClassifier)
	Classification *ClassificationResult

	// Enrichment metadata (from AlertEnricher)
	Metadata map[string]interface{}

	// Timestamps
	ReceivedAt  int64 // When alert was received
	ProcessedAt int64 // When alert was processed
	EnrichedAt  int64 // When alert was enriched
}

// ================================================================================
// LLM Classifier Interface (Optional - BYOK)
// ================================================================================
// For users who want to use LLM-based classification.
// This is OPTIONAL - OSS works perfectly without it.

// LLMClient defines LLM API client interface
type LLMClient interface {
	// Name returns LLM provider name (e.g., "openai", "anthropic", "custom")
	Name() string

	// ClassifyAlert uses LLM to classify alert
	ClassifyAlert(ctx context.Context, alert Alert, context map[string]interface{}) (*ClassificationResult, error)

	// GenerateRecommendations uses LLM to suggest actions
	GenerateRecommendations(ctx context.Context, alert Alert, classification *ClassificationResult) ([]string, error)

	// Health checks LLM API connectivity
	Health(ctx context.Context) error
}

// LLMConfig holds LLM configuration (BYOK - Bring Your Own Key)
type LLMConfig struct {
	Provider string // "openai", "anthropic", "azure-openai", "custom"

	// API credentials (user-provided)
	APIKey  string
	APIURL  string // custom endpoint (optional)

	// Model configuration
	Model       string  // "gpt-4", "claude-3", etc.
	Temperature float64 // 0.0-1.0
	MaxTokens   int

	// Caching (to reduce costs)
	CacheEnabled bool
	CacheTTL     int // seconds

	// Fallback behavior
	FallbackToRules bool // if LLM fails, use rule-based
}

// ================================================================================
// Classifier Registry (for multiple classifiers)
// ================================================================================

// ClassifierRegistry manages multiple classifiers
type ClassifierRegistry interface {
	// Register adds a classifier
	Register(name string, classifier AlertClassifier) error

	// Get retrieves a classifier by name
	Get(name string) (AlertClassifier, bool)

	// List returns all registered classifiers
	List() []string

	// Default returns the default classifier
	Default() AlertClassifier

	// SetDefault sets the default classifier
	SetDefault(name string) error
}
