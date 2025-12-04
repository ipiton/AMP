package parser

import (
	"regexp"
	"strings"
)

// ================================================================================
// TN-156: Template Validator - Variable Parser
// ================================================================================
// Parse variable references from template content.
//
// Features:
// - Extract .Variable references
// - Extract .Field.SubField nested references
// - Unique variable deduplication
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// VariableParser parses variable references from templates
//
// VariableParser extracts all variable references like:
// - .Status
// - .Labels.alertname
// - .Annotations.summary
//
// Used for semantic validation to check if variables exist in data model.
type VariableParser struct {
	// varPattern matches .Variable and .Field.SubField
	varPattern *regexp.Regexp

	// dotPattern matches individual dot-separated parts
	dotPattern *regexp.Regexp
}

// NewVariableParser creates a new variable parser
func NewVariableParser() *VariableParser {
	return &VariableParser{
		// Match .Variable or .Field.SubField (dot-notation)
		varPattern: regexp.MustCompile(`\.[\w]+(\.[\w]+)*`),

		// Match individual words after dot
		dotPattern: regexp.MustCompile(`\w+`),
	}
}

// ================================================================================

// ParseVariableReferences extracts all variable references from template
//
// Returns slice of unique variable paths found in template.
// Variables are returned in the form: ".Field" or ".Field.SubField"
//
// Example:
//
//	refs := parser.ParseVariableReferences("{{ .Status }}: {{ .Labels.alertname }}")
//	// Returns: [".Status", ".Labels.alertname"]
func (p *VariableParser) ParseVariableReferences(content string) []string {
	variablesMap := make(map[string]bool)

	// Find all variable references
	matches := p.varPattern.FindAllString(content, -1)

	for _, match := range matches {
		// Clean up match
		varName := strings.TrimSpace(match)

		// Skip single dot
		if varName == "." {
			continue
		}

		// Add to map (deduplicates automatically)
		variablesMap[varName] = true
	}

	// Convert map to slice
	variables := make([]string, 0, len(variablesMap))
	for varName := range variablesMap {
		variables = append(variables, varName)
	}

	return variables
}

// ================================================================================

// SplitVariablePath splits variable path into components
//
// Splits ".Field.SubField.SubSubField" into ["Field", "SubField", "SubSubField"]
//
// Returns slice of path components (without leading dots).
//
// Example:
//
//	parts := parser.SplitVariablePath(".Labels.alertname")
//	// Returns: ["Labels", "alertname"]
func (p *VariableParser) SplitVariablePath(varPath string) []string {
	// Remove leading dot
	varPath = strings.TrimPrefix(varPath, ".")

	// Split by dot
	parts := strings.Split(varPath, ".")

	// Filter empty parts
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}

	return filtered
}

// ================================================================================

// GetTopLevelField extracts top-level field from variable path
//
// Returns the first component of variable path.
//
// Example:
//
//	field := parser.GetTopLevelField(".Labels.alertname")
//	// Returns: "Labels"
func (p *VariableParser) GetTopLevelField(varPath string) string {
	parts := p.SplitVariablePath(varPath)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// ================================================================================

// IsNestedField returns true if variable path has nested fields
//
// Returns true for ".Labels.alertname" (nested)
// Returns false for ".Status" (top-level only)
//
// Example:
//
//	isNested := parser.IsNestedField(".Labels.alertname")
//	// Returns: true
func (p *VariableParser) IsNestedField(varPath string) bool {
	parts := p.SplitVariablePath(varPath)
	return len(parts) > 1
}

// ================================================================================

// GetFieldDepth returns nesting depth of variable path
//
// Returns:
// - 1 for ".Status" (top-level)
// - 2 for ".Labels.alertname" (one level nested)
// - 3 for ".Labels.foo.bar" (two levels nested)
//
// Example:
//
//	depth := parser.GetFieldDepth(".Labels.alertname")
//	// Returns: 2
func (p *VariableParser) GetFieldDepth(varPath string) int {
	parts := p.SplitVariablePath(varPath)
	return len(parts)
}

// ================================================================================
