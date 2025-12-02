package domain

import (
	"time"
)

// ================================================================================
// TN-155: Template API (CRUD) - Domain Models
// ================================================================================
// Domain models for notification templates.
//
// Features:
// - Full CRUD support
// - Version control
// - Type safety with enums
// - JSON serialization
// - Validation tags
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// TemplateType represents the type of notification template
type TemplateType string

const (
	// TemplateTypeSlack is for Slack notifications
	TemplateTypeSlack TemplateType = "slack"

	// TemplateTypePagerDuty is for PagerDuty incident creation
	TemplateTypePagerDuty TemplateType = "pagerduty"

	// TemplateTypeEmail is for email notifications
	TemplateTypeEmail TemplateType = "email"

	// TemplateTypeWebhook is for generic webhook calls
	TemplateTypeWebhook TemplateType = "webhook"

	// TemplateTypeGeneric is for custom/generic templates
	TemplateTypeGeneric TemplateType = "generic"
)

// Valid returns true if the template type is valid
func (t TemplateType) Valid() bool {
	switch t {
	case TemplateTypeSlack, TemplateTypePagerDuty, TemplateTypeEmail,
		 TemplateTypeWebhook, TemplateTypeGeneric:
		return true
	default:
		return false
	}
}

// String returns the string representation
func (t TemplateType) String() string {
	return string(t)
}

// AllTemplateTypes returns all valid template types
func AllTemplateTypes() []TemplateType {
	return []TemplateType{
		TemplateTypeSlack,
		TemplateTypePagerDuty,
		TemplateTypeEmail,
		TemplateTypeWebhook,
		TemplateTypeGeneric,
	}
}

// ================================================================================

// Template represents a notification template
type Template struct {
	// ID is the unique identifier (UUID)
	ID string `json:"id" db:"id"`

	// Name is the unique template name (lowercase alphanumeric + underscore)
	Name string `json:"name" db:"name" validate:"required,min=3,max=64,lowercase,alphanum_underscore"`

	// Type is the template type (slack, pagerduty, email, webhook, generic)
	Type TemplateType `json:"type" db:"type" validate:"required,oneof=slack pagerduty email webhook generic"`

	// Content is the Go text/template content (max 64KB)
	Content string `json:"content" db:"content" validate:"required,max=65536"`

	// Description is an optional description (max 500 chars)
	Description string `json:"description,omitempty" db:"description" validate:"max=500"`

	// Metadata is optional JSON metadata (author, tags, etc.)
	Metadata map[string]interface{} `json:"metadata,omitempty" db:"metadata"`

	// Version is the current version number (auto-increment on update)
	Version int `json:"version" db:"version" validate:"min=1"`

	// CreatedAt is the creation timestamp
	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// UpdatedAt is the last update timestamp
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// CreatedBy is the user who created the template
	CreatedBy string `json:"created_by,omitempty" db:"created_by"`

	// UpdatedBy is the user who last updated the template
	UpdatedBy string `json:"updated_by,omitempty" db:"updated_by"`

	// DeletedAt is the soft delete timestamp (nil if active)
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

// IsDeleted returns true if the template is soft-deleted
func (t *Template) IsDeleted() bool {
	return t.DeletedAt != nil
}

// IsActive returns true if the template is active (not deleted)
func (t *Template) IsActive() bool {
	return t.DeletedAt == nil
}

// ================================================================================

// TemplateVersion represents a historical version of a template
type TemplateVersion struct {
	// ID is the unique identifier (UUID)
	ID string `json:"id" db:"id"`

	// TemplateID is the foreign key to templates table
	TemplateID string `json:"template_id" db:"template_id"`

	// Version is the version number
	Version int `json:"version" db:"version" validate:"min=1"`

	// Content is the historical template content
	Content string `json:"content" db:"content" validate:"required"`

	// Description is the historical description
	Description string `json:"description,omitempty" db:"description"`

	// Metadata is the historical metadata
	Metadata map[string]interface{} `json:"metadata,omitempty" db:"metadata"`

	// CreatedAt is when this version was created
	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// CreatedBy is who created this version
	CreatedBy string `json:"created_by,omitempty" db:"created_by"`

	// ChangeSummary is an optional description of what changed
	ChangeSummary string `json:"change_summary,omitempty" db:"change_summary" validate:"max=1000"`
}

// ================================================================================

// ListFilters contains filters for listing templates
type ListFilters struct {
	// Type filters by template type
	Type TemplateType `json:"type"`

	// Tag filters by metadata tag
	Tag string `json:"tag"`

	// Search performs full-text search in name/description
	Search string `json:"search"`

	// IncludeDeleted includes soft-deleted templates
	IncludeDeleted bool `json:"include_deleted"`

	// Limit is the page size (default 50, max 200)
	Limit int `json:"limit" validate:"min=1,max=200"`

	// Offset is the pagination offset
	Offset int `json:"offset" validate:"min=0"`

	// Sort is the sort field (name, created_at, updated_at)
	Sort string `json:"sort" validate:"omitempty,oneof=name created_at updated_at"`

	// Order is the sort order (asc, desc)
	Order string `json:"order" validate:"omitempty,oneof=asc desc"`
}

// DefaultListFilters returns default list filters
func DefaultListFilters() ListFilters {
	return ListFilters{
		Limit:          50,
		Offset:         0,
		Sort:           "name",
		Order:          "asc",
		IncludeDeleted: false,
	}
}

// ================================================================================

// VersionFilters contains filters for listing template versions
type VersionFilters struct {
	// Limit is the page size (default 20, max 100)
	Limit int `json:"limit" validate:"min=1,max=100"`

	// Offset is the pagination offset
	Offset int `json:"offset" validate:"min=0"`
}

// DefaultVersionFilters returns default version filters
func DefaultVersionFilters() VersionFilters {
	return VersionFilters{
		Limit:  20,
		Offset: 0,
	}
}

// ================================================================================

// DeleteOptions contains options for delete operation
type DeleteOptions struct {
	// HardDelete performs physical deletion (default: false = soft delete)
	HardDelete bool `json:"hard_delete"`

	// Force allows deletion even if template is in use
	Force bool `json:"force"`
}

// DefaultDeleteOptions returns default delete options
func DefaultDeleteOptions() DeleteOptions {
	return DeleteOptions{
		HardDelete: false,
		Force:      false,
	}
}

// ================================================================================

// TemplateSummary represents a lightweight template without content
// Used for list operations to reduce payload size
type TemplateSummary struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        TemplateType `json:"type"`
	Description string       `json:"description,omitempty"`
	Version     int          `json:"version"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	CreatedBy   string       `json:"created_by,omitempty"`
}

// ToSummary converts a Template to TemplateSummary (without content)
func (t *Template) ToSummary() TemplateSummary {
	return TemplateSummary{
		ID:          t.ID,
		Name:        t.Name,
		Type:        t.Type,
		Description: t.Description,
		Version:     t.Version,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		CreatedBy:   t.CreatedBy,
	}
}

// ================================================================================

// PaginationInfo represents pagination metadata
type PaginationInfo struct {
	Total   int  `json:"total"`    // Total number of items
	Limit   int  `json:"limit"`    // Page size
	Offset  int  `json:"offset"`   // Current offset
	HasMore bool `json:"has_more"` // True if more items exist
}

// NewPaginationInfo creates pagination info from total count and filters
func NewPaginationInfo(total int, filters ListFilters) PaginationInfo {
	return PaginationInfo{
		Total:   total,
		Limit:   filters.Limit,
		Offset:  filters.Offset,
		HasMore: filters.Offset+filters.Limit < total,
	}
}

// ================================================================================

// TemplateList represents a paginated list of templates
type TemplateList struct {
	Templates  []TemplateSummary `json:"templates"`
	Pagination PaginationInfo    `json:"pagination"`
}

// ================================================================================

// VersionList represents a paginated list of template versions
type VersionList struct {
	Versions   []TemplateVersion `json:"versions"`
	Total      int               `json:"total"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
}

// ================================================================================

// TemplateDiff represents the difference between two template versions
type TemplateDiff struct {
	TemplateName string `json:"template_name"`
	FromVersion  int    `json:"from_version"`
	ToVersion    int    `json:"to_version"`
	Diff         string `json:"diff"` // Unified diff format
	ChangedAt    time.Time `json:"changed_at"`
	ChangedBy    string `json:"changed_by,omitempty"`
}

// ================================================================================

// TemplateStats represents aggregate statistics about templates
type TemplateStats struct {
	// TotalTemplates is the total number of active templates
	TotalTemplates int `json:"total_templates"`

	// ByType is a breakdown by template type
	ByType map[string]int `json:"by_type"`

	// MostUsed is a list of most frequently used templates
	MostUsed []TemplateUsageStat `json:"most_used,omitempty"`

	// ValidationErrorRate is the % of templates with validation errors
	ValidationErrorRate float64 `json:"validation_error_rate"`

	// AverageContentSize is the average template size in bytes
	AverageContentSize int `json:"average_content_size"`
}

// TemplateUsageStat represents usage statistics for a single template
type TemplateUsageStat struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	UsageCount int    `json:"usage_count"`
}

// ================================================================================

// ValidationError represents a template validation error
type ValidationError struct {
	// Line is the line number where error occurred
	Line int `json:"line"`

	// Column is the column number where error occurred
	Column int `json:"column"`

	// Message is the error message
	Message string `json:"message"`

	// Suggestion is an optional suggestion to fix the error
	Suggestion string `json:"suggestion,omitempty"`
}

// ValidationResult represents the result of template validation
type ValidationResult struct {
	// Valid is true if template is valid
	Valid bool `json:"valid"`

	// SyntaxErrors is a list of syntax errors
	SyntaxErrors []ValidationError `json:"syntax_errors"`

	// Warnings is a list of non-fatal warnings
	Warnings []string `json:"warnings"`

	// FunctionsUsed is a list of template functions used
	FunctionsUsed []string `json:"functions_used"`

	// VariablesUsed is a list of variables referenced
	VariablesUsed []string `json:"variables_used"`

	// RenderedOutput is the rendered output (if test data provided)
	RenderedOutput string `json:"rendered_output,omitempty"`
}

// ================================================================================

// RollbackRequest represents a request to rollback to a specific version
type RollbackRequest struct {
	// Version is the target version to rollback to
	Version int `json:"version" validate:"required,min=1"`

	// Reason is the reason for rollback (for audit trail)
	Reason string `json:"reason" validate:"required,max=500"`
}

// ================================================================================
