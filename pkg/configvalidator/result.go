package configvalidator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ================================================================================
// Validation Result Models
// ================================================================================
// Comprehensive validation result types (TN-151).
//
// Result contains:
// - Valid: Overall validity flag
// - Errors: Critical issues (block deployment)
// - Warnings: Potential problems (should be fixed)
// - Info: Recommendations and best practices
// - Suggestions: Actionable improvements
//
// Performance Target: Result construction < 1ms
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// Result represents comprehensive validation result
type Result struct {
	// Valid indicates if configuration is valid
	// In StrictMode: true only if no errors AND no warnings
	// In LenientMode: true if no errors (warnings OK)
	// In PermissiveMode: always true
	Valid bool `json:"valid"`

	// Errors are critical validation errors that block deployment
	Errors []Error `json:"errors,omitempty"`

	// Warnings are potential problems that should be fixed
	// May block in StrictMode
	Warnings []Warning `json:"warnings,omitempty"`

	// Info are informational messages and recommendations
	Info []Info `json:"info,omitempty"`

	// Suggestions are actionable improvements
	Suggestions []Suggestion `json:"suggestions,omitempty"`

	// FilePath is the validated file path
	FilePath string `json:"file_path,omitempty"`

	// Duration is validation duration
	Duration time.Duration `json:"-"`

	// DurationMS is duration in milliseconds (for JSON)
	DurationMS int64 `json:"duration_ms"`

	// ValidatedAt is timestamp
	ValidatedAt time.Time `json:"validated_at"`
}

// Error represents a critical validation error
type Error struct {
	// Type categorizes error (e.g., "syntax", "reference", "type", "security")
	Type string `json:"type"`

	// Code is unique error code (e.g., "E001", "E102")
	Code string `json:"code"`

	// Message is human-readable error message
	Message string `json:"message"`

	// Location is error location in file
	Location Location `json:"location"`

	// Context is surrounding code context (3 lines before/after)
	Context string `json:"context,omitempty"`

	// Suggestion is actionable fix suggestion
	Suggestion string `json:"suggestion,omitempty"`

	// DocsURL is link to relevant documentation
	DocsURL string `json:"docs_url,omitempty"`

	// Related is list of related errors (optional)
	Related []string `json:"related,omitempty"`
}

// Warning represents a potential problem
type Warning struct {
	// Type categorizes warning (e.g., "best_practice", "performance", "security")
	Type string `json:"type"`

	// Code is unique warning code (e.g., "W100", "W201")
	Code string `json:"code"`

	// Message is human-readable warning message
	Message string `json:"message"`

	// Location is warning location in file
	Location Location `json:"location"`

	// Suggestion is recommendation for improvement
	Suggestion string `json:"suggestion,omitempty"`

	// DocsURL is link to relevant documentation
	DocsURL string `json:"docs_url,omitempty"`
}

// Info represents informational message or recommendation
type Info struct {
	// Type categorizes info (e.g., "best_practice", "optimization", "tip")
	Type string `json:"type"`

	// Code is unique info code (e.g., "I001", "I050")
	Code string `json:"code"`

	// Message is human-readable informational message
	Message string `json:"message"`

	// Location is optional location in file
	Location Location `json:"location,omitempty"`

	// DocsURL is link to relevant documentation
	DocsURL string `json:"docs_url,omitempty"`
}

// Suggestion represents actionable improvement
type Suggestion struct {
	// Type categorizes suggestion (e.g., "refactor", "optimize", "clarify")
	Type string `json:"type"`

	// Code is unique suggestion code (e.g., "S001", "S025")
	Code string `json:"code"`

	// Message is human-readable suggestion message
	Message string `json:"message"`

	// Location is location in file
	Location Location `json:"location,omitempty"`

	// Before is current code snippet (optional)
	Before string `json:"before,omitempty"`

	// After is suggested code snippet (optional)
	After string `json:"after,omitempty"`

	// DocsURL is link to relevant documentation
	DocsURL string `json:"docs_url,omitempty"`
}

// Location represents location in configuration file
type Location struct {
	// File is file path (empty if same as result.FilePath)
	File string `json:"file,omitempty"`

	// Line is line number (1-based)
	Line int `json:"line"`

	// Column is column number (1-based, optional)
	Column int `json:"column,omitempty"`

	// Field is field path (e.g., "route.receiver", "receivers[0].name")
	Field string `json:"field,omitempty"`

	// Section is config section (e.g., "route", "receivers", "inhibit_rules")
	Section string `json:"section,omitempty"`
}

// String returns human-readable location string
func (l Location) String() string {
	parts := make([]string, 0, 3)

	if l.File != "" {
		parts = append(parts, l.File)
	}

	if l.Line > 0 {
		if l.Column > 0 {
			parts = append(parts, fmt.Sprintf("%d:%d", l.Line, l.Column))
		} else {
			parts = append(parts, fmt.Sprintf("%d", l.Line))
		}
	}

	if l.Field != "" {
		parts = append(parts, fmt.Sprintf("[%s]", l.Field))
	}

	if len(parts) == 0 {
		return "<unknown>"
	}

	return strings.Join(parts, ":")
}

// NewResult creates a new empty validation result
func NewResult() *Result {
	return &Result{
		Valid:       true, // Start optimistic
		Errors:      make([]Error, 0),
		Warnings:    make([]Warning, 0),
		Info:        make([]Info, 0),
		Suggestions: make([]Suggestion, 0),
		ValidatedAt: time.Now(),
	}
}

// AddError adds a critical error to result
func (r *Result) AddError(err Error) {
	r.Errors = append(r.Errors, err)
	r.Valid = false // Errors always invalidate
}

// AddWarning adds a warning to result
func (r *Result) AddWarning(warn Warning) {
	r.Warnings = append(r.Warnings, warn)
}

// AddInfo adds informational message to result
func (r *Result) AddInfo(info Info) {
	r.Info = append(r.Info, info)
}

// AddSuggestion adds improvement suggestion to result
func (r *Result) AddSuggestion(sug Suggestion) {
	r.Suggestions = append(r.Suggestions, sug)
}

// Merge merges another result into this result
func (r *Result) Merge(other *Result) {
	if other == nil {
		return
	}

	r.Errors = append(r.Errors, other.Errors...)
	r.Warnings = append(r.Warnings, other.Warnings...)
	r.Info = append(r.Info, other.Info...)
	r.Suggestions = append(r.Suggestions, other.Suggestions...)

	// Update validity if other has errors
	if len(other.Errors) > 0 {
		r.Valid = false
	}
}

// Summary returns a summary string
func (r *Result) Summary() string {
	if r.Valid {
		return fmt.Sprintf("✓ Configuration is valid (validated in %s)", r.Duration)
	}

	parts := []string{"✗ Configuration is invalid:"}

	if len(r.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", len(r.Errors)))
	}

	if len(r.Warnings) > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", len(r.Warnings)))
	}

	return strings.Join(parts, " ")
}

// ExitCode returns appropriate exit code for CLI
func (r *Result) ExitCode(mode ValidationMode) int {
	if len(r.Errors) > 0 {
		return 1 // Errors always fail
	}

	if mode == StrictMode && len(r.Warnings) > 0 {
		return 2 // Warnings fail in strict mode
	}

	return 0 // Success
}

// HasIssues returns true if there are any errors or warnings
func (r *Result) HasIssues() bool {
	return len(r.Errors) > 0 || len(r.Warnings) > 0
}

// HasErrors returns true if there are any errors
func (r *Result) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasWarnings returns true if there are any warnings
func (r *Result) HasWarnings() bool {
	return len(r.Warnings) > 0
}

// ErrorCount returns number of errors
func (r *Result) ErrorCount() int {
	return len(r.Errors)
}

// WarningCount returns number of warnings
func (r *Result) WarningCount() int {
	return len(r.Warnings)
}

// InfoCount returns number of info messages
func (r *Result) InfoCount() int {
	return len(r.Info)
}

// SuggestionCount returns number of suggestions
func (r *Result) SuggestionCount() int {
	return len(r.Suggestions)
}

// MarshalJSON implements json.Marshaler
func (r *Result) MarshalJSON() ([]byte, error) {
	type Alias Result
	return json.Marshal(&struct {
		*Alias
		DurationMS int64 `json:"duration_ms"`
	}{
		Alias:      (*Alias)(r),
		DurationMS: r.Duration.Milliseconds(),
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (r *Result) UnmarshalJSON(data []byte) error {
	type Alias Result
	aux := &struct {
		*Alias
		DurationMS int64 `json:"duration_ms"`
	}{
		Alias: (*Alias)(r),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	r.Duration = time.Duration(aux.DurationMS) * time.Millisecond
	return nil
}
