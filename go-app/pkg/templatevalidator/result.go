package templatevalidator

import (
	"fmt"
	"time"
)

// ================================================================================
// TN-156: Template Validator - Result Models
// ================================================================================
// Validation result structures and error types.
//
// Features:
// - Structured validation results (errors, warnings, suggestions)
// - Line:column error reporting
// - Severity levels (critical, high, medium, low)
// - Performance metrics
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// ValidationResult contains validation results
//
// The result includes all errors, warnings, and suggestions found during validation.
// Use Valid field to check if template passed validation.
//
// Example:
//
//	result := validator.Validate(ctx, content, opts)
//	if !result.Valid {
//	    for _, err := range result.Errors {
//	        fmt.Printf("Error at line %d: %s\n", err.Line, err.Message)
//	    }
//	}
type ValidationResult struct {
	// Valid is true if template has no blocking errors
	//
	// In ModeStrict: Valid = no errors and no warnings
	// In ModeLenient: Valid = no errors (warnings allowed)
	// In ModePermissive: Valid = no critical errors
	Valid bool `json:"valid"`

	// Errors are blocking validation errors
	//
	// These prevent template from being used.
	// Examples: syntax errors, security vulnerabilities
	Errors []ValidationError `json:"errors"`

	// Warnings are non-blocking issues
	//
	// Template can still be used, but improvements recommended.
	// Examples: missing best practices, potential issues
	Warnings []ValidationWarning `json:"warnings"`

	// Info are informational messages
	//
	// Non-actionable information about template.
	// Examples: functions used, variables referenced
	Info []ValidationInfo `json:"info"`

	// Suggestions are improvement suggestions
	//
	// Recommendations to improve template quality.
	// Examples: refactoring suggestions, style improvements
	Suggestions []ValidationSuggestion `json:"suggestions"`

	// Metrics contains performance metrics
	//
	// Duration, template size, phase durations, etc.
	Metrics ValidationMetrics `json:"metrics"`
}

// ================================================================================

// ValidationError represents a blocking validation error
//
// Errors prevent template from passing validation.
// They must be fixed before template can be used.
//
// Example:
//
//	err := ValidationError{
//	    Phase:      "syntax",
//	    Severity:   "critical",
//	    Line:       15,
//	    Column:     24,
//	    Message:    "unknown function: toUpperCase",
//	    Suggestion: "Did you mean 'toUpper'?",
//	    Code:       "unknown-function",
//	}
type ValidationError struct {
	// Phase is the validation phase (syntax, semantic, security, best_practices)
	Phase string `json:"phase"`

	// Severity is error severity (critical, high, medium, low)
	//
	// - critical: must fix immediately (syntax errors, security vulnerabilities)
	// - high: should fix soon (semantic errors, major issues)
	// - medium: should fix eventually (best practice violations)
	// - low: nice to fix (minor issues)
	Severity string `json:"severity"`

	// Line is the line number (1-indexed, 0 = unknown)
	Line int `json:"line"`

	// Column is the column number (1-indexed, 0 = unknown)
	Column int `json:"column"`

	// Message is the error message
	//
	// Clear, actionable description of the error.
	// Example: "unknown function: toUpperCase"
	Message string `json:"message"`

	// Suggestion is an actionable suggestion to fix the error
	//
	// How to fix the error.
	// Example: "Did you mean 'toUpper'?"
	Suggestion string `json:"suggestion,omitempty"`

	// Code is the error code (e.g., "syntax-error", "unknown-function")
	//
	// Machine-readable error identifier.
	// Used for filtering, grouping, and documentation lookup.
	Code string `json:"code"`
}

// Error implements the error interface
func (e ValidationError) Error() string {
	if e.Line > 0 && e.Column > 0 {
		return e.Message
	}
	return e.Message
}

// Location returns formatted location string
//
// Returns "line:column" or empty string if location unknown.
func (e ValidationError) Location() string {
	if e.Line > 0 && e.Column > 0 {
		return fmt.Sprintf("%d:%d", e.Line, e.Column)
	}
	if e.Line > 0 {
		return fmt.Sprintf("%d", e.Line)
	}
	return ""
}

// IsCritical returns true if severity is critical
func (e ValidationError) IsCritical() bool {
	return e.Severity == "critical"
}

// IsHigh returns true if severity is high
func (e ValidationError) IsHigh() bool {
	return e.Severity == "high"
}

// ================================================================================

// ValidationWarning represents a non-blocking warning
//
// Warnings don't prevent template from passing validation (in ModeLenient).
// They indicate potential issues or best practice violations.
//
// Example:
//
//	warning := ValidationWarning{
//	    Phase:      "semantic",
//	    Line:       22,
//	    Column:     10,
//	    Message:    "Field 'severity' not guaranteed to exist in Labels",
//	    Suggestion: "Use '| default \"unknown\"' to provide fallback",
//	    Code:       "optional-field",
//	}
type ValidationWarning struct {
	// Phase is the validation phase
	Phase string `json:"phase"`

	// Line is the line number (1-indexed, 0 = unknown)
	Line int `json:"line"`

	// Column is the column number (1-indexed, 0 = unknown)
	Column int `json:"column"`

	// Message is the warning message
	Message string `json:"message"`

	// Suggestion is an actionable suggestion
	Suggestion string `json:"suggestion,omitempty"`

	// Code is the warning code
	Code string `json:"code"`
}

// Location returns formatted location string
func (w ValidationWarning) Location() string {
	if w.Line > 0 && w.Column > 0 {
		return fmt.Sprintf("%d:%d", w.Line, w.Column)
	}
	if w.Line > 0 {
		return fmt.Sprintf("%d", w.Line)
	}
	return ""
}

// ================================================================================

// ValidationInfo represents an informational message
//
// Info messages provide non-actionable information about template.
//
// Example:
//
//	info := ValidationInfo{
//	    Message: "Template uses 5 functions: toUpper, default, range, if, end",
//	}
type ValidationInfo struct {
	// Message is the informational message
	Message string `json:"message"`
}

// ================================================================================

// ValidationSuggestion represents an improvement suggestion
//
// Suggestions recommend improvements to template quality.
//
// Example:
//
//	suggestion := ValidationSuggestion{
//	    Phase:      "best_practices",
//	    Line:       45,
//	    Column:     5,
//	    Message:    "Nested loops detected",
//	    Suggestion: "Consider refactoring for better performance",
//	}
type ValidationSuggestion struct {
	// Phase is the validation phase
	Phase string `json:"phase"`

	// Line is the line number (1-indexed, 0 = unknown)
	Line int `json:"line"`

	// Column is the column number (1-indexed, 0 = unknown)
	Column int `json:"column"`

	// Message is the suggestion message
	Message string `json:"message"`

	// Suggestion is the actionable suggestion
	Suggestion string `json:"suggestion"`
}

// Location returns formatted location string
func (s ValidationSuggestion) Location() string {
	if s.Line > 0 && s.Column > 0 {
		return fmt.Sprintf("%d:%d", s.Line, s.Column)
	}
	if s.Line > 0 {
		return fmt.Sprintf("%d", s.Line)
	}
	return ""
}

// ================================================================================

// ValidationMetrics contains performance metrics
//
// Metrics help track validation performance and template complexity.
//
// Example:
//
//	metrics := ValidationMetrics{
//	    Duration:       15 * time.Millisecond,
//	    PhaseDurations: map[string]time.Duration{
//	        "syntax":   5 * time.Millisecond,
//	        "semantic": 3 * time.Millisecond,
//	    },
//	    TemplateSize:   1024,
//	    FunctionsFound: 5,
//	    VariablesFound: 8,
//	}
type ValidationMetrics struct {
	// Duration is total validation duration
	Duration time.Duration `json:"duration_ms"`

	// PhaseDurations is duration per validation phase
	//
	// Map[phase_name] = duration
	// Example: {"syntax": 5ms, "semantic": 3ms}
	PhaseDurations map[string]time.Duration `json:"phase_durations,omitempty"`

	// TemplateSize is template size in bytes
	TemplateSize int `json:"template_size_bytes"`

	// FunctionsFound is count of functions found in template
	FunctionsFound int `json:"functions_found"`

	// VariablesFound is count of variables found in template
	VariablesFound int `json:"variables_found"`
}

// DurationMs returns duration in milliseconds
func (m ValidationMetrics) DurationMs() float64 {
	return float64(m.Duration) / float64(time.Millisecond)
}

// PhaseDurationMs returns phase duration in milliseconds
func (m ValidationMetrics) PhaseDurationMs(phase string) float64 {
	if d, ok := m.PhaseDurations[phase]; ok {
		return float64(d) / float64(time.Millisecond)
	}
	return 0
}

// ================================================================================

// Summary returns a human-readable summary of validation result
//
// Example output:
//
//	"✅ Valid (0 errors, 2 warnings, 1 suggestion)"
//	"❌ Invalid (3 errors, 1 warning)"
func (r ValidationResult) Summary() string {
	var status string
	if r.Valid {
		status = "✅ Valid"
	} else {
		status = "❌ Invalid"
	}

	return fmt.Sprintf("%s (%d errors, %d warnings, %d suggestions)",
		status,
		len(r.Errors),
		len(r.Warnings),
		len(r.Suggestions),
	)
}

// HasErrors returns true if result has errors
func (r ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasWarnings returns true if result has warnings
func (r ValidationResult) HasWarnings() bool {
	return len(r.Warnings) > 0
}

// HasSuggestions returns true if result has suggestions
func (r ValidationResult) HasSuggestions() bool {
	return len(r.Suggestions) > 0
}

// ErrorCount returns number of errors
func (r ValidationResult) ErrorCount() int {
	return len(r.Errors)
}

// WarningCount returns number of warnings
func (r ValidationResult) WarningCount() int {
	return len(r.Warnings)
}

// SuggestionCount returns number of suggestions
func (r ValidationResult) SuggestionCount() int {
	return len(r.Suggestions)
}

// CriticalErrorCount returns number of critical errors
func (r ValidationResult) CriticalErrorCount() int {
	count := 0
	for _, err := range r.Errors {
		if err.IsCritical() {
			count++
		}
	}
	return count
}

// HighErrorCount returns number of high severity errors
func (r ValidationResult) HighErrorCount() int {
	count := 0
	for _, err := range r.Errors {
		if err.IsHigh() {
			count++
		}
	}
	return count
}

// ================================================================================
