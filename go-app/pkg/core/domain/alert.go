package domain

import (
	"fmt"
	"time"
)

// ================================================================================
// Alert Domain Model - OSS Core
// ================================================================================
// Pure domain model with zero external dependencies (stdlib only).
// 100% Alertmanager-compatible structure.

// Alert represents an alert in the system.
//
// This is the core domain model for alerts, compatible with:
//   - Alertmanager API v2
//   - Prometheus alerting format
//   - All standard alert management tools
//
// Design Principles:
//   - Zero dependencies (stdlib only)
//   - Immutable after creation (use methods to create modified copies)
//   - Self-validating (Validate() method)
//   - Framework-agnostic (no HTTP, no DB, no cache)
//
// Example:
//
//	alert := &Alert{
//	    Fingerprint: "7a3b1f2c",
//	    AlertName:   "HighCPU",
//	    Status:      StatusFiring,
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "severity":  "critical",
//	        "instance":  "server-01",
//	    },
//	    Annotations: map[string]string{
//	        "summary": "CPU usage above 90%",
//	    },
//	    StartsAt: time.Now(),
//	}
type Alert struct {
	// Fingerprint is the unique identifier for this alert.
	// Generated from Labels using FNV-1a hash (Alertmanager-compatible).
	// Example: "7a3b1f2c4d5e6f7a"
	Fingerprint string `json:"fingerprint"`

	// AlertName is the alert name (from "alertname" label).
	// Required field for all alerts.
	// Example: "HighCPU", "DiskFull", "ServiceDown"
	AlertName string `json:"alert_name"`

	// Status indicates whether the alert is firing or resolved.
	// Must be one of: "firing", "resolved"
	Status AlertStatus `json:"status"`

	// Labels contains the alert labels (Prometheus format).
	// Labels are used for:
	//   - Alert identification (alertname)
	//   - Routing (routing rules match on labels)
	//   - Grouping (alerts grouped by common labels)
	//   - Silencing (silences match on labels)
	//
	// Common labels:
	//   - alertname: Alert name (required)
	//   - severity: critical, warning, info
	//   - instance: Target instance
	//   - job: Job name
	//   - namespace: Kubernetes namespace
	//
	// Example:
	//   Labels: map[string]string{
	//       "alertname": "HighCPU",
	//       "severity":  "critical",
	//       "instance":  "server-01",
	//       "job":       "api",
	//   }
	Labels map[string]string `json:"labels"`

	// Annotations contains additional metadata (not used for routing).
	// Annotations are human-readable text intended for:
	//   - Alert descriptions
	//   - Runbook links
	//   - Dashboard URLs
	//   - Resolution steps
	//
	// Common annotations:
	//   - summary: Short description
	//   - description: Detailed description
	//   - runbook_url: Link to runbook
	//   - dashboard_url: Link to dashboard
	//
	// Example:
	//   Annotations: map[string]string{
	//       "summary":      "CPU usage above 90%",
	//       "description":  "Server server-01 CPU usage is 95%",
	//       "runbook_url":  "https://wiki.company.com/runbooks/high-cpu",
	//   }
	Annotations map[string]string `json:"annotations"`

	// StartsAt is the timestamp when the alert started firing.
	// For firing alerts: when the condition first became true
	// For resolved alerts: when the condition originally became true
	StartsAt time.Time `json:"starts_at"`

	// EndsAt is the timestamp when the alert was resolved (optional).
	// Nil for firing alerts.
	// Set when alert status changes to "resolved".
	EndsAt *time.Time `json:"ends_at,omitempty"`

	// GeneratorURL is the URL of the Prometheus instance that generated the alert (optional).
	// Example: "http://prometheus:9090/graph?g0.expr=..."
	GeneratorURL *string `json:"generator_url,omitempty"`

	// Timestamp is when the alert was last updated (optional).
	// If nil, uses StartsAt for firing alerts or EndsAt for resolved alerts.
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// AlertStatus represents the current state of an alert.
type AlertStatus string

const (
	// StatusFiring indicates the alert is currently firing.
	// The alert condition is true and notifications should be sent.
	StatusFiring AlertStatus = "firing"

	// StatusResolved indicates the alert has been resolved.
	// The alert condition is no longer true.
	StatusResolved AlertStatus = "resolved"
)

// AlertSeverity represents alert severity levels.
// This is extracted from the "severity" label if present.
type AlertSeverity string

const (
	// SeverityCritical indicates a critical alert requiring immediate action.
	SeverityCritical AlertSeverity = "critical"

	// SeverityWarning indicates a warning that should be investigated soon.
	SeverityWarning AlertSeverity = "warning"

	// SeverityInfo indicates an informational alert for awareness.
	SeverityInfo AlertSeverity = "info"

	// SeverityNoise indicates a low-priority alert (filtered in some configurations).
	SeverityNoise AlertSeverity = "noise"
)

// ================================================================================
// Alert Methods
// ================================================================================

// Validate checks if the alert is valid.
//
// Validation rules:
//   - Fingerprint must not be empty
//   - AlertName must not be empty
//   - Status must be "firing" or "resolved"
//   - StartsAt must not be zero
//   - Labels must contain "alertname" key
//   - If status is "resolved", EndsAt must be set and after StartsAt
//
// Returns:
//   - nil if valid
//   - error with validation message if invalid
func (a *Alert) Validate() error {
	if a.Fingerprint == "" {
		return fmt.Errorf("fingerprint is required")
	}
	if a.AlertName == "" {
		return fmt.Errorf("alert_name is required")
	}
	if a.Status != StatusFiring && a.Status != StatusResolved {
		return fmt.Errorf("status must be 'firing' or 'resolved', got: %s", a.Status)
	}
	if a.StartsAt.IsZero() {
		return fmt.Errorf("starts_at is required")
	}
	if a.Labels == nil {
		return fmt.Errorf("labels is required")
	}
	if alertname, ok := a.Labels["alertname"]; !ok || alertname == "" {
		return fmt.Errorf("labels must contain 'alertname'")
	}
	if a.Status == StatusResolved {
		if a.EndsAt == nil {
			return fmt.Errorf("ends_at is required for resolved alerts")
		}
		if a.EndsAt.Before(a.StartsAt) {
			return fmt.Errorf("ends_at must be after starts_at")
		}
	}
	return nil
}

// IsFiring returns true if the alert is currently firing.
func (a *Alert) IsFiring() bool {
	return a.Status == StatusFiring
}

// IsResolved returns true if the alert has been resolved.
func (a *Alert) IsResolved() bool {
	return a.Status == StatusResolved
}

// Namespace returns the alert's namespace from labels (Kubernetes).
// Returns nil if "namespace" label is not present.
func (a *Alert) Namespace() *string {
	if ns, ok := a.Labels["namespace"]; ok {
		return &ns
	}
	return nil
}

// Severity returns the alert's severity from labels.
// Returns nil if "severity" label is not present.
func (a *Alert) Severity() *string {
	if sev, ok := a.Labels["severity"]; ok {
		return &sev
	}
	return nil
}

// Instance returns the alert's instance from labels.
// Returns nil if "instance" label is not present.
func (a *Alert) Instance() *string {
	if inst, ok := a.Labels["instance"]; ok {
		return &inst
	}
	return nil
}

// Job returns the alert's job name from labels.
// Returns nil if "job" label is not present.
func (a *Alert) Job() *string {
	if job, ok := a.Labels["job"]; ok {
		return &job
	}
	return nil
}

// Duration returns how long the alert has been firing/was firing.
// For firing alerts: time since StartsAt
// For resolved alerts: EndsAt - StartsAt
func (a *Alert) Duration() time.Duration {
	if a.Status == StatusResolved && a.EndsAt != nil {
		return a.EndsAt.Sub(a.StartsAt)
	}
	return time.Since(a.StartsAt)
}

// Copy creates a deep copy of the alert.
// Useful for creating modified versions without mutating the original.
func (a *Alert) Copy() *Alert {
	copy := &Alert{
		Fingerprint:  a.Fingerprint,
		AlertName:    a.AlertName,
		Status:       a.Status,
		Labels:       make(map[string]string, len(a.Labels)),
		Annotations:  make(map[string]string, len(a.Annotations)),
		StartsAt:     a.StartsAt,
		EndsAt:       a.EndsAt,
		GeneratorURL: a.GeneratorURL,
		Timestamp:    a.Timestamp,
	}

	// Deep copy maps
	for k, v := range a.Labels {
		copy.Labels[k] = v
	}
	for k, v := range a.Annotations {
		copy.Annotations[k] = v
	}

	return copy
}

// WithStatus returns a new alert with the specified status.
// Does not modify the original alert.
func (a *Alert) WithStatus(status AlertStatus) *Alert {
	copy := a.Copy()
	copy.Status = status
	if status == StatusResolved && copy.EndsAt == nil {
		now := time.Now()
		copy.EndsAt = &now
	}
	return copy
}

// WithLabel returns a new alert with an additional label.
// Does not modify the original alert.
func (a *Alert) WithLabel(key, value string) *Alert {
	copy := a.Copy()
	copy.Labels[key] = value
	return copy
}

// WithAnnotation returns a new alert with an additional annotation.
// Does not modify the original alert.
func (a *Alert) WithAnnotation(key, value string) *Alert {
	copy := a.Copy()
	copy.Annotations[key] = value
	return copy
}

// ================================================================================
// Alert List Operations
// ================================================================================

// AlertList represents a collection of alerts.
type AlertList []*Alert

// Filter returns alerts matching the predicate function.
func (l AlertList) Filter(predicate func(*Alert) bool) AlertList {
	result := make(AlertList, 0, len(l))
	for _, alert := range l {
		if predicate(alert) {
			result = append(result, alert)
		}
	}
	return result
}

// Firing returns only firing alerts.
func (l AlertList) Firing() AlertList {
	return l.Filter(func(a *Alert) bool { return a.IsFiring() })
}

// Resolved returns only resolved alerts.
func (l AlertList) Resolved() AlertList {
	return l.Filter(func(a *Alert) bool { return a.IsResolved() })
}

// ByNamespace returns alerts for the specified namespace.
func (l AlertList) ByNamespace(namespace string) AlertList {
	return l.Filter(func(a *Alert) bool {
		ns := a.Namespace()
		return ns != nil && *ns == namespace
	})
}

// BySeverity returns alerts with the specified severity.
func (l AlertList) BySeverity(severity string) AlertList {
	return l.Filter(func(a *Alert) bool {
		sev := a.Severity()
		return sev != nil && *sev == severity
	})
}

// Count returns the number of alerts.
func (l AlertList) Count() int {
	return len(l)
}
