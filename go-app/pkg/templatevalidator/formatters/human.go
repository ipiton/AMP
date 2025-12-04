package formatters

import (
	"fmt"
	"strings"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Human-Readable Formatter
// ================================================================================
// Human-readable terminal output with colors and symbols.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// HumanFormatter formats results for human reading
type HumanFormatter struct {
	// useColors enables ANSI color codes (default: true)
	useColors bool
}

// NewHumanFormatter creates a new human formatter
func NewHumanFormatter() OutputFormatter {
	return &HumanFormatter{
		useColors: true,
	}
}

// Format formats validation results into human-readable output
func (f *HumanFormatter) Format(
	results []templatevalidator.ValidationResult,
	paths []string,
) (string, error) {
	var sb strings.Builder

	// Header
	sb.WriteString(f.colorize("═══════════════════════════════════════════════════\n", colorCyan))
	sb.WriteString(f.colorize("  Template Validation Results\n", colorBold))
	sb.WriteString(f.colorize("═══════════════════════════════════════════════════\n", colorCyan))
	sb.WriteString("\n")

	// Results for each template
	for i, result := range results {
		path := "unknown"
		if i < len(paths) {
			path = paths[i]
		}

		// Template header
		sb.WriteString(f.formatTemplateHeader(path, result))
		sb.WriteString("\n")

		// Errors
		if len(result.Errors) > 0 {
			sb.WriteString(f.formatErrors(result.Errors))
			sb.WriteString("\n")
		}

		// Warnings
		if len(result.Warnings) > 0 {
			sb.WriteString(f.formatWarnings(result.Warnings))
			sb.WriteString("\n")
		}

		// Suggestions
		if len(result.Suggestions) > 0 {
			sb.WriteString(f.formatSuggestions(result.Suggestions))
			sb.WriteString("\n")
		}

		// Separator
		if i < len(results)-1 {
			sb.WriteString("───────────────────────────────────────────────────\n\n")
		}
	}

	// Summary
	sb.WriteString(f.formatSummary(results))

	return sb.String(), nil
}

// ================================================================================

// formatTemplateHeader formats template header
func (f *HumanFormatter) formatTemplateHeader(
	path string,
	result templatevalidator.ValidationResult,
) string {
	var status string
	var statusColor string

	if result.Valid {
		status = "✓ PASSED"
		statusColor = colorGreen
	} else {
		status = "✗ FAILED"
		statusColor = colorRed
	}

	return fmt.Sprintf("%s %s\n  %d errors, %d warnings, %d suggestions\n",
		f.colorize(status, statusColor),
		f.colorize(path, colorBold),
		len(result.Errors),
		len(result.Warnings),
		len(result.Suggestions),
	)
}

// formatErrors formats validation errors
func (f *HumanFormatter) formatErrors(errors []templatevalidator.ValidationError) string {
	var sb strings.Builder

	sb.WriteString(f.colorize("Errors:\n", colorRed))

	for _, err := range errors {
		// Icon based on severity
		icon := "✗"
		if err.IsCritical() {
			icon = "⚠"
		}

		// Location
		loc := err.Location()
		if loc == "" {
			loc = "unknown"
		}

		// Format error
		sb.WriteString(fmt.Sprintf("  %s %s %s\n", icon, f.colorize(loc, colorYellow), err.Message))

		// Suggestion
		if err.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("      💡 %s\n", f.colorize(err.Suggestion, colorCyan)))
		}
	}

	return sb.String()
}

// formatWarnings formats warnings
func (f *HumanFormatter) formatWarnings(warnings []templatevalidator.ValidationWarning) string {
	var sb strings.Builder

	sb.WriteString(f.colorize("Warnings:\n", colorYellow))

	for _, warning := range warnings {
		loc := warning.Location()
		if loc == "" {
			loc = "unknown"
		}

		sb.WriteString(fmt.Sprintf("  ⚠ %s %s\n", f.colorize(loc, colorYellow), warning.Message))

		if warning.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("      💡 %s\n", f.colorize(warning.Suggestion, colorCyan)))
		}
	}

	return sb.String()
}

// formatSuggestions formats suggestions
func (f *HumanFormatter) formatSuggestions(suggestions []templatevalidator.ValidationSuggestion) string {
	var sb strings.Builder

	sb.WriteString(f.colorize("Suggestions:\n", colorCyan))

	for _, suggestion := range suggestions {
		loc := suggestion.Location()
		if loc == "" {
			loc = "unknown"
		}

		sb.WriteString(fmt.Sprintf("  💡 %s %s\n", loc, suggestion.Message))
		if suggestion.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("      → %s\n", suggestion.Suggestion))
		}
	}

	return sb.String()
}

// formatSummary formats overall summary
func (f *HumanFormatter) formatSummary(results []templatevalidator.ValidationResult) string {
	totalErrors := 0
	totalWarnings := 0
	totalSuggestions := 0
	passedCount := 0

	for _, result := range results {
		totalErrors += len(result.Errors)
		totalWarnings += len(result.Warnings)
		totalSuggestions += len(result.Suggestions)
		if result.Valid {
			passedCount++
		}
	}

	var sb strings.Builder

	sb.WriteString(f.colorize("═══════════════════════════════════════════════════\n", colorCyan))
	sb.WriteString(f.colorize("  Summary\n", colorBold))
	sb.WriteString(f.colorize("═══════════════════════════════════════════════════\n", colorCyan))

	// Overall status
	status := fmt.Sprintf("  Templates: %d/%d passed\n", passedCount, len(results))
	if passedCount == len(results) {
		sb.WriteString(f.colorize(status, colorGreen))
	} else {
		sb.WriteString(f.colorize(status, colorRed))
	}

	sb.WriteString(fmt.Sprintf("  Errors: %d\n", totalErrors))
	sb.WriteString(fmt.Sprintf("  Warnings: %d\n", totalWarnings))
	sb.WriteString(fmt.Sprintf("  Suggestions: %d\n", totalSuggestions))

	return sb.String()
}

// ================================================================================

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// colorize applies ANSI color if enabled
func (f *HumanFormatter) colorize(text, color string) string {
	if !f.useColors {
		return text
	}
	return color + text + colorReset
}

// ================================================================================
