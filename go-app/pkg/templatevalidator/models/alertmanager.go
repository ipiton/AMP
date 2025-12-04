package models

import "time"

// ================================================================================
// TN-156: Template Validator - Alertmanager Data Model
// ================================================================================
// Alertmanager TemplateData schema for semantic validation.
//
// This schema defines the data model available in Alertmanager templates.
// Semantic validator uses this to check if template variables exist.
//
// Based on: Alertmanager v0.25+ TemplateData structure
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// TemplateDataSchema defines the Alertmanager template data structure
//
// This matches the data structure provided by Alertmanager to templates.
// All templates receive this data when executed.
//
// Example template usage:
//
//	{{ .Status }}                    - "firing" or "resolved"
//	{{ .Labels.alertname }}          - Alert name
//	{{ .Annotations.summary }}       - Alert summary
//	{{ .StartsAt | date }}           - Alert start time
//
// Reference: https://prometheus.io/docs/alerting/latest/notifications/
type TemplateDataSchema struct {
	// Status is the alert status
	//
	// Values: "firing" | "resolved"
	// Required: yes
	// Type: string
	Status FieldSchema

	// Labels are alert labels
	//
	// Example: {"alertname": "HighCPU", "severity": "critical"}
	// Required: yes
	// Type: map[string]string
	Labels FieldSchema

	// Annotations are alert annotations
	//
	// Example: {"summary": "CPU usage high", "description": "..."}
	// Required: yes
	// Type: map[string]string
	Annotations FieldSchema

	// StartsAt is when the alert started
	//
	// Required: yes
	// Type: time.Time
	StartsAt FieldSchema

	// EndsAt is when the alert ended (may be zero for active alerts)
	//
	// Required: no (may be zero time for firing alerts)
	// Type: time.Time
	EndsAt FieldSchema

	// GeneratorURL is the Prometheus/Alertmanager URL
	//
	// Example: "http://prometheus:9090/graph?..."
	// Required: no (may be empty)
	// Type: string
	GeneratorURL FieldSchema

	// Fingerprint is the unique alert identifier
	//
	// Example: "abc123..."
	// Required: yes
	// Type: string
	Fingerprint FieldSchema
}

// FieldSchema defines a field in the data model
type FieldSchema struct {
	// Name is the field name
	Name string

	// Type is the Go type
	//
	// Values: "string", "time.Time", "map[string]string"
	Type string

	// Required indicates if field is always present
	//
	// true: field always exists (no nil check needed)
	// false: field may be nil/zero (nil check recommended)
	Required bool

	// Description is the field description
	Description string

	// Example is an example value
	Example string
}

// ================================================================================

// AlertmanagerSchema returns the standard Alertmanager template data schema
//
// Returns TemplateDataSchema with all fields defined.
//
// Use this to validate template variable references.
func AlertmanagerSchema() *TemplateDataSchema {
	return &TemplateDataSchema{
		Status: FieldSchema{
			Name:        "Status",
			Type:        "string",
			Required:    true,
			Description: "Alert status: 'firing' or 'resolved'",
			Example:     "firing",
		},
		Labels: FieldSchema{
			Name:        "Labels",
			Type:        "map[string]string",
			Required:    true,
			Description: "Alert labels (key-value pairs)",
			Example:     `{"alertname": "HighCPU", "severity": "critical"}`,
		},
		Annotations: FieldSchema{
			Name:        "Annotations",
			Type:        "map[string]string",
			Required:    true,
			Description: "Alert annotations (key-value pairs)",
			Example:     `{"summary": "CPU high", "description": "CPU usage at 95%"}`,
		},
		StartsAt: FieldSchema{
			Name:        "StartsAt",
			Type:        "time.Time",
			Required:    true,
			Description: "Alert start time",
			Example:     "2025-11-25T10:00:00Z",
		},
		EndsAt: FieldSchema{
			Name:        "EndsAt",
			Type:        "time.Time",
			Required:    false, // May be zero for firing alerts
			Description: "Alert end time (may be zero for active alerts)",
			Example:     "2025-11-25T11:00:00Z",
		},
		GeneratorURL: FieldSchema{
			Name:        "GeneratorURL",
			Type:        "string",
			Required:    false, // May be empty
			Description: "Prometheus/Alertmanager URL",
			Example:     "http://prometheus:9090/graph?...",
		},
		Fingerprint: FieldSchema{
			Name:        "Fingerprint",
			Type:        "string",
			Required:    true,
			Description: "Unique alert identifier",
			Example:     "abc123def456...",
		},
	}
}

// ================================================================================

// GetField returns field schema by name
//
// Returns FieldSchema and true if field exists, or empty FieldSchema and false if not.
//
// Example:
//
//	field, ok := schema.GetField("Status")
//	if ok {
//	    fmt.Println(field.Description)
//	}
func (s *TemplateDataSchema) GetField(name string) (FieldSchema, bool) {
	switch name {
	case "Status":
		return s.Status, true
	case "Labels":
		return s.Labels, true
	case "Annotations":
		return s.Annotations, true
	case "StartsAt":
		return s.StartsAt, true
	case "EndsAt":
		return s.EndsAt, true
	case "GeneratorURL":
		return s.GeneratorURL, true
	case "Fingerprint":
		return s.Fingerprint, true
	default:
		return FieldSchema{}, false
	}
}

// AllFields returns all field names
//
// Returns slice of all field names in schema.
func (s *TemplateDataSchema) AllFields() []string {
	return []string{
		"Status",
		"Labels",
		"Annotations",
		"StartsAt",
		"EndsAt",
		"GeneratorURL",
		"Fingerprint",
	}
}

// IsMapField returns true if field is a map type
//
// Returns true for Labels and Annotations (map[string]string).
func (s *TemplateDataSchema) IsMapField(name string) bool {
	field, ok := s.GetField(name)
	if !ok {
		return false
	}
	return field.Type == "map[string]string"
}

// IsTimeField returns true if field is a time.Time type
//
// Returns true for StartsAt and EndsAt.
func (s *TemplateDataSchema) IsTimeField(name string) bool {
	field, ok := s.GetField(name)
	if !ok {
		return false
	}
	return field.Type == "time.Time"
}

// IsRequired returns true if field is required (always present)
func (s *TemplateDataSchema) IsRequired(name string) bool {
	field, ok := s.GetField(name)
	if !ok {
		return false
	}
	return field.Required
}

// ================================================================================

// MockTemplateData returns mock data for validation
//
// Returns mock Alertmanager data for template execution.
// Used by validators to test templates with realistic data.
func MockTemplateData() map[string]interface{} {
	return map[string]interface{}{
		"Status": "firing",
		"Labels": map[string]string{
			"alertname": "TestAlert",
			"severity":  "critical",
			"instance":  "localhost:9090",
			"job":       "prometheus",
		},
		"Annotations": map[string]string{
			"summary":     "Test alert summary",
			"description": "This is a test alert for validation",
		},
		"StartsAt":     time.Now(),
		"EndsAt":       time.Time{}, // Zero time for firing alert
		"GeneratorURL": "http://prometheus:9090/graph?...",
		"Fingerprint":  "abc123def456",
	}
}

// ================================================================================
