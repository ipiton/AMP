package webhook

import (
	"encoding/json"
	"fmt"
)

// WebhookType represents the detected webhook format type.
type WebhookType string

const (
	// WebhookTypeAlertmanager represents Prometheus Alertmanager webhook format
	WebhookTypeAlertmanager WebhookType = "alertmanager"

	// WebhookTypeGeneric represents a generic webhook format
	WebhookTypeGeneric WebhookType = "generic"

	// WebhookTypePrometheus represents Prometheus direct alert format
	// This includes both v1 (array) and v2 (grouped) formats
	WebhookTypePrometheus WebhookType = "prometheus"
)

// Prometheus-specific format subtypes for fine-grained detection
const (
	// PrometheusFormatV1 represents Prometheus v1 API format (flat array of alerts)
	// Used by: /api/v1/alerts endpoint
	// Structure: [{"labels": {...}, "state": "firing", ...}]
	PrometheusFormatV1 = "prometheus_v1"

	// PrometheusFormatV2 represents Prometheus v2 API format (grouped alerts)
	// Used by: /api/v2/alerts endpoint
	// Structure: {"groups": [{"labels": {...}, "alerts": [...]}]}
	PrometheusFormatV2 = "prometheus_v2"
)

// WebhookDetector defines the interface for detecting webhook types.
type WebhookDetector interface {
	// Detect analyzes the payload and determines the webhook type.
	Detect(payload []byte) (WebhookType, error)
}

// PrometheusFormatDetector provides fine-grained detection for Prometheus alert formats.
//
// This interface is used to distinguish between Prometheus v1 (flat array)
// and v2 (grouped) formats after detecting WebhookTypePrometheus.
type PrometheusFormatDetector interface {
	// DetectPrometheusFormat determines the specific Prometheus format (v1 or v2).
	//
	// Parameters:
	//   - payload: Raw JSON payload bytes
	//
	// Returns:
	//   - string: Format identifier (PrometheusFormatV1 or PrometheusFormatV2)
	//   - error: Detection error if format cannot be determined
	DetectPrometheusFormat(payload []byte) (string, error)
}

// webhookDetector implements WebhookDetector.
type webhookDetector struct{}

// NewWebhookDetector creates a new webhook type detector.
func NewWebhookDetector() WebhookDetector {
	return &webhookDetector{}
}

// Detect analyzes the webhook payload and determines its type.
//
// Detection logic:
//  1. Try to parse as JSON
//  2. Check for Alertmanager-specific fields (version, groupKey, receiver)
//  3. Check for Prometheus-specific fields (state, activeAt, generatorURL)
//  4. Otherwise → WebhookTypeGeneric
//
// Parameters:
//   - payload: Raw webhook payload bytes
//
// Returns:
//   - WebhookType: Detected webhook type
//   - error: Detection error (invalid JSON, empty payload)
func (d *webhookDetector) Detect(payload []byte) (WebhookType, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("webhook payload is empty")
	}

	// Parse as generic JSON to inspect structure
	var data interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", fmt.Errorf("invalid JSON payload: %w", err)
	}

	// Try as map[string]interface{} first (most common)
	if dataMap, ok := data.(map[string]interface{}); ok {
		// Check for Alertmanager-specific fields (highest priority)
		if d.isAlertmanagerWebhook(dataMap) {
			return WebhookTypeAlertmanager, nil
		}

		// Check for Prometheus v2 format (grouped alerts)
		if d.isPrometheusV2Webhook(dataMap) {
			return WebhookTypePrometheus, nil
		}
	}

	// Try as []interface{} (Prometheus v1 array format)
	if dataArray, ok := data.([]interface{}); ok {
		if d.isPrometheusV1Webhook(dataArray) {
			return WebhookTypePrometheus, nil
		}
	}

	// Default to generic format
	return WebhookTypeGeneric, nil
}

// isAlertmanagerWebhook checks if the payload has Alertmanager-specific fields.
//
// Alertmanager webhooks have these characteristic fields:
//   - "version" (string) - Alertmanager API version (typically "4")
//   - "groupKey" (string) - Alert grouping key
//   - "receiver" (string) - Receiver name
//   - "alerts" (array) - Array of alerts
//
// We require at least 2 of these fields to confidently detect Alertmanager format.
func (d *webhookDetector) isAlertmanagerWebhook(data map[string]interface{}) bool {
	matchCount := 0

	// Check for "version" field (Alertmanager-specific)
	if version, ok := data["version"]; ok {
		if versionStr, ok := version.(string); ok && versionStr != "" {
			matchCount++
		}
	}

	// Check for "groupKey" field (Alertmanager-specific)
	if groupKey, ok := data["groupKey"]; ok {
		if groupKeyStr, ok := groupKey.(string); ok && groupKeyStr != "" {
			matchCount++
		}
	}

	// Check for "receiver" field (Alertmanager-specific)
	if receiver, ok := data["receiver"]; ok {
		if receiverStr, ok := receiver.(string); ok && receiverStr != "" {
			matchCount++
		}
	}

	// Check for "alerts" array structure
	if alerts, ok := data["alerts"]; ok {
		if alertsArray, ok := alerts.([]interface{}); ok && len(alertsArray) > 0 {
			// Verify alerts have Alertmanager structure (status, labels, annotations)
			if d.hasAlertmanagerAlertStructure(alertsArray) {
				matchCount++
			}
		}
	}

	// Require at least 2 Alertmanager-specific fields to confidently detect
	return matchCount >= 2
}

// hasAlertmanagerAlertStructure checks if alerts have Alertmanager-specific structure.
func (d *webhookDetector) hasAlertmanagerAlertStructure(alerts []interface{}) bool {
	if len(alerts) == 0 {
		return false
	}

	// Check first alert for Alertmanager fields
	firstAlert, ok := alerts[0].(map[string]interface{})
	if !ok {
		return false
	}

	// Alertmanager alerts have: status, labels, annotations, startsAt
	hasStatus := false
	hasLabels := false

	if status, ok := firstAlert["status"]; ok {
		if statusStr, ok := status.(string); ok && (statusStr == "firing" || statusStr == "resolved") {
			hasStatus = true
		}
	}

	if labels, ok := firstAlert["labels"]; ok {
		if labelsMap, ok := labels.(map[string]interface{}); ok && len(labelsMap) > 0 {
			hasLabels = true
		}
	}

	// Annotations are optional in Alertmanager format, so we don't check them

	// Require status + labels (annotations optional)
	return hasStatus && hasLabels
}

// isPrometheusV1Webhook checks if the payload is Prometheus v1 format (array).
//
// Prometheus v1 characteristics:
//   - Top-level array structure: [...]
//   - Each element has "state" field (not "status")
//   - Each element has "activeAt" field (not "startsAt")
//   - Each element has "generatorURL" field (required in Prometheus)
//
// Example:
//
//	[
//	  {
//	    "labels": {"alertname": "HighCPU"},
//	    "state": "firing",
//	    "activeAt": "2025-11-18T10:00:00Z",
//	    "generatorURL": "http://prometheus:9090/graph"
//	  }
//	]
func (d *webhookDetector) isPrometheusV1Webhook(data []interface{}) bool {
	if len(data) == 0 {
		return false
	}

	// Check first alert for Prometheus v1 signature
	firstAlert, ok := data[0].(map[string]interface{})
	if !ok {
		return false
	}

	return hasPrometheusV1Fields(firstAlert)
}

// isPrometheusV2Webhook checks if the payload is Prometheus v2 format (grouped).
//
// Prometheus v2 characteristics:
//   - Top-level object with "groups" array
//   - Each group has "labels" and "alerts" fields
//   - Alerts have Prometheus v1 structure (state, activeAt, generatorURL)
//
// Example:
//
//	{
//	  "groups": [
//	    {
//	      "labels": {"job": "api"},
//	      "alerts": [
//	        {
//	          "labels": {"alertname": "HighCPU"},
//	          "state": "firing",
//	          "activeAt": "2025-11-18T10:00:00Z",
//	          "generatorURL": "http://prometheus:9090/graph"
//	        }
//	      ]
//	    }
//	  ]
//	}
func (d *webhookDetector) isPrometheusV2Webhook(data map[string]interface{}) bool {
	// Check for "groups" field
	groups, ok := data["groups"]
	if !ok {
		return false
	}

	groupsArray, ok := groups.([]interface{})
	if !ok || len(groupsArray) == 0 {
		return false
	}

	// Check first group structure
	firstGroup, ok := groupsArray[0].(map[string]interface{})
	if !ok {
		return false
	}

	// Groups must have "labels" and "alerts" fields
	if _, hasLabels := firstGroup["labels"]; !hasLabels {
		return false
	}

	alerts, hasAlerts := firstGroup["alerts"]
	if !hasAlerts {
		return false
	}

	alertsArray, ok := alerts.([]interface{})
	if !ok || len(alertsArray) == 0 {
		return false
	}

	// Check first alert has Prometheus v1 structure
	firstAlert, ok := alertsArray[0].(map[string]interface{})
	if !ok {
		return false
	}

	return hasPrometheusV1Fields(firstAlert)
}

// hasPrometheusV1Fields checks if an alert has Prometheus v1 signature fields.
//
// Prometheus v1 indicators:
//   - "state" field (vs "status" in Alertmanager)
//   - "activeAt" field (vs "startsAt" in Alertmanager)
//   - "generatorURL" field (required in Prometheus)
//   - "labels" field (common to both)
//
// This function distinguishes Prometheus from Alertmanager alerts.
func hasPrometheusV1Fields(alert map[string]interface{}) bool {
	// Check for Prometheus-specific "state" field
	state, hasState := alert["state"]
	if !hasState {
		return false
	}
	// Verify state is a valid Prometheus state
	if stateStr, ok := state.(string); ok {
		if stateStr != "firing" && stateStr != "pending" && stateStr != "inactive" {
			return false
		}
	} else {
		return false
	}

	// Check for Prometheus-specific "activeAt" field
	_, hasActiveAt := alert["activeAt"]
	if !hasActiveAt {
		return false
	}

	// Check for required "labels" field
	labels, hasLabels := alert["labels"]
	if !hasLabels {
		return false
	}
	// Verify labels is a map
	if _, ok := labels.(map[string]interface{}); !ok {
		return false
	}

	// Check for required "generatorURL" field (Prometheus always provides this)
	generatorURL, hasGeneratorURL := alert["generatorURL"]
	if !hasGeneratorURL {
		return false
	}
	// Verify generatorURL is a non-empty string
	if urlStr, ok := generatorURL.(string); !ok || urlStr == "" {
		return false
	}

	// All Prometheus v1 indicators present
	return true
}

// hasField checks if a map contains a specific key.
func hasField(data map[string]interface{}, field string) bool {
	_, ok := data[field]
	return ok
}

// prometheusFormatDetector implements PrometheusFormatDetector.
type prometheusFormatDetector struct{}

// NewPrometheusFormatDetector creates a new Prometheus format detector.
func NewPrometheusFormatDetector() PrometheusFormatDetector {
	return &prometheusFormatDetector{}
}

// DetectPrometheusFormat determines the specific Prometheus format (v1 or v2).
//
// This method should be called after Detect() returns WebhookTypePrometheus
// to determine the exact format variant.
//
// Detection logic:
//  1. Try to parse as JSON
//  2. Check if top-level is array → Prometheus v1
//  3. Check if top-level has "groups" field → Prometheus v2
//  4. Otherwise → error
//
// Parameters:
//   - payload: Raw JSON payload bytes
//
// Returns:
//   - string: PrometheusFormatV1 or PrometheusFormatV2
//   - error: If format cannot be determined
func (d *prometheusFormatDetector) DetectPrometheusFormat(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("prometheus webhook payload is empty")
	}

	// Parse as generic JSON
	var data interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return "", fmt.Errorf("invalid JSON in prometheus webhook: %w", err)
	}

	// Check for array structure (v1 format)
	if dataArray, ok := data.([]interface{}); ok {
		if len(dataArray) > 0 {
			// Verify first element has Prometheus fields
			if firstAlert, ok := dataArray[0].(map[string]interface{}); ok {
				if hasPrometheusV1Fields(firstAlert) {
					return PrometheusFormatV1, nil
				}
			}
		}
		return "", fmt.Errorf("array structure but not valid Prometheus v1 format")
	}

	// Check for object structure (v2 format)
	if dataMap, ok := data.(map[string]interface{}); ok {
		// Check for "groups" field
		if groups, hasGroups := dataMap["groups"]; hasGroups {
			if groupsArray, ok := groups.([]interface{}); ok && len(groupsArray) > 0 {
				return PrometheusFormatV2, nil
			}
		}
		return "", fmt.Errorf("object structure but no 'groups' field (not Prometheus v2)")
	}

	return "", fmt.Errorf("unknown Prometheus format structure")
}

// DetectWithHints analyzes payload with additional hints from HTTP headers.
//
// This method can use Content-Type, User-Agent, or custom headers to assist detection.
// For now, it falls back to standard detection, but can be extended.
//
// Parameters:
//   - payload: Raw webhook payload bytes
//   - contentType: HTTP Content-Type header value
//   - userAgent: HTTP User-Agent header value
//
// Returns:
//   - WebhookType: Detected webhook type
//   - error: Detection error
func (d *webhookDetector) DetectWithHints(payload []byte, contentType string, userAgent string) (WebhookType, error) {
	// Future: Use Content-Type or User-Agent hints
	// For example, User-Agent might contain "Alertmanager" or "Prometheus"

	// For now, use standard detection
	return d.Detect(payload)
}
