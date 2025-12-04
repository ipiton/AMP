package template

import (
	"errors"
	"fmt"
)

// ================================================================================
// TN-153: Template Engine - Error Types
// ================================================================================
// Error definitions for template parsing and execution.
//
// Error Categories:
// - Parse errors: Template syntax errors
// - Execution errors: Runtime template errors
// - Timeout errors: Context deadline exceeded
// - Data errors: Invalid template data
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// Sentinel errors for template operations
var (
	// ErrTemplateParse indicates template parsing failed
	//
	// Causes:
	// - Invalid Go template syntax
	// - Unclosed braces/brackets
	// - Invalid function calls
	//
	// Example:
	//   "{{ .Labels.alertname" → missing closing brace
	ErrTemplateParse = errors.New("template parse failed")

	// ErrTemplateExecute indicates template execution failed
	//
	// Causes:
	// - Missing fields in data
	// - Function execution errors
	// - Type conversion errors
	//
	// Example:
	//   "{{ .NonExistentField }}" → field not found
	ErrTemplateExecute = errors.New("template execute failed")

	// ErrTemplateTimeout indicates template execution timeout
	//
	// Causes:
	// - Infinite loops in template
	// - Slow function execution
	// - Context deadline exceeded
	//
	// Default timeout: 5s
	ErrTemplateTimeout = errors.New("template execution timeout")

	// ErrInvalidData indicates invalid template data
	//
	// Causes:
	// - Nil TemplateData
	// - Missing required fields
	// - Invalid data types
	ErrInvalidData = errors.New("invalid template data")

	// ErrCacheOperation indicates cache operation failed
	//
	// Causes:
	// - Cache initialization failed
	// - Cache eviction failed
	ErrCacheOperation = errors.New("cache operation failed")
)

// TemplateError wraps template errors with additional context
type TemplateError struct {
	// Op is the operation that failed (parse, execute, cache)
	Op string

	// Template is the template string that caused the error
	Template string

	// Err is the underlying error
	Err error

	// Context provides additional error context
	Context map[string]interface{}
}

// Error implements error interface
func (e *TemplateError) Error() string {
	if len(e.Context) > 0 {
		return fmt.Sprintf("%s failed for template %q: %v (context: %+v)",
			e.Op, truncateTemplate(e.Template), e.Err, e.Context)
	}
	return fmt.Sprintf("%s failed for template %q: %v",
		e.Op, truncateTemplate(e.Template), e.Err)
}

// Unwrap returns the underlying error
func (e *TemplateError) Unwrap() error {
	return e.Err
}

// IsParseError checks if error is a parse error
func IsParseError(err error) bool {
	return errors.Is(err, ErrTemplateParse)
}

// IsExecuteError checks if error is an execution error
func IsExecuteError(err error) bool {
	return errors.Is(err, ErrTemplateExecute)
}

// IsTimeoutError checks if error is a timeout error
func IsTimeoutError(err error) bool {
	return errors.Is(err, ErrTemplateTimeout)
}

// IsDataError checks if error is a data error
func IsDataError(err error) bool {
	return errors.Is(err, ErrInvalidData)
}

// truncateTemplate truncates template string for error messages
func truncateTemplate(tmpl string) string {
	const maxLen = 100
	if len(tmpl) <= maxLen {
		return tmpl
	}
	return tmpl[:maxLen] + "..."
}

// NewParseError creates a new parse error
func NewParseError(template string, err error) error {
	return &TemplateError{
		Op:       "parse",
		Template: template,
		Err:      fmt.Errorf("%w: %v", ErrTemplateParse, err),
	}
}

// NewExecuteError creates a new execution error
func NewExecuteError(template string, err error) error {
	return &TemplateError{
		Op:       "execute",
		Template: template,
		Err:      fmt.Errorf("%w: %v", ErrTemplateExecute, err),
	}
}

// NewTimeoutError creates a new timeout error
func NewTimeoutError(template string) error {
	return &TemplateError{
		Op:       "execute",
		Template: template,
		Err:      ErrTemplateTimeout,
	}
}

// NewDataError creates a new data error
func NewDataError(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidData, reason)
}
