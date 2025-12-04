package formatters

import (
	"encoding/json"
	"fmt"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - SARIF Formatter
// ================================================================================
// SARIF v2.1.0 output for GitHub/GitLab Code Scanning.
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// SARIFFormatter formats results as SARIF v2.1.0
//
// SARIF (Static Analysis Results Interchange Format) is an industry-standard
// format for static analysis tools. Supported by GitHub, GitLab, Azure DevOps.
//
// Spec: https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html
type SARIFFormatter struct{}

// NewSARIFFormatter creates a new SARIF formatter
func NewSARIFFormatter() OutputFormatter {
	return &SARIFFormatter{}
}

// SARIF v2.1.0 structures (simplified)
type SARIFOutput struct {
	Schema  string      `json:"$schema"`
	Version string      `json:"version"`
	Runs    []SARIFRun  `json:"runs"`
}

type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

type SARIFDriver struct {
	Name            string      `json:"name"`
	Version         string      `json:"version"`
	InformationUri  string      `json:"informationUri"`
	Rules           []SARIFRule `json:"rules"`
}

type SARIFRule struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	ShortDescription SARIFMessage `json:"shortDescription"`
	FullDescription  SARIFMessage `json:"fullDescription"`
	DefaultConfiguration SARIFConfiguration `json:"defaultConfiguration"`
}

type SARIFConfiguration struct {
	Level string `json:"level"`
}

type SARIFMessage struct {
	Text string `json:"text"`
}

type SARIFResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   SARIFMessage    `json:"message"`
	Locations []SARIFLocation `json:"locations,omitempty"`
}

type SARIFLocation struct {
	PhysicalLocation SARIFPhysicalLocation `json:"physicalLocation"`
}

type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifactLocation `json:"artifactLocation"`
	Region           SARIFRegion           `json:"region"`
}

type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

type SARIFRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

// Format formats validation results as SARIF v2.1.0
func (f *SARIFFormatter) Format(
	results []templatevalidator.ValidationResult,
	paths []string,
) (string, error) {
	// Build SARIF output
	output := SARIFOutput{
		Schema:  "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:           "template-validator",
						Version:        "1.0.0",
						InformationUri: "https://github.com/alert-history-service",
						Rules:          buildRules(),
					},
				},
				Results: buildResults(results, paths),
			},
		},
	}

	// Marshal to JSON (pretty-printed)
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// buildRules builds SARIF rules from known error codes
func buildRules() []SARIFRule {
	return []SARIFRule{
		{
			ID:   "syntax-error",
			Name: "Template Syntax Error",
			ShortDescription: SARIFMessage{Text: "Go text/template syntax error"},
			FullDescription:  SARIFMessage{Text: "Template contains invalid Go text/template syntax"},
			DefaultConfiguration: SARIFConfiguration{Level: "error"},
		},
		{
			ID:   "unknown-field",
			Name: "Unknown Data Field",
			ShortDescription: SARIFMessage{Text: "Field not in Alertmanager data model"},
			FullDescription:  SARIFMessage{Text: "Template references a field that doesn't exist in Alertmanager data model"},
			DefaultConfiguration: SARIFConfiguration{Level: "error"},
		},
		{
			ID:   "hardcoded-secret",
			Name: "Hardcoded Secret",
			ShortDescription: SARIFMessage{Text: "Hardcoded secret detected"},
			FullDescription:  SARIFMessage{Text: "Template contains hardcoded secret (API key, password, token)"},
			DefaultConfiguration: SARIFConfiguration{Level: "error"},
		},
	}
}

// buildResults builds SARIF results from validation results
func buildResults(
	results []templatevalidator.ValidationResult,
	paths []string,
) []SARIFResult {
	var sarifResults []SARIFResult

	for i, result := range results {
		path := "unknown"
		if i < len(paths) {
			path = paths[i]
		}

		// Add errors
		for _, err := range result.Errors {
			sarifResults = append(sarifResults, SARIFResult{
				RuleID: err.Code,
				Level:  mapSeverityToLevel(err.Severity),
				Message: SARIFMessage{
					Text: fmt.Sprintf("%s%s", err.Message, formatSuggestion(err.Suggestion)),
				},
				Locations: buildLocations(path, err.Line, err.Column),
			})
		}

		// Add warnings
		for _, warning := range result.Warnings {
			sarifResults = append(sarifResults, SARIFResult{
				RuleID: warning.Code,
				Level:  "warning",
				Message: SARIFMessage{
					Text: fmt.Sprintf("%s%s", warning.Message, formatSuggestion(warning.Suggestion)),
				},
				Locations: buildLocations(path, warning.Line, warning.Column),
			})
		}

		// Add suggestions (as notes)
		for _, suggestion := range result.Suggestions {
			sarifResults = append(sarifResults, SARIFResult{
				RuleID: "best-practice",
				Level:  "note",
				Message: SARIFMessage{
					Text: fmt.Sprintf("%s - %s", suggestion.Message, suggestion.Suggestion),
				},
				Locations: buildLocations(path, suggestion.Line, suggestion.Column),
			})
		}
	}

	return sarifResults
}

// mapSeverityToLevel maps error severity to SARIF level
func mapSeverityToLevel(severity string) string {
	switch severity {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	case "low":
		return "note"
	default:
		return "warning"
	}
}

// buildLocations builds SARIF locations
func buildLocations(path string, line, column int) []SARIFLocation {
	if line == 0 {
		// No location info
		return nil
	}

	return []SARIFLocation{
		{
			PhysicalLocation: SARIFPhysicalLocation{
				ArtifactLocation: SARIFArtifactLocation{
					URI: path,
				},
				Region: SARIFRegion{
					StartLine:   line,
					StartColumn: column,
				},
			},
		},
	}
}

// formatSuggestion formats suggestion for SARIF message
func formatSuggestion(suggestion string) string {
	if suggestion == "" {
		return ""
	}
	return fmt.Sprintf(" (Suggestion: %s)", suggestion)
}

// ================================================================================
