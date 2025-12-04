// Package types provides common types for config validation
package types

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ================================================================================
// Validation Modes and Options
// ================================================================================

// ValidationMode defines validation strictness level
type ValidationMode string

const (
	// StrictMode: Errors and warnings block validation
	StrictMode ValidationMode = "strict"

	// LenientMode: Only errors block validation, warnings pass
	LenientMode ValidationMode = "lenient"

	// PermissiveMode: Nothing blocks, all issues reported but validation passes
	PermissiveMode ValidationMode = "permissive"
)

// Options configures validator behavior
type Options struct {
	Mode                ValidationMode
	Sections            []string
	IncludeInfo         bool
	IncludeSuggestions  bool
	FailFast            bool
	MaxErrors           int
	EnableSecurity      bool
	EnableBestPractices bool
	TemplateBasePath    string
	DefaultDocsURL      string
}

// ================================================================================
// Result Types
// ================================================================================

// Result represents comprehensive validation result
type Result struct {
	Valid        bool          `json:"valid"`
	Errors       []Error       `json:"errors,omitempty"`
	Warnings     []Warning     `json:"warnings,omitempty"`
	Info         []Info        `json:"info,omitempty"`
	Suggestions  []Suggestion  `json:"suggestions,omitempty"`
	FilePath     string        `json:"file_path,omitempty"`
	Duration     time.Duration `json:"-"`
	DurationMS   int64         `json:"duration_ms"`
	ValidatedAt  time.Time     `json:"validated_at"`
}

// Error represents a critical validation error
type Error struct {
	Type       string   `json:"type"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Location   Location `json:"location"`
	Context    string   `json:"context,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	DocsURL    string   `json:"docs_url,omitempty"`
	Related    []string `json:"related,omitempty"`
}

// Warning represents a potential problem
type Warning struct {
	Type       string   `json:"type"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Location   Location `json:"location"`
	Suggestion string   `json:"suggestion,omitempty"`
	DocsURL    string   `json:"docs_url,omitempty"`
}

// Info represents informational message or recommendation
type Info struct {
	Type     string   `json:"type"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Location Location `json:"location,omitempty"`
	DocsURL  string   `json:"docs_url,omitempty"`
}

// Suggestion represents actionable improvement
type Suggestion struct {
	Type     string   `json:"type"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Location Location `json:"location,omitempty"`
	Before   string   `json:"before,omitempty"`
	After    string   `json:"after,omitempty"`
	DocsURL  string   `json:"docs_url,omitempty"`
}

// Location represents location in configuration file
type Location struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Field   string `json:"field,omitempty"`
	Section string `json:"section,omitempty"`
}

// ================================================================================
// Helper Methods
// ================================================================================

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

// AddError adds an error to the result
func (r *Result) AddError(err Error) {
	r.Errors = append(r.Errors, err)
	r.Valid = false
}

// AddWarning adds a warning to the result
func (r *Result) AddWarning(warn Warning) {
	r.Warnings = append(r.Warnings, warn)
}

// AddInfo adds an info message to the result
func (r *Result) AddInfo(info Info) {
	r.Info = append(r.Info, info)
}

// AddSuggestion adds a suggestion to the result
func (r *Result) AddSuggestion(sugg Suggestion) {
	r.Suggestions = append(r.Suggestions, sugg)
}

// HasErrors returns true if result has errors
func (r *Result) HasErrors() bool {
	return len(r.Errors) > 0
}

// HasWarnings returns true if result has warnings
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

// ToJSON converts result to JSON
func (r *Result) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Summary returns a human-readable summary
func (r *Result) Summary() string {
	var sb strings.Builder

	if r.Valid {
		sb.WriteString("✓ Configuration is valid\n")
	} else {
		sb.WriteString("✗ Configuration is invalid\n")
	}

	if len(r.Errors) > 0 {
		sb.WriteString(fmt.Sprintf("  Errors: %d\n", len(r.Errors)))
	}

	if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("  Warnings: %d\n", len(r.Warnings)))
	}

	if len(r.Info) > 0 {
		sb.WriteString(fmt.Sprintf("  Info: %d\n", len(r.Info)))
	}

	if len(r.Suggestions) > 0 {
		sb.WriteString(fmt.Sprintf("  Suggestions: %d\n", len(r.Suggestions)))
	}

	if r.Duration > 0 {
		sb.WriteString(fmt.Sprintf("  Duration: %v\n", r.Duration))
	}

	return sb.String()
}

// NewResult creates a new validation result
func NewResult() *Result {
	return &Result{
		Valid:       true,
		Errors:      []Error{},
		Warnings:    []Warning{},
		Info:        []Info{},
		Suggestions: []Suggestion{},
		ValidatedAt: time.Now(),
	}
}

// ================================================================================
// Constants
// ================================================================================

// Error types
const (
	ErrorTypeSyntax     = "syntax"
	ErrorTypeSemantic   = "semantic"
	ErrorTypeSecurity   = "security"
	ErrorTypeLogic      = "logic"
	ErrorTypeReference  = "reference"
	ErrorTypeType       = "type"
	ErrorTypeValidation = "validation"
)

// Warning types
const (
	WarningTypeBestPractice = "best_practice"
	WarningTypePerformance  = "performance"
	WarningTypeSecurity     = "security"
	WarningTypeDeprecated   = "deprecated"
)

// Info types
const (
	InfoTypeRecommendation = "recommendation"
	InfoTypeOptimization   = "optimization"
	InfoTypeCompatibility  = "compatibility"
)

// Severity levels
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)
