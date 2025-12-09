// Package aiclassifier provides AI-powered alert classification.
package aiclassifier

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// ClassificationResult represents the AI classification output.
type ClassificationResult struct {
	// Primary classification
	IncidentType string  `json:"incident_type"` // database, network, application, security, etc.
	Severity     string  `json:"severity"`      // critical, high, medium, low
	Urgency      string  `json:"urgency"`       // immediate, high, normal, low
	Category     string  `json:"category"`      // performance, availability, security, compliance

	// Confidence scores (0.0 to 1.0)
	TypeConfidence     float64 `json:"type_confidence"`
	SeverityConfidence float64 `json:"severity_confidence"`
	UrgencyConfidence  float64 `json:"urgency_confidence"`

	// Routing suggestions
	SuggestedTeam     string   `json:"suggested_team"`      // dba, network, security, ops
	SuggestedPriority string   `json:"suggested_priority"`  // high, medium, low
	SuggestedTargets  []string `json:"suggested_targets"`   // slack channels, pagerduty, etc.

	// Analysis
	IsAnomaly         bool    `json:"is_anomaly"`          // Unusual pattern detected
	IsFalsePositive   bool    `json:"is_false_positive"`   // Likely false positive
	RecommendedAction string  `json:"recommended_action"`  // Human-readable action
	Reasoning         string  `json:"reasoning"`           // Why this classification

	// Metadata
	ModelVersion string    `json:"model_version"`
	ProcessedAt  time.Time `json:"processed_at"`
}

// LLMProvider defines the interface for LLM providers.
type LLMProvider interface {
	// Classify sends alert to LLM for classification
	Classify(ctx context.Context, alert *core.Alert) (*ClassificationResult, error)

	// Health checks if the LLM provider is available
	Health(ctx context.Context) error
}

// ClassifierConfig holds configuration for AI classifier.
type ClassifierConfig struct {
	// Enabled controls whether AI classification is enabled
	Enabled bool

	// Provider is the LLM provider (openai, anthropic, ollama)
	Provider string

	// Model is the model name (gpt-4, claude-3, etc.)
	Model string

	// APIKey for the LLM provider
	APIKey string

	// BaseURL for the LLM API
	BaseURL string

	// Timeout for classification requests
	Timeout time.Duration

	// MaxRetries for failed requests
	MaxRetries int

	// CacheTTL for caching classifications
	CacheTTL time.Duration

	// FallbackEnabled enables rule-based fallback
	FallbackEnabled bool

	// Logger for error reporting
	Logger *slog.Logger
}

// Classifier provides AI-powered alert classification.
type Classifier struct {
	config   *ClassifierConfig
	provider LLMProvider
	cache    ClassificationCache
	fallback *RuleBasedClassifier
	logger   *slog.Logger
}

// NewClassifier creates a new AI classifier.
func NewClassifier(config *ClassifierConfig, provider LLMProvider, cache ClassificationCache) (*Classifier, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if !config.Enabled {
		return &Classifier{
			config: config,
			logger: config.Logger,
		}, nil
	}

	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Create fallback classifier if enabled
	var fallback *RuleBasedClassifier
	if config.FallbackEnabled {
		fallback = NewRuleBasedClassifier()
	}

	return &Classifier{
		config:   config,
		provider: provider,
		cache:    cache,
		fallback: fallback,
		logger:   config.Logger,
	}, nil
}

// Classify classifies an alert using AI or fallback.
func (c *Classifier) Classify(ctx context.Context, alert *core.Alert) (*ClassificationResult, error) {
	if !c.config.Enabled {
		// Use fallback if available
		if c.fallback != nil {
			return c.fallback.Classify(alert)
		}
		return nil, fmt.Errorf("classifier is disabled")
	}

	// Check cache first
	if c.cache != nil {
		if cached, ok := c.cache.Get(alert.Fingerprint); ok {
			c.logger.Debug("Classification cache hit", "fingerprint", alert.Fingerprint)
			return cached, nil
		}
	}

	// Classify using AI
	result, err := c.provider.Classify(ctx, alert)
	if err != nil {
		c.logger.Error("AI classification failed", "error", err, "fingerprint", alert.Fingerprint)

		// Try fallback
		if c.fallback != nil {
			c.logger.Info("Using fallback classification", "fingerprint", alert.Fingerprint)
			return c.fallback.Classify(alert)
		}

		return nil, fmt.Errorf("classification failed: %w", err)
	}

	// Cache result
	if c.cache != nil {
		c.cache.Set(alert.Fingerprint, result, c.config.CacheTTL)
	}

	c.logger.Info("Alert classified",
		"fingerprint", alert.Fingerprint,
		"type", result.IncidentType,
		"severity", result.Severity,
		"confidence", result.TypeConfidence,
	)

	return result, nil
}

// ClassificationCache defines the interface for caching classifications.
type ClassificationCache interface {
	Get(fingerprint string) (*ClassificationResult, bool)
	Set(fingerprint string, result *ClassificationResult, ttl time.Duration)
	Delete(fingerprint string)
	Clear()
}

// MemoryCache implements in-memory classification cache.
type MemoryCache struct {
	cache map[string]*cacheEntry
	mu    sync.RWMutex
}

type cacheEntry struct {
	result    *ClassificationResult
	expiresAt time.Time
}

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache() *MemoryCache {
	cache := &MemoryCache{
		cache: make(map[string]*cacheEntry),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// Get retrieves a classification from cache.
func (m *MemoryCache) Get(fingerprint string) (*ClassificationResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.cache[fingerprint]
	if !ok {
		return nil, false
	}

	// Check expiration
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.result, true
}

// Set stores a classification in cache.
func (m *MemoryCache) Set(fingerprint string, result *ClassificationResult, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache[fingerprint] = &cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(ttl),
	}
}

// Delete removes a classification from cache.
func (m *MemoryCache) Delete(fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.cache, fingerprint)
}

// Clear removes all classifications from cache.
func (m *MemoryCache) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache = make(map[string]*cacheEntry)
}

// cleanup removes expired entries periodically.
func (m *MemoryCache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for key, entry := range m.cache {
			if now.After(entry.expiresAt) {
				delete(m.cache, key)
			}
		}
		m.mu.Unlock()
	}
}

// RuleBasedClassifier provides simple rule-based classification as fallback.
type RuleBasedClassifier struct{}

// NewRuleBasedClassifier creates a new rule-based classifier.
func NewRuleBasedClassifier() *RuleBasedClassifier {
	return &RuleBasedClassifier{}
}

// Classify uses simple rules to classify alerts.
func (r *RuleBasedClassifier) Classify(alert *core.Alert) (*ClassificationResult, error) {
	result := &ClassificationResult{
		ModelVersion: "rule-based-v1",
		ProcessedAt:  time.Now(),
	}

	// Classify by alert name patterns
	alertName := alert.AlertName

	// Database alerts
	if contains(alertName, []string{"database", "db", "postgres", "mysql", "sql", "query"}) {
		result.IncidentType = "database"
		result.SuggestedTeam = "dba"
	}

	// Network alerts
	if contains(alertName, []string{"network", "connection", "latency", "timeout", "dns"}) {
		result.IncidentType = "network"
		result.SuggestedTeam = "network"
	}

	// Security alerts
	if contains(alertName, []string{"security", "auth", "unauthorized", "breach", "attack"}) {
		result.IncidentType = "security"
		result.SuggestedTeam = "security"
		result.Urgency = "immediate"
	}

	// Application alerts
	if contains(alertName, []string{"app", "service", "api", "http", "error"}) {
		result.IncidentType = "application"
		result.SuggestedTeam = "ops"
	}

	// Default
	if result.IncidentType == "" {
		result.IncidentType = "general"
		result.SuggestedTeam = "ops"
	}

	// Classify severity by status
	switch alert.Status {
	case "firing":
		result.Severity = "high"
		result.Urgency = "high"
	case "resolved":
		result.Severity = "low"
		result.Urgency = "low"
	default:
		result.Severity = "medium"
		result.Urgency = "normal"
	}

	// Set confidence (lower for rule-based)
	result.TypeConfidence = 0.6
	result.SeverityConfidence = 0.7
	result.UrgencyConfidence = 0.7

	// Set priority
	if result.Urgency == "immediate" || result.Severity == "critical" {
		result.SuggestedPriority = "high"
	} else if result.Severity == "high" {
		result.SuggestedPriority = "medium"
	} else {
		result.SuggestedPriority = "low"
	}

	result.Reasoning = "Classified using rule-based heuristics"

	return result, nil
}

// contains checks if any of the keywords are in the text (case-insensitive).
func contains(text string, keywords []string) bool {
	lowerText := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lowerText, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
