// Package webhook provides webhook parsers for multiple alert formats.
//
// This file implements Prometheus-specific data models for parsing
// Prometheus native alerts (both v1 and v2 formats).
package webhook

import "time"

// PrometheusAlert represents a single alert in Prometheus format.
//
// This structure is compatible with both:
//   - Prometheus API v1 (legacy /api/v1/alerts)
//   - Prometheus API v2 (modern /api/v2/alerts)
//
// Key differences from Alertmanager format:
//   - "state" instead of "status" ("firing" | "pending" | "inactive")
//   - "activeAt" instead of "startsAt" (when alert became active)
//   - "generatorURL" is required (always present in Prometheus)
//   - "value" field contains the alert's metric value (optional)
//
// Example JSON:
//
//	{
//	  "labels": {
//	    "alertname": "HighCPU",
//	    "instance": "server-1:9100",
//	    "job": "node-exporter",
//	    "severity": "warning"
//	  },
//	  "annotations": {
//	    "summary": "CPU usage is above 80%",
//	    "description": "Instance server-1 has CPU > 80% for 5 minutes"
//	  },
//	  "state": "firing",
//	  "activeAt": "2025-11-18T10:00:00Z",
//	  "value": "0.85",
//	  "generatorURL": "http://prometheus:9090/graph?g0.expr=...",
//	  "fingerprint": "a3f8b2c1..."
//	}
type PrometheusAlert struct {
	// Labels contains all alert labels (including alertname).
	// REQUIRED: Must contain at least "alertname" label.
	//
	// Label naming conventions (Prometheus):
	//   - Valid: [a-zA-Z_][a-zA-Z0-9_]*
	//   - Common labels: alertname, job, instance, severity, namespace
	//
	// Example:
	//   {
	//     "alertname": "HighCPU",
	//     "instance": "server-1:9100",
	//     "job": "node-exporter",
	//     "severity": "warning"
	//   }
	Labels map[string]string `json:"labels" validate:"required,dive,keys,required,endkeys,required"`

	// Annotations contains alert annotations (descriptions, runbooks, etc).
	// Optional: Can be empty or nil.
	//
	// Common annotations:
	//   - summary: Short description
	//   - description: Detailed explanation
	//   - runbook_url: Link to troubleshooting guide
	//
	// Example:
	//   {
	//     "summary": "CPU usage is above 80%",
	//     "description": "Instance server-1 has CPU > 80% for 5 minutes",
	//     "runbook_url": "https://wiki.example.com/runbooks/high-cpu"
	//   }
	Annotations map[string]string `json:"annotations"`

	// State represents the current alert state.
	// REQUIRED: Must be one of "firing", "pending", "inactive".
	//
	// States:
	//  - "firing": Alert condition is true and alert is active
	//  - "pending": Alert condition is true but waiting for "for" duration
	//  - "inactive": Alert condition is false (equivalent to "resolved")
	//
	// Note: "pending" state exists in Prometheus but not in Alertmanager.
	// We map "pending" → "firing" during conversion.
	State string `json:"state" validate:"required,oneof=firing pending inactive"`

	// ActiveAt is the timestamp when the alert first became active.
	// REQUIRED: Must be valid RFC3339 timestamp.
	//
	// Note: This is different from Alertmanager's "startsAt" which can be
	// updated on alert re-firing. "activeAt" remains constant for the same alert.
	//
	// Format: RFC3339 (e.g. "2025-11-18T10:00:00Z")
	ActiveAt time.Time `json:"activeAt" validate:"required"`

	// Value contains the alert's metric value at evaluation time.
	// Optional: May be empty for non-threshold alerts.
	//
	// This field stores the actual metric value that triggered the alert.
	// For example, if the rule is "cpu_usage > 0.8", the value might be "0.85".
	//
	// Example: "0.85" for CPU usage 85%
	Value string `json:"value,omitempty"`

	// GeneratorURL is the URL to the Prometheus expression browser for this alert.
	// REQUIRED in Prometheus format (always present).
	//
	// This URL allows users to view the alert query in Prometheus UI.
	//
	// Example: "http://prometheus:9090/graph?g0.expr=up%7Bjob%3D%22api%22%7D+%3D%3D+0"
	GeneratorURL string `json:"generatorURL" validate:"required,url"`

	// Fingerprint is a unique identifier for this alert based on labels.
	// Optional: Will be generated if not provided (SHA256 of sorted labels).
	//
	// The fingerprint is used for:
	//   - Alert deduplication (same labels → same fingerprint)
	//   - Alert tracking across state changes (firing → resolved)
	//   - Storage and retrieval
	//
	// Format: Hex-encoded SHA256 hash (64 characters)
	// Example: "a3f8b2c1d4e5f6..." (64 hex digits)
	//
	// If not provided by Prometheus, we generate it using the same algorithm
	// as the Alertmanager parser (TN-41) for consistency.
	Fingerprint string `json:"fingerprint,omitempty" validate:"omitempty,hexadecimal,len=64"`
}

// PrometheusAlertGroup represents a group of alerts in Prometheus v2 format.
//
// Prometheus v2 API returns alerts grouped by common labels to reduce payload size.
// This structure is only used when parsing /api/v2/alerts responses.
//
// Structure:
//
//	{
//	  "labels": {"job": "api-server", "severity": "warning"},
//	  "alerts": [
//	    {"labels": {"alertname": "HighCPU", "instance": "server-1"}, ...},
//	    {"labels": {"alertname": "HighMemory", "instance": "server-1"}, ...}
//	  ]
//	}
//
// The group labels are shared by all alerts in the group and are NOT duplicated
// in individual alert Labels. During parsing, we merge group labels into each alert.
type PrometheusAlertGroup struct {
	// Labels contains the common labels shared by all alerts in this group.
	// These labels are NOT duplicated in individual alert Labels.
	//
	// Example:
	//   {
	//     "job": "api-server",
	//     "severity": "warning"
	//   }
	//
	// These labels will be merged into each alert's Labels during FlattenAlerts().
	Labels map[string]string `json:"labels"`

	// Alerts contains all alerts in this group.
	// The group Labels are implicitly added to each alert during parsing.
	//
	// REQUIRED: Must contain at least one alert.
	Alerts []PrometheusAlert `json:"alerts" validate:"required,min=1,dive"`
}

// PrometheusWebhook represents the top-level Prometheus webhook payload.
//
// This structure supports both formats:
//   - v1: Direct array of alerts in "Alerts" field
//   - v2: Grouped alerts in "Groups" field
//
// Exactly one of Alerts or Groups must be non-empty.
//
// Examples:
//
// Prometheus v1 format:
//
//	{
//	  "alerts": [
//	    {"labels": {"alertname": "HighCPU"}, "state": "firing", ...}
//	  ]
//	}
//
// Prometheus v2 format:
//
//	{
//	  "groups": [
//	    {
//	      "labels": {"job": "api"},
//	      "alerts": [
//	        {"labels": {"alertname": "HighCPU"}, "state": "firing", ...}
//	      ]
//	    }
//	  ]
//	}
type PrometheusWebhook struct {
	// Alerts contains alerts in v1 format (direct array).
	// Used when parsing /api/v1/alerts responses or Prometheus webhook config.
	//
	// If non-empty, Groups must be empty.
	//
	// Format: Flat array of PrometheusAlert objects
	Alerts []PrometheusAlert `json:"alerts,omitempty" validate:"required_without=Groups,dive"`

	// Groups contains alerts in v2 format (grouped by labels).
	// Used when parsing /api/v2/alerts responses.
	//
	// If non-empty, Alerts must be empty.
	//
	// Format: Array of PrometheusAlertGroup objects
	Groups []PrometheusAlertGroup `json:"groups,omitempty" validate:"required_without=Alerts,dive"`
}

// AlertCount returns the total number of alerts in the webhook.
//
// For v1 format, this is simply len(Alerts).
// For v2 format, this is the sum of alerts across all groups.
//
// Example:
//
//	webhook := &PrometheusWebhook{
//	    Alerts: []PrometheusAlert{{...}, {...}},
//	}
//	count := webhook.AlertCount() // Returns 2
//
// Returns:
//   - int: Total number of alerts (0 if webhook is empty)
func (w *PrometheusWebhook) AlertCount() int {
	if len(w.Alerts) > 0 {
		return len(w.Alerts)
	}

	count := 0
	for _, group := range w.Groups {
		count += len(group.Alerts)
	}
	return count
}

// FlattenAlerts returns all alerts as a flat array.
//
// This method handles both v1 and v2 formats:
//   - v1: Returns Alerts as-is (already flat)
//   - v2: Merges group labels into each alert and returns flat array
//
// For v2 format, group labels are merged into each alert's Labels:
//   - Group labels are added first
//   - Alert labels override group labels (if duplicate keys)
//
// Example (v2 format):
//
//	webhook := &PrometheusWebhook{
//	    Groups: []PrometheusAlertGroup{
//	        {
//	            Labels: {"job": "api", "severity": "warning"},
//	            Alerts: []PrometheusAlert{
//	                {Labels: {"alertname": "HighCPU", "instance": "server-1"}},
//	            },
//	        },
//	    },
//	}
//	flat := webhook.FlattenAlerts()
//	// flat[0].Labels = {"job": "api", "severity": "warning", "alertname": "HighCPU", "instance": "server-1"}
//
// Returns:
//   - []PrometheusAlert: Flat array of alerts with merged labels
func (w *PrometheusWebhook) FlattenAlerts() []PrometheusAlert {
	if len(w.Alerts) > 0 {
		return w.Alerts // v1 format: already flat
	}

	// v2 format: flatten groups
	var flattened []PrometheusAlert
	for _, group := range w.Groups {
		for _, alert := range group.Alerts {
			// Merge group labels into alert labels
			// Step 1: Copy group labels
			merged := make(map[string]string, len(group.Labels)+len(alert.Labels))
			for k, v := range group.Labels {
				merged[k] = v
			}
			// Step 2: Copy alert labels (override group labels if duplicate)
			for k, v := range alert.Labels {
				merged[k] = v
			}

			// Create new alert with merged labels
			flatAlert := alert
			flatAlert.Labels = merged
			flattened = append(flattened, flatAlert)
		}
	}
	return flattened
}
