package webhook

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// WebhookParser defines the interface for parsing webhook payloads.
type WebhookParser interface {
	// Parse parses raw webhook payload bytes into AlertmanagerWebhook structure.
	Parse(data []byte) (*AlertmanagerWebhook, error)

	// Validate validates the parsed webhook using WebhookValidator.
	Validate(webhook *AlertmanagerWebhook) *ValidationResult

	// ConvertToDomain converts AlertmanagerWebhook alerts to core.Alert domain models.
	ConvertToDomain(webhook *AlertmanagerWebhook) ([]*core.Alert, error)
}

// alertmanagerParser implements WebhookParser for Alertmanager webhooks.
type alertmanagerParser struct {
	validator WebhookValidator
}

// NewAlertmanagerParser creates a new Alertmanager webhook parser.
//
// Returns:
//   - WebhookParser: Initialized parser with validator
func NewAlertmanagerParser() WebhookParser {
	return &alertmanagerParser{
		validator: NewWebhookValidator(),
	}
}

// Parse parses raw JSON bytes into AlertmanagerWebhook structure.
//
// Parameters:
//   - data: Raw JSON bytes from webhook request body
//
// Returns:
//   - *AlertmanagerWebhook: Parsed webhook structure
//   - error: JSON parsing error or validation error
func (p *alertmanagerParser) Parse(data []byte) (*AlertmanagerWebhook, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("webhook payload is empty")
	}

	var webhook AlertmanagerWebhook
	if err := json.Unmarshal(data, &webhook); err != nil {
		return nil, fmt.Errorf("failed to parse webhook JSON: %w", err)
	}

	return &webhook, nil
}

// Validate validates the parsed webhook structure.
//
// Parameters:
//   - webhook: Parsed AlertmanagerWebhook to validate
//
// Returns:
//   - *ValidationResult: Validation result with errors (if any)
func (p *alertmanagerParser) Validate(webhook *AlertmanagerWebhook) *ValidationResult {
	return p.validator.ValidateAlertmanager(webhook)
}

// ConvertToDomain converts AlertmanagerWebhook alerts to core.Alert domain models.
//
// This method maps Alertmanager-specific fields to the core domain model:
//   - Extracts "alertname" from labels → AlertName field
//   - Maps "status" string → core.AlertStatus type
//   - Generates fingerprint if missing (based on alertname + labels)
//   - Converts timestamps (StartsAt required, EndsAt optional)
//   - Maps GeneratorURL as pointer
//
// Parameters:
//   - webhook: Validated AlertmanagerWebhook
//
// Returns:
//   - []*core.Alert: Array of domain alert models
//   - error: Conversion error if alert structure is invalid
func (p *alertmanagerParser) ConvertToDomain(webhook *AlertmanagerWebhook) ([]*core.Alert, error) {
	if webhook == nil {
		return nil, fmt.Errorf("webhook is nil")
	}

	if len(webhook.Alerts) == 0 {
		return []*core.Alert{}, nil
	}

	alerts := make([]*core.Alert, 0, len(webhook.Alerts))

	for i, amAlert := range webhook.Alerts {
		domainAlert, err := p.convertSingleAlert(&amAlert, i)
		if err != nil {
			return nil, fmt.Errorf("failed to convert alert[%d]: %w", i, err)
		}
		alerts = append(alerts, domainAlert)
	}

	return alerts, nil
}

// convertSingleAlert converts a single AlertmanagerAlert to core.Alert.
func (p *alertmanagerParser) convertSingleAlert(amAlert *AlertmanagerAlert, index int) (*core.Alert, error) {
	// Extract alertname from labels (required)
	alertName, ok := amAlert.Labels["alertname"]
	if !ok || alertName == "" {
		return nil, fmt.Errorf("alert[%d]: missing required label 'alertname'", index)
	}

	// Map status to core.AlertStatus
	status, err := mapAlertStatus(amAlert.Status)
	if err != nil {
		return nil, fmt.Errorf("alert[%d]: %w", index, err)
	}

	// Generate or use existing fingerprint
	fingerprint := amAlert.Fingerprint
	if fingerprint == "" {
		fingerprint = generateFingerprint(alertName, amAlert.Labels)
	}

	// Validate timestamps
	if amAlert.StartsAt.IsZero() {
		return nil, fmt.Errorf("alert[%d]: startsAt is required", index)
	}

	// Convert EndsAt to pointer (only if not zero)
	var endsAt *time.Time
	if !amAlert.EndsAt.IsZero() {
		endsAt = &amAlert.EndsAt
	}

	// Convert GeneratorURL to pointer
	var generatorURL *string
	if amAlert.GeneratorURL != "" {
		generatorURL = &amAlert.GeneratorURL
	}

	// Set timestamp to current time
	now := time.Now()

	return &core.Alert{
		Fingerprint:  fingerprint,
		AlertName:    alertName,
		Status:       status,
		Labels:       amAlert.Labels,
		Annotations:  amAlert.Annotations,
		StartsAt:     amAlert.StartsAt,
		EndsAt:       endsAt,
		GeneratorURL: generatorURL,
		Timestamp:    &now,
	}, nil
}

// mapAlertStatus maps Alertmanager status string to core.AlertStatus.
func mapAlertStatus(status string) (core.AlertStatus, error) {
	switch status {
	case "firing":
		return core.StatusFiring, nil
	case "resolved":
		return core.StatusResolved, nil
	default:
		return "", fmt.Errorf("invalid alert status '%s', must be 'firing' or 'resolved'", status)
	}
}

// generateFingerprint generates a deterministic fingerprint for an alert.
//
// The fingerprint is generated from:
//   - alertname
//   - sorted labels (key=value pairs)
//
// This ensures the same alert generates the same fingerprint consistently.
func generateFingerprint(alertName string, labels map[string]string) string {
	// Sort labels by key for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build fingerprint string
	var builder strings.Builder
	builder.WriteString(alertName)
	builder.WriteString("|")

	for _, k := range keys {
		builder.WriteString(k)
		builder.WriteString("=")
		builder.WriteString(labels[k])
		builder.WriteString("|")
	}

	// Hash the fingerprint string
	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

// ParseAndValidate is a convenience method that parses and validates in one call.
//
// Parameters:
//   - data: Raw webhook payload bytes
//
// Returns:
//   - *AlertmanagerWebhook: Parsed webhook (nil if validation fails)
//   - *ValidationResult: Validation result
//   - error: Parse error (validation errors are in ValidationResult)
func (p *alertmanagerParser) ParseAndValidate(data []byte) (*AlertmanagerWebhook, *ValidationResult, error) {
	webhook, err := p.Parse(data)
	if err != nil {
		return nil, nil, err
	}

	result := p.Validate(webhook)
	if !result.Valid {
		return webhook, result, nil // Return webhook + validation errors, no parse error
	}

	return webhook, result, nil
}
