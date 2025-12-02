package routing

import "fmt"

// TreeValidationError represents a validation error in the routing tree.
//
// Validation errors are collected during tree validation and include:
// - Type: category of error (cycle, receiver not found, etc.)
// - Path: location in tree where error occurred ("route.routes[0]")
// - Message: human-readable description
// - Field: specific field name (optional)
//
// Example:
//
//	TreeValidationError{
//	    Type: ErrReceiverNotFound,
//	    Path: "route.routes[0]",
//	    Message: "receiver 'critical' not found in config",
//	    Field: "receiver",
//	}
type TreeValidationError struct {
	// Type is the category of validation error
	Type ValidationErrorType

	// Path is the location in tree where error occurred
	// Format: "route.routes[0].routes[1]"
	// Used for debugging and error reporting
	Path string

	// Message is a human-readable description of the error
	Message string

	// Field is the specific field name that caused the error (optional)
	// Example: "receiver", "match", "group_wait"
	Field string
}

// Error implements the error interface for TreeValidationError.
//
// Returns a formatted error message with type, path, and message.
//
// Example output:
//
//	"[receiver_not_found] route.routes[0]: receiver 'critical' not found in config"
func (e TreeValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("[%s] %s (field: %s): %s", e.Type, e.Path, e.Field, e.Message)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Type, e.Path, e.Message)
}

// ValidationErrorType represents a category of validation error.
type ValidationErrorType string

// Validation error types.
//
// These constants define the different categories of validation errors
// that can occur during tree validation.
const (
	// ErrCycle indicates a cyclic dependency in the tree.
	// A cycle exists if a node is reachable from itself by following parent-child links.
	// This should never happen with trees built via TreeBuilder.
	//
	// Example: route A → route B → route A (cycle)
	ErrCycle ValidationErrorType = "cycle"

	// ErrReceiverNotFound indicates a receiver reference that doesn't exist.
	// The route references a receiver name that is not defined in config.receivers.
	//
	// Example:
	//   route:
	//     receiver: critical  # receiver 'critical' not found in config.receivers
	ErrReceiverNotFound ValidationErrorType = "receiver_not_found"

	// ErrDuplicateMatcher indicates duplicate matchers on the same level.
	// Two sibling routes have identical matchers, causing ambiguous routing.
	//
	// Example:
	//   routes:
	//     - match: {severity: critical}
	//       receiver: pagerduty
	//     - match: {severity: critical}  # duplicate matcher
	//       receiver: slack
	ErrDuplicateMatcher ValidationErrorType = "duplicate_matcher"

	// ErrInvalidRegex indicates an invalid regex pattern in match_re.
	// The regex pattern cannot be compiled by Go's regexp package.
	//
	// Example:
	//   match_re:
	//     namespace: "[invalid(regex"  # invalid regex syntax
	ErrInvalidRegex ValidationErrorType = "invalid_regex"

	// ErrInvalidDuration indicates an invalid duration value.
	// Duration is zero, negative, or semantically incorrect.
	//
	// Examples:
	//   group_wait: -30s           # negative duration
	//   group_interval: 0s         # zero duration
	//   repeat_interval: 1s        # less than group_interval (semantic error)
	ErrInvalidDuration ValidationErrorType = "invalid_duration"

	// ErrEmptyReceiver indicates a route with no receiver name.
	// Every route must have a receiver (inherited or explicit).
	//
	// Example:
	//   route:
	//     receiver: ""  # empty receiver name
	ErrEmptyReceiver ValidationErrorType = "empty_receiver"
)

// String returns the string representation of the validation error type.
func (e ValidationErrorType) String() string {
	return string(e)
}

// ValidationErrors is a collection of TreeValidationError.
//
// Implements the error interface for convenient error handling.
//
// Example usage:
//
//	errors := tree.Validate()
//	if len(errors) > 0 {
//	    return ValidationErrors(errors)
//	}
type ValidationErrors []TreeValidationError

// Error implements the error interface for ValidationErrors.
//
// Returns a summary message with the count of errors and first error details.
//
// Example output:
//
//	"3 validation errors (first: receiver 'critical' not found at route.routes[0])"
func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("%d validation errors (first: %s)", len(e), e[0].Message)
}

// HasErrors returns true if there are any validation errors.
func (e ValidationErrors) HasErrors() bool {
	return len(e) > 0
}

// Count returns the number of validation errors.
func (e ValidationErrors) Count() int {
	return len(e)
}

// ByType returns all errors of the given type.
//
// Useful for filtering specific error categories.
//
// Example:
//
//	receiverErrors := errors.ByType(ErrReceiverNotFound)
func (e ValidationErrors) ByType(typ ValidationErrorType) ValidationErrors {
	var filtered ValidationErrors
	for _, err := range e {
		if err.Type == typ {
			filtered = append(filtered, err)
		}
	}
	return filtered
}

// String returns a human-readable representation of all validation errors.
//
// Format: one error per line with full details.
//
// Example output:
//
//	[receiver_not_found] route.routes[0]: receiver 'critical' not found in config
//	[invalid_regex] route.routes[1]: invalid regex pattern '[invalid(regex'
func (e ValidationErrors) String() string {
	if len(e) == 0 {
		return "no validation errors"
	}

	result := fmt.Sprintf("%d validation errors:\n", len(e))
	for i, err := range e {
		result += fmt.Sprintf("  %d. %s\n", i+1, err.Error())
	}
	return result
}
