package inhibition

import (
	"errors"
	"fmt"
	"strings"
)

// ParseError represents an error that occurred during YAML parsing.
//
// Use this error type when:
//   - YAML syntax is invalid
//   - Required fields are missing
//   - Type conversion fails
//   - Regex pattern compilation fails
//
// Example:
//
//	return &ParseError{
//	    Field: "rules[0].source_match_re.service",
//	    Value: "^(unclosed",
//	    Err:   fmt.Errorf("invalid regex: %w", err),
//	}
type ParseError struct {
	// Field is the YAML field path where the error occurred.
	// Examples: "rules[0].source_match", "inhibit_rules"
	Field string

	// Value is the value that caused the error.
	// Can be of any type (string, int, map, etc.)
	Value interface{}

	// Err is the underlying error.
	// Should provide details about what went wrong.
	Err error
}

// Error implements the error interface.
// Returns a formatted error message with field, value, and underlying error.
func (e *ParseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("parse error at field %q (value: %v): %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("parse error at field %q (value: %v)", e.Field, e.Value)
}

// Unwrap returns the underlying error.
// Implements errors.Unwrap interface for error chain traversal.
func (e *ParseError) Unwrap() error {
	return e.Err
}

// Is implements errors.Is interface for error type checking.
func (e *ParseError) Is(target error) bool {
	_, ok := target.(*ParseError)
	return ok
}

// NewParseError creates a new ParseError.
//
// Parameters:
//   - field: YAML field path where error occurred
//   - value: value that caused the error
//   - err: underlying error
//
// Returns:
//   - *ParseError: new parse error instance
//
// Example:
//
//	return NewParseError("rules[0].name", "invalid-name!", fmt.Errorf("contains invalid characters"))
func NewParseError(field string, value interface{}, err error) *ParseError {
	return &ParseError{
		Field: field,
		Value: value,
		Err:   err,
	}
}

// ValidationError represents an error that occurred during validation.
//
// Use this error type when:
//   - Business rules are violated
//   - Semantic validation fails
//   - Cross-field validation fails
//   - Label names are invalid
//
// Example:
//
//	return &ValidationError{
//	    Field:   "rules[0]",
//	    Rule:    "required_source",
//	    Message: "at least one of source_match or source_match_re required",
//	}
type ValidationError struct {
	// Field is the field that failed validation.
	// Examples: "rules[0]", "source_match.alertname", "equal"
	Field string

	// Rule is the validation rule that failed.
	// Examples: "required", "min_length", "valid_label_name", "required_one_of"
	Rule string

	// Message is a human-readable error message.
	// Should explain what went wrong and how to fix it.
	Message string
}

// Error implements the error interface.
// Returns a formatted error message with field, rule, and message.
func (e *ValidationError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("validation error for field %q (rule: %s): %s", e.Field, e.Rule, e.Message)
	}
	return fmt.Sprintf("validation error for field %q: %s", e.Field, e.Message)
}

// Is implements errors.Is interface for error type checking.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// NewValidationError creates a new ValidationError.
//
// Parameters:
//   - field: field that failed validation
//   - rule: validation rule that failed
//   - message: human-readable error message
//
// Returns:
//   - *ValidationError: new validation error instance
//
// Example:
//
//	return NewValidationError("equal", "valid_label_name", "invalid label name: 123invalid")
func NewValidationError(field, rule, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Rule:    rule,
		Message: message,
	}
}

// ConfigError represents a high-level configuration error with multiple underlying errors.
//
// Use this error type when:
//   - Multiple validation errors occur
//   - Configuration is structurally invalid
//   - Multiple rules fail validation
//
// Example:
//
//	return &ConfigError{
//	    Message: "configuration validation failed: 3 errors",
//	    Errors: []error{
//	        fmt.Errorf("rule 0: %w", err1),
//	        fmt.Errorf("rule 1: %w", err2),
//	        fmt.Errorf("rule 2: %w", err3),
//	    },
//	}
type ConfigError struct {
	// Message is a high-level error message.
	// Should summarize the overall problem.
	Message string

	// Errors is a list of underlying errors.
	// Can be nil if there are no specific underlying errors.
	Errors []error
}

// Error implements the error interface.
// Returns the high-level message, optionally with error count.
func (e *ConfigError) Error() string {
	if len(e.Errors) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (%d errors)", e.Message, len(e.Errors))
}

// Unwrap returns the underlying errors.
// Implements errors.Unwrap interface for multi-error unwrapping (Go 1.20+).
func (e *ConfigError) Unwrap() []error {
	return e.Errors
}

// Is implements errors.Is interface for error type checking.
func (e *ConfigError) Is(target error) bool {
	_, ok := target.(*ConfigError)
	return ok
}

// DetailedError returns a detailed error message with all underlying errors.
// Each error is printed on a separate line with indentation.
//
// Example output:
//
//	configuration validation failed: 2 errors
//	  - rule 0: at least one source condition required
//	  - rule 1: invalid label name: 123invalid
func (e *ConfigError) DetailedError() string {
	if len(e.Errors) == 0 {
		return e.Message
	}

	var sb strings.Builder
	sb.WriteString(e.Message)
	sb.WriteString(":\n")

	for i, err := range e.Errors {
		sb.WriteString(fmt.Sprintf("  %d. %v\n", i+1, err))
	}

	return sb.String()
}

// NewConfigError creates a new ConfigError.
//
// Parameters:
//   - message: high-level error message
//   - errors: list of underlying errors (can be nil)
//
// Returns:
//   - *ConfigError: new config error instance
//
// Example:
//
//	return NewConfigError("configuration validation failed", []error{err1, err2})
func NewConfigError(message string, errors []error) *ConfigError {
	return &ConfigError{
		Message: message,
		Errors:  errors,
	}
}

// IsParseError checks if the error is a ParseError or wraps one.
//
// Parameters:
//   - err: error to check
//
// Returns:
//   - bool: true if err is or wraps a ParseError
//
// Example:
//
//	if IsParseError(err) {
//	    log.Error("Failed to parse config")
//	}
func IsParseError(err error) bool {
	var parseErr *ParseError
	return errors.As(err, &parseErr)
}

// IsValidationError checks if the error is a ValidationError or wraps one.
//
// Parameters:
//   - err: error to check
//
// Returns:
//   - bool: true if err is or wraps a ValidationError
//
// Example:
//
//	if IsValidationError(err) {
//	    log.Error("Validation failed")
//	}
func IsValidationError(err error) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr)
}

// IsConfigError checks if the error is a ConfigError or wraps one.
//
// Parameters:
//   - err: error to check
//
// Returns:
//   - bool: true if err is or wraps a ConfigError
//
// Example:
//
//	if IsConfigError(err) {
//	    log.Error("Config error")
//	}
func IsConfigError(err error) bool {
	var configErr *ConfigError
	return errors.As(err, &configErr)
}

// GetParseError extracts a ParseError from an error chain.
//
// Parameters:
//   - err: error to extract from
//
// Returns:
//   - *ParseError: extracted parse error, or nil if not found
//
// Example:
//
//	if parseErr := GetParseError(err); parseErr != nil {
//	    log.Errorf("Parse error at field %s", parseErr.Field)
//	}
func GetParseError(err error) *ParseError {
	var parseErr *ParseError
	if errors.As(err, &parseErr) {
		return parseErr
	}
	return nil
}

// GetValidationError extracts a ValidationError from an error chain.
//
// Parameters:
//   - err: error to extract from
//
// Returns:
//   - *ValidationError: extracted validation error, or nil if not found
//
// Example:
//
//	if validationErr := GetValidationError(err); validationErr != nil {
//	    log.Errorf("Validation error for field %s", validationErr.Field)
//	}
func GetValidationError(err error) *ValidationError {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return validationErr
	}
	return nil
}

// GetConfigError extracts a ConfigError from an error chain.
//
// Parameters:
//   - err: error to extract from
//
// Returns:
//   - *ConfigError: extracted config error, or nil if not found
//
// Example:
//
//	if configErr := GetConfigError(err); configErr != nil {
//	    log.Errorf("Config error: %s", configErr.DetailedError())
//	}
func GetConfigError(err error) *ConfigError {
	var configErr *ConfigError
	if errors.As(err, &configErr) {
		return configErr
	}
	return nil
}
