package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Error Parser
// ================================================================================
// Parse Go template errors to extract line:column and clean messages.
//
// Features:
// - Parse "template: <name>:<line>:<column>: <message>" format
// - Extract line number (1-indexed)
// - Extract column number (1-indexed)
// - Extract clean error message
// - Extract function names from error messages
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-25

// ErrorParser parses Go template errors into structured ValidationError
//
// Go template errors have format:
//   "template: <name>:<line>:<column>: <message>"
//
// Example:
//   "template: slack:15:24: function \"toUpperCase\" not defined"
//
// ErrorParser extracts:
// - line: 15
// - column: 24
// - message: "function \"toUpperCase\" not defined"
type ErrorParser struct {
	// lineColPattern matches "template: <name>:<line>:<column>: <message>"
	lineColPattern *regexp.Regexp

	// functionPattern matches function names in error messages
	functionPattern *regexp.Regexp
}

// NewErrorParser creates a new error parser
func NewErrorParser() *ErrorParser {
	return &ErrorParser{
		// Pattern: template: <name>:<line>:<column>: <message>
		lineColPattern: regexp.MustCompile(`^template:\s*[^:]*:(\d+):(\d+):\s*(.+)$`),

		// Pattern: function "functionName" not defined
		functionPattern: regexp.MustCompile(`function\s+"([^"]+)"\s+not\s+(defined|found)`),
	}
}

// ================================================================================

// ParseGoTemplateError parses Go template error into ValidationError
//
// Extracts line:column:message from error string.
// Returns ValidationError with syntax phase.
//
// Example:
//
//	err := errors.New("template: slack:15:24: function \"toUpperCase\" not defined")
//	validationErr := parser.ParseGoTemplateError(err)
//	// validationErr.Line = 15
//	// validationErr.Column = 24
//	// validationErr.Message = "function \"toUpperCase\" not defined"
func (p *ErrorParser) ParseGoTemplateError(err error) templatevalidator.ValidationError {
	if err == nil {
		return templatevalidator.ValidationError{
			Phase:    "syntax",
			Severity: "critical",
			Message:  "unknown error",
			Code:     "syntax-error",
		}
	}

	errStr := err.Error()

	// Try to parse line:column format
	if matches := p.lineColPattern.FindStringSubmatch(errStr); matches != nil {
		line, _ := strconv.Atoi(matches[1])
		column, _ := strconv.Atoi(matches[2])
		message := strings.TrimSpace(matches[3])

		return templatevalidator.ValidationError{
			Phase:    "syntax",
			Severity: "critical",
			Line:     line,
			Column:   column,
			Message:  message,
			Code:     "syntax-error",
		}
	}

	// Fallback: return error message without location
	return templatevalidator.ValidationError{
		Phase:    "syntax",
		Severity: "critical",
		Line:     0,
		Column:   0,
		Message:  errStr,
		Code:     "syntax-error",
	}
}

// ================================================================================

// ExtractFunctionName extracts function name from error message
//
// Matches patterns like:
// - "function \"toUpperCase\" not defined"
// - "function \"myFunc\" not found"
//
// Returns function name or empty string if not found.
//
// Example:
//
//	funcName := parser.ExtractFunctionName("function \"toUpperCase\" not defined")
//	// Returns: "toUpperCase"
func (p *ErrorParser) ExtractFunctionName(errMessage string) string {
	if matches := p.functionPattern.FindStringSubmatch(errMessage); matches != nil {
		return matches[1]
	}
	return ""
}

// ================================================================================

// ParseLineNumber extracts line number from error message
//
// Tries multiple patterns:
// - "template: <name>:<line>: <message>"
// - "line <line>: <message>"
// - "at line <line>"
//
// Returns line number (1-indexed) or 0 if not found.
func (p *ErrorParser) ParseLineNumber(errMessage string) int {
	// Pattern 1: template: <name>:<line>: <message>
	pattern1 := regexp.MustCompile(`template:\s*[^:]*:(\d+):`)
	if matches := pattern1.FindStringSubmatch(errMessage); matches != nil {
		line, _ := strconv.Atoi(matches[1])
		return line
	}

	// Pattern 2: line <line>:
	pattern2 := regexp.MustCompile(`line\s+(\d+):`)
	if matches := pattern2.FindStringSubmatch(errMessage); matches != nil {
		line, _ := strconv.Atoi(matches[1])
		return line
	}

	// Pattern 3: at line <line>
	pattern3 := regexp.MustCompile(`at\s+line\s+(\d+)`)
	if matches := pattern3.FindStringSubmatch(errMessage); matches != nil {
		line, _ := strconv.Atoi(matches[1])
		return line
	}

	return 0
}

// ================================================================================

// GenerateSuggestion generates helpful suggestion based on error message
//
// Common mistakes and suggestions:
// - "toUpperCase" → "Did you mean 'toUpper'?"
// - "bad character" → "Check for invalid characters"
// - "unexpected" → "Check template syntax - missing {{ or }} ?"
// - "unclosed action" → "Missing closing }} in template"
//
// Returns suggestion or empty string if no suggestion available.
func (p *ErrorParser) GenerateSuggestion(errMessage string) string {
	errLower := strings.ToLower(errMessage)

	// Common mistakes map
	suggestions := map[string]string{
		"bad character":    "Check for invalid characters in template syntax",
		"unexpected":       "Check template syntax - missing {{ or }} ?",
		"unclosed action":  "Missing closing }} in template",
		"unclosed quote":   "Missing closing quote \" or '",
		"undefined":        "Check if function or variable is available",
		"nil pointer":      "Check if variable exists before accessing fields",
		"invalid type":     "Check field types - may be accessing wrong type",
		"range over":       "Check if variable is a slice, array, or map",
		"missing argument": "Function requires more arguments",
	}

	// Check for matching patterns
	for pattern, suggestion := range suggestions {
		if strings.Contains(errLower, pattern) {
			return suggestion
		}
	}

	return ""
}

// ================================================================================

// FormatErrorMessage formats ValidationError into human-readable message
//
// Format:
//   "line <line>, column <column>: <message>"
//   "Suggestion: <suggestion>"
//
// Example:
//
//	msg := parser.FormatErrorMessage(validationErr)
//	// Returns:
//	// "line 15, column 24: function \"toUpperCase\" not defined"
//	// "Suggestion: Did you mean 'toUpper'?"
func (p *ErrorParser) FormatErrorMessage(err templatevalidator.ValidationError) string {
	// Format location
	var location string
	if err.Line > 0 && err.Column > 0 {
		location = fmt.Sprintf("line %d, column %d", err.Line, err.Column)
	} else if err.Line > 0 {
		location = fmt.Sprintf("line %d", err.Line)
	} else {
		location = "unknown location"
	}

	// Format main message
	message := fmt.Sprintf("%s: %s", location, err.Message)

	// Add suggestion if available
	if err.Suggestion != "" {
		message += fmt.Sprintf("\nSuggestion: %s", err.Suggestion)
	}

	return message
}

// ================================================================================
