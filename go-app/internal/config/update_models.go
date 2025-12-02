package config

import (
	"fmt"
	"time"
)

// ================================================================================
// Configuration Update Models
// ================================================================================
// This file contains data models for configuration update operations (TN-150).
// These models support:
// - Dynamic configuration updates via POST /api/v2/config
// - Dry-run validation mode
// - Partial updates (section filtering)
// - Configuration diff calculation
// - Version tracking and rollback
// - Audit logging
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// UpdateOptions specifies options for configuration update operation
type UpdateOptions struct {
	// Format specifies request body format: "json" (default) or "yaml"
	Format string

	// DryRun when true, validates configuration without applying changes
	// Useful for pre-deployment validation and CI/CD pipelines
	DryRun bool

	// Sections filters update to specific config sections (e.g., ["server", "database"])
	// Empty slice means update all sections
	// Supported sections: server, database, redis, llm, log, cache, lock, app, metrics, webhook
	Sections []string

	// Source identifies the origin of the update: "api", "gitops", "manual", "sighup"
	Source string

	// UserID identifies the user performing the update (from auth context)
	UserID string

	// Description provides human-readable description of changes
	Description string

	// Ticket references external tracking ticket (JIRA, GitHub issue, etc.)
	Ticket string
}

// UpdateResult represents the result of a configuration update operation
type UpdateResult struct {
	// Version is the new configuration version number (monotonic counter)
	// Zero if dry-run mode or update failed
	Version int64 `json:"version"`

	// Diff contains structured diff between old and new configuration
	// Shows added, modified, deleted fields with sanitized secrets
	Diff *ConfigDiff `json:"diff,omitempty"`

	// Applied indicates whether changes were actually applied
	// False for dry-run mode or validation failures
	Applied bool `json:"applied"`

	// RolledBack indicates whether changes were automatically rolled back
	// True if hot reload failed on critical components
	RolledBack bool `json:"rolled_back"`

	// ValidationErrors contains validation errors if validation failed
	// Empty if validation succeeded
	ValidationErrors []ValidationErrorDetail `json:"validation_errors,omitempty"`

	// ReloadErrors contains component reload errors
	// Empty if all reloads succeeded or if not applied
	ReloadErrors []ReloadError `json:"reload_errors,omitempty"`

	// Duration is the total operation duration
	Duration time.Duration `json:"duration"`
}

// ConfigDiff represents structured diff between two configurations
type ConfigDiff struct {
	// Added contains fields that were added (key: field path, value: new value)
	// Example: {"server.read_timeout": "30s"}
	Added map[string]interface{} `json:"added,omitempty"`

	// Modified contains fields that were changed
	// Maps field path to DiffEntry showing old and new values
	Modified map[string]DiffEntry `json:"modified,omitempty"`

	// Deleted contains field paths that were removed
	// Example: ["cache.enable_metrics"]
	Deleted []string `json:"deleted,omitempty"`

	// Affected lists components affected by this diff
	// Used to determine which components need hot reload
	// Example: ["database", "redis", "llm"]
	Affected []string `json:"affected_components,omitempty"`

	// IsCritical indicates whether diff contains critical changes
	// Critical changes: database host, redis addr, authentication settings
	IsCritical bool `json:"is_critical"`

	// Summary provides human-readable summary of changes
	// Example: "3 fields added, 5 modified, 1 deleted"
	Summary string `json:"summary"`
}

// DiffEntry represents a single field modification
type DiffEntry struct {
	// OldValue is the previous value (sanitized if secret)
	OldValue interface{} `json:"old_value"`

	// NewValue is the new value (sanitized if secret)
	NewValue interface{} `json:"new_value"`

	// Type indicates the field type for better formatting
	// Example: "duration", "integer", "boolean", "string"
	Type string `json:"type,omitempty"`
}

// ValidationError represents validation failure with detailed errors
type ValidationError struct {
	// Message is the high-level error message
	Message string `json:"message"`

	// Errors contains detailed validation errors per field
	Errors []ValidationErrorDetail `json:"errors"`

	// Phase indicates which validation phase failed
	// Values: "syntax", "schema", "type", "business", "cross_field"
	Phase string `json:"phase"`
}

// Error implements error interface
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %d validation error(s) in phase %s", e.Message, len(e.Errors), e.Phase)
}

// ValidationErrorDetail represents a single field validation error
type ValidationErrorDetail struct {
	// Field is the field path that failed validation
	// Example: "database.max_connections"
	Field string `json:"field"`

	// Message is the human-readable error message
	// Example: "must be greater than or equal to min_connections (5)"
	Message string `json:"message"`

	// Code is the error code for programmatic handling
	// Examples: "required", "invalid_type", "out_of_range", "invalid_format"
	Code string `json:"code"`

	// Value is the invalid value (sanitized if secret)
	Value interface{} `json:"value,omitempty"`

	// Constraint describes the validation constraint
	// Example: "min: 1, max: 65535" for port validation
	Constraint string `json:"constraint,omitempty"`
}

// ConflictError represents a concurrent update conflict
type ConflictError struct {
	// Message describes the conflict
	Message string `json:"message"`

	// CurrentVersion is the current configuration version
	CurrentVersion int64 `json:"current_version"`

	// ExpectedVersion is the version the client expected
	ExpectedVersion int64 `json:"expected_version,omitempty"`

	// LockHolder identifies who holds the lock (if applicable)
	LockHolder string `json:"lock_holder,omitempty"`
}

// Error implements error interface
func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict: %s (current version: %d)", e.Message, e.CurrentVersion)
}

// ReloadError represents component reload failure
type ReloadError struct {
	// Component is the component name that failed to reload
	Component string `json:"component"`

	// Error is the error message
	Error string `json:"error"`

	// Critical indicates whether this component is critical
	// Critical component failures trigger automatic rollback
	Critical bool `json:"critical"`

	// Duration is how long the reload attempt took
	Duration time.Duration `json:"duration"`
}

// ConfigVersion represents a historical configuration version
// Used for rollback and audit trail
type ConfigVersion struct {
	// Version is the unique version number (monotonic counter)
	Version int64 `json:"version"`

	// Config contains the full configuration as JSON
	// Secrets are stored encrypted or sanitized
	Config map[string]interface{} `json:"config"`

	// Hash is SHA256 hash of the configuration
	// Used for integrity checking and comparison
	Hash string `json:"hash"`

	// CreatedAt is when this version was created
	CreatedAt time.Time `json:"created_at"`

	// CreatedBy identifies who created this version
	// Format: "user:<user_id>" or "system:<source>"
	CreatedBy string `json:"created_by"`

	// Source identifies how this version was created
	// Values: "api", "gitops", "manual", "sighup", "rollback"
	Source string `json:"source"`

	// Description is a human-readable description of changes
	Description string `json:"description,omitempty"`

	// Ticket references external tracking ticket
	Ticket string `json:"ticket,omitempty"`

	// PreviousVersion is the version this was derived from
	// Zero for initial version
	PreviousVersion int64 `json:"previous_version,omitempty"`

	// Diff contains the diff from previous version (if available)
	Diff *ConfigDiff `json:"diff,omitempty"`
}

// AuditLogEntry represents a configuration audit log entry
type AuditLogEntry struct {
	// ID is the unique audit log entry ID
	ID int64 `json:"id"`

	// Version is the configuration version this entry relates to
	Version int64 `json:"version"`

	// Action is the action performed
	// Values: "create", "update", "rollback", "validate" (dry-run)
	Action string `json:"action"`

	// UserID identifies who performed the action
	UserID string `json:"user_id,omitempty"`

	// IPAddress is the client IP address
	IPAddress string `json:"ip_address,omitempty"`

	// UserAgent is the client User-Agent header
	UserAgent string `json:"user_agent,omitempty"`

	// Diff contains the configuration diff
	Diff *ConfigDiff `json:"diff,omitempty"`

	// Sections lists which sections were updated (if partial update)
	Sections []string `json:"sections,omitempty"`

	// DryRun indicates whether this was a dry-run operation
	DryRun bool `json:"dry_run"`

	// Success indicates whether the operation succeeded
	Success bool `json:"success"`

	// ErrorMessage contains error message if operation failed
	ErrorMessage string `json:"error_message,omitempty"`

	// Duration is how long the operation took (in milliseconds)
	DurationMS int64 `json:"duration_ms"`

	// CreatedAt is when this audit log entry was created
	CreatedAt time.Time `json:"created_at"`
}

// ================================================================================
// Helper Functions
// ================================================================================

// NewUpdateOptions creates UpdateOptions with defaults
func NewUpdateOptions() UpdateOptions {
	return UpdateOptions{
		Format:   "json",
		DryRun:   false,
		Sections: nil,
		Source:   "api",
	}
}

// IsEmpty checks if UpdateOptions represents default/empty state
func (opts UpdateOptions) IsEmpty() bool {
	return opts.Format == "" && !opts.DryRun && len(opts.Sections) == 0
}

// Validate validates UpdateOptions
func (opts UpdateOptions) Validate() error {
	// Validate format
	if opts.Format != "" && opts.Format != "json" && opts.Format != "yaml" {
		return fmt.Errorf("invalid format: %s (supported: json, yaml)", opts.Format)
	}

	// Validate source
	validSources := map[string]bool{
		"api": true, "gitops": true, "manual": true, "sighup": true,
	}
	if opts.Source != "" && !validSources[opts.Source] {
		return fmt.Errorf("invalid source: %s (supported: api, gitops, manual, sighup)", opts.Source)
	}

	return nil
}

// HasSections returns true if specific sections are targeted
func (opts UpdateOptions) HasSections() bool {
	return len(opts.Sections) > 0
}

// SectionsString returns comma-separated list of sections
func (opts UpdateOptions) SectionsString() string {
	if len(opts.Sections) == 0 {
		return "all"
	}
	return fmt.Sprintf("%v", opts.Sections)
}

// NewConfigDiff creates empty ConfigDiff
func NewConfigDiff() *ConfigDiff {
	return &ConfigDiff{
		Added:    make(map[string]interface{}),
		Modified: make(map[string]DiffEntry),
		Deleted:  make([]string, 0),
		Affected: make([]string, 0),
	}
}

// IsEmpty checks if diff is empty (no changes)
func (diff *ConfigDiff) IsEmpty() bool {
	return len(diff.Added) == 0 && len(diff.Modified) == 0 && len(diff.Deleted) == 0
}

// ChangeCount returns total number of changes
func (diff *ConfigDiff) ChangeCount() int {
	return len(diff.Added) + len(diff.Modified) + len(diff.Deleted)
}

// GenerateSummary generates human-readable summary
func (diff *ConfigDiff) GenerateSummary() string {
	if diff.IsEmpty() {
		return "No changes"
	}

	added := len(diff.Added)
	modified := len(diff.Modified)
	deleted := len(diff.Deleted)

	summary := ""
	if added > 0 {
		summary += fmt.Sprintf("%d added", added)
	}
	if modified > 0 {
		if summary != "" {
			summary += ", "
		}
		summary += fmt.Sprintf("%d modified", modified)
	}
	if deleted > 0 {
		if summary != "" {
			summary += ", "
		}
		summary += fmt.Sprintf("%d deleted", deleted)
	}

	return summary
}

// NewUpdateResult creates empty UpdateResult
func NewUpdateResult() *UpdateResult {
	return &UpdateResult{
		Version:          0,
		Diff:             NewConfigDiff(),
		Applied:          false,
		RolledBack:       false,
		ValidationErrors: make([]ValidationErrorDetail, 0),
		ReloadErrors:     make([]ReloadError, 0),
	}
}

// IsSuccess checks if update was successful
func (r *UpdateResult) IsSuccess() bool {
	return len(r.ValidationErrors) == 0 && len(r.ReloadErrors) == 0 && !r.RolledBack
}

// HasValidationErrors checks if there are validation errors
func (r *UpdateResult) HasValidationErrors() bool {
	return len(r.ValidationErrors) > 0
}

// HasReloadErrors checks if there are reload errors
func (r *UpdateResult) HasReloadErrors() bool {
	return len(r.ReloadErrors) > 0
}

// HasCriticalReloadErrors checks if there are critical reload errors
func (r *UpdateResult) HasCriticalReloadErrors() bool {
	for _, e := range r.ReloadErrors {
		if e.Critical {
			return true
		}
	}
	return false
}
