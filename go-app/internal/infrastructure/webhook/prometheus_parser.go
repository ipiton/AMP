package webhook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// prometheusParser implements WebhookParser for Prometheus alerts.
//
// This parser handles both Prometheus v1 and v2 formats:
//   - v1: Direct array of alerts (from /api/v1/alerts)
//   - v2: Grouped alerts with shared labels (from /api/v2/alerts)
//
// The parser normalizes both formats to core.Alert domain model using
// an adapter pattern: Prometheus → AlertmanagerWebhook → core.Alert.
// This allows reusing existing validation and downstream processing logic.
//
// Example usage:
//
//	parser := NewPrometheusParser()
//	webhook, err := parser.Parse(jsonPayload)
//	alerts, err := parser.ConvertToDomain(webhook)
//
// Performance:
//   - Parse single alert: < 10µs (target)
//   - Parse 100 alerts: < 1ms (target)
//   - Zero allocations in hot path
type prometheusParser struct {
	validator      WebhookValidator
	formatDetector PrometheusFormatDetector
}

// NewPrometheusParser creates a new Prometheus alert parser.
//
// The parser includes:
//   - Format detector for distinguishing v1 vs v2
//   - Validator for comprehensive validation
//   - Converter for domain model transformation
//
// Returns:
//   - WebhookParser: Initialized parser ready for use
func NewPrometheusParser() WebhookParser {
	return &prometheusParser{
		validator:      NewWebhookValidator(),
		formatDetector: NewPrometheusFormatDetector(),
	}
}

// Parse parses raw JSON bytes into AlertmanagerWebhook structure.
//
// This method handles both Prometheus v1 and v2 formats automatically.
// The format is detected based on payload structure:
//   - Array → Prometheus v1
//   - Object with "groups" → Prometheus v2
//
// The parsed Prometheus webhook is converted to AlertmanagerWebhook format
// for interface compatibility. This allows reusing existing validation and
// processing logic without breaking existing code.
//
// Parameters:
//   - data: Raw JSON bytes from webhook request body
//
// Returns:
//   - *AlertmanagerWebhook: Parsed webhook (converted for interface compatibility)
//   - error: JSON parsing error, validation error, or conversion error
//
// Example:
//
//	parser := NewPrometheusParser()
//	webhook, err := parser.Parse([]byte(`[{"labels":{"alertname":"Test"},...}]`))
//	if err != nil {
//	    log.Fatal(err)
//	}
func (p *prometheusParser) Parse(data []byte) (*AlertmanagerWebhook, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("prometheus webhook payload is empty")
	}

	// Detect Prometheus format (v1 or v2)
	format, err := p.formatDetector.DetectPrometheusFormat(data)
	if err != nil {
		return nil, fmt.Errorf("failed to detect Prometheus format: %w", err)
	}

	// Parse Prometheus JSON based on detected format
	var webhook PrometheusWebhook
	if format == PrometheusFormatV1 {
		// v1 format: array of alerts
		var alerts []PrometheusAlert
		if err := json.Unmarshal(data, &alerts); err != nil {
			return nil, fmt.Errorf("failed to parse Prometheus v1 webhook JSON: %w", err)
		}
		webhook.Alerts = alerts
	} else if format == PrometheusFormatV2 {
		// v2 format: object with groups
		if err := json.Unmarshal(data, &webhook); err != nil {
			return nil, fmt.Errorf("failed to parse Prometheus v2 webhook JSON: %w", err)
		}
	} else {
		return nil, fmt.Errorf("unsupported Prometheus format: %s", format)
	}

	// Validate structure
	if webhook.AlertCount() == 0 {
		return nil, fmt.Errorf("prometheus webhook contains no alerts")
	}

	// Convert to AlertmanagerWebhook for interface compatibility
	// This allows reusing existing validation and downstream processing
	amWebhook := p.convertToAlertmanagerFormat(&webhook, format)

	return amWebhook, nil
}

// Validate validates the parsed webhook using WebhookValidator.
//
// This method delegates to the existing AlertmanagerWebhook validator
// since we converted Prometheus format to Alertmanager format in Parse().
//
// Parameters:
//   - webhook: Parsed AlertmanagerWebhook to validate
//
// Returns:
//   - *ValidationResult: Validation result with errors (if any)
func (p *prometheusParser) Validate(webhook *AlertmanagerWebhook) *ValidationResult {
	// Delegate to existing validator (TN-43)
	// The webhook is already in Alertmanager format, so validation works as-is
	return p.validator.ValidateAlertmanager(webhook)
}

// ConvertToDomain converts AlertmanagerWebhook alerts to core.Alert domain models.
//
// This method performs:
//  1. Status mapping (state → Status)
//  2. Timestamp conversion (activeAt → StartsAt)
//  3. Fingerprint generation (if missing)
//  4. Field validation
//
// The conversion is lossless - all Prometheus-specific fields (like "value")
// are preserved in alert annotations.
//
// Parameters:
//   - webhook: Parsed AlertmanagerWebhook (converted from Prometheus format)
//
// Returns:
//   - []*core.Alert: Converted domain models ready for processing
//   - error: Conversion error (missing required fields, invalid data)
//
// Example:
//
//	alerts, err := parser.ConvertToDomain(webhook)
//	for _, alert := range alerts {
//	    fmt.Printf("Alert: %s, Status: %s\n", alert.AlertName, alert.Status)
//	}
func (p *prometheusParser) ConvertToDomain(webhook *AlertmanagerWebhook) ([]*core.Alert, error) {
	if webhook == nil {
		return nil, fmt.Errorf("webhook is nil")
	}

	alerts := make([]*core.Alert, 0, len(webhook.Alerts))

	for i, amAlert := range webhook.Alerts {
		alert, err := p.convertSingleAlert(&amAlert, i)
		if err != nil {
			return nil, fmt.Errorf("failed to convert alert[%d]: %w", i, err)
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// convertToAlertmanagerFormat converts PrometheusWebhook → AlertmanagerWebhook.
//
// This conversion allows reusing existing validation and processing logic.
// The conversion is lossless - all fields are preserved, with Prometheus-specific
// fields (like "value") stored in alert annotations.
//
// Conversion mapping:
//   - PrometheusAlert.State → AlertmanagerAlert.Status
//   - PrometheusAlert.ActiveAt → AlertmanagerAlert.StartsAt
//   - PrometheusAlert.Value → AlertmanagerAlert.Annotations["__prometheus_value__"]
//   - PrometheusAlert.Labels → AlertmanagerAlert.Labels (merged with group labels for v2)
//
// Parameters:
//   - prometheus: Parsed PrometheusWebhook
//   - format: Detected format (PrometheusFormatV1 or PrometheusFormatV2)
//
// Returns:
//   - *AlertmanagerWebhook: Converted webhook in Alertmanager format
func (p *prometheusParser) convertToAlertmanagerFormat(
	prometheus *PrometheusWebhook,
	format string,
) *AlertmanagerWebhook {
	// Flatten alerts (handles both v1 and v2)
	// For v1: returns Alerts as-is
	// For v2: merges group labels into each alert
	flatAlerts := prometheus.FlattenAlerts()

	// Convert each Prometheus alert → Alertmanager alert
	amAlerts := make([]AlertmanagerAlert, 0, len(flatAlerts))
	for _, promAlert := range flatAlerts {
		amAlert := AlertmanagerAlert{
			Status:       mapPrometheusState(promAlert.State),
			Labels:       promAlert.Labels,
			Annotations:  promAlert.Annotations,
			StartsAt:     promAlert.ActiveAt, // activeAt → startsAt
			EndsAt:       time.Time{},        // Not provided in Prometheus
			GeneratorURL: promAlert.GeneratorURL,
			Fingerprint:  promAlert.Fingerprint,
		}

		// Store original Prometheus value in annotations (lossless conversion)
		if promAlert.Value != "" {
			if amAlert.Annotations == nil {
				amAlert.Annotations = make(map[string]string)
			}
			amAlert.Annotations["__prometheus_value__"] = promAlert.Value
		}

		amAlerts = append(amAlerts, amAlert)
	}

	// Create Alertmanager-compatible webhook
	// Use "prom_v1" or "prom_v2" as version to track source format
	return &AlertmanagerWebhook{
		Version:  "prom_" + format, // e.g. "prom_v1", "prom_v2"
		GroupKey: "prometheus",     // Fake group key
		Receiver: "prometheus",     // Fake receiver
		Status:   "firing",         // Assume firing for Prometheus
		Alerts:   amAlerts,
		GroupLabels:       make(map[string]string),
		CommonLabels:      make(map[string]string),
		CommonAnnotations: make(map[string]string),
		ExternalURL:       "",
	}
}

// convertSingleAlert converts a single AlertmanagerAlert to core.Alert.
//
// This method performs:
//   - Extract alertname from labels (required)
//   - Map status to core.AlertStatus
//   - Generate fingerprint if missing
//   - Validate timestamps
//   - Convert pointers
//
// Parameters:
//   - amAlert: AlertmanagerAlert to convert
//   - index: Alert index in array (for error messages)
//
// Returns:
//   - *core.Alert: Converted domain model
//   - error: Conversion error with detailed message
func (p *prometheusParser) convertSingleAlert(amAlert *AlertmanagerAlert, index int) (*core.Alert, error) {
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

// mapPrometheusState maps Prometheus state to Alertmanager status.
//
// Prometheus states:
//   - "firing": Alert condition is true and alert is active
//   - "pending": Alert condition is true but waiting for "for" duration
//   - "inactive": Alert condition is false (equivalent to "resolved")
//
// Mapping:
//   - "firing" → "firing"
//   - "pending" → "firing" (treat pending as firing - conservative approach)
//   - "inactive" → "resolved"
//
// Parameters:
//   - state: Prometheus state string
//
// Returns:
//   - string: Alertmanager status ("firing" or "resolved")
func mapPrometheusState(state string) string {
	switch state {
	case "firing", "pending":
		// Map both firing and pending to "firing"
		// This is conservative - we'd rather alert than miss an alert
		return "firing"
	case "inactive":
		return "resolved"
	default:
		// Unknown state - default to firing (conservative)
		return "firing"
	}
}

// Note: mapAlertStatus and generateFingerprint are defined in parser.go (TN-41)
// We reuse those implementations for consistency and DRY principle.
