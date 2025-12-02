package domain

import (
	"fmt"
	"time"
)

// ================================================================================
// Classification Domain Model - OSS Core
// ================================================================================
// Alert classification results (rule-based OSS + optional LLM).
// Zero dependencies (stdlib only).

// ClassificationResult represents the result of alert classification.
//
// Classification provides:
//   - Severity assessment (critical, warning, info, noise)
//   - Confidence score (0.0-1.0)
//   - Reasoning explanation
//   - Recommended actions
//
// Classification can be done by:
//   - Rule-based classifier (OSS, free, always available)
//   - LLM classifier (optional, BYOK - Bring Your Own Key)
//   - Custom classifier (user-provided implementation)
//
// Example:
//
//	result := &ClassificationResult{
//	    Severity:   SeverityCritical,
//	    Confidence: 0.95,
//	    Reasoning:  "High CPU usage in production instance",
//	    Recommendations: []string{
//	        "Check for memory leaks",
//	        "Review recent deployments",
//	        "Scale horizontally if needed",
//	    },
//	    ClassifierName: "rule-based",
//	    ClassifiedAt:   time.Now(),
//	}
type ClassificationResult struct {
	// Severity is the classified severity level.
	// Must be one of: critical, warning, info, noise
	Severity AlertSeverity `json:"severity"`

	// Confidence is the confidence score for this classification (0.0-1.0).
	// Higher values indicate higher confidence.
	// Examples:
	//   - 1.0: Exact rule match
	//   - 0.95: LLM high confidence
	//   - 0.5: Uncertain classification
	Confidence float64 `json:"confidence"`

	// Reasoning explains why this classification was chosen.
	// For rule-based: which rule matched
	// For LLM: reasoning from model
	// Example: "Matched rule: HighCPU + Production => Critical"
	Reasoning string `json:"reasoning"`

	// Recommendations suggests actions to take.
	// Optional, can be nil/empty.
	// Example: ["Check logs", "Review metrics", "Contact on-call"]
	Recommendations []string `json:"recommendations,omitempty"`

	// Category groups alerts by type (optional).
	// Examples: "infrastructure", "application", "network", "database"
	Category string `json:"category,omitempty"`

	// Priority assigns priority level (optional).
	// Examples: "p0", "p1", "p2", "p3", "p4"
	Priority string `json:"priority,omitempty"`

	// Tags are additional classification tags (optional).
	// Examples: ["requires-escalation", "auto-resolvable", "known-issue"]
	Tags []string `json:"tags,omitempty"`

	// ClassifierName identifies which classifier produced this result.
	// Examples: "rule-based", "openai-gpt4", "anthropic-claude", "custom"
	ClassifierName string `json:"classifier_name"`

	// ClassifiedAt is when this classification was performed.
	ClassifiedAt time.Time `json:"classified_at"`

	// ModelVersion is the version of the classifier/model used.
	// For rule-based: rule set version
	// For LLM: model name/version
	// Example: "rules-v1.2", "gpt-4-turbo-2024-04-09"
	ModelVersion string `json:"model_version,omitempty"`

	// ProcessingTime is how long classification took (seconds).
	ProcessingTime float64 `json:"processing_time"`

	// TokensUsed tracks API token usage (for API-based classifiers like LLM).
	// Useful for monitoring API usage.
	TokensUsed int `json:"tokens_used,omitempty"`

	// CostUSD is the estimated API cost of this classification.
	// Useful for budget tracking when using external APIs.
	CostUSD float64 `json:"cost_usd,omitempty"`

	// Metadata contains additional classifier-specific data.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ================================================================================
// ClassificationResult Methods
// ================================================================================

// Validate checks if the classification result is valid.
//
// Validation rules:
//   - Severity must be one of: critical, warning, info, noise
//   - Confidence must be 0.0-1.0
//   - Reasoning must not be empty
//   - ClassifierName must not be empty
//   - ClassifiedAt must not be zero
//   - ProcessingTime must be >= 0
//
// Returns:
//   - nil if valid
//   - error with validation message if invalid
func (c *ClassificationResult) Validate() error {
	// Severity validation
	switch c.Severity {
	case SeverityCritical, SeverityWarning, SeverityInfo, SeverityNoise:
		// Valid
	default:
		return fmt.Errorf("invalid severity: %s (must be critical, warning, info, or noise)", c.Severity)
	}

	// Confidence validation
	if c.Confidence < 0.0 || c.Confidence > 1.0 {
		return fmt.Errorf("confidence must be 0.0-1.0, got: %f", c.Confidence)
	}

	// Reasoning validation
	if c.Reasoning == "" {
		return fmt.Errorf("reasoning is required")
	}

	// ClassifierName validation
	if c.ClassifierName == "" {
		return fmt.Errorf("classifier_name is required")
	}

	// ClassifiedAt validation
	if c.ClassifiedAt.IsZero() {
		return fmt.Errorf("classified_at is required")
	}

	// ProcessingTime validation
	if c.ProcessingTime < 0 {
		return fmt.Errorf("processing_time must be >= 0, got: %f", c.ProcessingTime)
	}

	return nil
}

// IsCritical returns true if severity is critical.
func (c *ClassificationResult) IsCritical() bool {
	return c.Severity == SeverityCritical
}

// IsWarning returns true if severity is warning.
func (c *ClassificationResult) IsWarning() bool {
	return c.Severity == SeverityWarning
}

// IsInfo returns true if severity is info.
func (c *ClassificationResult) IsInfo() bool {
	return c.Severity == SeverityInfo
}

// IsNoise returns true if severity is noise.
func (c *ClassificationResult) IsNoise() bool {
	return c.Severity == SeverityNoise
}

// IsHighConfidence returns true if confidence >= 0.8.
func (c *ClassificationResult) IsHighConfidence() bool {
	return c.Confidence >= 0.8
}

// HasRecommendations returns true if recommendations are present.
func (c *ClassificationResult) HasRecommendations() bool {
	return len(c.Recommendations) > 0
}

// ================================================================================
// Enriched Alert (Alert + Classification)
// ================================================================================

// EnrichedAlert represents an alert with classification and metadata.
//
// EnrichedAlert combines:
//   - Original alert (labels, annotations, etc.)
//   - Classification result (severity, recommendations)
//   - Enrichment metadata (processing info)
//
// This is the format used for:
//   - Publishing to external systems
//   - Storage in database
//   - API responses
//
// Example:
//
//	enriched := &EnrichedAlert{
//	    Alert: originalAlert,
//	    Classification: classificationResult,
//	    EnrichmentMetadata: map[string]interface{}{
//	        "enriched_by": "classification-service",
//	        "version": "v1.0",
//	    },
//	    ProcessingTimestamp: time.Now(),
//	}
type EnrichedAlert struct {
	// Alert is the original alert.
	Alert *Alert `json:"alert"`

	// Classification is the classification result (optional).
	// Nil if classification not performed/failed.
	Classification *ClassificationResult `json:"classification,omitempty"`

	// EnrichmentMetadata contains additional metadata added during enrichment.
	// Examples:
	//   - "enriched_by": service name
	//   - "runbook_url": dynamically added runbook
	//   - "dashboard_url": dynamically added dashboard
	EnrichmentMetadata map[string]interface{} `json:"enrichment_metadata,omitempty"`

	// ProcessingTimestamp is when this alert was processed/enriched.
	ProcessingTimestamp time.Time `json:"processing_timestamp"`
}

// Validate checks if the enriched alert is valid.
func (e *EnrichedAlert) Validate() error {
	if e.Alert == nil {
		return fmt.Errorf("alert is required")
	}
	if err := e.Alert.Validate(); err != nil {
		return fmt.Errorf("alert validation failed: %w", err)
	}
	if e.Classification != nil {
		if err := e.Classification.Validate(); err != nil {
			return fmt.Errorf("classification validation failed: %w", err)
		}
	}
	if e.ProcessingTimestamp.IsZero() {
		return fmt.Errorf("processing_timestamp is required")
	}
	return nil
}

// HasClassification returns true if classification is present.
func (e *EnrichedAlert) HasClassification() bool {
	return e.Classification != nil
}

// EffectiveSeverity returns the severity to use for this alert.
// Uses classification severity if available, otherwise falls back to label.
func (e *EnrichedAlert) EffectiveSeverity() AlertSeverity {
	if e.Classification != nil {
		return e.Classification.Severity
	}
	// Fallback to severity label
	if e.Alert != nil {
		if sev := e.Alert.Severity(); sev != nil {
			return AlertSeverity(*sev)
		}
	}
	// Default
	return SeverityInfo
}
