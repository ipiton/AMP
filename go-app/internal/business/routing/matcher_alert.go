package routing

import "time"

// Alert represents an alert for routing purposes.
//
// This is a simplified version of the core Alert type
// (from internal/core/interfaces.go) with only the fields
// needed for routing decisions.
//
// The routing engine only needs:
//   - Labels (for matching)
//   - StartsAt/EndsAt (for time-based routing, future)
//
// Example:
//
//	alert := &Alert{
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "severity": "critical",
//	        "namespace": "production",
//	    },
//	}
type Alert struct {
	// Labels are the alert labels (key-value pairs).
	//
	// These are matched against route matchers to determine
	// which routes apply to this alert.
	//
	// Standard labels:
	// - alertname: The alert name (e.g., "HighCPU")
	// - severity: The alert severity (e.g., "critical")
	// - namespace: The K8s namespace (e.g., "production")
	//
	// Custom labels are also supported.
	Labels map[string]string

	// StartsAt is when the alert started.
	//
	// Used for time-based routing (future feature).
	StartsAt time.Time

	// EndsAt is when the alert ended (resolved).
	//
	// Zero value means alert is still firing.
	EndsAt time.Time
}

// IsFiring returns true if the alert is currently firing.
//
// An alert is firing if EndsAt is zero or in the future.
func (a *Alert) IsFiring() bool {
	return a.EndsAt.IsZero() || a.EndsAt.After(time.Now())
}

// IsResolved returns true if the alert is resolved.
//
// An alert is resolved if EndsAt is non-zero and in the past.
func (a *Alert) IsResolved() bool {
	return !a.EndsAt.IsZero() && a.EndsAt.Before(time.Now())
}

// GetLabel returns a label value by name.
//
// Returns empty string if label doesn't exist.
//
// Example:
//
//	severity := alert.GetLabel("severity") // "critical"
func (a *Alert) GetLabel(name string) string {
	if a.Labels == nil {
		return ""
	}
	return a.Labels[name]
}

// HasLabel returns true if the alert has the specified label.
//
// Example:
//
//	if alert.HasLabel("severity") {
//	    severity := alert.Labels["severity"]
//	}
func (a *Alert) HasLabel(name string) bool {
	if a.Labels == nil {
		return false
	}
	_, ok := a.Labels[name]
	return ok
}
