package template

import "time"

// Alert represents a single alert within a template.
// Used in TemplateData.Alerts array for grouped notifications.
type Alert struct {
	// Status is alert status: "firing" or "resolved"
	Status string

	// Labels are alert labels
	Labels map[string]string

	// Annotations are alert annotations
	Annotations map[string]string

	// StartsAt is when alert started firing
	StartsAt time.Time

	// EndsAt is when alert resolved (zero if still firing)
	EndsAt time.Time

	// GeneratorURL is Prometheus generator URL
	GeneratorURL string

	// Fingerprint is unique alert fingerprint
	Fingerprint string
}
