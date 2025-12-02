package core

import (
	"errors"
	"time"
)

// Filter validation errors
var (
	// Limit/Offset errors
	ErrInvalidFilterLimit  = errors.New("filter limit must be >= 0")
	ErrFilterLimitTooLarge = errors.New("filter limit must be <= 1000")
	ErrInvalidFilterOffset = errors.New("filter offset must be >= 0")

	// Status errors
	ErrInvalidFilterStatus = errors.New("invalid filter status: must be 'firing' or 'resolved'")

	// Severity errors
	ErrInvalidFilterSeverity = errors.New("invalid filter severity: must be 'critical', 'warning', 'info', or 'noise'")

	// TimeRange errors
	ErrInvalidTimeRange = errors.New("invalid time range: 'from' must be before 'to'")

	// Label errors
	ErrTooManyLabels     = errors.New("too many label filters: maximum 20 labels allowed")
	ErrEmptyLabelKey     = errors.New("label key cannot be empty")
	ErrLabelKeyTooLong   = errors.New("label key too long: maximum 255 characters")
	ErrLabelValueTooLong = errors.New("label value too long: maximum 255 characters")

	// Pagination errors
	ErrInvalidPagination = errors.New("pagination parameters are required")
	ErrInvalidPage       = errors.New("page must be >= 1")
	ErrInvalidPerPage    = errors.New("per_page must be >= 1")
	ErrPerPageTooLarge   = errors.New("per_page must be <= 1000")

	// Sorting errors
	ErrInvalidSortField = errors.New("invalid sort field")
	ErrInvalidSortOrder = errors.New("invalid sort order: must be 'asc' or 'desc'")

	// Alert storage errors
	ErrAlertNotFound  = errors.New("alert not found")
	ErrDuplicateAlert = errors.New("alert already exists")
)

// TN-064: Validation and timeout errors for report endpoint

// ValidationError represents a validation error with field context
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// TimeoutError represents a timeout error with operation context
type TimeoutError struct {
	Operation string
	Duration  time.Duration
}

func (e *TimeoutError) Error() string {
	return "timeout after " + e.Duration.String() + " in operation: " + e.Operation
}
