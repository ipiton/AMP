package formatters

import (
	"encoding/json"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - JSON Formatter
// ================================================================================
// Machine-readable JSON output for CI/CD integration.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// JSONFormatter formats results as JSON
type JSONFormatter struct{}

// NewJSONFormatter creates a new JSON formatter
func NewJSONFormatter() OutputFormatter {
	return &JSONFormatter{}
}

// JSONOutput is the top-level JSON structure
type JSONOutput struct {
	Results []JSONResult `json:"results"`
	Summary JSONSummary  `json:"summary"`
}

// JSONResult is a single validation result
type JSONResult struct {
	Path        string                                `json:"path"`
	Valid       bool                                  `json:"valid"`
	Errors      []templatevalidator.ValidationError   `json:"errors"`
	Warnings    []templatevalidator.ValidationWarning `json:"warnings"`
	Suggestions []templatevalidator.ValidationSuggestion `json:"suggestions"`
}

// JSONSummary is the overall summary
type JSONSummary struct {
	TotalTemplates  int `json:"total_templates"`
	PassedTemplates int `json:"passed_templates"`
	FailedTemplates int `json:"failed_templates"`
	TotalErrors     int `json:"total_errors"`
	TotalWarnings   int `json:"total_warnings"`
	TotalSuggestions int `json:"total_suggestions"`
}

// Format formats validation results as JSON
func (f *JSONFormatter) Format(
	results []templatevalidator.ValidationResult,
	paths []string,
) (string, error) {
	// Build JSON output
	output := JSONOutput{
		Results: make([]JSONResult, len(results)),
		Summary: computeSummary(results),
	}

	for i, result := range results {
		path := "unknown"
		if i < len(paths) {
			path = paths[i]
		}

		output.Results[i] = JSONResult{
			Path:        path,
			Valid:       result.Valid,
			Errors:      result.Errors,
			Warnings:    result.Warnings,
			Suggestions: result.Suggestions,
		}
	}

	// Marshal to JSON (pretty-printed)
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// computeSummary computes overall summary
func computeSummary(results []templatevalidator.ValidationResult) JSONSummary {
	summary := JSONSummary{
		TotalTemplates: len(results),
	}

	for _, result := range results {
		summary.TotalErrors += len(result.Errors)
		summary.TotalWarnings += len(result.Warnings)
		summary.TotalSuggestions += len(result.Suggestions)

		if result.Valid {
			summary.PassedTemplates++
		} else {
			summary.FailedTemplates++
		}
	}

	return summary
}

// ================================================================================
