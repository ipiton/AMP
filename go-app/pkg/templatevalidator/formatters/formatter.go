package formatters

import "github.com/ipiton/AMP/pkg/templatevalidator"

// ================================================================================
// TN-156: Template Validator - Output Formatters
// ================================================================================
// Multiple output format support (human, JSON, SARIF).
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// OutputFormatter formats validation results for output
//
// Formatters convert ValidationResult into different output formats:
// - Human: colorized, readable format for terminal
// - JSON: machine-readable JSON for CI/CD
// - SARIF: Static Analysis Results Interchange Format for code scanning
type OutputFormatter interface {
	// Format formats validation results
	//
	// Parameters:
	// - results: slice of validation results
	// - paths: corresponding file paths
	//
	// Returns formatted output string.
	Format(results []templatevalidator.ValidationResult, paths []string) (string, error)
}

// ================================================================================
