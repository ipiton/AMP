package publishing

import (
	"fmt"
	"time"
)

// CreateIncidentRequest represents request to create Rootly incident
type CreateIncidentRequest struct {
	Title        string                 `json:"title"`                    // Required: Incident title
	Description  string                 `json:"description"`              // Required: Incident description
	Severity     string                 `json:"severity"`                 // Required: critical, major, minor, low
	StartedAt    time.Time              `json:"started_at"`               // Required: Incident start time
	Tags         []string               `json:"tags,omitempty"`           // Optional: Tags (key:value format)
	CustomFields map[string]interface{} `json:"custom_fields,omitempty"` // Optional: Custom fields
}

// Validate validates CreateIncidentRequest
func (r *CreateIncidentRequest) Validate() error {
	if r.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len(r.Title) > 255 {
		return fmt.Errorf("title too long (max 255 chars, got %d)", len(r.Title))
	}
	if r.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(r.Description) > 10000 {
		return fmt.Errorf("description too long (max 10000 chars, got %d)", len(r.Description))
	}
	if !isValidSeverity(r.Severity) {
		return fmt.Errorf("invalid severity: %s (must be critical, major, minor, or low)", r.Severity)
	}
	if r.StartedAt.IsZero() {
		return fmt.Errorf("started_at is required")
	}
	if len(r.Tags) > 20 {
		return fmt.Errorf("too many tags (max 20, got %d)", len(r.Tags))
	}
	return nil
}

// isValidSeverity checks if severity is valid
func isValidSeverity(s string) bool {
	return s == "critical" || s == "major" || s == "minor" || s == "low"
}

// UpdateIncidentRequest represents request to update Rootly incident
type UpdateIncidentRequest struct {
	Description  string                 `json:"description,omitempty"`    // Optional: Updated description
	CustomFields map[string]interface{} `json:"custom_fields,omitempty"` // Optional: Updated custom fields
}

// Validate validates UpdateIncidentRequest
func (r *UpdateIncidentRequest) Validate() error {
	if r.Description != "" && len(r.Description) > 10000 {
		return fmt.Errorf("description too long (max 10000 chars, got %d)", len(r.Description))
	}
	return nil
}

// ResolveIncidentRequest represents request to resolve Rootly incident
type ResolveIncidentRequest struct {
	Summary string `json:"summary,omitempty"` // Optional: Resolution summary
}

// Validate validates ResolveIncidentRequest
func (r *ResolveIncidentRequest) Validate() error {
	if r.Summary != "" && len(r.Summary) > 1000 {
		return fmt.Errorf("summary too long (max 1000 chars, got %d)", len(r.Summary))
	}
	return nil
}

// IncidentResponse represents response from Rootly API
type IncidentResponse struct {
	Data struct {
		ID         string `json:"id"`   // Incident ID (e.g., "01HKXYZ...")
		Type       string `json:"type"` // "incidents"
		Attributes struct {
			Title      string     `json:"title"`
			Severity   string     `json:"severity"`
			StartedAt  time.Time  `json:"started_at"`
			Status     string     `json:"status"` // "started", "resolved"
			CreatedAt  time.Time  `json:"created_at"`
			UpdatedAt  time.Time  `json:"updated_at,omitempty"`
			ResolvedAt *time.Time `json:"resolved_at,omitempty"`
		} `json:"attributes"`
	} `json:"data"`
}

// GetID returns incident ID from response
func (r *IncidentResponse) GetID() string {
	return r.Data.ID
}

// GetStatus returns incident status from response
func (r *IncidentResponse) GetStatus() string {
	return r.Data.Attributes.Status
}

// IsResolved returns true if incident is resolved
func (r *IncidentResponse) IsResolved() bool {
	return r.Data.Attributes.Status == "resolved"
}
