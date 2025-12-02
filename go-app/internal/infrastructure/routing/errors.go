package routing

import (
	"fmt"
	"strings"
)

// ValidationError represents a single validation error.
// Includes field path, message, and suggestion for resolution.
type ValidationError struct {
	Field      string // Field path (e.g., "receivers[0].webhook_configs[0].url")
	Message    string // Human-readable error message
	Suggestion string // Optional suggestion for fixing the error
}

// Error implements the error interface.
func (v *ValidationError) Error() string {
	if v.Suggestion != "" {
		return fmt.Sprintf("%s: %s (suggestion: %s)", v.Field, v.Message, v.Suggestion)
	}
	return fmt.Sprintf("%s: %s", v.Field, v.Message)
}

// ValidationErrors represents multiple validation errors.
type ValidationErrors []*ValidationError

// Error implements the error interface.
// Returns a concatenated string of all errors.
func (v ValidationErrors) Error() string {
	if len(v) == 0 {
		return "no validation errors"
	}

	var messages []string
	for _, err := range v {
		messages = append(messages, err.Error())
	}

	return fmt.Sprintf("validation failed: %s", strings.Join(messages, "; "))
}

// Add appends a validation error.
func (v *ValidationErrors) Add(field, message, suggestion string) {
	*v = append(*v, &ValidationError{
		Field:      field,
		Message:    message,
		Suggestion: suggestion,
	})
}

// HasErrors returns true if there are any errors.
func (v ValidationErrors) HasErrors() bool {
	return len(v) > 0
}

// ErrType returns error if there are errors, nil otherwise.
func (v ValidationErrors) ErrType() error {
	if len(v) == 0 {
		return nil
	}
	return v
}
