package template

import (
	"time"
)

// ================================================================================
// TN-153: Template Engine - Template Data
// ================================================================================
// Data structure passed to notification templates.
//
// Compatible with Alertmanager template data structure.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// TemplateData contains all data available to notification templates.
//
// This structure is compatible with Alertmanager's template data,
// allowing seamless migration from Alertmanager to Alert History.
//
// Example Template Usage:
//
//	Title: "🔥 {{ .GroupLabels.alertname }} - {{ .Status }}"
//	Text: |
//	  *Severity*: {{ .Labels.severity | default "unknown" }}
//	  *Instance*: {{ .Labels.instance }}
//	  *Started*: {{ .StartsAt | humanizeTimestamp }}
//	  {{ if .Annotations.runbook_url }}
//	  📖 [Runbook]({{ .Annotations.runbook_url }})
//	  {{ end }}
type TemplateData struct {
	// ===================================================================
	// Alert Fields
	// ===================================================================

	// Status is alert status: "firing" or "resolved"
	Status string

	// Labels are alert labels (e.g., alertname, severity, instance)
	// Example: {"alertname": "HighCPU", "severity": "critical", "instance": "prod-1"}
	Labels map[string]string

	// Annotations are alert annotations (e.g., summary, description, runbook_url)
	// Example: {"summary": "CPU usage is high", "description": "CPU > 90% for 5m"}
	Annotations map[string]string

	// StartsAt is when alert started firing
	StartsAt time.Time

	// EndsAt is when alert resolved (zero value if still firing)
	EndsAt time.Time

	// GeneratorURL is Prometheus generator URL
	// Example: "http://prometheus:9090/graph?g0.expr=..."
	GeneratorURL string

	// Fingerprint is unique alert fingerprint (SHA256)
	// Example: "a1b2c3d4e5f6..."
	Fingerprint string

	// Value is alert value (if available)
	// For threshold alerts: current metric value
	// Example: 95.3 (for CPU usage)
	Value float64

	// ===================================================================
	// Group Fields (for grouped notifications)
	// ===================================================================

	// Alerts is array of all alerts in the group
	// For single alerts: array of 1 element
	// For grouped alerts: array of multiple elements
	// Example: [Alert{Status: "firing", Labels: {...}}, ...]
	Alerts []Alert

	// GroupLabels are labels used for grouping
	// Example: {"alertname": "HighCPU", "cluster": "prod"}
	GroupLabels map[string]string

	// CommonLabels are labels common to all alerts in group
	// Example: {"severity": "critical", "team": "platform"}
	CommonLabels map[string]string

	// CommonAnnotations are annotations common to all alerts in group
	// Example: {"runbook_url": "https://wiki.company.com/runbooks/cpu"}
	CommonAnnotations map[string]string

	// GroupKey is unique group identifier
	// Example: "alertname=HighCPU,cluster=prod"
	GroupKey string

	// ===================================================================
	// External URLs
	// ===================================================================

	// ExternalURL is Alert History external URL
	// Example: "https://alerts.company.com"
	ExternalURL string

	// SilenceURL is direct link to create silence for this alert
	// Example: "https://alerts.company.com/silences/new?filter=alertname%3DHighCPU"
	SilenceURL string

	// ===================================================================
	// Receiver Context
	// ===================================================================

	// Receiver is receiver name
	// Example: "slack-oncall"
	Receiver string

	// ReceiverType is receiver type: "slack", "pagerduty", "email", "webhook"
	ReceiverType string
}

// NewTemplateData creates TemplateData with required fields
//
// Parameters:
//   - status: Alert status ("firing" or "resolved")
//   - labels: Alert labels
//   - annotations: Alert annotations
//   - startsAt: Alert start time
//
// Returns:
//   - *TemplateData: Initialized template data
//
// Example:
//
//	data := NewTemplateData(
//	    "firing",
//	    map[string]string{"alertname": "HighCPU", "severity": "critical"},
//	    map[string]string{"summary": "CPU is high"},
//	    time.Now(),
//	)
func NewTemplateData(
	status string,
	labels map[string]string,
	annotations map[string]string,
	startsAt time.Time,
) *TemplateData {
	// Initialize maps if nil
	if labels == nil {
		labels = make(map[string]string)
	}
	if annotations == nil {
		annotations = make(map[string]string)
	}

	return &TemplateData{
		Status:      status,
		Labels:      labels,
		Annotations: annotations,
		StartsAt:    startsAt,
		// Other fields initialized to zero values
		GroupLabels:       make(map[string]string),
		CommonLabels:      make(map[string]string),
		CommonAnnotations: make(map[string]string),
	}
}

// IsResolved returns true if alert is resolved
func (d *TemplateData) IsResolved() bool {
	return d.Status == "resolved"
}

// IsFiring returns true if alert is firing
func (d *TemplateData) IsFiring() bool {
	return d.Status == "firing"
}

// Duration returns alert duration
//
// For firing alerts: time since StartsAt
// For resolved alerts: EndsAt - StartsAt
func (d *TemplateData) Duration() time.Duration {
	if d.IsResolved() && !d.EndsAt.IsZero() {
		return d.EndsAt.Sub(d.StartsAt)
	}
	return time.Since(d.StartsAt)
}

// GetLabel returns label value or empty string if not found
func (d *TemplateData) GetLabel(key string) string {
	if d.Labels == nil {
		return ""
	}
	return d.Labels[key]
}

// GetAnnotation returns annotation value or empty string if not found
func (d *TemplateData) GetAnnotation(key string) string {
	if d.Annotations == nil {
		return ""
	}
	return d.Annotations[key]
}

// HasLabel returns true if label exists
func (d *TemplateData) HasLabel(key string) bool {
	if d.Labels == nil {
		return false
	}
	_, exists := d.Labels[key]
	return exists
}

// HasAnnotation returns true if annotation exists
func (d *TemplateData) HasAnnotation(key string) bool {
	if d.Annotations == nil {
		return false
	}
	_, exists := d.Annotations[key]
	return exists
}

// Validate validates template data
//
// Returns error if:
// - Status is not "firing" or "resolved"
// - Labels is nil
// - StartsAt is zero
func (d *TemplateData) Validate() error {
	if d.Status != "firing" && d.Status != "resolved" {
		return NewDataError("status must be 'firing' or 'resolved'")
	}

	if d.Labels == nil {
		return NewDataError("labels cannot be nil")
	}

	if d.StartsAt.IsZero() {
		return NewDataError("startsAt cannot be zero")
	}

	return nil
}

// WithGroupInfo adds group information to template data
func (d *TemplateData) WithGroupInfo(
	groupLabels map[string]string,
	commonLabels map[string]string,
	commonAnnotations map[string]string,
	groupKey string,
) *TemplateData {
	d.GroupLabels = groupLabels
	d.CommonLabels = commonLabels
	d.CommonAnnotations = commonAnnotations
	d.GroupKey = groupKey
	return d
}

// WithExternalURL sets external URL
func (d *TemplateData) WithExternalURL(url string) *TemplateData {
	d.ExternalURL = url
	return d
}

// WithSilenceURL sets silence URL
func (d *TemplateData) WithSilenceURL(url string) *TemplateData {
	d.SilenceURL = url
	return d
}

// WithReceiver sets receiver information
func (d *TemplateData) WithReceiver(name string, receiverType string) *TemplateData {
	d.Receiver = name
	d.ReceiverType = receiverType
	return d
}

// WithValue sets alert value
func (d *TemplateData) WithValue(value float64) *TemplateData {
	d.Value = value
	return d
}

// WithEndsAt sets alert end time
func (d *TemplateData) WithEndsAt(endsAt time.Time) *TemplateData {
	d.EndsAt = endsAt
	return d
}

// WithGeneratorURL sets Prometheus generator URL
func (d *TemplateData) WithGeneratorURL(url string) *TemplateData {
	d.GeneratorURL = url
	return d
}

// WithFingerprint sets alert fingerprint
func (d *TemplateData) WithFingerprint(fingerprint string) *TemplateData {
	d.Fingerprint = fingerprint
	return d
}
