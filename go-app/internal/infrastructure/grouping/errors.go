package grouping

import "fmt"

// === Parser Errors (TN-121) ===

// ParseError represents a YAML parsing error.
type ParseError struct {
	Field  string // Field that caused the error
	Value  string // Value that was invalid
	Line   int    // Line number in YAML file (if available)
	Column int    // Column number in YAML file (if available)
	Err    error  // Underlying error
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("parse error in field '%s' (value: '%s'): %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("parse error in field '%s': %v", e.Field, e.Err)
}

// Unwrap returns the underlying error.
func (e *ParseError) Unwrap() error {
	return e.Err
}

// ValidationError represents a single validation failure.
type ValidationError struct {
	Field   string // Field that failed validation
	Message string // Validation error message
	Value   string // Invalid value (optional)
	Rule    string // Validation rule that failed (optional, e.g., "labelname", "range")
}

// Error implements the error interface.
// Note: Using value receiver instead of pointer receiver for errors.Is/As compatibility
func (e ValidationError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("validation error in field '%s': %s (value: '%s')", e.Field, e.Message, e.Value)
	}
	return fmt.Sprintf("validation error in field '%s': %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

// Error implements the error interface.
func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("multiple validation errors (%d): %s", len(e), e[0].Error())
}

// Add adds a validation error to the collection.
// Parameters:
//   - field: the field name that failed validation
//   - value: the invalid value
//   - tag: the validation tag (e.g., "labelname", "range", "max_depth")
//   - message: human-readable error message
func (e *ValidationErrors) Add(field, value, tag, message string) {
	*e = append(*e, ValidationError{
		Field:   field,
		Message: message,
		Value:   value,
	})
}

// HasErrors returns true if there are any validation errors.
func (e ValidationErrors) HasErrors() bool {
	return len(e) > 0
}

// ToError returns nil if there are no errors, or the ValidationErrors itself as an error.
func (e ValidationErrors) ToError() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

// AddError adds an error to the validation errors collection.
// Parameters:
//   - errOrField: can be either a ValidationError or a field name (string)
//
// Usage:
//   errors.AddError(validationError)                // Add ValidationError directly
//   errors.AddError(field, err)                      // Add error for field
func (e *ValidationErrors) AddError(args ...interface{}) {
	if len(args) == 0 {
		return
	}

	// Case 1: Single ValidationError argument
	if len(args) == 1 {
		if ve, ok := args[0].(ValidationError); ok {
			*e = append(*e, ve)
			return
		}
	}

	// Case 2: (field string, err error)
	if len(args) == 2 {
		if field, ok := args[0].(string); ok {
			if err, ok := args[1].(error); ok && err != nil {
				if ve, ok := err.(ValidationError); ok {
					*e = append(*e, ve)
					return
				}
				*e = append(*e, ValidationError{
					Field:   field,
					Message: err.Error(),
				})
				return
			}
		}
	}
}

// ConfigError represents a configuration error (file not found, invalid structure, etc.).
type ConfigError struct {
	Message string // Error message
	Source  string // Source file path (if applicable)
	Err     error  // Underlying error (if applicable)
}

// Error implements the error interface.
func (e *ConfigError) Error() string {
	if e.Source != "" && e.Err != nil {
		return fmt.Sprintf("config error in '%s': %s: %v", e.Source, e.Message, e.Err)
	}
	if e.Source != "" {
		return fmt.Sprintf("config error in '%s': %s", e.Source, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("config error: %s: %v", e.Message, e.Err)
	}
	return fmt.Sprintf("config error: %s", e.Message)
}

// Unwrap returns the underlying error.
func (e *ConfigError) Unwrap() error {
	return e.Err
}

// NewConfigError creates a new ConfigError.
func NewConfigError(message, source string, err error) *ConfigError {
	return &ConfigError{
		Message: message,
		Source:  source,
		Err:     err,
	}
}

// NewParseError creates a new ParseError.
func NewParseError(field, value string, err error) *ParseError {
	return &ParseError{
		Field: field,
		Value: value,
		Err:   err,
	}
}

// NewValidationError creates a new ValidationError.
// Can be called with 3 or 4 arguments:
//   NewValidationError(field, message, value)
//   NewValidationError(field, message, value, rule)
func NewValidationError(field, message, value string, rule ...string) ValidationError {
	ve := ValidationError{
		Field:   field,
		Message: message,
		Value:   value,
	}
	if len(rule) > 0 {
		ve.Rule = rule[0]
	}
	return ve
}

// Count returns the number of validation errors.
func (e ValidationErrors) Count() int {
	return len(e)
}

// === Group Manager Errors (TN-123) ===

// InvalidAlertError indicates that an alert failed validation.
//
// Returned by AddAlertToGroup when:
//   - alert is nil
//   - alert.Fingerprint is empty
//   - alert data is malformed
type InvalidAlertError struct {
	// Reason describes why the alert is invalid
	Reason string
}

// Error implements the error interface.
func (e *InvalidAlertError) Error() string {
	return fmt.Sprintf("invalid alert: %s", e.Reason)
}

// GroupNotFoundError indicates that a requested group does not exist.
//
// Returned by:
//   - GetGroup when group key doesn't exist
//   - RemoveAlertFromGroup when group doesn't exist
//   - UpdateGroupState when group doesn't exist
//   - GetGroupByFingerprint when alert not in any group
type GroupNotFoundError struct {
	// Key is the group key that was not found
	Key GroupKey
}

// Error implements the error interface.
func (e *GroupNotFoundError) Error() string {
	return fmt.Sprintf("group not found: %s", e.Key)
}

// StorageError wraps errors from the underlying storage layer.
//
// Used for future Redis storage (TN-125). Currently wraps in-memory errors.
//
// Implements error wrapping (Unwrap) for errors.Is and errors.As support.
type StorageError struct {
	// Operation is the storage operation that failed (e.g., "store", "load", "delete")
	Operation string

	// Err is the underlying error
	Err error
}

// Error implements the error interface.
func (e *StorageError) Error() string {
	return fmt.Sprintf("storage error during %s: %v", e.Operation, e.Err)
}

// Unwrap returns the underlying error for errors.Is and errors.As.
func (e *StorageError) Unwrap() error {
	return e.Err
}

// NewStorageError creates a new StorageError.
func NewStorageError(operation string, err error) *StorageError {
	return &StorageError{
		Operation: operation,
		Err:       err,
	}
}

// NewGroupNotFoundError creates a new GroupNotFoundError.
func NewGroupNotFoundError(key GroupKey) *GroupNotFoundError {
	return &GroupNotFoundError{Key: key}
}

// === TN-125: Group Storage Error Types ===

// ErrVersionMismatch indicates optimistic locking conflict during concurrent group updates.
//
// This error occurs when two replicas attempt to update the same group simultaneously,
// and the Version field has changed between read and write operations.
//
// Resolution:
//   - Retry the operation after reloading the group (exponential backoff recommended)
//   - Merge changes if possible (application-specific logic)
//   - Log conflict for monitoring
//
// Example:
//
//	group, err := storage.Load(ctx, groupKey)
//	// ... modify group ...
//	err = storage.Store(ctx, group)
//	if errors.Is(err, &ErrVersionMismatch{}) {
//	    // Reload and retry
//	    group, _ = storage.Load(ctx, groupKey)
//	    // ... apply changes again ...
//	    err = storage.Store(ctx, group)
//	}
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
type ErrVersionMismatch struct {
	Key             GroupKey // Group key that experienced conflict
	ExpectedVersion int64    // Version we expected (from previous Load)
	ActualVersion   int64    // Version in storage (updated by another replica)
}

func (e *ErrVersionMismatch) Error() string {
	return fmt.Sprintf("version mismatch for group %s: expected version %d, got %d (concurrent update detected)",
		e.Key, e.ExpectedVersion, e.ActualVersion)
}

// NewVersionMismatchError creates a new ErrVersionMismatch error.
func NewVersionMismatchError(key GroupKey, expected, actual int64) *ErrVersionMismatch {
	return &ErrVersionMismatch{
		Key:             key,
		ExpectedVersion: expected,
		ActualVersion:   actual,
	}
}
